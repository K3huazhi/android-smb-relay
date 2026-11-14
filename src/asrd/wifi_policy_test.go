package main

import (
	"strings"
	"testing"
)

func TestParseSSID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "cmd wifi status",
			in: "Wifi is enabled\nWifi is connected to \"202-5G\"\n" +
				"WifiInfo: SSID: \"202-5G\", BSSID: cc:26:14:c3:96:bc, IP: /192.168.1.223\n",
			want: "202-5G",
		},
		{
			name: "dumpsys shape",
			in:   "WifiInfo: SSID: \"Home 2.4G\", BSSID: 00:11:22:33:44:55\n",
			want: "Home 2.4G",
		},
		{
			name: "disconnected",
			in:   "Wifi is enabled\nWifi is not connected to any network\n",
			want: "",
		},
		{
			name: "empty ssid",
			in:   `Wifi is connected to ""`,
			want: "",
		},
		{
			name: "chinese ssid",
			in:   `WifiInfo: SSID: "家里的网", misc: 1`,
			want: "家里的网",
		},
	}
	for _, tc := range cases {
		if got := parseSSID(tc.in); got != tc.want {
			t.Errorf("%s: parseSSID = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestNormalizeSSIDList(t *testing.T) {
	got := normalizeSSIDList([]string{" Home ", "Home", "", "Office,Home", "  "})
	want := []string{"Home", "Office"}
	if len(got) != len(want) {
		t.Fatalf("normalizeSSIDList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalizeSSIDList = %v, want %v", got, want)
		}
	}
	if out := normalizeSSIDList(nil); len(out) != 0 {
		t.Fatalf("nil input should stay empty, got %v", out)
	}
}

func TestExposurePolicy(t *testing.T) {
	base := &Config{}
	base.Network.UntrustedAction = "readonly"

	// No list configured: policy off.
	if policy, _, trusted := exposurePolicy(base, NetworkInfo{WiFi: true, SSID: "Cafe"}); policy != "allow" || !trusted {
		t.Fatalf("empty list should allow, got %s/trusted=%v", policy, trusted)
	}

	cfg := &Config{}
	cfg.Network.TrustedSSIDs = []string{"Home"}
	cfg.Network.UntrustedAction = "readonly"

	if policy, _, _ := exposurePolicy(cfg, NetworkInfo{WiFi: true, SSID: "Home"}); policy != "allow" {
		t.Fatalf("trusted SSID should allow, got %s", policy)
	}
	if policy, note, trusted := exposurePolicy(cfg, NetworkInfo{WiFi: true, SSID: "Cafe"}); policy != "readonly" || trusted || note == "" {
		t.Fatalf("untrusted SSID should be readonly, got %s/trusted=%v/note=%q", policy, trusted, note)
	}
	if policy, _, _ := exposurePolicy(cfg, NetworkInfo{WiFi: false, SSID: ""}); policy != "allow" {
		t.Fatalf("wired link should be allowed, got %s", policy)
	}
	if policy, _, _ := exposurePolicy(cfg, NetworkInfo{WiFi: true, SSID: ""}); policy != "allow" {
		t.Fatalf("unknown SSID fails open, got %s", policy)
	}

	cfg.Network.UntrustedAction = "stop"
	if policy, _, _ := exposurePolicy(cfg, NetworkInfo{WiFi: true, SSID: "Cafe"}); policy != "stop" {
		t.Fatalf("stop action expected, got %s", policy)
	}
}

func TestEffectiveConfigReadOnly(t *testing.T) {
	cfg := &Config{Shares: []Share{{Name: "Internal"}, {Name: "Media"}}}
	out := effectiveConfig(cfg, true)
	for _, sh := range out.Shares {
		if !sh.ReadOnly {
			t.Fatalf("share %s should be read-only", sh.Name)
		}
	}
	if cfg.Shares[0].ReadOnly {
		t.Fatalf("original config must not be modified")
	}
	if effectiveConfig(cfg, false) != cfg {
		t.Fatalf("read-only=false should return the same config pointer")
	}
}

func TestRenderConfigForcesReadOnly(t *testing.T) {
	cfg := defaultConfig()
	cfg.Storage.Root = "/tmp/share"
	body := RenderConfig(effectiveConfig(cfg, true), "", "/tmp/data")
	if !strings.Contains(body, "read only = yes") {
		t.Fatalf("forced read-only render missing \"read only = yes\":\n%s", body)
	}
	if strings.Contains(body, "read only = no") {
		t.Fatalf("forced read-only render still has a writable share")
	}
	// The stored config must be untouched.
	if cfg.Shares[0].ReadOnly {
		t.Fatalf("default config was modified")
	}
}
