package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type API struct {
	d          *Daemon
	webuiDir   string
	loginLimit *LoginLimiter
}

func NewAPIHandler(d *Daemon) http.Handler {
	a := &API{d: d, webuiDir: d.webuiDir, loginLimit: d.limiter}
	mux := http.NewServeMux()

	// Public endpoints.
	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("GET /api/auth/status", a.handleAuthStatus)
	mux.HandleFunc("POST /api/auth/login", a.handleLogin)

	// Authenticated endpoints.
	mux.HandleFunc("POST /api/auth/logout", a.authed(a.handleLogout))
	mux.HandleFunc("POST /api/auth/password", a.authed(a.handlePasswordChange))
	mux.HandleFunc("GET /api/status", a.authed(a.handleStatus))
	mux.HandleFunc("GET /api/network", a.authed(a.handleNetwork))
	mux.HandleFunc("GET /api/system", a.authed(a.handleSystem))

	mux.HandleFunc("GET /api/shares", a.authed(a.handleSharesList))
	mux.HandleFunc("POST /api/shares", a.authed(a.csrf(a.handleShareCreate)))
	mux.HandleFunc("PUT /api/shares/{id}", a.authed(a.csrf(a.handleShareUpdate)))
	mux.HandleFunc("DELETE /api/shares/{id}", a.authed(a.csrf(a.handleShareDelete)))
	mux.HandleFunc("GET /api/fs/list", a.authed(a.handleFSList))

	mux.HandleFunc("GET /api/users", a.authed(a.handleUsersList))
	mux.HandleFunc("POST /api/users", a.authed(a.csrf(a.handleUserCreate)))
	mux.HandleFunc("PUT /api/users/{name}", a.authed(a.csrf(a.handleUserUpdate)))
	mux.HandleFunc("DELETE /api/users/{name}", a.authed(a.csrf(a.handleUserDelete)))

	mux.HandleFunc("POST /api/service/start", a.authed(a.csrf(a.handleServiceStart)))
	mux.HandleFunc("POST /api/service/stop", a.authed(a.csrf(a.handleServiceStop)))
	mux.HandleFunc("POST /api/service/restart", a.authed(a.csrf(a.handleServiceRestart)))
	mux.HandleFunc("POST /api/service/reload", a.authed(a.csrf(a.handleServiceReload)))

	mux.HandleFunc("GET /api/logs", a.authed(a.handleLogs))
	mux.HandleFunc("GET /api/settings", a.authed(a.handleSettingsGet))
	mux.HandleFunc("PUT /api/settings", a.authed(a.csrf(a.handleSettingsPut)))

	// Static WebUI. Unknown /api paths must be JSON 404.
	mux.HandleFunc("/", a.handleStatic)
	return a.securityHeaders(a.lanGuard(mux))
}

func (a *API) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
				"connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (a *API) lanGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := a.d.Config()
		if cfg.WebUI.LANOnly {
			ip := clientIP(r)
			if ip != nil && !isPrivateIP(ip) {
				writeError(w, http.StatusForbidden, "lan_only", "access is restricted to private LAN addresses")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "unix" || host == "" {
		return net.ParseIP("127.0.0.1")
	}
	return net.ParseIP(host)
}

func (a *API) session(r *http.Request) *Session {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	return a.d.sessions.Get(c.Value)
}

func (a *API) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.d.Config().WebUI.Auth {
			next(w, r)
			return
		}
		if a.session(r) == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if a.d.MustChangePassword() && r.URL.Path != "/api/auth/password" && r.URL.Path != "/api/auth/logout" {
			writeError(w, http.StatusForbidden, "password_change_required", "首次登录必须修改管理员密码")
			return
		}
		next(w, r)
	}
}

