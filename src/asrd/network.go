package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type InterfaceInfo struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Up      bool     `json:"up"`
	IPv4    []string `json:"ipv4"`
	IPv6    []string `json:"ipv6"`
	MAC     string   `json:"mac"`
	Gateway string   `json:"gateway,omitempty"`
	Default bool     `json:"default"`
}

type NetworkInfo struct {
	Interfaces []InterfaceInfo `json:"interfaces"`
	Selected   string          `json:"selected"`
	IPv4       string          `json:"ipv4"`
	IPv6       []string        `json:"ipv6"`
	Gateway    string          `json:"gateway"`
	DNS        []string        `json:"dns"`
	Ready      bool            `json:"ready"`
	Message    string          `json:"message"`

	// Exposure policy (network.trusted_ssids): the Wi-Fi name is only read for
	// the selected interface, and only when a trusted list is configured.
	WiFi       bool   `json:"wifi"`
	SSID       string `json:"ssid"`
	SSIDSource string `json:"ssid_source,omitempty"`
	Trusted    bool   `json:"trusted"`
	Policy     string `json:"policy"`
	PolicyNote string `json:"policy_note,omitempty"`
}

func detectNetwork(cfg *Config) NetworkInfo {
	var info NetworkInfo
	ifaces, err := net.Interfaces()
	if err != nil {
		info.Message = "无法枚举网络接口：" + err.Error()
		return info
	}
	gateways, defaults := readRoutes()
	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		ii := InterfaceInfo{Name: ifc.Name, Type: classifyInterface(ifc.Name), MAC: ifc.HardwareAddr.String()}
		ii.Up = ifc.Flags&net.FlagUp != 0
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP
			if ip.To4() != nil {
				ii.IPv4 = append(ii.IPv4, ip.String())
			} else {
				ii.IPv6 = append(ii.IPv6, ip.String())
			}
		}
		ii.Gateway = gateways[ifc.Name]
		ii.Default = defaults[ifc.Name]
		sort.Strings(ii.IPv4)
		sort.Strings(ii.IPv6)
		info.Interfaces = append(info.Interfaces, ii)
	}
	sort.Slice(info.Interfaces, func(i, j int) bool {
		return interfaceRank(info.Interfaces[i]) < interfaceRank(info.Interfaces[j])
	})

	selected, msg := selectInterface(cfg, info.Interfaces)
	info.Message = msg
	for _, ii := range info.Interfaces {
		if ii.Name == selected {
			info.Selected = ii.Name
			info.WiFi = ii.Type == "wifi"
			if len(ii.IPv4) > 0 {
				info.IPv4 = ii.IPv4[0]
				info.Ready = true
			}
			info.IPv6 = ii.IPv6
			info.Gateway = ii.Gateway
			break
		}
	}
	if info.Ready {
		info.DNS = readDNS()
	}
	if info.WiFi && len(cfg.Network.TrustedSSIDs) > 0 {
		info.SSID, info.SSIDSource = currentSSID()
	}
	info.Policy, info.PolicyNote, info.Trusted = exposurePolicy(cfg, info)
	return info
}

func interfaceRank(i InterfaceInfo) int {
	if !i.Up {
		return 90
	}
	if len(i.IPv4) == 0 {
		return 80
	}
	switch i.Type {
	case "wifi":
		return 10
	case "ethernet":
		return 20
	case "usb":
		return 30
	case "vpn":
		return 40
	case "cellular":
		return 60
	case "other":
		return 50
	case "loopback":
		return 70
	}
	return 55
}

func selectInterface(cfg *Config, ifaces []InterfaceInfo) (string, string) {
	if cfg == nil {
		return "", "没有配置"
	}
	want := cfg.Network.Interface
	if want != "" && want != "auto" {
		for _, ii := range ifaces {
			if ii.Name != want {
				continue
			}
			if !ii.Up {
				return "", fmt.Sprintf("接口 %s 未启用", want)
			}
			if len(ii.IPv4) == 0 {
				return "", fmt.Sprintf("接口 %s 没有 IPv4 地址", want)
			}
			return want, ""
		}
		return "", fmt.Sprintf("找不到接口 %s", want)
	}
	var candidates []InterfaceInfo
	for _, ii := range ifaces {
		if ii.Type == "loopback" || !ii.Up || len(ii.IPv4) == 0 {
			continue
		}
		if ii.Type == "cellular" && !cfg.Network.AllowCellular {
			continue
		}
		if ii.Type == "vpn" {
			// VPNs are never selected automatically: binding SMB to a VPN
			// tunnel is an explicit choice through network.interface.
			continue
		}
		if !hasUsableIPv4(ii.IPv4) {
			continue
		}
		candidates = append(candidates, ii)
	}
	if len(candidates) == 0 {
		if cfg.Network.AllowCellular {
			return "", "没有已启用且带 IPv4 的局域网接口"
		}
		return "", "没有已启用且带私网 IPv4 的 Wi-Fi/以太网/USB 接口（默认排除蜂窝网络）"
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Default != candidates[j].Default {
			return candidates[i].Default
		}
		return interfaceRank(candidates[i]) < interfaceRank(candidates[j])
	})
	return candidates[0].Name, ""
}

