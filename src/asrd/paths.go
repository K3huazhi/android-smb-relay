package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// protectedPaths must never be exposed as SMB shares even if a user asks.
var protectedPaths = []string{
	"/", "/data", "/data/adb", "/data/system", "/data/data", "/data/user",
	"/system", "/vendor", "/product", "/system_ext", "/apex", "/proc", "/sys",
	"/dev", "/root", "/sbin", "/etc", "/mnt", "/storage",
}

// allowedStorageRoots returns the directories under which share paths may live.
// The configured root is always allowed; standard Android shared-storage
// views are allowed as well so a user can point a share at an existing folder.
func allowedStorageRoots(cfg *Config) []string {
	roots := []string{
		"/storage/emulated/0",
		"/data/media/0",
		"/mnt/user/0/emulated/0",
		"/mnt/runtime/default/emulated/0",
		"/sdcard",
	}
	if cfg != nil && cfg.Storage.Root != "" {
		roots = append(roots, filepath.Clean(cfg.Storage.Root))
	}
	// A configured root such as /srv/smb on a dev machine should also work.
	if cfg != nil && cfg.Storage.Root != "" {
		parent := filepath.Dir(filepath.Clean(cfg.Storage.Root))
		if parent != "/" && parent != "." {
			roots = append(roots, parent)
		}
	}
	return roots
}

func isProtectedPath(p string) bool {
	clean := filepath.Clean(p)
	for _, prot := range protectedPaths {
		if clean == prot {
			return true
		}
	}
	return strings.HasPrefix(clean, "/data/adb/") || strings.HasPrefix(clean, "/data/system/") ||
		strings.HasPrefix(clean, "/data/data/") || strings.HasPrefix(clean, "/proc/") ||
		strings.HasPrefix(clean, "/sys/") || strings.HasPrefix(clean, "/dev/")
}

func pathWithin(root, p string) bool {
	root = filepath.Clean(root)
	p = filepath.Clean(p)
	if root == p {
		return true
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ValidateSharePath performs static validation. It does not require the path
// to exist yet (the runtime can create the default tree), but if it exists it
// resolves symlinks and checks the real target too.
func ValidateSharePath(cfg *Config, raw string) (string, error) {
	if raw == "" {
		return "", errors.New("path is required")
	}
	if strings.ContainsAny(raw, "\x00\r\n") {
		return "", errors.New("path contains control characters")
	}
	if !filepath.IsAbs(raw) {
		return "", errors.New("path must be absolute")
	}
	clean := filepath.Clean(raw)
	if strings.Contains(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", errors.New("path traversal is not allowed")
	}
	if isProtectedPath(clean) {
		return "", fmt.Errorf("path %q is protected and cannot be shared", clean)
	}
	roots := allowedStorageRoots(cfg)
	ok := false
	for _, r := range roots {
		if pathWithin(r, clean) {
			ok = true
			break
		}
	}
	if !ok {
		return "", fmt.Errorf("path must be inside shared storage (%s)", strings.Join(roots, ", "))
	}
	if st, err := os.Stat(clean); err == nil {
		if !st.IsDir() {
			return "", errors.New("path is not a directory")
		}
		if strings.HasPrefix(clean, "/storage/emulated/0") && !isFuseMount("/storage/emulated/0") {
			return "", errors.New("shared storage is not mounted yet (waiting for user unlock)")
		}
		real, err := filepath.EvalSymlinks(clean)
		if err != nil {
			return "", fmt.Errorf("cannot resolve path: %w", err)
		}
		for _, r := range roots {
			if pathWithin(r, real) && !isProtectedPath(real) {
				return clean, nil
			}
		}
		return "", errors.New("path resolves outside the allowed storage roots")
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("cannot access path: %w", err)
	}
	return clean, nil
}

// RuntimePath maps the configured path to the path handed to Samba. On some
// Android builds root cannot traverse the FUSE view of /storage/emulated/0,
// while /data/media/0 exposes the same files. The mapping is only used when
// the direct view is not usable.
func RuntimePath(cfg *Config, configured string) string {
	if cfg == nil || !cfg.Storage.UseDataMedia {
		return configured
	}
	const emulated = "/storage/emulated/0"
	if configured == emulated || strings.HasPrefix(configured, emulated+"/") {
		if storageUsable(configured) {
			return configured
		}
		candidate := "/data/media/0" + strings.TrimPrefix(configured, emulated)
		if storageUsable(candidate) {
			return candidate
		}
	}
	return configured
}

const fuseSuperMagic = 0x65735546

func isFuseMount(path string) bool {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return false
	}
	return uint64(st.Type) == fuseSuperMagic
}

func storageUsable(path string) bool {
	clean := filepath.Clean(path)
	// /storage/emulated/0 always exists as a directory, even before the FUSE
	// daemon is started. Without the FUSE magic it is just the empty mount
	// point; using it would silently create shares in the wrong place.
	if clean == "/storage/emulated" || clean == "/storage/emulated/0" ||
		strings.HasPrefix(clean, "/storage/emulated/0/") {
		if !isFuseMount("/storage/emulated/0") {
			return false
		}
	}
	st, err := os.Stat(clean)
	if err != nil || !st.IsDir() {
		return false
	}
	f, err := os.Open(clean)
	if err != nil {
		return false
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	if err != nil && err != io.EOF {
		return false
	}
	return true
}

// StorageFingerprint changes when the mapping between the configured path and
// the path handed to Samba changes (for example /data/media/0 while locked vs
// /storage/emulated/0 after unlock). A running smbd must be restarted then.
func StorageFingerprint(cfg *Config) string {
	parts := []string{RuntimePath(cfg, cfg.Storage.Root)}
	for i := range cfg.Shares {
		s := &cfg.Shares[i]
		if s.Enabled {
			parts = append(parts, RuntimePath(cfg, s.Path))
		}
	}
	return strings.Join(parts, "|")
}

func ensureShareTree(cfg *Config, log *Logger) error {
	if cfg == nil || !cfg.Storage.AutoCreate {
		return nil
	}
	root := RuntimePath(cfg, cfg.Storage.Root)
	if err := os.MkdirAll(root, 0o775); err != nil {
		return fmt.Errorf("create storage root %s: %w", root, err)
	}
	for _, sub := range []string{"Shared", "Upload", "Download"} {
		p := filepath.Join(root, sub)
		if err := os.MkdirAll(p, 0o775); err != nil {
			return fmt.Errorf("create %s: %w", p, err)
		}
		_ = os.Chmod(p, 0o775)
	}
	_ = os.Chmod(root, 0o775)
	return nil
}

// EnsureSharePaths creates configured share directories that are missing. It
// only creates the final component and its parents inside the storage root;
// it never deletes anything.
func EnsureSharePaths(cfg *Config, log *Logger) {
	for i := range cfg.Shares {
		s := &cfg.Shares[i]
		if !s.Enabled {
			continue
		}
		p := RuntimePath(cfg, s.Path)
		if st, err := os.Stat(p); err == nil {
			if !st.IsDir() && log != nil {
				log.Warnf("storage", "share %s path is not a directory: %s", s.Name, p)
			}
			continue
		}
		if err := os.MkdirAll(p, 0o775); err != nil {
			if log != nil {
				log.Warnf("storage", "cannot create share %s path %s: %v", s.Name, p, err)
			}
			continue
		}
		_ = os.Chmod(p, 0o775)
		if log != nil {
			log.Infof("storage", "created missing share directory %s", p)
		}
	}
}
