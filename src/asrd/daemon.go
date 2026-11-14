package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const appVersion = "0.1.0"

type ServiceStatus struct {
	State     string    `json:"state"`
	Since     time.Time `json:"since"`
	PID       int       `json:"pid"`
	Restarts  int       `json:"restarts"`
	LastError string    `json:"last_error"`
	Interface string    `json:"interface"`
	IP        string    `json:"ip"`
	Port      int       `json:"port"`
	ReadOnly  bool      `json:"read_only"`
	UptimeS   int64     `json:"uptime_s"`
}

type WebUIStatus struct {
	Enabled bool     `json:"enabled"`
	Running bool     `json:"running"`
	Port    int      `json:"port"`
	Bind    string   `json:"bind"`
	URLs    []string `json:"urls"`
	Error   string   `json:"error"`
}

type ShareStatus struct {
	Share
	Exists bool   `json:"exists"`
	UNC    string `json:"unc"`
}

type Status struct {
	Version  string        `json:"version"`
	Time     time.Time     `json:"time"`
	SMB      ServiceStatus `json:"smb"`
	WebUI    WebUIStatus   `json:"webui"`
	Network  NetworkInfo   `json:"network"`
	System   SystemInfo    `json:"system"`
	Shares   []ShareStatus `json:"shares"`
	Warnings []string      `json:"warnings"`
	UptimeS  int64         `json:"uptime_s"`
}

type Daemon struct {
	cfgPath   string
	moduleDir string
	dataDir   string
	webuiDir  string
	log       *Logger
	samba     *SambaManager
	secrets   *Secrets
	sessions  *SessionStore
	limiter   *LoginLimiter
	sys       *SysMonitor
	startTime time.Time

	mu           sync.RWMutex
	cfg          *Config
	bootMode     bool
	net          NetworkInfo
	smb          ServiceStatus
	web          *WebUIServer
	userStopped  bool
	expectExit   bool
	lastIface    string
	lastIP       string
	lastStorage  string
	lastReadOnly bool
	restarts     []time.Time
	opMu         sync.Mutex

	wake       chan struct{}
	exitEvents chan error
	stopCh     chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
}

func NewDaemon(cfgPath, moduleDir, dataDir, webuiDir string) (*Daemon, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	for _, sub := range []string{"config", "logs", "run", "samba", "tmp", "secrets"} {
		if err := os.MkdirAll(filepath.Join(dataDir, sub), 0o700); err != nil {
			return nil, err
		}
	}
	log, err := NewLogger(filepath.Join(dataDir, "logs", "asrd.log"), "info", 512, 3)
	if err != nil {
		return nil, err
	}
	cfg, err := loadConfig(cfgPath)
	if os.IsNotExist(err) {
		cfg = defaultConfig()
		if err := saveConfig(cfgPath, cfg); err != nil {
			return nil, err
		}
		log.Infof("config", "created default configuration at %s", cfgPath)
	} else if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	logCfg := cfg.Log
	log.Close()
	log, err = NewLogger(filepath.Join(dataDir, "logs", "asrd.log"), logCfg.Level, logCfg.MaxSizeKB, logCfg.MaxFiles)
	if err != nil {
		return nil, err
	}
	secrets, err := ensureSecrets(dataDir, log)
	if err != nil {
		return nil, err
	}
	d := &Daemon{
		cfgPath:    cfgPath,
		moduleDir:  moduleDir,
		dataDir:    dataDir,
		webuiDir:   webuiDir,
		log:        log,
		samba:      NewSambaManager(cfg, moduleDir, dataDir, log),
		secrets:    secrets,
		sessions:   NewSessionStore(cfg.WebUI.SessionHours),
		limiter:    NewLoginLimiter(),
		sys:        NewSysMonitor(),
		startTime:  time.Now(),
		cfg:        cfg,
		wake:       make(chan struct{}, 4),
		exitEvents: make(chan error, 4),
		stopCh:     make(chan struct{}),
	}
	if err := ensureDataLibLink(dataDir, moduleDir); err != nil {
		log.Warnf("daemon", "cannot create Samba lib symlink: %v", err)
	}
	d.smb = ServiceStatus{State: "stopped", Since: time.Now(), Port: cfg.SMB.Port}
	if !cfg.Startup.StartOnBoot {
		d.userStopped = true
	}
	return d, nil
}

