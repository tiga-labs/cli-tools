package keystore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var openaiVendor = Vendor{Name: "openai", Category: "feature", EnvVars: []string{"OPENAI_API_KEY", "OPENAI_KEY"}}

func TestDefaultPathUsesTigaDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x")
	if p, err := DefaultPath(); err != nil || p != "/x/tiga/config.json" {
		t.Fatalf("DefaultPath = %q, %v", p, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	home, _ := os.UserHomeDir()
	if p, _ := DefaultPath(); p != filepath.Join(home, ".config", "tiga", "config.json") {
		t.Fatalf("DefaultPath = %q", p)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.json"), []Vendor{openaiVendor})
	if err != nil || cfg == nil || len(cfg.Keys) != 0 {
		t.Fatalf("Load = %+v, %v", cfg, err)
	}
}

func TestSaveIsAtomic0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	cfg, _ := Load(path, []Vendor{openaiVendor})
	if err := cfg.SetKey("openai", "sk-1"); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file mode = %v, %v", info.Mode().Perm(), err)
	}
	dirInfo, _ := os.Stat(filepath.Dir(path))
	if dirInfo.Mode().Perm() != 0700 {
		t.Fatalf("dir mode = %v", dirInfo.Mode().Perm())
	}
}

func TestSaveFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.json")
	link := filepath.Join(dir, "config.json")
	if err := os.WriteFile(target, []byte(`{"keys":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	cfg, _ := Load(link, []Vendor{openaiVendor})
	_ = cfg.SetKey("openai", "sk-linked")
	if err := Save(link, cfg); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was replaced")
	}
	if data, _ := os.ReadFile(target); !strings.Contains(string(data), "sk-linked") {
		t.Fatalf("target not updated: %s", data)
	}
}

func TestGetKeyEnvBeatsFile(t *testing.T) {
	cfg, _ := Load(filepath.Join(t.TempDir(), "c.json"), []Vendor{openaiVendor})
	_ = cfg.SetKey("openai", "from-file")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_KEY", "")
	if got := cfg.GetKey("openai"); got != "from-file" {
		t.Fatalf("GetKey = %q", got)
	}
	t.Setenv("OPENAI_KEY", "from-env")
	if got := cfg.GetKey("openai"); got != "from-env" {
		t.Fatalf("GetKey = %q", got)
	}
}

func TestNormalizeVendor(t *testing.T) {
	cfg, _ := Load(filepath.Join(t.TempDir(), "c.json"), []Vendor{{Name: "leadiq"}, openaiVendor})
	if got, err := cfg.NormalizeVendor("Lead-IQ"); err != nil || got != "leadiq" {
		t.Fatalf("NormalizeVendor = %q, %v", got, err)
	}
	if _, err := cfg.NormalizeVendor("nope"); err == nil || !strings.Contains(err.Error(), "supported:") {
		t.Fatalf("expected unsupported error, got %v", err)
	}
}

func TestSavePreservesUnknownVendors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"keys":{"findymail":"a","openai":"b"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, []Vendor{openaiVendor})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.RemoveKey("openai"); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := Load(path, []Vendor{{Name: "findymail"}})
	if reloaded.Keys["findymail"] != "a" {
		t.Fatalf("findymail key lost: %+v", reloaded.Keys)
	}
	if _, ok := reloaded.Keys["openai"]; ok {
		t.Fatalf("openai key not removed")
	}
}

func TestRedactSecretsCoversFileKeysOfOtherTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	_ = os.WriteFile(path, []byte(`{"keys":{"findymail":"fm-secret-xyz"}}`), 0600)
	cfg, _ := Load(path, []Vendor{openaiVendor})
	if got := cfg.RedactSecrets("err fm-secret-xyz"); got != "err [REDACTED]" {
		t.Fatalf("RedactSecrets = %q", got)
	}
}

func TestListKeyStatusesRegistryOrder(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_KEY", "")
	cfg, _ := Load(filepath.Join(t.TempDir(), "c.json"), []Vendor{openaiVendor, {Name: "linkdapi", Category: "feature"}})
	_ = cfg.SetKey("linkdapi", "ld-abcdefghijklmnopqrstuvwxyz")
	st := cfg.ListKeyStatuses()
	if len(st) != 2 || st[0].Vendor != "openai" || st[0].Configured || st[1].Source != "config" || st[1].MaskedKey != "ld-a...wxyz" {
		t.Fatalf("statuses = %+v", st)
	}
}

func TestMaskKey(t *testing.T) {
	cases := map[string]string{
		"":                               "",
		"short":                          "****",
		"abcdefghijklmnop":               "...mnop",
		"abcdefghijklmnopqrstuvwxyz1234": "abcd...1234",
	}
	for in, want := range cases {
		if got := MaskKey(in); got != want {
			t.Errorf("MaskKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestListKeyStatusesNilConfig(t *testing.T) {
	var c *Config
	if got := c.ListKeyStatuses(); got != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestRedactSecretsLongestKeyFirst(t *testing.T) {
	// One key is a prefix of another; map order must not let the longer key's tail leak.
	for i := 0; i < 50; i++ {
		c := New(nil, map[string]string{"a": "secret-abcdefgh", "b": "secret-abcdefgh-123456"})
		if got := c.RedactSecrets("x secret-abcdefgh-123456 y"); got != "x [REDACTED] y" {
			t.Fatalf("RedactSecrets = %q", got)
		}
	}
}
