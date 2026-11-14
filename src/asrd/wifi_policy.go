package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	maxTrustedSSIDs = 32
	maxSSIDLen      = 64
)

// normalizeSSIDList trims entries, drops empties and duplicates, and caps the
// list length. Order is preserved so the WebUI round-trips what the user typed.
func normalizeSSIDList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		for _, part := range strings.Split(raw, ",") {
			ssid := strings.TrimSpace(part)
			if ssid == "" || seen[ssid] {
				continue
			}
			if len(ssid) > maxSSIDLen {
				ssid = ssid[:maxSSIDLen]
			}
			seen[ssid] = true
			out = append(out, ssid)
			if len(out) >= maxTrustedSSIDs {
				return out
			}
		}
	}
	return out
}

func ssidTrusted(cfg *Config, ssid string) bool {
	for _, t := range cfg.Network.TrustedSSIDs {
		if t == ssid {
			return true
		}
	}
	return false
}

// exposurePolicy decides how much the current link may do. It is a pure
// function of config plus the detected network so the WebUI can preview it.
//
// Rules:
//   - no trusted list configured: policy is off, everything is allowed;
//   - non-Wi-Fi links (ethernet, USB tethering) have no SSID, so they are
//     allowed rather than locked out by a Wi-Fi-only feature;
//   - SSID unreadable: fail open with a warning, because locking out a working
//     setup is worse than the exposure this feature protects against;
//   - SSID not in the list: apply network.untrusted_action.
func exposurePolicy(cfg *Config, info NetworkInfo) (policy, note string, trusted bool) {
	if len(cfg.Network.TrustedSSIDs) == 0 {
		return "allow", "", true
	}
	if !info.WiFi {
		return "allow", "非 Wi-Fi 连接，跳过 SSID 白名单检查", true
	}
	if info.SSID == "" {
		return "allow", "读不到当前 Wi-Fi 名称，已按可信处理（白名单未生效）", false
	}
	if ssidTrusted(cfg, info.SSID) {
		return "allow", "", true
	}
	switch cfg.Network.UntrustedAction {
	case "stop":
		return "stop", fmt.Sprintf("当前 Wi-Fi「%s」不在可信列表，已停止 SMB", info.SSID), false
	case "readonly":
		return "readonly", fmt.Sprintf("当前 Wi-Fi「%s」不在可信列表，共享已强制只读", info.SSID), false
	}
	return "allow", "", false
}

// ssidCache keeps `cmd wifi status` from being spawned on every network sample.
type ssidCache struct {
	mu     sync.Mutex
	value  string
	source string
	at     time.Time
}

var wifiSSID ssidCache

const ssidCacheTTL = 10 * time.Second

// currentSSID reports the connected Wi-Fi SSID and where it came from.
// Android only exposes this to privileged callers, which asrd is (root).
func currentSSID() (string, string) {
	wifiSSID.mu.Lock()
	defer wifiSSID.mu.Unlock()
	if !wifiSSID.at.IsZero() && time.Since(wifiSSID.at) < ssidCacheTTL {
		return wifiSSID.value, wifiSSID.source
	}
	ssid, src := querySSID()
	wifiSSID.value, wifiSSID.source, wifiSSID.at = ssid, src, time.Now()
	return ssid, src
}

func querySSID() (string, string) {
	if out, err := runQuickCmd("cmd", "wifi", "status"); err == nil {
		if s := parseSSID(out); s != "" {
			return s, "cmd wifi status"
		}
	}
	return "", ""
}

func runQuickCmd(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

// parseSSID understands the two shapes Android has used for years:
//
//	Wifi is connected to "Home"
//	WifiInfo: SSID: "Home", BSSID: ...
func parseSSID(out string) string {
	const connected = `is connected to `
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, connected); i >= 0 {
			if s := quotedValue(line[i+len(connected):]); s != "" {
				return s
			}
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "SSID:"); i >= 0 {
			if s := quotedValue(line[i+len("SSID:"):]); s != "" {
				return s
			}
		}
	}
	return ""
}

// quotedValue returns the first double-quoted string in s ("" when absent).
func quotedValue(s string) string {
	start := strings.IndexByte(s, '"')
	if start < 0 {
		return ""
	}
	rest := s[start+1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}
