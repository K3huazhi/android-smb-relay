//go:build !linux

package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func setProcessGroup(cmd *exec.Cmd) {}

func killGroup(pid int, sig syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(sig)
}

func sendSignal(pid int, sig syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(sig)
}

func processAlive(pid int) bool { return pid > 0 }

type connKey struct{}

func connContext(ctx context.Context, c net.Conn) context.Context { return ctx }

func peerUID(r *http.Request) int { return 0 }

func unixSocketPath(dataDir string) string {
	return filepath.Join(dataDir, "run", "asr.sock")
}

func ensureSocketDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o700)
}