func (d *Daemon) SetBootMode(v bool) { d.bootMode = v }

// The release binaries have their compiled-in Termux prefix rewritten to the
// persistent data directory. Samba loads its passdb/vfs modules from
// <dataDir>/lib/samba, so keep that path pointing at the module library.
func ensureDataLibLink(dataDir, moduleDir string) error {
	link := filepath.Join(dataDir, "lib")
	if _, err := os.Lstat(link); err == nil {
		return nil
	}
	return os.Symlink(filepath.Join(moduleDir, "lib"), link)
}

func (d *Daemon) Config() *Config {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cfg.clone()
}

func (d *Daemon) DataDir() string { return d.dataDir }

func (d *Daemon) Start() error {
	d.log.Infof("daemon", "starting version %s data=%s module=%s", appVersion, d.dataDir, d.moduleDir)
	if err := d.initSambaUsers(); err != nil {
		d.log.Warnf("smb", "initial SMB user setup: %v", err)
	}
	if !d.bootMode || d.Config().Startup.StartWebUIOnBoot {
		web := NewWebUIServer(d)
		if err := web.Apply(d.Config().WebUI); err != nil {
			d.log.Errorf("webui", "initial WebUI start failed: %v", err)
		}
		d.mu.Lock()
		d.web = web
		d.mu.Unlock()
	}

	netWatch := NewNetworkWatcher(d.Config())
	netWatch.Start()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer netWatch.Stop()
		for {
			select {
			case <-d.stopCh:
				return
			case <-netWatch.Events:
				d.trigger()
			}
		}
	}()

	d.wg.Add(1)
	go func() { defer d.wg.Done(); d.supervise() }()

	ctl := NewControlServer(d)
	if err := ctl.Start(); err != nil {
		d.log.Errorf("ctl", "control socket: %v", err)
	}

	d.trigger()
	return nil
}

func (d *Daemon) trigger() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *Daemon) Stop() {
	d.stopOnce.Do(func() {
		close(d.stopCh)
		d.mu.Lock()
		web := d.web
		d.mu.Unlock()
		if web != nil {
			web.Close()
		}
		d.mu.Lock()
		d.expectExit = true
		d.mu.Unlock()
		_ = d.samba.Stop(8 * time.Second)
		d.log.Infof("daemon", "stopped")
		d.log.Close()
	})
}

func (d *Daemon) supervise() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-d.stopCh:
			return
		case <-d.wake:
			d.reconcile()
		case <-ticker.C:
			d.reconcile()
		case err := <-d.exitEvents:
			d.onSMBExit(err)
		}
	}
}

func (d *Daemon) configSnapshot() *Config {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cfg.clone()
}

func (d *Daemon) setState(state, lastErr string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.smb.State != state {
		d.smb.Since = time.Now()
	}
	d.smb.State = state
	if lastErr != "" {
		d.smb.LastError = lastErr
	}
	if state == "running" {
		d.smb.LastError = ""
	}
}

