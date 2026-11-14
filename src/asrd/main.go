package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
	case "serve", "daemon":
		os.Exit(runServe(args))
	case "ctl":
		os.Exit(runCtl(args))
	case "render":
		os.Exit(runRender(args))
	case "init":
		os.Exit(runInit(args))
	case "doctor":
		os.Exit(runDoctor(args))
	case "version", "--version", "-V":
		fmt.Printf("asrd %s\n", appVersion)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Android SMB Relay daemon and CLI

Usage:
  asrd serve [--module-dir DIR] [--data-dir DIR] [--config FILE] [--webui DIR]
  asrd ctl <command> [args]
  asrd render
  asrd init
  asrd doctor

ctl commands:
  status [--json]      service, network, throughput and warnings
  start|stop|restart   control smbd (daemon must be running)
  webui-start|webui-stop  control the HTTP listener without stopping smbd
  reload               reload smb.conf without dropping sessions
  url                  print WebUI and SMB URLs
  shares               list shares
  users                list SMB users
  network              list interfaces and selection
  logs [-n N] [--source asrd|smbd|console] [--level debug|info|warn|error]
  config               print the live config and its path
  credentials          print the one-time initial credentials
  passwd <user>        change an SMB user's password (prompts)
  webui-passwd         change the WebUI admin password (prompts)

Environment:
  ASR_MODULE_DIR  module root (default: parent of the bin/ directory)
  ASR_DATA_DIR    persistent data root (default: /data/adb/android-smb-relay)
  ASR_CONFIG      config path (default: $ASR_DATA_DIR/config/config.json)
