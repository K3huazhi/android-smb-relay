package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

type ControlServer struct {
	d   *Daemon
	srv *http.Server
	ln  net.Listener
}

func NewControlServer(d *Daemon) *ControlServer { return &ControlServer{d: d} }

func (c *ControlServer) Start() error {
	path := unixSocketPath(c.d.dataDir)
	if err := ensureSocketDir(path); err != nil {
		return err
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("listen unix %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ctl/status", c.root(c.handleStatus))
	mux.HandleFunc("GET /ctl/network", c.root(c.handleNetwork))
	mux.HandleFunc("GET /ctl/users", c.root(c.handleUsers))
	mux.HandleFunc("GET /ctl/credentials", c.root(c.handleCredentials))
	mux.HandleFunc("GET /ctl/config", c.root(c.handleConfig))
	mux.HandleFunc("GET /ctl/logs", c.root(c.handleLogs))
	mux.HandleFunc("POST /ctl/start", c.root(c.handleStart))
	mux.HandleFunc("POST /ctl/stop", c.root(c.handleStop))
	mux.HandleFunc("POST /ctl/restart", c.root(c.handleRestart))
	mux.HandleFunc("POST /ctl/reload", c.root(c.handleReload))
	mux.HandleFunc("POST /ctl/passwd", c.root(c.handlePasswd))
	mux.HandleFunc("POST /ctl/webui-password", c.root(c.handleWebUIPassword))
	mux.HandleFunc("POST /ctl/webui-start", c.root(c.handleWebUIStart))
	mux.HandleFunc("POST /ctl/webui-stop", c.root(c.handleWebUIStop))
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		ConnContext:       connContext,
	}
	c.srv = srv
	c.ln = ln
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			c.d.log.Errorf("ctl", "control socket stopped: %v", err)
		}
	}()
	return nil
}

func (c *ControlServer) root(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if uid := peerUID(r); uid != 0 {
			writeError(w, http.StatusForbidden, "forbidden", "control socket is root-only")
			return
		}
		next(w, r)
	}
}

func (c *ControlServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, c.d.Status())
}

func (c *ControlServer) handleNetwork(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, detectNetwork(c.d.Config()))
}

func (c *ControlServer) handleUsers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, c.d.Config().Users)
}

func (c *ControlServer) handleCredentials(w http.ResponseWriter, r *http.Request) {
	content, err := c.d.readCredentials()
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{"exists": false, "content": ""})
			return
		}
		writeError(w, http.StatusInternalServerError, "credentials", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"exists": true, "content": content})
}

func (c *ControlServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	cfg := c.d.Config()
	writeJSON(w, http.StatusOK, map[string]any{"path": c.d.cfgPath, "config": cfg})
}

func (c *ControlServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "asrd"
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	lines, err := c.d.logTail(source, limit, r.URL.Query().Get("level"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "logs", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": source, "lines": lines})
}

func (c *ControlServer) handleStart(w http.ResponseWriter, r *http.Request) {
	if err := c.d.StartSMB(); err != nil {
		writeError(w, http.StatusBadRequest, "service", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *ControlServer) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := c.d.StopSMB(); err != nil {
		writeError(w, http.StatusInternalServerError, "service", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *ControlServer) handleRestart(w http.ResponseWriter, r *http.Request) {
	if err := c.d.RestartSMB(); err != nil {
		writeError(w, http.StatusInternalServerError, "service", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *ControlServer) handleReload(w http.ResponseWriter, r *http.Request) {
	if err := c.d.ReloadSMB(); err != nil {
		writeError(w, http.StatusInternalServerError, "service", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *ControlServer) handlePasswd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req, 8192); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := validateSMBPassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "password", err.Error())
		return
	}
	cfg := c.d.Config()
	if _, idx := cfg.userByName(req.Name); idx < 0 {
		writeError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if err := c.d.samba.SetUserPassword(req.Name, req.Password); err != nil {
		writeError(w, http.StatusInternalServerError, "samba", err.Error())
		return
	}
	c.d.ClearInitialCredentials()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *ControlServer) handleWebUIStart(w http.ResponseWriter, r *http.Request) {
	if err := c.d.StartWebUI(); err != nil {
		writeError(w, http.StatusInternalServerError, "webui", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *ControlServer) handleWebUIStop(w http.ResponseWriter, r *http.Request) {
	c.d.StopWebUI()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *ControlServer) handleWebUIPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req, 8192); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := c.d.SetWebUIPassword("", req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "password", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// CLI client
// ---------------------------------------------------------------------------

type ControlClient struct {
	dataDir string
	http    *http.Client
}

func NewControlClient(dataDir string) *ControlClient {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", unixSocketPath(dataDir))
		},
		DisableKeepAlives: true,
	}
	return &ControlClient{dataDir: dataDir, http: &http.Client{Transport: tr, Timeout: 30 * time.Second}}
}

func (c *ControlClient) do(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(data)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, "http://unix"+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach daemon (is it running?): %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		var e apiErrorBody
		if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
			return fmt.Errorf("%s", e.Error.Message)
		}
		return fmt.Errorf("daemon returned HTTP %d", resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *ControlClient) Status() (Status, error) {
	var st Status
	err := c.do("GET", "/ctl/status", nil, &st)
	return st, err
}