func (d *Daemon) reconcile() {
	cfg := d.configSnapshot()
	info := detectNetwork(cfg)
	d.mu.Lock()
	d.net = info
	d.mu.Unlock()

	d.mu.RLock()
	userStopped := d.userStopped
	d.mu.RUnlock()

	running := d.samba.PID() != 0

	if !cfg.SMB.Enabled || userStopped {
		if running {
			d.stopSMB(false)
		}
		d.setState("stopped", "")
		d.mu.Lock()
		d.smb.PID = 0
		d.mu.Unlock()
		return
	}

	if cfg.Startup.WaitForNetwork && !info.Ready {
		if running {
			d.stopSMB(false)
		}
		msg := info.Message
		if msg == "" {
			msg = "等待局域网接口"
		}
		d.setState("waiting_network", msg)
		return
	}
	if !info.Ready {
		if running {
			d.stopSMB(false)
		}
		d.setState("waiting_network", info.Message)
		return
	}

	// Exposure policy: an untrusted Wi-Fi may stop the service or force every
	// share read-only. This runs before storage checks so a hostile network is
	// shut down even while the phone is still locked.
	readOnly := info.Policy == "readonly"
	if info.Policy == "stop" {
		if running {
			d.stopSMB(false)
			running = false
		}
		d.mu.Lock()
		d.smb.PID = 0
		d.smb.ReadOnly = false
		d.lastReadOnly = false
		d.mu.Unlock()
		d.setState("untrusted_network", info.PolicyNote)
		return
	}

	storagePath := RuntimePath(cfg, cfg.Storage.Root)
	if cfg.Startup.WaitForStorage && !storageUsable(storagePath) {
		if running {
			d.stopSMB(false)
		}
		d.setState("waiting_storage", fmt.Sprintf("存储尚未就绪：%s", storagePath))
		return
	}
	if err := ensureShareTree(cfg, d.log); err != nil {
		d.log.Warnf("storage", "%v", err)
	}
	EnsureSharePaths(cfg, d.log)

	storageFP := StorageFingerprint(cfg)
	if running {
		d.mu.RLock()
		iface, ip, lastRO := d.lastIface, d.lastIP, d.lastReadOnly
		d.mu.RUnlock()
		if iface != info.Selected || ip != info.IPv4 {
			d.log.Infof("smb", "network changed %s/%s -> %s/%s, restarting smbd", iface, ip, info.Selected, info.IPv4)
			d.stopSMB(false)
			running = false
		} else {
			d.mu.RLock()
			lastStorage := d.lastStorage
			d.mu.RUnlock()
			if lastStorage != "" && lastStorage != storageFP {
				d.log.Infof("smb", "storage mapping changed, restarting smbd")
				d.stopSMB(false)
				running = false
			} else if lastRO != readOnly {
				// Share options are read when a client connects, so a SIGHUP
				// would leave existing sessions writable; restart instead.
				d.log.Infof("smb", "exposure policy changed (read_only=%v, note=%q), restarting smbd", readOnly, info.PolicyNote)
				d.stopSMB(false)
				running = false
			}
		}
	}
	if running {
		return
	}
	// Do not start while a watchdog backoff is in effect.
	d.mu.RLock()
	state := d.smb.State
	d.mu.RUnlock()
	if state == "failed" {
		return
	}
	d.startSMB(effectiveConfig(cfg, readOnly), info)
	d.mu.Lock()
	d.lastReadOnly = readOnly
	d.smb.ReadOnly = readOnly
	d.mu.Unlock()
}