func hasUsableIPv4(ips []string) bool {
	for _, s := range ips {
		ip := net.ParseIP(s)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		return true
	}
	return false
}

func classifyInterface(name string) string {
	lower := strings.ToLower(name)
	switch {
	case lower == "lo":
		return "loopback"
	case strings.HasPrefix(lower, "wlan"), strings.HasPrefix(lower, "swlan"), strings.HasPrefix(lower, "ap"), strings.HasPrefix(lower, "wifi"):
		return "wifi"
	case strings.HasPrefix(lower, "rmnet"), strings.HasPrefix(lower, "ccmni"), strings.HasPrefix(lower, "pdp"):
		return "cellular"
	case strings.HasPrefix(lower, "tun"), strings.HasPrefix(lower, "tap"), strings.HasPrefix(lower, "ppp"),
		strings.HasPrefix(lower, "wg"), strings.HasPrefix(lower, "vgate"), strings.HasPrefix(lower, "v4-"),
		strings.HasPrefix(lower, "clat"):
		return "vpn"
	case strings.HasPrefix(lower, "usb"), strings.HasPrefix(lower, "rndis"):
		return "usb"
	case strings.HasPrefix(lower, "eth"):
		return "ethernet"
	}
	if _, err := os.Stat("/sys/class/net/" + name + "/wireless"); err == nil {
		return "wifi"
	}
	if target, err := os.Readlink("/sys/class/net/" + name); err == nil && strings.Contains(strings.ToLower(target), "usb") {
		return "usb"
	}
	return "other"
}

// readRoutes parses /proc/net/route. Android does not guarantee the `ip`
// command exists or is usable from every domain, while /proc/net/route is
// available to root and shell.
func readRoutes() (map[string]string, map[string]bool) {
	gateways := map[string]string{}
	defaults := map[string]bool{}
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return gateways, defaults
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		iface, destHex, gwHex := fields[0], fields[1], fields[2]
		gw := parseRouteHex(gwHex)
		if gw != "" {
			gateways[iface] = gw
		}
		if destHex == "00000000" {
			defaults[iface] = true
		}
	}
	return gateways, defaults
}

func parseRouteHex(h string) string {
	if len(h) != 8 || h == "00000000" {
		return ""
	}
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 4 {
		return ""
	}
	// /proc/net/route stores the address in host (little-endian) order.
	return net.IPv4(b[3], b[2], b[1], b[0]).String()
}

func readDNS() []string {
	var out []string
	if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[0] == "nameserver" {
				out = append(out, f[1])
			}
		}
	}
	if len(out) == 0 {
		for _, prop := range []string{"net.dns1", "net.dns2"} {
			ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
			cmd := exec.CommandContext(ctx, "/system/bin/getprop", prop)
			b, err := cmd.Output()
			cancel()
			if err == nil {
				v := strings.TrimSpace(string(b))
				if v != "" && net.ParseIP(v) != nil {
					out = append(out, v)
				}
			}
		}
	}
	return out
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 10 {
			return true
		}
		if v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31 {
			return true
		}
		if v4[0] == 192 && v4[1] == 168 {
			return true
		}
		return false
	}
	// IPv6 unique local addresses fc00::/7.
	return len(ip) == net.IPv6len && (ip[0]&0xfe) == 0xfc
}

func isPublicRoutable(ip net.IP) bool {
	if ip == nil || isPrivateIP(ip) {
		return false
	}
	return !ip.IsUnspecified() && !ip.IsMulticast()
}

// NetworkWatcher emits a debounced event when the selected interface or its
// addresses change. Polling is intentionally low-frequency; SMB does not need
// sub-second reaction and a busy loop would waste battery.
type NetworkWatcher struct {
	cfg      *Config
	Events   chan struct{}
	stop     chan struct{}
	done     chan struct{}
	lastFP   string
	debounce time.Duration
}

func NewNetworkWatcher(cfg *Config) *NetworkWatcher {
	settle := time.Duration(cfg.Network.SettleSeconds) * time.Second
	if settle <= 0 {
		settle = 5 * time.Second
	}
	return &NetworkWatcher{
		cfg:      cfg,
		Events:   make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		debounce: settle,
	}
}

func fingerprint(info NetworkInfo) string {
	if info.Selected == "" {
		return "none"
	}
	return info.Selected + "|" + info.IPv4 + "|" + strings.Join(info.IPv6, ",") +
		"|" + info.SSID + "|" + info.Policy
}

func (w *NetworkWatcher) Start() {
	w.lastFP = fingerprint(detectNetwork(w.cfg))
	go func() {
		defer close(w.done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		var pending *time.Timer
		var pendingC <-chan time.Time
		for {
			select {
			case <-w.stop:
				return
			case <-ticker.C:
				fp := fingerprint(detectNetwork(w.cfg))
				if fp == w.lastFP {
					continue
				}
				w.lastFP = fp
				if pending != nil {
					pending.Stop()
				}
				pending = time.NewTimer(w.debounce)
				pendingC = pending.C
			case <-pendingC:
				pendingC = nil
				select {
				case w.Events <- struct{}{}:
				default:
				}
			}
		}
	}()
}

func (w *NetworkWatcher) Stop() {
	close(w.stop)
	<-w.done
}

var _ = strconv.Itoa
