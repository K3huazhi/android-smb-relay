// Package main implements asrd, the Android SMB Relay supervisor and WebUI
// server. It intentionally depends only on the Go standard library so that it
// can be cross-compiled for Android/arm64 without cgo, a package manager or a
// network connection.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	schemaVersion    = 1
	defaultSMBPort   = 445
	defaultWebUIPort = 8080
)

// Config is the persistent, user-editable configuration. Secrets are kept in
// separate root-only files and never appear in this struct.
type Config struct {
	Schema  int         `json:"schema"`
	SMB     SMBConfig   `json:"smb"`
	WebUI   WebUIConfig `json:"webui"`
	Network NetConfig   `json:"network"`
	Startup Startup     `json:"startup"`
	Storage Storage     `json:"storage"`
	Log     LogConfig   `json:"log"`
	Shares  []Share     `json:"shares"`
	Users   []User      `json:"users"`
}

type SMBConfig struct {
	Enabled        bool   `json:"enabled"`
	Port           int    `json:"port"`
	MinProtocol    string `json:"min_protocol"`
	MaxProtocol    string `json:"max_protocol"`
	Signing        string `json:"signing"`    // mandatory | default | off
	Encryption     string `json:"encryption"` // off | desired | required
	GuestAccess    bool   `json:"guest_access"`
	AllowAnonymous bool   `json:"allow_anonymous"`
	NetBIOSName    string `json:"netbios_name"`
	Workgroup      string `json:"workgroup"`
	Interface      string `json:"interface"`  // auto or interface name
	ForceUser      string `json:"force_user"` // Unix user smbd impersonates; default root
}

type WebUIConfig struct {
	Enabled      bool   `json:"enabled"`
	Port         int    `json:"port"`
	Bind         string `json:"bind"`
	Auth         bool   `json:"auth"`
	LANOnly      bool   `json:"lan_only"`
	SessionHours int    `json:"session_hours"`
}

type NetConfig struct {
	Interface     string `json:"interface"`
	AllowCellular bool   `json:"allow_cellular"`
	SettleSeconds int    `json:"settle_seconds"`

	// TrustedSSIDs is an allow-list of Wi-Fi networks (exact, case-sensitive
	// SSID match). Empty means the policy is off and every network is allowed.
	TrustedSSIDs []string `json:"trusted_ssids"`
	// UntrustedAction decides what happens when the selected Wi-Fi is not in
	// TrustedSSIDs: "readonly" (default), "stop" or "allow".
	UntrustedAction string `json:"untrusted_action"`
}

type Startup struct {
	StartOnBoot         bool `json:"start_on_boot"`
	StartWebUIOnBoot    bool `json:"start_webui_on_boot"`
	WaitForNetwork      bool `json:"wait_for_network"`
	WaitForStorage      bool `json:"wait_for_storage"`
	Watchdog            bool `json:"watchdog"`
	WatchdogMaxRestarts int  `json:"watchdog_max_restarts"`
	WatchdogWindowMin   int  `json:"watchdog_window_minutes"`
}

type Storage struct {
	Root       string `json:"root"`
	AutoCreate bool   `json:"auto_create"`
	// UseDataMedia lets the runtime substitute /data/media/0 for
	// /storage/emulated/0 when the FUSE mount is not usable by root. The
	// configured and displayed path stays unchanged.
	UseDataMedia bool `json:"use_data_media"`
}

type LogConfig struct {
	Level     string `json:"level"` // debug | info | warn | error
	MaxSizeKB int    `json:"max_size_kb"`
	MaxFiles  int    `json:"max_files"`
}

type Share struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Comment    string `json:"comment"`
	ReadOnly   bool   `json:"read_only"`
	GuestOK    bool   `json:"guest_ok"`
	Hidden     bool   `json:"hidden"`
	Enabled    bool   `json:"enabled"`
	UploadOnly bool   `json:"upload_only"`
}

type User struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