func (d *Daemon) startSMB(cfg *Config, info NetworkInfo) {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	if d.samba.PID() != 0 {
		return
	}
	d.samba.cfg = cfg
	d.setState("starting", "")
	if err := d.samba.Start(info.Selected); err != nil {
		d.log.Errorf("smb", "start failed: %v", err)
		d.mu.Lock()
		d.smb.PID = 0
		d.smb.LastError = err.Error()
		d.restarts = append(d.restarts, time.Now())
		d.pruneRestartsLocked(cfg)
		tooMany := len(d.restarts) >= cfg.Startup.WatchdogMaxRestarts
		watchdog := cfg.Startup.Watchdog
		d.mu.Unlock()
		if !watchdog || tooMany {
			d.setState("failed", err.Error())
			return
		}
		d.setState("restarting", err.Error())
		d.scheduleRestart(cfg, len(d.restarts))
		return
	}
	d.mu.Lock()
	d.smb.PID = d.samba.PID()
	d.smb.Interface = info.Selected
	d.smb.IP = info.IPv4
	d.smb.Port = cfg.SMB.Port
	d.smb.UptimeS = 0
	d.lastIface = info.Selected
	d.lastIP = info.IPv4
	d.lastStorage = StorageFingerprint(cfg)
	d.expectExit = false
	d.mu.Unlock()
	d.setState("running", "")
	go func() {
		err := d.samba.Wait()
		select {
		case d.exitEvents <- err:
		case <-d.stopCh:
		}
	}()
	// A process that stays up for a whole watchdog window resets the counter.
	window := time.Duration(cfg.Startup.WatchdogWindowMin) * time.Minute
	go func(pid int) {
		t := time.NewTimer(window)
		defer t.Stop()
		select {
		case <-t.C:
			if d.samba.PID() == pid {
				d.mu.Lock()
				d.restarts = nil
				d.mu.Unlock()
				d.log.Infof("watchdog", "smbd stable for %s, restart counter reset", window)
			}
		case <-d.stopCh:
		}
	}(d.samba.PID())
}

func (d *Daemon) onSMBExit(err error) {
	d.mu.Lock()
	expect := d.expectExit
	if expect {
		d.expectExit = false
		d.smb.PID = 0
		d.smb.State = "stopped"
		d.mu.Unlock()
		return
	}
	cfg := d.cfg.clone()
	d.smb.PID = 0
	d.mu.Unlock()

	msg := "smbd exited"
	if err != nil {
		msg = "smbd exited: " + err.Error()
	}
	d.log.Errorf("watchdog", "%s", msg)
	_ = d.samba.Stop(0)
	d.mu.Lock()
	d.restarts = append(d.restarts, time.Now())
	d.pruneRestartsLocked(cfg)
	count := len(d.restarts)
	d.smb.LastError = msg
	d.mu.Unlock()

	if !cfg.Startup.Watchdog {
		d.setState("failed", msg)
		return
	}
	if count >= cfg.Startup.WatchdogMaxRestarts {
		d.log.Errorf("watchdog", "giving up after %d restarts in %d minutes", count, cfg.Startup.WatchdogWindowMin)
		d.setState("failed", fmt.Sprintf("%s (restart limit reached)", msg))
		return
	}
	d.setState("restarting", msg)
	d.scheduleRestart(cfg, count)
}

