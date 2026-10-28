package linkdapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// CacheDir returns the cache directory: $XDG_CACHE_HOME/tiga/linkdapi, else
// ~/.cache/tiga/linkdapi. It is a var so tests can use a temp dir.
var CacheDir = func() (string, error) {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "tiga", "linkdapi"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "tiga", "linkdapi"), nil
}

// cacheKey hashes a request's path and query into a file name.
func cacheKey(pathAndQuery string) string {
	sum := sha256.Sum256([]byte(pathAndQuery))
	return hex.EncodeToString(sum[:])
}

func cachePath(key string) (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, key+".json"), nil
}

// readCache returns a cached body younger than ttl. Missing, stale, or invalid files are misses.
func readCache(key string, ttl time.Duration) ([]byte, bool) {
	path, err := cachePath(key)
	if err != nil {
		return nil, false
	}
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > ttl {
		return nil, false
	}
	body, err := os.ReadFile(path)
	if err != nil || !json.Valid(body) {
		return nil, false
	}
	return body, true
}

// writeCache stores body atomically (temp file + rename) with private permissions.
func writeCache(key string, body []byte) error {
	path, err := cachePath(key)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	_, writeErr := tmp.Write(body)
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// ClearCache removes every cached linkdapi response and reports how many were removed.
func ClearCache() (int, error) {
	dir, err := CacheDir()
	if err != nil {
		return 0, err
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range files {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return n, err
		}
		n++
	}
	return n, nil
}
