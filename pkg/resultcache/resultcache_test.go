package resultcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func withDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "signals")
	prev := Dir
	Dir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { Dir = prev })
	return dir
}

var now = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func TestStorePutGet(t *testing.T) {
	withDir(t)
	key := Key("a", "b")
	if _, ok := Get(key, now); ok {
		t.Fatal("empty store hit")
	}
	if err := Put(key, json.RawMessage(`{"value":"x","status":1}`), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, ok := Get(key, now)
	if !ok || string(got) != `{"value":"x","status":1}` {
		t.Fatalf("got %s %v", got, ok)
	}
}

func TestStoreExpired(t *testing.T) {
	withDir(t)
	key := Key("a")
	Put(key, json.RawMessage(`{}`), now.Add(time.Minute))
	if _, ok := Get(key, now.Add(2*time.Minute)); ok {
		t.Fatal("expired entry hit")
	}
}

func TestStoreCorruptIsMiss(t *testing.T) {
	dir := withDir(t)
	key := Key("a")
	Put(key, json.RawMessage(`{"value":"x"}`), now.Add(time.Hour))
	for _, body := range []string{`{"result":{"val`, `null`, `{"result":null,"expires_at":"2099-01-01T00:00:00Z"}`, ``} {
		os.WriteFile(filepath.Join(dir, key+".json"), []byte(body), 0o600)
		if _, ok := Get(key, now); ok {
			t.Errorf("corrupt %q was a hit", body)
		}
	}
}

func TestStorePrivateFiles(t *testing.T) {
	dir := withDir(t)
	os.MkdirAll(dir, 0o755) // an existing, looser directory is tightened
	Put(Key("a"), json.RawMessage(`{}`), now.Add(time.Hour))
	info, _ := os.Stat(dir)
	files, _ := os.ReadDir(dir)
	fi, _ := files[0].Info()
	if info.Mode().Perm() != 0o700 || fi.Mode().Perm() != 0o600 || len(files) != 1 {
		t.Fatalf("dir=%v file=%v n=%d", info.Mode().Perm(), fi.Mode().Perm(), len(files))
	}
}

func TestStoreClear(t *testing.T) {
	withDir(t)
	if n, err := Clear(); err != nil || n != 0 {
		t.Fatalf("clear empty: %d %v", n, err)
	}
	Put(Key("a"), json.RawMessage(`{}`), now.Add(time.Hour))
	Put(Key("b"), json.RawMessage(`{}`), now.Add(time.Hour))
	if n, err := Clear(); err != nil || n != 2 {
		t.Fatalf("clear: %d %v", n, err)
	}
	if _, ok := Get(Key("a"), now); ok {
		t.Fatal("hit after clear")
	}
}

func TestKeyStable(t *testing.T) {
	if Key("a", "b") != Key("a", "b") || Key("a", "b") == Key("a", "c") || Key("ab") == Key("a", "b") || len(Key("a")) != 64 {
		t.Fatal("Key is not a stable, part-sensitive sha256")
	}
}