`)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func defaultDataDir() string {
	if v := os.Getenv("ASR_DATA_DIR"); v != "" {
		return v
	}
	if st, err := os.Stat("/data/adb"); err == nil && st.IsDir() {
		return "/data/adb/android-smb-relay"
	}
	return filepath.Join(os.TempDir(), "android-smb-relay")
}

func defaultModuleDir() string {
	if v := os.Getenv("ASR_MODULE_DIR"); v != "" {
		return v
	}
	exe, err := os.Executable()
	if err == nil {
		exe, _ = filepath.EvalSymlinks(exe)
		dir := filepath.Dir(exe)
		if filepath.Base(dir) == "bin" {
			return filepath.Dir(dir)
		}
	}
	if wd, err := os.Getwd(); err == nil {
		if _, err := os.Stat(filepath.Join(wd, "webui", "index.html")); err == nil {
			return wd
		}
	}
	return "/data/adb/modules/android-smb-relay"
}

func defaultWebUIDir(moduleDir string) string {
	if v := os.Getenv("ASR_WEBUI_DIR"); v != "" {
		return v
	}
	return filepath.Join(moduleDir, "webui")
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	moduleDir := fs.String("module-dir", defaultModuleDir(), "module root")
	dataDirFlag := fs.String("data-dir", defaultDataDir(), "persistent data root")
	configFlag := fs.String("config", "", "config file")
	webuiFlag := fs.String("webui", "", "webui directory")
	bootFlag := fs.Bool("boot", false, "apply boot-time startup policy")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *configFlag == "" {
		*configFlag = filepath.Join(*dataDirFlag, "config", "config.json")
	}
	if *webuiFlag == "" {
		*webuiFlag = defaultWebUIDir(*moduleDir)
	}
	if err := os.MkdirAll(*dataDirFlag, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "cannot create data dir: %v\n", err)
		return 1
	}
	pidPath := filepath.Join(*dataDirFlag, "run", "asrd.pid")
	if data, err := os.ReadFile(pidPath); err == nil {
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(data))); pid > 0 && pid != os.Getpid() && processAlive(pid) {
			fmt.Fprintf(os.Stderr, "asrd already running with pid %d\n", pid)
			return 1
		}
	}
	_ = os.MkdirAll(filepath.Dir(pidPath), 0o700)
	if err := atomicWriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "cannot write pid file: %v\n", err)
		return 1
	}
	defer os.Remove(pidPath)

	d, err := NewDaemon(*configFlag, *moduleDir, *dataDirFlag, *webuiFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init: %v\n", err)
		return 1
	}
	d.SetBootMode(*bootFlag)
	if err := d.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		return 1
	}
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	d.Stop()
	return 0
}

func runRender(args []string) int {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	dataDir := fs.String("data-dir", defaultDataDir(), "data root")
	configFlag := fs.String("config", "", "config file")
	iface := fs.String("interface", "auto", "interface name or auto")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *configFlag == "" {
		*configFlag = filepath.Join(*dataDir, "config", "config.json")
	}
	cfg, err := loadConfig(*configFlag)
	if err != nil {
		cfg = defaultConfig()
	}
	fmt.Print(RenderConfig(cfg, *iface, *dataDir))
	return 0
}

func runInit(args []string) int {
	dataDir := defaultDataDir()
	_ = os.MkdirAll(dataDir, 0o700)
	for _, sub := range []string{"config", "logs", "run", "samba", "tmp"} {
		_ = os.MkdirAll(filepath.Join(dataDir, sub), 0o700)
	}
	cfgPath := filepath.Join(dataDir, "config", "config.json")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := saveConfig(cfgPath, defaultConfig()); err != nil {
			fmt.Fprintf(os.Stderr, "write config: %v\n", err)
			return 1
		}
	}
	log, err := NewLogger(filepath.Join(dataDir, "logs", "asrd.log"), "info", 512, 3)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger: %v\n", err)
		return 1
	}
	defer log.Close()
	if _, err := ensureSecrets(dataDir, log); err != nil {
		fmt.Fprintf(os.Stderr, "secrets: %v\n", err)
		return 1
	}
	fmt.Printf("initialized: %s\n", dataDir)
	fmt.Printf("config:     %s\n", cfgPath)
	if _, err := os.Stat(credsPath(dataDir)); err == nil {
		fmt.Printf("credentials: %s (root-only)\n", credsPath(dataDir))
	}
	return 0
}

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------

func runCtl(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	dataDir := defaultDataDir()
	client := NewControlClient(dataDir)
	cmd := args[0]
	rest := args[1:]
	switch cmd {
	case "status":
		st, err := client.Status()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if hasFlag(rest, "--json") {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(st)
			return 0
		}
		printStatus(st)
		return 0
	case "start", "stop", "restart", "reload", "webui-start", "webui-stop":
		if err := client.do("POST", "/ctl/"+cmd, nil, nil); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("%s requested\n", cmd)
		time.Sleep(700 * time.Millisecond)
		if st, err := client.Status(); err == nil {
			fmt.Printf("smb: %s\n", st.SMB.State)
			if st.SMB.LastError != "" {
				fmt.Printf("last error: %s\n", st.SMB.LastError)
			}
		}
		return 0
	case "url":
		st, err := client.Status()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("WebUI:")
		if len(st.WebUI.URLs) == 0 {
			fmt.Println("  (disabled)")
		}
		for _, u := range st.WebUI.URLs {
			fmt.Println("  " + u)
		}
		fmt.Println("SMB:")
		if st.SMB.IP == "" {
			fmt.Println("  (no LAN address)")
		}
		for _, s := range st.Shares {
			if s.Enabled {
				fmt.Printf("  \\\\%s\\%s\n", st.SMB.IP, s.Name)
			}
		}
		return 0
	case "shares":
		var shares []ShareStatus
		if err := client.do("GET", "/api/shares", nil, &shares); err != nil {
			// The control socket exposes status; shares are in it.
			st, err2 := client.Status()
			if err2 != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			shares = st.Shares
		}
		printShares(shares)
		return 0
	case "users":
		st, err := client.Status()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		_ = st
		var users []User
		if err := client.do("GET", "/ctl/users", nil, &users); err != nil {
			fmt.Fprintln(os.Stderr, "cannot list users:", err)
			return 1
		}
		printUsers(users)
		return 0
	case "network":
		var info NetworkInfo
		if err := client.do("GET", "/ctl/network", nil, &info); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		printNetwork(info)
		return 0
	case "logs":
		source := "asrd"
		limit := 100
		level := ""
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "-n":
				if i+1 < len(rest) {
					limit, _ = strconv.Atoi(rest[i+1])
					i++
				}
			case "--source":
				if i+1 < len(rest) {
					source = rest[i+1]
					i++
				}
			case "--level":
				if i+1 < len(rest) {
					level = rest[i+1]
					i++
				}
			}
		}
		var out struct {
			Lines []string `json:"lines"`
		}
		q := "/ctl/logs?source=" + source + "&limit=" + strconv.Itoa(limit)
		if level != "" {
			q += "&level=" + level
		}
		if err := client.do("GET", q, nil, &out); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		for i := len(out.Lines) - 1; i >= 0; i-- {
			fmt.Println(out.Lines[i])
		}
		return 0
	case "config":
		var out struct {
			Path   string `json:"path"`
			Config any    `json:"config"`
		}
		if err := client.do("GET", "/ctl/config", nil, &out); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("path:", out.Path)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out.Config)
		return 0
	case "credentials":
		var out struct {
			Exists  bool   `json:"exists"`
			Content string `json:"content"`
		}
		if err := client.do("GET", "/ctl/credentials", nil, &out); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if !out.Exists {
			fmt.Println("No initial credential file. Use `smbctl webui-passwd` and `smbctl passwd <user>` to set new ones.")
			return 0
		}
		fmt.Print(out.Content)
		return 0
	case "passwd":
		if len(rest) < 1 {
			fmt.Fprintln(os.Stderr, "usage: asrd ctl passwd <user>")
			return 2
		}
		pw, err := readPassword("New SMB password: ")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		confirm, err := readPassword("Retype SMB password: ")
		if err != nil || pw != confirm {
			fmt.Fprintln(os.Stderr, "passwords do not match")
			return 1
		}
		if err := client.do("POST", "/ctl/passwd", map[string]string{"name": rest[0], "password": pw}, nil); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("password changed")
		return 0
	case "webui-passwd":
		pw, err := readPassword("New WebUI password: ")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		confirm, err := readPassword("Retype WebUI password: ")
		if err != nil || pw != confirm {
			fmt.Fprintln(os.Stderr, "passwords do not match")
			return 1
		}
		if err := client.do("POST", "/ctl/webui-password", map[string]string{"password": pw}, nil); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("WebUI password changed; log in again")
		return 0
	case "doctor":
		return runDoctor(nil)
	default:
		fmt.Fprintf(os.Stderr, "unknown ctl command %q\n", cmd)
		return 2
	}
}

func hasFlag(args []string, f string) bool {
	for _, a := range args {
		if a == f {
			return true
		}
	}
	return false
}

func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fi, err := os.Stdin.Stat()
	interactive := err == nil && fi.Mode()&os.ModeCharDevice != 0
	if interactive {
		cmd := exec.Command("stty", "-echo")
		cmd.Stdin = os.Stdin
		_ = cmd.Run()
		defer func() {
			c := exec.Command("stty", "echo")
			c.Stdin = os.Stdin
			_ = c.Run()
			fmt.Fprintln(os.Stderr)
		}()
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func printStatus(st Status) {
	fmt.Printf("Android SMB Relay %s\n\n", st.Version)
	fmt.Printf("SMB       %s", st.SMB.State)
	if st.SMB.PID > 0 {
		fmt.Printf(" (pid %d, up %s)", st.SMB.PID, formatDuration(st.SMB.UptimeS))
	}
	fmt.Println()
	fmt.Printf("WebUI     %s\n", boolWord(st.WebUI.Running, "running", "stopped"))
	for _, u := range st.WebUI.URLs {
		fmt.Printf("          %s\n", u)
	}
	fmt.Printf("Network   %s %s\n", st.Network.Selected, st.Network.IPv4)
	fmt.Printf("Port      %d\n", st.SMB.Port)
	fmt.Printf("Clients   %d\n", st.System.Clients)
	fmt.Printf("Speed     up %s  down %s\n", formatBps(st.System.Throughput.UploadBps), formatBps(st.System.Throughput.DownloadBps))
	fmt.Printf("Total     up %s  down %s\n", formatBytes(st.System.Throughput.UploadTotal), formatBytes(st.System.Throughput.DownloadTotal))
	fmt.Printf("Restarts  %d\n", st.SMB.Restarts)
	if st.SMB.LastError != "" {
		fmt.Printf("Error     %s\n", st.SMB.LastError)
	}
	fmt.Printf("Storage   %s used of %s (%s)\n", formatBytes(st.System.Storage.UsedBytes), formatBytes(st.System.Storage.TotalBytes), st.System.Storage.Path)
	if len(st.SMB.Interface) > 0 {
		fmt.Printf("SMB URL   \\\\%s\\%s\n", st.SMB.IP, firstShareName(st))
	}
	if len(st.Warnings) > 0 {
		fmt.Println("\nWarnings:")
		for _, w := range st.Warnings {
			fmt.Println("  ! " + w)
		}
	}
}

func firstShareName(st Status) string {
	for _, s := range st.Shares {
		if s.Enabled {
			return s.Name
		}
	}
	return "Shared"
}

func printShares(shares []ShareStatus) {
	fmt.Printf("%-16s %-8s %-6s %-6s %-6s %s\n", "NAME", "MODE", "GUEST", "HIDDEN", "ENABLED", "PATH")
	for _, s := range shares {
		mode := "rw"
		if s.ReadOnly {
			mode = "ro"
		}
		fmt.Printf("%-16s %-8s %-6s %-6s %-6s %s\n", s.Name, mode, yesNo(s.GuestOK), yesNo(s.Hidden), yesNo(s.Enabled), s.Path)
	}
}

func printUsers(users []User) {
	fmt.Printf("%-20s %-8s %s\n", "NAME", "ENABLED", "CREATED")
	for _, u := range users {
		created := "-"
		if u.CreatedAt > 0 {
			created = time.Unix(u.CreatedAt, 0).Format("2006-01-02 15:04")
		}
		fmt.Printf("%-20s %-8s %s\n", u.Name, yesNo(u.Enabled), created)
	}
}

func printNetwork(info NetworkInfo) {
	fmt.Printf("Selected: %s  IPv4: %s  gateway: %s  ready: %v\n", info.Selected, info.IPv4, info.Gateway, info.Ready)
	if info.Message != "" {
		fmt.Println("Note:", info.Message)
	}
	fmt.Printf("%-10s %-9s %-5s %-28s %s\n", "IFACE", "TYPE", "UP", "IPv4", "MAC")
	for _, ii := range info.Interfaces {
		fmt.Printf("%-10s %-9s %-5s %-28s %s\n", ii.Name, ii.Type, yesNo(ii.Up), strings.Join(ii.IPv4, ","), ii.MAC)
	}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func boolWord(v bool, a, b string) string {
	if v {
		return a
	}
	return b
}

func formatBytes(v uint64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%d B", v)
	}
	div, exp := uint64(unit), 0
	for n := v / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(v)/float64(div), "KMGTPE"[exp])
}

func formatBps(v float64) string {
	return formatBytes(uint64(v)) + "/s"
}

func formatDuration(sec int64) string {
	if sec <= 0 {
		return "0s"
	}
	d := time.Duration(sec) * time.Second
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

// ---------------------------------------------------------------------------
// doctor
// ---------------------------------------------------------------------------

type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fatal  bool   `json:"fatal"`
}

func runDoctor(args []string) int {
	moduleDir := defaultModuleDir()
	dataDir := defaultDataDir()
	webuiDir := defaultWebUIDir(moduleDir)
	cfgPath := filepath.Join(dataDir, "config", "config.json")
	var checks []check
	add := func(name string, ok bool, detail string, fatal bool) {
		checks = append(checks, check{Name: name, OK: ok, Detail: detail, Fatal: fatal})
	}
	add("uid", os.Getuid() == 0, fmt.Sprintf("uid=%d (root required for SMB/445 and /data/adb)", os.Getuid()), true)
	for _, b := range []string{"smbd", "smbpasswd", "testparm", "asrd"} {
		p := filepath.Join(moduleDir, "bin", b)
		st, err := os.Stat(p)
		add("binary:"+b, err == nil && !st.IsDir(), p, b != "testparm")
	}
	libs := []string{"libsamba-util.so.0", "libtalloc.so.2", "libtevent-private-samba.so", "libtdb.so.1", "libgnutls.so", "libicuuc.so.79", "libzstd.so.1", "libkrb5.so.3"}
	for _, l := range libs {
		p := filepath.Join(moduleDir, "lib", l)
		_, err := os.Stat(p)
		add("lib:"+l, err == nil, p, false)
	}
	_, err := os.Stat(filepath.Join(webuiDir, "index.html"))
	add("webui", err == nil, webuiDir, false)
	if err := os.MkdirAll(filepath.Join(dataDir, "run"), 0o700); err == nil {
		f, err := os.CreateTemp(filepath.Join(dataDir, "run"), ".doctor-*")
		if err == nil {
			f.Close()
			os.Remove(f.Name())
			add("data-writable", true, dataDir, true)
		} else {
			add("data-writable", false, err.Error(), true)
		}
	}
	if cfg, err := loadConfig(cfgPath); err != nil {
		add("config", false, err.Error(), true)
	} else {
		add("config", true, cfgPath, true)
		if st := readStorage(RuntimePath(cfg, cfg.Storage.Root)); st.TotalBytes > 0 {
			add("storage", true, fmt.Sprintf("%s (%s free)", st.Path, formatBytes(st.FreeBytes)), true)
		} else {
			add("storage", false, RuntimePath(cfg, cfg.Storage.Root), true)
		}
		info := detectNetwork(cfg)
		add("network", info.Ready, fmt.Sprintf("%s %s %s", info.Selected, info.IPv4, info.Message), false)
		if !portInUse(cfg.SMB.Port) {
			add("port:smb", true, fmt.Sprintf("%d free", cfg.SMB.Port), false)
		} else {
			add("port:smb", true, fmt.Sprintf("%d in use (expected if running)", cfg.SMB.Port), false)
		}
		if !portInUse(cfg.WebUI.Port) {
			add("port:webui", true, fmt.Sprintf("%d free", cfg.WebUI.Port), false)
		} else {
			add("port:webui", true, fmt.Sprintf("%d in use (expected if running)", cfg.WebUI.Port), false)
		}
	}
	if enforce, err := os.ReadFile("/sys/fs/selinux/enforce"); err == nil {
		add("selinux", true, strings.TrimSpace(string(enforce))+" (1=enforcing; never disable)", true)
	}
	ksu := pathExists("/data/adb/ksu")
	magisk := pathExists("/data/adb/magisk")
	add("root-manager", ksu || magisk, fmt.Sprintf("KernelSU=%v Magisk=%v", ksu, magisk), false)
	if _, err := os.Stat(credsPath(dataDir)); err == nil {
		add("initial-credentials", true, credsPath(dataDir)+" (root-only)", false)
	}
	sort.SliceStable(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })
	fail := false
	for _, c := range checks {
		status := "OK  "
		if !c.OK {
			status = "FAIL"
			if c.Fatal {
				fail = true
			}
		}
		fmt.Printf("[%s] %-24s %s\n", status, c.Name, c.Detail)
	}
	if fail {
		return 1
	}
	return 0
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func portInUse(port int) bool { return !portFree(port) }
