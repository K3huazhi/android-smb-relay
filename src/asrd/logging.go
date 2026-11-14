package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// logLevels are ordered from most to least verbose.
var logLevels = map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}

// RotatingWriter appends to a file and rotates it when it grows beyond limit.
// Rotation is best-effort but never truncates the current log in place, so a
// reader always sees a complete line prefix.
type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxSize  int64
	maxFiles int
	f        *os.File
	size     int64
}

func NewRotatingWriter(path string, maxSizeKB, maxFiles int) (*RotatingWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	w := &RotatingWriter{
		path:     path,
		maxSize:  int64(maxSizeKB) * 1024,
		maxFiles: maxFiles,
	}
	if st, err := os.Stat(path); err == nil {
		w.size = st.Size()
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	w.f = f
	return w, nil
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(p)) > w.maxSize {
		w.rotateLocked()
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingWriter) rotateLocked() {
	_ = w.f.Close()
	// path.1 is newest; shift older files upwards.
	for i := w.maxFiles - 1; i >= 1; i-- {
		from := w.path
		if i > 1 {
			from = fmt.Sprintf("%s.%d", w.path, i-1)
		}
		to := fmt.Sprintf("%s.%d", w.path, i)
		_ = os.Rename(from, to)
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		// Keep the old descriptor path open if possible; re-open in append mode.
		f, _ = os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	}
	w.f = f
	w.size = 0
}

func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		return w.f.Close()
	}
	return nil
}

// Logger writes a stable, greppable format:
//
//	2026-11-14T22:00:00+08:00 [INFO] component: message
type Logger struct {
	mu    sync.Mutex
	w     *RotatingWriter
	level int
}

func NewLogger(path, level string, maxSizeKB, maxFiles int) (*Logger, error) {
	w, err := NewRotatingWriter(path, maxSizeKB, maxFiles)
	if err != nil {
		return nil, err
	}
	lv, ok := logLevels[level]
	if !ok {
		lv = logLevels["info"]
	}
	return &Logger{w: w, level: lv}, nil
}

func (l *Logger) logf(level, component, format string, args ...any) {
	if l == nil {
		return
	}
	lv, ok := logLevels[level]
	if !ok || lv < l.level {
		return
	}
	msg := fmt.Sprintf(format, args...)
	msg = strings.TrimRight(msg, "\r\n")
	line := fmt.Sprintf("%s [%s] %s: %s\n", time.Now().Format(time.RFC3339), strings.ToUpper(level), component, redactSecrets(msg))
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write([]byte(line))
}

func (l *Logger) Debugf(component, format string, args ...any) {
	l.logf("debug", component, format, args...)
}
func (l *Logger) Infof(component, format string, args ...any) {
	l.logf("info", component, format, args...)
}
func (l *Logger) Warnf(component, format string, args ...any) {
	l.logf("warn", component, format, args...)
}
func (l *Logger) Errorf(component, format string, args ...any) {
	l.logf("error", component, format, args...)
}

func (l *Logger) Close() { _ = l.w.Close() }

// redactSecrets is a last line of defence against accidentally logging a
// credential. Callers must still never log passwords in the first place.
func redactSecrets(s string) string {
	lower := strings.ToLower(s)
	for _, key := range []string{"password", "passwd", "secret", "token"} {
		idx := 0
		for {
			i := strings.Index(lower[idx:], key)
			if i < 0 {
				break
			}
			i += idx
			end := strings.IndexAny(s[i:], " \t\n,;\"'")
			if end < 0 {
				end = len(s) - i
			}
			// Only redact key=value/key: value shapes, not prose.
			rest := s[i+len(key) : i+end]
			if strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, ":") {
				s = s[:i+len(key)] + strings.Repeat("*", len(rest))
				lower = strings.ToLower(s)
			}
			idx = i + len(key)
			if idx >= len(s) {
				break
			}
		}
	}
	return s
}

// readLogTail returns at most limit recent lines, optionally filtered by level.
// It reads a bounded amount from the end of the file so a corrupted or huge
// log cannot exhaust memory.
func readLogTail(path string, limit int, level string) ([]string, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	const maxBytes = 512 * 1024
	start := st.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	buf := make([]byte, st.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && len(buf) > 0 {
		// A short read still gives useful tail data.
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if start > 0 && len(lines) > 0 {
		lines = lines[1:] // drop partial first line
	}
	want := 0
	if level != "" {
		want = logLevels[level]
	}
	out := make([]string, 0, limit)
	for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
		line := lines[i]
		if line == "" {
			continue
		}
		if level != "" {
			lv := parseLineLevel(line)
			if lv < want {
				continue
			}
		}
		out = append(out, line)
	}
	sort.SliceStable(out, func(i, j int) bool { return i > j })
	return out, nil
}

func parseLineLevel(line string) int {
	i := strings.Index(line, " [")
	if i < 0 {
		return 1
	}
	j := strings.Index(line[i+2:], "]")
	if j < 0 {
		return 1
	}
	if v, ok := logLevels[strings.ToLower(line[i+2:i+2+j])]; ok {
		return v
	}
	return 1
}
