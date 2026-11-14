package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type WebUIServer struct {
	d *Daemon

	mu      sync.Mutex
	srv     *http.Server
	ln      net.Listener
	cfg     WebUIConfig
	running bool
	lastErr string
}

func NewWebUIServer(d *Daemon) *WebUIServer {
	return &WebUIServer{d: d}
}

func (w *WebUIServer) Apply(cfg WebUIConfig) error {
	w.mu.Lock()
	if w.srv != nil && w.cfg == cfg {
		w.mu.Unlock()
		return nil
	}
	if !cfg.Enabled {
		old := w.srv
		w.srv, w.ln, w.running, w.cfg = nil, nil, false, cfg
		w.lastErr = ""
		w.mu.Unlock()
		if old != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = old.Shutdown(ctx)
			cancel()
		}
		return nil
	}
	addr := net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		w.lastErr = fmt.Sprintf("bind %s: %v", addr, err)
		w.mu.Unlock()
		return fmt.Errorf("WebUI bind %s: %w", addr, err)
	}
	handler := NewAPIHandler(w.d)
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 * 1024,
	}
	old := w.srv
	w.srv, w.ln, w.running, w.cfg = srv, ln, true, cfg
	w.lastErr = ""
	w.mu.Unlock()

	go func() {
		err := srv.Serve(ln)
		if err != nil && err != http.ErrServerClosed {
			w.mu.Lock()
			w.lastErr = err.Error()
			w.running = false
			w.mu.Unlock()
			w.d.log.Errorf("webui", "server stopped: %v", err)
		}
	}()
	if old != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = old.Shutdown(ctx)
		cancel()
	}
	return nil
}

func (w *WebUIServer) Close() {
	w.mu.Lock()
	srv := w.srv
	w.srv, w.ln, w.running = nil, nil, false
	w.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
}

func (w *WebUIServer) Status() WebUIStatus {
	w.mu.Lock()
	cfg, running, lastErr := w.cfg, w.running, w.lastErr
	w.mu.Unlock()
	if cfg.Port == 0 {
		cfg = w.d.Config().WebUI
	}
	st := WebUIStatus{Enabled: cfg.Enabled, Running: running, Port: cfg.Port, Bind: cfg.Bind, Error: lastErr}
	if !running || !cfg.Enabled {
		return st
	}
	hosts := []string{}
	switch cfg.Bind {
	case "127.0.0.1":
		hosts = append(hosts, "127.0.0.1")
	case "::1":
		hosts = append(hosts, "::1")
	case "0.0.0.0", "::", "":
		info := w.d.Network()
		if info.IPv4 != "" {
			hosts = append(hosts, info.IPv4)
		} else {
			for _, ii := range info.Interfaces {
				if ii.Type == "loopback" {
					continue
				}
				hosts = append(hosts, ii.IPv4...)
			}
		}
	default:
		hosts = append(hosts, cfg.Bind)
	}
	seen := map[string]bool{}
	for _, h := range hosts {
		u := "http://" + net.JoinHostPort(h, strconv.Itoa(cfg.Port))
		if seen[u] {
			continue
		}
		seen[u] = true
		st.URLs = append(st.URLs, u)
	}
	return st
}
