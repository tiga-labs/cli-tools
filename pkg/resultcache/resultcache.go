// Package resultcache stores signal results on disk, keyed by a hash of everything that
// affects them, so a rerun of an unchanged signal on an unchanged row is free.
package resultcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// keyVersion changes when the key's inputs change meaning, so old entries stop matching.
const keyVersion = "v1"

// Dir returns the cache directory: $XDG_CACHE_HOME/tiga/signals, else ~/.cache/tiga/signals.
// It is a var so tests can use a temp dir.
var Dir = func() (string, error) {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "tiga", "signals"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "tiga", "signals"), nil
}

type entry struct {
	Result    json.RawMessage `json:"result"`
	ExpiresAt time.Time       `json:"expires_at"`
}

// Key hashes parts (with the key version) into a hex SHA-256. Parts are length-prefixed so
// ("ab") and ("a", "b") differ.
func Key(parts ...string) string {
	h := sha256.New()
	for _, p := range append([]string{keyVersion}, parts...) {
		h.Write([]byte(lenPrefix(p)))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func lenPrefix(s string) string {
	return strconv.Itoa(len(s)) + ":" + s
}

func path(key string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, key+".json"), nil
}

// Get returns the stored result for key if it has not expired at now. Missing, expired, or
// invalid entries are misses.
func Get(key string, now time.Time) (json.RawMessage, bool) {
	p, err := path(key)
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	var e entry
	if err := json.Unmarshal(data, &e); err != nil || len(e.Result) == 0 || string(e.Result) == "null" || !now.Before(e.ExpiresAt) {
		return nil, false
	}
	return e.Result, true
}

// Put stores result under key until expires, atomically, in a private directory.
func Put(key string, result json.RawMessage, expires time.Time) error {
	p, err := path(key)
	if err != nil {
		return err
	}
	data, err := json.Marshal(entry{Result: result, ExpiresAt: expires})
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
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
	_, writeErr := tmp.Write(data)
	if err := errors.Join(writeErr, tmp.Close()); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// Clear removes every stored result and reports how many were removed.
func Clear() (int, error) {
	dir, err := Dir()
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