func (a *API) csrf(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.d.Config().WebUI.Auth {
			next(w, r)
			return
		}
		sess := a.session(r)
		if sess == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		// Same-origin check when the browser supplies Origin.
		if origin := r.Header.Get("Origin"); origin != "" {
			if u, err := url.Parse(origin); err != nil || !strings.EqualFold(u.Host, r.Host) {
				writeError(w, http.StatusForbidden, "origin", "cross-origin request rejected")
				return
			}
		}
		token := r.Header.Get("X-CSRF-Token")
		if token == "" || token != sess.CSRF {
			writeError(w, http.StatusForbidden, "csrf", "invalid CSRF token")
			return
		}
		next(w, r)
	}
}

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": appVersion})
}

func (a *API) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	cfg := a.d.Config()
	sess := a.session(r)
	mustChange := cfg.WebUI.Auth && a.d.MustChangePassword()
	out := map[string]any{
		"auth_required":        cfg.WebUI.Auth,
		"authenticated":        !cfg.WebUI.Auth || (sess != nil && !mustChange),
		"must_change_password": mustChange,
		"admin_user":           a.d.AdminUser(),
	}
	if sess != nil {
		out["expires_at"] = sess.ExpiresAt
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	cfg := a.d.Config()
	if !cfg.WebUI.Auth {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "csrf_token": ""})
		return
	}
	ip := clientIP(r)
	ipKey := "unknown"
	if ip != nil {
		ipKey = ip.String()
	}
	if ok, retry := a.loginLimit.Allow(ipKey); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many failed attempts, try again later")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req, 4096); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Username != a.d.AdminUser() || !a.d.CheckPassword(req.Password) {
		a.loginLimit.Fail(ipKey)
		a.d.log.Warnf("auth", "failed WebUI login from %s", ipKey)
		time.Sleep(300 * time.Millisecond)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	a.loginLimit.Success(ipKey)
	// Rotate any session that was already presented.
	if old := a.session(r); old != nil {
		a.d.sessions.Delete(old.Token)
	}
	sess := a.d.sessions.Create(req.Username, ipKey)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sess.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(time.Until(sess.ExpiresAt).Seconds()),
	})
	a.d.log.Infof("auth", "WebUI login from %s", ipKey)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated":        true,
		"must_change_password": a.d.MustChangePassword(),
		"csrf_token":           sess.CSRF,
		"expires_at":           sess.ExpiresAt,
	})
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sess := a.session(r); sess != nil {
		a.d.sessions.Delete(sess.Token)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &req, 4096); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := a.d.SetWebUIPassword(req.CurrentPassword, req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, "password", err.Error())
		return
	}
	// Invalidate all sessions; the caller must log in again.
	if sess := a.session(r); sess != nil {
		a.d.sessions.Delete(sess.Token)
	}
	a.d.sessions.DeleteOtherThan("")
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "relogin": true})
}

func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.d.Status())
}

func (a *API) handleNetwork(w http.ResponseWriter, r *http.Request) {
	cfg := a.d.Config()
	writeJSON(w, http.StatusOK, detectNetwork(cfg))
}

func (a *API) handleSystem(w http.ResponseWriter, r *http.Request) {
	st := a.d.Status()
	writeJSON(w, http.StatusOK, st.System)
}

func (a *API) handleSharesList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.d.Status().Shares)
}

type shareRequest struct {
	Name       *string `json:"name"`
	Path       *string `json:"path"`
	Comment    *string `json:"comment"`
	ReadOnly   *bool   `json:"read_only"`
	GuestOK    *bool   `json:"guest_ok"`
	Hidden     *bool   `json:"hidden"`
	Enabled    *bool   `json:"enabled"`
	UploadOnly *bool   `json:"upload_only"`
}