var (
	reShareID   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	reShareName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,31}$`)
	reUserName  = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,31}$`)
	reIface     = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,32}$`)
)

func defaultConfig() *Config {
	return &Config{
		Schema: schemaVersion,
		SMB: SMBConfig{
			Enabled:        true,
			Port:           defaultSMBPort,
			MinProtocol:    "SMB2_02",
			MaxProtocol:    "SMB3_11",
			Signing:        "default",
			Encryption:     "off",
			GuestAccess:    true,
			AllowAnonymous: true,
			NetBIOSName:    "ANDROID-SMB",
			Workgroup:      "WORKGROUP",
			Interface:      "auto",
			ForceUser:      "root",
		},
		WebUI: WebUIConfig{
			Enabled:      true,
			Port:         defaultWebUIPort,
			Bind:         "0.0.0.0",
			Auth:         false,
			LANOnly:      true,
			SessionHours: 12,
		},
		Network: NetConfig{Interface: "auto", AllowCellular: false, SettleSeconds: 5, UntrustedAction: "readonly"},
		Startup: Startup{
			StartOnBoot:         true,
			StartWebUIOnBoot:    true,
			WaitForNetwork:      true,
			WaitForStorage:      true,
			Watchdog:            true,
			WatchdogMaxRestarts: 5,
			WatchdogWindowMin:   10,
		},
		Storage: Storage{
			Root:         "/storage/emulated/0",
			AutoCreate:   false,
			UseDataMedia: true,
		},
		Log: LogConfig{Level: "info", MaxSizeKB: 512, MaxFiles: 3},
		Shares: []Share{
			{ID: "internal", Name: "Internal", Path: "/storage/emulated/0", Comment: "完整内部存储（匿名读写）", ReadOnly: false, GuestOK: true, Enabled: true},
		},
		Users: []User{},
	}
}

func (c *Config) normalize() {
	if c.Schema == 0 {
		c.Schema = schemaVersion
	}
	if c.SMB.Port == 0 {
		c.SMB.Port = defaultSMBPort
	}
	if c.WebUI.Port == 0 {
		c.WebUI.Port = defaultWebUIPort
	}
	if c.SMB.MinProtocol == "" {
		c.SMB.MinProtocol = "SMB2_02"
	}
	if c.SMB.MaxProtocol == "" {
		c.SMB.MaxProtocol = "SMB3_11"
	}
	if c.SMB.Signing == "" {
		c.SMB.Signing = "default"
	}
	if c.SMB.Encryption == "" {
		c.SMB.Encryption = "off"
	}
	if c.SMB.NetBIOSName == "" {
		c.SMB.NetBIOSName = "ANDROID-SMB"
	}
	if c.SMB.Workgroup == "" {
		c.SMB.Workgroup = "WORKGROUP"
	}
	if c.SMB.ForceUser == "" {
		c.SMB.ForceUser = "root"
	}
	if c.SMB.Interface == "" {
		c.SMB.Interface = "auto"
	}
	if c.WebUI.Bind == "" {
		c.WebUI.Bind = "0.0.0.0"
	}
	if c.WebUI.SessionHours <= 0 {
		c.WebUI.SessionHours = 12
	}
	if c.Network.Interface == "" {
		c.Network.Interface = "auto"
	}
	if c.Network.SettleSeconds <= 0 {
		c.Network.SettleSeconds = 5
	}
	c.Network.UntrustedAction = strings.ToLower(strings.TrimSpace(c.Network.UntrustedAction))
	switch c.Network.UntrustedAction {
	case "allow", "readonly", "stop":
	default:
		c.Network.UntrustedAction = "readonly"
	}
	c.Network.TrustedSSIDs = normalizeSSIDList(c.Network.TrustedSSIDs)
	if c.Startup.WatchdogMaxRestarts <= 0 {
		c.Startup.WatchdogMaxRestarts = 5
	}
	if c.Startup.WatchdogWindowMin <= 0 {
		c.Startup.WatchdogWindowMin = 10
	}
	if c.Storage.Root == "" {
		c.Storage.Root = "/storage/emulated/0/SMB"
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.MaxSizeKB <= 0 {
		c.Log.MaxSizeKB = 512
	}
	if c.Log.MaxFiles <= 0 {
		c.Log.MaxFiles = 3
	}
}

var (
	validProtocols = map[string]bool{
		"SMB2_02": true, "SMB2_10": true, "SMB2_11": true,
		"SMB3_00": true, "SMB3_02": true, "SMB3_10": true, "SMB3_11": true,
	}
	protocolOrder = []string{"SMB2_02", "SMB2_10", "SMB2_11", "SMB3_00", "SMB3_02", "SMB3_10", "SMB3_11"}
)

func protocolRank(p string) int {
	for i, v := range protocolOrder {
		if v == p {
			return i
		}
	}
	return -1
}

// Validate checks every externally controlled field. It is called before any
// write and again after loading a config file, so a hand-edited corrupt file
// cannot be silently used.
func (c *Config) Validate() error {
	if c.Schema != schemaVersion {
		return fmt.Errorf("unsupported config schema %d (want %d)", c.Schema, schemaVersion)
	}
	if c.SMB.Port < 1 || c.SMB.Port > 65535 {
		return fmt.Errorf("smb.port must be 1..65535")
	}
	if c.WebUI.Port < 1 || c.WebUI.Port > 65535 {
		return fmt.Errorf("webui.port must be 1..65535")
	}
	if c.SMB.Port == c.WebUI.Port {
		return fmt.Errorf("smb and webui ports must differ")
	}
	if !validProtocols[c.SMB.MinProtocol] {
		return fmt.Errorf("invalid smb.min_protocol %q", c.SMB.MinProtocol)
	}
	if !validProtocols[c.SMB.MaxProtocol] {
		return fmt.Errorf("invalid smb.max_protocol %q", c.SMB.MaxProtocol)
	}
	if protocolRank(c.SMB.MinProtocol) > protocolRank(c.SMB.MaxProtocol) {
		return fmt.Errorf("smb.min_protocol must not be higher than smb.max_protocol")
	}
	switch c.SMB.Signing {
	case "mandatory", "default", "off":
	default:
		return fmt.Errorf("invalid smb.signing %q", c.SMB.Signing)
	}
	switch c.SMB.Encryption {
	case "off", "desired", "required":
	default:
		return fmt.Errorf("invalid smb.encryption %q", c.SMB.Encryption)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9-]{1,15}$`).MatchString(c.SMB.NetBIOSName) {
		return fmt.Errorf("invalid smb.netbios_name (1..15 alphanumeric or '-')")
	}
	if len(c.SMB.Workgroup) > 15 || strings.ContainsAny(c.SMB.Workgroup, "\r\n") {
		return fmt.Errorf("invalid smb.workgroup")
	}
	if c.SMB.Interface != "auto" && !reIface.MatchString(c.SMB.Interface) {
		return fmt.Errorf("invalid smb.interface")
	}
	if c.SMB.ForceUser != "" && !reUserName.MatchString(c.SMB.ForceUser) {
		return fmt.Errorf("invalid smb.force_user")
	}
	if c.Network.Interface != "auto" && !reIface.MatchString(c.Network.Interface) {
		return fmt.Errorf("invalid network.interface")
	}
	if c.Network.SettleSeconds < 0 || c.Network.SettleSeconds > 120 {
		return fmt.Errorf("network.settle_seconds must be 0..120")
	}
	switch c.Network.UntrustedAction {
	case "allow", "readonly", "stop":
	default:
		return fmt.Errorf("network.untrusted_action must be allow, readonly or stop")
	}
	if len(c.Network.TrustedSSIDs) > maxTrustedSSIDs {
		return fmt.Errorf("network.trusted_ssids must have at most %d entries", maxTrustedSSIDs)
	}
	for _, ssid := range c.Network.TrustedSSIDs {
		if len(ssid) > maxSSIDLen {
			return fmt.Errorf("network.trusted_ssids entry too long (max %d)", maxSSIDLen)
		}
	}
	switch {
	case c.WebUI.Bind == "0.0.0.0", c.WebUI.Bind == "::", c.WebUI.Bind == "127.0.0.1", c.WebUI.Bind == "::1":
	case isIPLiteral(c.WebUI.Bind):
	default:
		return fmt.Errorf("webui.bind must be an IP address (0.0.0.0, 127.0.0.1 or a specific address)")
	}
	if c.WebUI.SessionHours < 1 || c.WebUI.SessionHours > 24*30 {
		return fmt.Errorf("webui.session_hours must be 1..720")
	}
	if c.Startup.WatchdogMaxRestarts < 1 || c.Startup.WatchdogMaxRestarts > 100 {
		return fmt.Errorf("startup.watchdog_max_restarts must be 1..100")
	}
	if c.Startup.WatchdogWindowMin < 1 || c.Startup.WatchdogWindowMin > 1440 {
		return fmt.Errorf("startup.watchdog_window_minutes must be 1..1440")
	}
	if !filepath.IsAbs(c.Storage.Root) {
		return fmt.Errorf("storage.root must be an absolute path")
	}
	if strings.ContainsAny(c.Storage.Root, "\x00\r\n") {
		return fmt.Errorf("storage.root contains control characters")
	}
	if !validLogLevel(c.Log.Level) {
		return fmt.Errorf("invalid log.level %q", c.Log.Level)
	}
	if c.Log.MaxSizeKB < 64 || c.Log.MaxSizeKB > 1024*64 {
		return fmt.Errorf("log.max_size_kb must be 64..65536")
	}
	if c.Log.MaxFiles < 1 || c.Log.MaxFiles > 10 {
		return fmt.Errorf("log.max_files must be 1..10")
	}
	seenIDs := map[string]bool{}
	seenNames := map[string]bool{}
	for i := range c.Shares {
		s := &c.Shares[i]
		if !reShareID.MatchString(s.ID) {
			return fmt.Errorf("share %d: invalid id", i)
		}
		if seenIDs[s.ID] {
			return fmt.Errorf("duplicate share id %q", s.ID)
		}
		seenIDs[s.ID] = true
		if !reShareName.MatchString(s.Name) {
			return fmt.Errorf("share %q: invalid name", s.ID)
		}
		if seenNames[strings.ToLower(s.Name)] {
			return fmt.Errorf("duplicate share name %q", s.Name)
		}
		seenNames[strings.ToLower(s.Name)] = true
		if !filepath.IsAbs(s.Path) {
			return fmt.Errorf("share %q: path must be absolute", s.ID)
		}
		if strings.ContainsAny(s.Path, "\x00\r\n") {
			return fmt.Errorf("share %q: path contains control characters", s.ID)
		}
		if len(s.Comment) > 255 || strings.ContainsAny(s.Comment, "\x00\r\n") {
			return fmt.Errorf("share %q: invalid comment", s.ID)
		}
	}
	seenUsers := map[string]bool{}
	for i := range c.Users {
		u := &c.Users[i]
		if !reUserName.MatchString(u.Name) {
			return fmt.Errorf("user %d: invalid name", i)
		}
		if seenUsers[u.Name] {
			return fmt.Errorf("duplicate user %q", u.Name)
		}
		seenUsers[u.Name] = true
	}
	return nil
}

