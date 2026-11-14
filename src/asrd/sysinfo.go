package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type CPUStat struct {
	Percent float64 `json:"percent"`
	Cores   int     `json:"cores"`
}

type MemoryStat struct {
	TotalBytes     uint64  `json:"total_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	Percent        float64 `json:"percent"`
}

type StorageStat struct {
	Path       string  `json:"path"`
	TotalBytes uint64  `json:"total_bytes"`
	FreeBytes  uint64  `json:"free_bytes"`
	UsedBytes  uint64  `json:"used_bytes"`
	Percent    float64 `json:"percent"`
}

type Throughput struct {
	UploadBps     float64 `json:"upload_bps"`
	DownloadBps   float64 `json:"download_bps"`
	UploadTotal   uint64  `json:"upload_total"`
	DownloadTotal uint64  `json:"download_total"`
}

type SystemInfo struct {
	CPU        CPUStat     `json:"cpu"`
	Memory     MemoryStat  `json:"memory"`
	UptimeS    int64       `json:"uptime_s"`
	Storage    StorageStat `json:"storage"`
	SambaProcs int         `json:"samba_processes"`
	Throughput Throughput  `json:"throughput"`
	Clients    int         `json:"clients"`
	ClientIPs  []string    `json:"client_ips"`
}

type SysMonitor struct {
	mu        sync.Mutex
	prevTotal uint64
	prevIdle  uint64
	prevRX    uint64
	prevTX    uint64
	prevTime  time.Time
	iface     string
	upTotal   uint64
	downTotal uint64
	lastUpBps float64
	lastDnBps float64
}

func NewSysMonitor() *SysMonitor { return &SysMonitor{} }

func (s *SysMonitor) SampleCPU() CPUStat {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return CPUStat{}
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return CPUStat{}
	}
	var total, idle uint64
	for i, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			continue
		}
		total += v
		if i == 3 || i == 4 { // idle + iowait
			idle += v
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cores := 0
	for _, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, "cpu") && len(l) > 3 && l[3] >= '0' && l[3] <= '9' {
			cores++
		}
	}
	prevTotal, prevIdle := s.prevTotal, s.prevIdle
	s.prevTotal, s.prevIdle = total, idle
	if prevTotal == 0 || total <= prevTotal {
		return CPUStat{Percent: 0, Cores: cores}
	}
	dt := total - prevTotal
	di := idle - prevIdle
	pct := 0.0
	if dt > 0 {
		pct = 100 * float64(dt-di) / float64(dt)
	}
	return CPUStat{Percent: round1(pct), Cores: cores}
}

func readMemory() MemoryStat {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return MemoryStat{}
	}
	defer f.Close()
	vals := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		vals[key] = v * 1024 // kB
	}
	total := vals["MemTotal"]
	avail := vals["MemAvailable"]
	if avail == 0 {
		avail = vals["MemFree"] + vals["Buffers"] + vals["Cached"]
	}
	used := uint64(0)
	if total > avail {
		used = total - avail
	}
	pct := 0.0
	if total > 0 {
		pct = 100 * float64(used) / float64(total)
	}
	return MemoryStat{TotalBytes: total, AvailableBytes: avail, UsedBytes: used, Percent: round1(pct)}
}

func readUptime() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(data))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return int64(v)
}

func readStorage(path string) StorageStat {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return StorageStat{Path: path}
	}
	total := st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	used := total - st.Bfree*uint64(st.Bsize)
	pct := 0.0
	if total > 0 {
		pct = 100 * float64(used) / float64(total)
	}
	return StorageStat{Path: path, TotalBytes: total, FreeBytes: free, UsedBytes: used, Percent: round1(pct)}
}

// SampleThroughput computes bytes/s and cumulative counters for the selected
// interface. RX is traffic from clients (upload), TX is to clients (download).
func (s *SysMonitor) SampleThroughput(iface string) Throughput {
	rx, tx := readInterfaceBytes(iface)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.iface != iface {
		s.iface = iface
		s.prevRX, s.prevTX, s.prevTime = rx, tx, now
		s.lastUpBps, s.lastDnBps = 0, 0
		return Throughput{UploadTotal: 0, DownloadTotal: 0}
	}
	if s.prevTime.IsZero() {
		s.prevTime = now
	}
	dt := now.Sub(s.prevTime).Seconds()
	if dt >= 0.5 {
		if rx >= s.prevRX {
			s.upTotal += rx - s.prevRX
			s.lastUpBps = float64(rx-s.prevRX) / dt
		}
		if tx >= s.prevTX {
			s.downTotal += tx - s.prevTX
			s.lastDnBps = float64(tx-s.prevTX) / dt
		}
		s.prevRX, s.prevTX, s.prevTime = rx, tx, now
	}
	return Throughput{
		UploadBps:     round1(s.lastUpBps),
		DownloadBps:   round1(s.lastDnBps),
		UploadTotal:   s.upTotal,
		DownloadTotal: s.downTotal,
	}
}

func readInterfaceBytes(iface string) (uint64, uint64) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		name := strings.TrimSpace(line[:i])
		if name != iface {
			continue
		}
		fields := strings.Fields(line[i+1:])
		if len(fields) < 9 {
			return 0, 0
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		return rx, tx
	}
	return 0, 0
}

// CountSMBConnections counts established TCP sessions to the SMB port. It
// uses /proc/net/tcp{,6}, available to root, instead of parsing localized
// smbstatus output.
func CountSMBConnections(port int) (int, []string) {
	portHex := fmt.Sprintf("%04X", port)
	remotes := map[string]bool{}
	lines := []string{}
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			if i == 0 {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 4 || fields[3] != "01" { // 01 = ESTABLISHED
				continue
			}
			local := fields[1]
			remote := fields[2]
			li := strings.LastIndex(local, ":")
			if li < 0 || !strings.EqualFold(local[li+1:], portHex) {
				continue
			}
			ri := strings.LastIndex(remote, ":")
			if ri < 0 {
				continue
			}
			ip := parseProcNetIP(remote[:ri])
			if ip == "" {
				continue
			}
			remotes[ip] = true
		}
	}
	for ip := range remotes {
		lines = append(lines, ip)
	}
	return len(lines), lines
}

func parseProcNetIP(h string) string {
	if len(h) == 8 { // IPv4, little-endian
		b := make([]byte, 4)
		for i := 0; i < 4; i++ {
			v, err := strconv.ParseUint(h[i*2:i*2+2], 16, 8)
			if err != nil {
				return ""
			}
			b[3-i] = byte(v)
		}
		return net.IPv4(b[0], b[1], b[2], b[3]).String()
	}
	if len(h) == 32 { // IPv6, four little-endian 32-bit words
		b := make([]byte, 16)
		for w := 0; w < 4; w++ {
			for i := 0; i < 4; i++ {
				v, err := strconv.ParseUint(h[w*8+i*2:w*8+i*2+2], 16, 8)
				if err != nil {
					return ""
				}
				b[w*4+3-i] = byte(v)
			}
		}
		return net.IP(b).String()
	}
	return ""
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