func (a *API) handleShareCreate(w http.ResponseWriter, r *http.Request) {
	var req shareRequest
	if err := decodeJSON(w, r, &req, 16384); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Name == nil || req.Path == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "name and path are required")
		return
	}
	if req.UploadOnly != nil && *req.UploadOnly {
		writeError(w, http.StatusBadRequest, "unsupported", "upload-only shares are not supported; use a separate directory and SMB user permissions instead")
		return
	}
	cfg := a.d.Config()
	clean, err := ValidateSharePath(cfg, *req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "path", err.Error())
		return
	}
	id := slugID(*req.Name)
	for i := 2; ; i++ {
		if _, idx := cfg.shareByID(id); idx < 0 {
			break
		}
		id = fmt.Sprintf("%s-%d", slugID(*req.Name), i)
	}
	s := Share{ID: id, Name: *req.Name, Path: clean, Enabled: true}
	if req.Comment != nil {
		s.Comment = *req.Comment
	}
	if req.ReadOnly != nil {
		s.ReadOnly = *req.ReadOnly
	}
	if req.GuestOK != nil {
		s.GuestOK = *req.GuestOK
	}
	if req.Hidden != nil {
		s.Hidden = *req.Hidden
	}
	if req.Enabled != nil {
		s.Enabled = *req.Enabled
	}
	err = a.d.UpdateConfig(func(c *Config) error {
		c.Shares = append(c.Shares, s)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "config", err.Error())
		return
	}
	a.d.applySMBOnly()
	writeJSON(w, http.StatusCreated, s)
}

func (a *API) handleShareUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req shareRequest
	if err := decodeJSON(w, r, &req, 16384); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.UploadOnly != nil && *req.UploadOnly {
		writeError(w, http.StatusBadRequest, "unsupported", "upload-only shares are not supported")
		return
	}
	var updated Share
	err := a.d.UpdateConfig(func(c *Config) error {
		s, idx := c.shareByID(id)
		if idx < 0 {
			return errNotFound
		}
		if req.Name != nil {
			s.Name = *req.Name
		}
		if req.Path != nil {
			clean, err := ValidateSharePath(c, *req.Path)
			if err != nil {
				return err
			}
			s.Path = clean
		}
		if req.Comment != nil {
			s.Comment = *req.Comment
		}
		if req.ReadOnly != nil {
			s.ReadOnly = *req.ReadOnly
		}
		if req.GuestOK != nil {
			s.GuestOK = *req.GuestOK
		}
		if req.Hidden != nil {
			s.Hidden = *req.Hidden
		}
		if req.Enabled != nil {
			s.Enabled = *req.Enabled
		}
		updated = *s
		return nil
	})
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "share not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "config", err.Error())
		return
	}
	a.d.applySMBOnly()
	writeJSON(w, http.StatusOK, updated)
}

func (a *API) handleShareDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := a.d.UpdateConfig(func(c *Config) error {
		_, idx := c.shareByID(id)
		if idx < 0 {
			return errNotFound
		}
		c.Shares = append(c.Shares[:idx], c.Shares[idx+1:]...)
		return nil
	})
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "share not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "config", err.Error())
		return
	}
	// Removing a share never touches the files on disk.
	a.d.applySMBOnly()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleFSList(w http.ResponseWriter, r *http.Request) {
	cfg := a.d.Config()
	raw := r.URL.Query().Get("path")
	if raw == "" {
		raw = cfg.Storage.Root
	}
	clean, err := ValidateSharePath(cfg, raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "path", err.Error())
		return
	}
	entries, err := os.ReadDir(RuntimePath(cfg, clean))
	if err != nil {
		writeError(w, http.StatusBadRequest, "path", err.Error())
		return
	}
	type entry struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		IsDir bool   `json:"is_dir"`
		Size  int64  `json:"size"`
	}
	out := make([]entry, 0, len(entries))
	for i, e := range entries {
		if i >= 500 {
			break
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, entry{Name: e.Name(), Path: filepath.Join(clean, e.Name()), IsDir: e.IsDir(), Size: info.Size()})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{"path": clean, "parent": filepath.Dir(clean), "entries": out})
}

func (a *API) handleUsersList(w http.ResponseWriter, r *http.Request) {
	cfg := a.d.Config()
	writeJSON(w, http.StatusOK, cfg.Users)
}