func (c *Config) clone() *Config {
	b, _ := json.Marshal(c)
	var out Config
	_ = json.Unmarshal(b, &out)
	return &out
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c.normalize()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// saveConfig writes atomically and durably: a reader can never observe a
// half-written config and a power loss leaves either old or new content.
func saveConfig(path string, c *Config) error {
	c.normalize()
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(path, data, 0o600)
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func isIPLiteral(s string) bool {
	if strings.Count(s, ".") == 3 && !strings.Contains(s, ":") {
		parts := strings.Split(s, ".")
		for _, p := range parts {
			n := 0
			if p == "" || len(p) > 3 {
				return false
			}
			for _, r := range p {
				if r < '0' || r > '9' {
					return false
				}
				n = n*10 + int(r-'0')
			}
			if n > 255 {
				return false
			}
		}
		return true
	}
	return strings.Contains(s, ":")
}

func validLogLevel(s string) bool {
	switch s {
	case "debug", "info", "warn", "error":
		return true
	}
	return false
}

func (c *Config) shareByID(id string) (*Share, int) {
	for i := range c.Shares {
		if c.Shares[i].ID == id {
			return &c.Shares[i], i
		}
	}
	return nil, -1
}

func (c *Config) userByName(name string) (*User, int) {
	for i := range c.Users {
		if c.Users[i].Name == name {
			return &c.Users[i], i
		}
	}
	return nil, -1
}

var errNotFound = errors.New("not found")