func (d *Daemon) pruneRestartsLocked(cfg *Config) {
	cutoff := time.Now().Add(-time.Duration(cfg.Startup.WatchdogWindowMin) * time.Minute)
	out := d.restarts[:0]
	for _, t := range d.restarts {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	d.restarts = out
}

func (d *Daemon) scheduleRestart(cfg *Config, attempt int) {
	delay := time.Duration(3*(1<<minInt(attempt-1, 5))) * time.Second
	if delay > 60*time.Second {
		delay = 60 * time.Second
	}
	d.log.Infof("watchdog", "restarting smbd in %s (attempt %d/%d)", delay, attempt, cfg.Startup.WatchdogMaxRestarts)
	go func() {
		t := time.NewTimer(delay)
		defer t.Stop()
		select {
		case <-t.C:
			d.trigger()
		case <-d.stopCh:
		}
	}()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (d *Daemon) stopSMB(intentional bool) {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	d.expectExit = true
	if intentional {
		d.userStopped = true
	}
	d.mu.Unlock()
	_ = d.samba.Stop(8 * time.Second)
	d.mu.Lock()
	d.smb.PID = 0
	d.smb.State = "stopped"
	d.smb.Since = time.Now()
	if intentional {
		d.smb.LastError = ""
	}
	d.mu.Unlock()
}

// Public control operations -------------------------------------------------

func (d *Daemon) StartSMB() error {
	cfg := d.Config()
	if !cfg.SMB.Enabled {
		return errors.New("SMB 已在设置中关闭")
	}
	d.mu.Lock()
	d.userStopped = false
	d.restarts = nil
	d.smb.State = "starting"
	d.mu.Unlock()
	d.trigger()
	return nil
}

func (d *Daemon) StopSMB() error {
	d.stopSMB(true)
	return nil
}

func (d *Daemon) RestartSMB() error {
	d.stopSMB(false)
	d.mu.Lock()
	d.userStopped = false
	d.restarts = nil
	d.smb.State = "starting"
	d.mu.Unlock()
	d.trigger()
	return nil
}

func (d *Daemon) ReloadSMB() error {
	cfg := d.Config()
	if d.samba.PID() == 0 {
		return d.StartSMB()
	}
	d.samba.cfg = cfg
	info := d.Network()
	if err := d.samba.WriteConfig(info.Selected); err != nil {
		return err
	}
	if err := d.samba.Reload(); err != nil {
		return d.RestartSMB()
	}
	return nil
}

func (d *Daemon) StartWebUI() error {
	cfg := d.Config()
	if !cfg.WebUI.Enabled {
		return errors.New("WebUI is disabled in settings")
	}
	d.mu.Lock()
	web := d.web
	if web == nil {
		web = NewWebUIServer(d)
		d.web = web
	}
	d.mu.Unlock()
	return web.Apply(cfg.WebUI)
}

func (d *Daemon) StopWebUI() {
	d.mu.Lock()
	web := d.web
	d.web = nil
	d.mu.Unlock()
	if web != nil {
		web.Close()
	}
}

func (d *Daemon) Network() NetworkInfo {
	d.mu.RLock()
	info := d.net
	d.mu.RUnlock()
	if info.Selected == "" {
		info = detectNetwork(d.Config())
	}
	return info
}

func (d *Daemon) WebUIStatus() WebUIStatus {
	d.mu.RLock()
	web := d.web
	d.mu.RUnlock()
	if web == nil {
		cfg := d.Config().WebUI
		return WebUIStatus{Enabled: cfg.Enabled, Port: cfg.Port, Bind: cfg.Bind}
	}
	return web.Status()
}

// Status builds a consistent snapshot for the API. It is called frequently
// (every 2s by the UI), so all work here is bounded and cheap.
// effectiveConfig returns cfg unchanged, or a clone whose shares are all
// read-only when the exposure policy demands it. The stored config is never
// modified, so leaving the untrusted network restores write access.
func effectiveConfig(cfg *Config, readOnly bool) *Config {
	if !readOnly {
		return cfg
	}
	out := cfg.clone()
	for i := range out.Shares {
		out.Shares[i].ReadOnly = true
	}
	return out
}

func (d *Daemon) Status() Status {
	cfg := d.Config()
	info := d.Network()
	d.mu.RLock()
	state := d.smb
	restarts := len(d.restarts)
	web := d.web
	userStopped := d.userStopped
	d.mu.RUnlock()

	conns, clients := CountSMBConnections(cfg.SMB.Port)
	tp := d.sys.SampleThroughput(info.Selected)
	cpu := d.sys.SampleCPU()
	storagePath := RuntimePath(cfg, cfg.Storage.Root)
	store := readStorage(storagePath)
	if store.TotalBytes == 0 {
		store = readStorage("/storage/emulated/0")
	}
	state.PID = d.samba.PID()
	state.Restarts = restarts
	state.Interface = info.Selected
	state.IP = info.IPv4
	state.Port = cfg.SMB.Port
	if state.PID != 0 {
		state.UptimeS = int64(time.Since(d.samba.StartedAt()).Seconds())
	}
	if state.State == "" {
		state.State = "stopped"
	}

	shares := make([]ShareStatus, 0, len(cfg.Shares))
	for _, s := range cfg.Shares {
		st, err := os.Stat(RuntimePath(cfg, s.Path))
		unc := ""
		if info.IPv4 != "" {
			unc = `\\` + info.IPv4 + `\` + s.Name
		}
		shares = append(shares, ShareStatus{Share: s, Exists: err == nil && st.IsDir(), UNC: unc})
	}
	webStatus := WebUIStatus{Enabled: cfg.WebUI.Enabled, Port: cfg.WebUI.Port, Bind: cfg.WebUI.Bind}
	if web != nil {
		webStatus = web.Status()
	}

	warnings := d.warnings(cfg, info, userStopped)
	return Status{
		Version: appVersion,
		Time:    time.Now(),
		SMB:     state,
		WebUI:   webStatus,
		Network: info,
		System: SystemInfo{
			CPU:        cpu,
			Memory:     readMemory(),
			UptimeS:    readUptime(),
			Storage:    store,
			SambaProcs: smbdProcessCount(),
			Throughput: tp,
			Clients:    conns,
			ClientIPs:  clients,
		},
		Shares:   shares,
		Warnings: warnings,
		UptimeS:  int64(time.Since(d.startTime).Seconds()),
	}
}

func (d *Daemon) warnings(cfg *Config, info NetworkInfo, userStopped bool) []string {
	var out []string
	if !cfg.SMB.Enabled {
		out = append(out, "SMB 已在设置中关闭")
	} else if userStopped {
		out = append(out, "SMB 已被手动停止")
	}
	if cfg.SMB.GuestAccess {
		out = append(out, "访客访问已启用：局域网内任何人都可以写入访客共享")
	}
	if cfg.SMB.AllowAnonymous {
		out = append(out, "匿名访问已启用")
	}
	if !cfg.WebUI.Auth {
		out = append(out, "WebUI 认证已关闭")
	}
	if cfg.WebUI.Bind == "0.0.0.0" || cfg.WebUI.Bind == "::" {
		if !cfg.WebUI.LANOnly {
			out = append(out, "WebUI 监听所有接口且未限制私网来源")
		}
	}
	if len(enabledUsers(cfg)) == 0 && !cfg.SMB.GuestAccess {
		out = append(out, "没有启用的 SMB 用户；需要认证的共享不可访问")
	}
	if !info.Ready && cfg.SMB.Enabled {
		out = append(out, "没有可用的局域网接口："+info.Message)
	}
	if cfg.SMB.Signing == "off" {
		out = append(out, "SMB 签名已关闭（不强制、也不启用签名）")
	}
	if cfg.SMB.Encryption == "off" {
		out = append(out, "SMB 传输加密已关闭")
	}
	if len(cfg.Network.TrustedSSIDs) > 0 && info.PolicyNote != "" {
		out = append(out, info.PolicyNote)
	}
	return out
}

// UpdateConfig applies a validated configuration atomically and reconciles
// running services. Mutating callbacks must not perform I/O.
func (d *Daemon) UpdateConfig(mutate func(*Config) error) error {
	// Serialize the read-modify-write, but release the config lock before
	// applying service changes: RestartSMB/StopSMB take the same lock.
	d.opMu.Lock()
	old := d.Config()
	next := old.clone()
	if err := mutate(next); err != nil {
		d.opMu.Unlock()
		return err
	}
	next.normalize()
	if err := next.Validate(); err != nil {
		d.opMu.Unlock()
		return err
	}
	if err := saveConfig(d.cfgPath, next); err != nil {
		d.opMu.Unlock()
		return err
	}
	d.mu.Lock()
	d.cfg = next
	web := d.web
	readOnly := d.lastReadOnly
	d.mu.Unlock()
	d.samba.cfg = effectiveConfig(next, readOnly)
	d.opMu.Unlock()
	d.log.Infof("config", "configuration updated")

	// WebUI listener changes take effect without touching SMB.
	if web != nil && webuiNeedsRebind(old, next) {
		if err := web.Apply(next.WebUI); err != nil {
			d.log.Errorf("webui", "apply webui settings: %v", err)
			return fmt.Errorf("settings saved but WebUI listener failed: %w", err)
		}
	}
	// SMB settings that cannot be changed by a reload require a restart.
	if smbNeedsRestart(old, next) {
		if !next.SMB.Enabled {
			d.StopSMB()
		} else if d.samba.PID() != 0 {
			d.RestartSMB()
		} else {
			d.trigger()
		}
	} else {
		if d.samba.PID() != 0 {
			info := d.Network()
			if err := d.samba.WriteConfig(info.Selected); err != nil {
				d.log.Errorf("smb", "write config: %v", err)
			} else if err := d.samba.Reload(); err != nil {
				d.log.Warnf("smb", "reload failed, restarting: %v", err)
				d.RestartSMB()
			}
		} else if next.SMB.Enabled {
			d.trigger()
		}
	}
	// Re-evaluate the exposure policy even when nothing needed a restart: a
	// new trusted list applies on the next reconcile.
	d.trigger()
	return nil
}

func webuiNeedsRebind(a, b *Config) bool {
	return a.WebUI.Enabled != b.WebUI.Enabled || a.WebUI.Port != b.WebUI.Port || a.WebUI.Bind != b.WebUI.Bind
}

func smbNeedsRestart(a, b *Config) bool {
	if a.SMB.Port != b.SMB.Port || a.SMB.MinProtocol != b.SMB.MinProtocol ||
		a.SMB.MaxProtocol != b.SMB.MaxProtocol || a.SMB.Signing != b.SMB.Signing ||
		a.SMB.Encryption != b.SMB.Encryption || a.SMB.Interface != b.SMB.Interface ||
		a.SMB.ForceUser != b.SMB.ForceUser || a.SMB.NetBIOSName != b.SMB.NetBIOSName ||
		a.SMB.Workgroup != b.SMB.Workgroup || a.SMB.GuestAccess != b.SMB.GuestAccess ||
		a.SMB.AllowAnonymous != b.SMB.AllowAnonymous {
		return true
	}
	if a.Network.Interface != b.Network.Interface || a.Network.AllowCellular != b.Network.AllowCellular {
		return true
	}
	if a.Storage.Root != b.Storage.Root || a.Storage.UseDataMedia != b.Storage.UseDataMedia {
		return true
	}
	return false
}

func (d *Daemon) initSambaUsers() error {
	cfg := d.Config()
	if len(cfg.Users) > 0 || !cfg.SMB.Enabled {
		return nil
	}
	// Anonymous/guest-only mode intentionally has no default SMB account.
	if cfg.SMB.GuestAccess && cfg.SMB.AllowAnonymous {
		return nil
	}
	// Create a default authenticated user so a fresh install is usable without
	// first opening the WebUI. The password is random and written once to the
	// root-only credentials file.
	// smbpasswd needs a readable smb.conf and its private directory, even
	// before smbd is started.
	if err := d.samba.WriteConfig(""); err != nil {
		return err
	}
	pw := randomPassword(20)
	if err := d.samba.AddUser("android", pw); err != nil {
		return err
	}
	err := d.UpdateConfig(func(c *Config) error {
		c.Users = append(c.Users, User{Name: "android", Enabled: true, CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()})
		return nil
	})
	if err != nil {
		return err
	}
	d.appendCredential("SMB user \"android\"", pw)
	d.log.Infof("smb", "created default SMB user android (password in initial-credentials.txt)")
	return nil
}

func (d *Daemon) appendCredential(label, pw string) {
	p := credsPath(d.dataDir)
	data, _ := os.ReadFile(p)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	if len(data) == 0 {
		_, _ = fmt.Fprintf(f, "Android SMB Relay credentials\nGenerated: %s\n\n", time.Now().Format(time.RFC3339))
	}
	_, _ = fmt.Fprintf(f, "%s\n  Password: %s\n\n", label, pw)
}

func (d *Daemon) ClearInitialCredentials() {
	_ = os.Remove(credsPath(d.dataDir))
}

func (d *Daemon) readCredentials() (string, error) {
	data, err := os.ReadFile(credsPath(d.dataDir))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (d *Daemon) SetWebUIPassword(currentPassword, newPassword string) error {
	if err := validateAdminPassword(newPassword); err != nil {
		return err
	}
	d.mu.RLock()
	secrets := *d.secrets
	d.mu.RUnlock()
	if currentPassword != "" && !verifyPassword(secrets.PasswordHash, currentPassword) {
		return errors.New("current password is incorrect")
	}
	secrets.PasswordHash = hashPassword(newPassword)
	secrets.MustChangePassword = false
	secrets.Version = 2
	secrets.UpdatedAt = time.Now().Unix()
	if err := saveSecrets(d.dataDir, &secrets); err != nil {
		return err
	}
	d.mu.Lock()
	d.secrets = &secrets
	d.mu.Unlock()
	_ = os.Remove(credsPath(d.dataDir))
	d.sessions.DeleteOtherThan("")
	return nil
}

func (d *Daemon) CheckPassword(pw string) bool {
	d.mu.RLock()
	secrets := *d.secrets
	d.mu.RUnlock()
	return verifyPassword(secrets.PasswordHash, pw)
}

func (d *Daemon) AdminUser() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.secrets.AdminUser
}

// MustChangePassword reports whether the administrator is still using the
// initial password and must set a new one before using the WebUI.
func (d *Daemon) MustChangePassword() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.secrets != nil && d.secrets.MustChangePassword
}

// helpers ------------------------------------------------------------------

func (d *Daemon) applySMBOnly() {
	// Used after share/user changes: reload first, restart if reload fails.
	if d.samba.PID() == 0 {
		d.trigger()
		return
	}
	info := d.Network()
	d.samba.cfg = d.Config()
	if err := d.samba.WriteConfig(info.Selected); err != nil {
		d.log.Errorf("smb", "write config: %v", err)
		return
	}
	if err := d.samba.Reload(); err != nil {
		d.log.Warnf("smb", "reload failed: %v", err)
		d.trigger()
	}
}

func (d *Daemon) logTail(source string, limit int, level string) ([]string, error) {
	switch source {
	case "asrd", "webui", "daemon":
		return readLogTail(filepath.Join(d.dataDir, "logs", "asrd.log"), limit, level)
	case "smbd":
		path := filepath.Join(d.dataDir, "logs", "smbd.log")
		if _, err := os.Stat(path); err != nil {
			// Builds that passed "smbd -l <logs>" made Samba write
			// <logs>/log.smbd and skip "log file"; still show it.
			alt := filepath.Join(d.dataDir, "logs", "log.smbd")
			if _, altErr := os.Stat(alt); altErr == nil {
				path = alt
			}
		}
		return readLogTail(path, limit, level)
	case "console":
		return readLogTail(filepath.Join(d.dataDir, "logs", "smbd-console.log"), limit, level)
	}
	return nil, errors.New("unknown log source")
}

// SecretsSnapshot returns non-sensitive metadata for diagnostics.
func (d *Daemon) SecretsSnapshot() map[string]any {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return map[string]any{
		"admin_user":           d.secrets.AdminUser,
		"must_change_password": d.secrets.MustChangePassword,
		"created_at":           d.secrets.CreatedAt,
		"updated_at":           d.secrets.UpdatedAt,
		"initial_file":         credsPath(d.dataDir),
	}
}

// MarshalStatus is used by the control socket and tests.
func (d *Daemon) MarshalStatus() ([]byte, error) {
	return json.MarshalIndent(d.Status(), "", "  ")
}

func sortedClients(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var _ = strings.TrimSpace