func (a *API) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		Enabled  *bool  `json:"enabled"`
	}
	if err := decodeJSON(w, r, &req, 8192); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := validateSMBPassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "password", err.Error())
		return
	}
	cfg := a.d.Config()
	if _, idx := cfg.userByName(req.Name); idx >= 0 {
		writeError(w, http.StatusConflict, "exists", "user already exists")
		return
	}
	if err := a.d.samba.AddUser(req.Name, req.Password); err != nil {
		writeError(w, http.StatusInternalServerError, "samba", err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	now := time.Now().Unix()
	u := User{Name: req.Name, Enabled: enabled, CreatedAt: now, UpdatedAt: now}
	if err := a.d.UpdateConfig(func(c *Config) error { c.Users = append(c.Users, u); return nil }); err != nil {
		_ = a.d.samba.DeleteUser(req.Name)
		writeError(w, http.StatusBadRequest, "config", err.Error())
		return
	}
	if !enabled {
		_ = a.d.samba.SetUserEnabled(req.Name, false)
	}
	a.d.applySMBOnly()
	writeJSON(w, http.StatusCreated, u)
}

func (a *API) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Password *string `json:"password"`
		Enabled  *bool   `json:"enabled"`
	}
	if err := decodeJSON(w, r, &req, 8192); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	cfg := a.d.Config()
	if _, idx := cfg.userByName(name); idx < 0 {
		writeError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if req.Password != nil {
		if err := validateSMBPassword(*req.Password); err != nil {
			writeError(w, http.StatusBadRequest, "password", err.Error())
			return
		}
		if err := a.d.samba.SetUserPassword(name, *req.Password); err != nil {
			writeError(w, http.StatusInternalServerError, "samba", err.Error())
			return
		}
		a.d.ClearInitialCredentials()
	}
	if req.Enabled != nil {
		if err := a.d.samba.SetUserEnabled(name, *req.Enabled); err != nil {
			writeError(w, http.StatusInternalServerError, "samba", err.Error())
			return
		}
	}
	var updated User
	err := a.d.UpdateConfig(func(c *Config) error {
		u, idx := c.userByName(name)
		if idx < 0 {
			return errNotFound
		}
		if req.Enabled != nil {
			u.Enabled = *req.Enabled
		}
		u.UpdatedAt = time.Now().Unix()
		updated = *u
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "config", err.Error())
		return
	}
	a.d.applySMBOnly()
	writeJSON(w, http.StatusOK, updated)
}

