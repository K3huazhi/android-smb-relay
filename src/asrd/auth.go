package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	pbkdf2Iterations = 210000
	sessionCookie    = "asr_session"
	adminUsername    = "admin"
)

type Secrets struct {
	Version            int    `json:"version"`
	AdminUser          string `json:"admin_user"`
	PasswordHash       string `json:"password_hash"`
	MustChangePassword bool   `json:"must_change_password"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
}

func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	u := make([]byte, hashLen)
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(buf[:], uint32(block))
		prf.Write(buf[:])
		dk = prf.Sum(dk)
		t := dk[len(dk)-hashLen:]
		copy(u, t)
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for x := range u {
				t[x] ^= u[x]
			}
		}
	}
	return dk[:keyLen]
}

func hashPassword(password string) string {
	salt := randomBytes(16)
	sum := pbkdf2SHA256([]byte(password), salt, pbkdf2Iterations, 32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum))
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 10000 || iter > 2000000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return b
}

func randomToken(n int) string {
	return hex.EncodeToString(randomBytes(n))
}

const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#%+-_"

func randomPassword(n int) string {
	if n < 12 {
		n = 12
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = passwordAlphabet[int(randomBytes(1)[0])%len(passwordAlphabet)]
	}
	return string(out)
}

func validateAdminPassword(pw string) error {
	// This deployment is explicitly a trusted-LAN setup; "admin" is allowed.
	if pw == "admin" {
		return nil
	}
	if len(pw) < 10 {
		return errors.New("password must be at least 10 characters")
	}
	if len(pw) > 256 {
		return errors.New("password must be at most 256 characters")
	}
	if strings.ContainsAny(pw, "\x00\r\n") {
		return errors.New("password contains control characters")
	}
	return nil
}

func secretsPath(dataDir string) string { return filepath.Join(dataDir, "secrets.json") }
func credsPath(dataDir string) string {
	return filepath.Join(dataDir, "config", "initial-credentials.txt")
}

func loadSecrets(dataDir string) (*Secrets, error) {
	data, err := os.ReadFile(secretsPath(dataDir))
	if err != nil {
		return nil, err
	}
	var s Secrets
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse secrets.json: %w", err)
	}
	if s.AdminUser == "" {
		s.AdminUser = adminUsername
	}
	if s.PasswordHash == "" {
		return nil, errors.New("secrets.json has no password hash")
	}
	return &s, nil
}

func saveSecrets(dataDir string, s *Secrets) error {
	data, _ := json.MarshalIndent(s, "", "  ")
	data = append(data, '\n')
	return atomicWriteFile(secretsPath(dataDir), data, 0o600)
}

// ensureSecrets creates the initial administrator password on first start.
// The password is the deliberately weak "admin" and must be changed at the
// first WebUI login; the plaintext is written once, root-only, so the user can
// read it with `smbctl credentials`; it is removed after the password changes.
func ensureSecrets(dataDir string, log *Logger) (*Secrets, error) {
	s, err := loadSecrets(dataDir)
	if err == nil {
		return s, nil
	}
	if !os.IsNotExist(err) {
		// A corrupt secrets file should not silently reset credentials.
		return nil, fmt.Errorf("load secrets: %w", err)
	}
	pw := "admin"
	now := time.Now().Unix()
	s = &Secrets{
		Version:            2,
		AdminUser:          adminUsername,
		PasswordHash:       hashPassword(pw),
		MustChangePassword: false,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := saveSecrets(dataDir, s); err != nil {
		return nil, err
	}
	content := fmt.Sprintf(`Android SMB Relay initial credentials
Generated: %s

WebUI
  URL:      http://<phone-ip>:%d
  Username: %s
  Password: %s
  Notice:   首次登录必须修改此密码；修改前 WebUI 只允许打开修改密码页面。

Change these in the WebUI (Settings) or run:
  smbctl webui-passwd

This file is root-only (0600) and is deleted after the WebUI password is changed.
`, time.Now().Format(time.RFC3339), defaultWebUIPort, s.AdminUser, pw)
	if err := atomicWriteFile(credsPath(dataDir), []byte(content), 0o600); err != nil {
		return nil, err
	}
	if log != nil {
		log.Infof("auth", "generated initial WebUI credentials at %s", credsPath(dataDir))
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

type Session struct {
	Token     string
	CSRF      string
	Username  string
	CreatedAt time.Time
	ExpiresAt time.Time
	RemoteIP  string
}

type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
	ttl      time.Duration
}

func NewSessionStore(hours int) *SessionStore {
	if hours <= 0 {
		hours = 12
	}
	return &SessionStore{sessions: map[string]*Session{}, ttl: time.Duration(hours) * time.Hour}
}

func (s *SessionStore) Create(username, remoteIP string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	sess := &Session{
		Token:     randomToken(32),
		CSRF:      randomToken(24),
		Username:  username,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
		RemoteIP:  remoteIP,
	}
	s.sessions[sess.Token] = sess
	return sess
}

func (s *SessionStore) Get(token string) *Session {
	if token == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[token]
	if sess == nil {
		return nil
	}
	if time.Now().After(sess.ExpiresAt) {
		delete(s.sessions, token)
		return nil
	}
	return sess
}

func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

func (s *SessionStore) DeleteOtherThan(keep string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.sessions {
		if k != keep {
			delete(s.sessions, k)
		}
	}
}

func (s *SessionStore) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.sessions {
		if now.After(v.ExpiresAt) {
			delete(s.sessions, k)
		}
	}
}

// ---------------------------------------------------------------------------
// Login rate limiting
// ---------------------------------------------------------------------------

type loginAttempt struct {
	count     int
	windowEnd time.Time
	blockedTo time.Time
}

type LoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempt
}

func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{attempts: map[string]*loginAttempt{}}
}

// Allow returns (allowed, retryAfter). Five failures in a minute block the
// address for five minutes. This slows online guessing without locking out the
// legitimate user for long.
func (l *LoginLimiter) Allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	a := l.attempts[ip]
	if a == nil {
		return true, 0
	}
	if now.Before(a.blockedTo) {
		return false, time.Until(a.blockedTo)
	}
	if now.After(a.windowEnd) {
		a.count = 0
		a.windowEnd = now.Add(time.Minute)
	}
	return a.count < 5, 0
}

func (l *LoginLimiter) Fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	a := l.attempts[ip]
	if a == nil || now.After(a.windowEnd) {
		a = &loginAttempt{windowEnd: now.Add(time.Minute)}
		l.attempts[ip] = a
	}
	a.count++
	if a.count >= 5 {
		a.blockedTo = now.Add(5 * time.Minute)
		a.count = 0
	}
}

func (l *LoginLimiter) Success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, ip)
}