func (a *API) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cfg := a.d.Config()
	if _, idx := cfg.userByName(name); idx < 0 {
		writeError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if err := a.d.samba.DeleteUser(name); err != nil {
		writeError(w, http.StatusInternalServerError, "samba", err.Error())
		return
	}
	err := a.d.UpdateConfig(func(c *Config) error {
		_, idx := c.userByName(name)
		if idx < 0 {
			return errNotFound
		}
		c.Users = append(c.Users[:idx], c.Users[idx+1:]...)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "config", err.Error())
		return
	}
	a.d.applySMBOnly()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleServiceStart(w http.ResponseWriter, r *http.Request) {
	if err := a.d.StartSMB(); err != nil {
		writeError(w, http.StatusBadRequest, "service", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, a.d.Status().SMB)
}

func (a *API) handleServiceStop(w http.ResponseWriter, r *http.Request) {
	if err := a.d.StopSMB(); err != nil {
		writeError(w, http.StatusInternalServerError, "service", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.d.Status().SMB)
}

func (a *API) handleServiceRestart(w http.ResponseWriter, r *http.Request) {
	if err := a.d.RestartSMB(); err != nil {
		writeError(w, http.StatusInternalServerError, "service", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, a.d.Status().SMB)
}

func (a *API) handleServiceReload(w http.ResponseWriter, r *http.Request) {
	if err := a.d.ReloadSMB(); err != nil {
		writeError(w, http.StatusInternalServerError, "service", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.d.Status().SMB)
}

func (a *API) handleLogs(w http.ResponseWriter, r *http.Request) {
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
	level := r.URL.Query().Get("level")
	lines, err := a.d.logTail(source, limit, level)
	if err != nil {
		writeError(w, http.StatusBadRequest, "logs", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": source, "lines": lines})
}

func (a *API) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	c := a.d.Config()
	writeJSON(w, http.StatusOK, map[string]any{
		"smb": c.SMB, "webui": c.WebUI, "network": c.Network,
		"startup": c.Startup, "storage": c.Storage, "log": c.Log,
	})
}

func (a *API) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	var patch struct {
		SMB     *SMBConfig   `json:"smb"`
		WebUI   *WebUIConfig `json:"webui"`
		Network *NetConfig   `json:"network"`
		Startup *Startup     `json:"startup"`
		Storage *Storage     `json:"storage"`
		Log     *LogConfig   `json:"log"`
	}
	if err := decodeJSON(w, r, &patch, 65536); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	err := a.d.UpdateConfig(func(c *Config) error {
		if patch.SMB != nil {
			c.SMB = *patch.SMB
		}
		if patch.WebUI != nil {
			c.WebUI = *patch.WebUI
		}
		if patch.Network != nil {
			c.Network = *patch.Network
		}
		if patch.Startup != nil {
			c.Startup = *patch.Startup
		}
		if patch.Storage != nil {
			c.Storage = *patch.Storage
		}
		if patch.Log != nil {
			c.Log = *patch.Log
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "config", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": a.d.Status()})
}

func (a *API) handleStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "not_found", "unknown API endpoint")
		return
	}
	p := path.Clean(r.URL.Path)
	if p == "/" || p == "/index.html" {
		a.serveFile(w, r, "index.html", "text/html; charset=utf-8", false)
		return
	}
	ext := strings.ToLower(path.Ext(p))
	allowed := map[string]string{
		".html":  "text/html; charset=utf-8",
		".css":   "text/css; charset=utf-8",
		".js":    "text/javascript; charset=utf-8",
		".svg":   "image/svg+xml",
		".png":   "image/png",
		".ico":   "image/x-icon",
		".woff2": "font/woff2",
		".ps1":   "text/plain; charset=utf-8",
		".cmd":   "text/plain; charset=utf-8",
		".bat":   "text/plain; charset=utf-8",
		".sh":    "text/x-shellscript; charset=utf-8",
	}
	ct, ok := allowed[ext]
	if !ok {
		http.NotFound(w, r)
		return
	}
	a.serveFile(w, r, strings.TrimPrefix(p, "/"), ct, true)
}

func (a *API) serveFile(w http.ResponseWriter, r *http.Request, name, contentType string, cache bool) {
	root, err := filepath.Abs(a.webuiDir)
	if err != nil {
		http.Error(w, "webui unavailable", http.StatusServiceUnavailable)
		return
	}
	full := filepath.Join(root, filepath.FromSlash(name))
	if !pathWithin(root, full) {
		http.NotFound(w, r)
		return
	}
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	// The WebUI is updated together with the module; never let a browser keep
	// an old module asset after an upgrade.
	_ = cache
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Content-Type", contentType)
	http.ServeFile(w, r, full)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

type apiErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, code int, errCode, msg string) {
	var body apiErrorBody
	body.Error.Code = errCode
	body.Error.Message = msg
	writeJSON(w, code, body)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		return errors.New("Content-Type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func slugID(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" {
		id = "share"
	}
	if len(id) > 32 {
		id = strings.TrimRight(id[:32], "-")
	}
	if id == "" {
		id = "share"
	}
	return id
}

func validateSMBPassword(pw string) error {
	// This deployment is explicitly a trusted-LAN setup; "admin" is allowed.
	if pw == "admin" {
		return nil
	}
	if len(pw) < 10 {
		return errors.New("SMB password must be at least 10 characters")
	}
	if len(pw) > 128 {
		return errors.New("SMB password must be at most 128 characters")
	}
	if strings.ContainsAny(pw, "\x00\r\n") {
		return errors.New("password contains control characters")
	}
	return nil
}
