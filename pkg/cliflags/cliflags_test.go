package cliflags

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/tiga-labs/cli-tools/pkg/keystore"
)

func TestSplitPositionalAndFlags(t *testing.T) {
	pos, flags := SplitPositionalAndFlags([]string{"keys", "set", "x", "--config", "p", "k"}, map[string]bool{"--config": true})
	if !reflect.DeepEqual(pos, []string{"keys", "set", "x", "k"}) || !reflect.DeepEqual(flags, []string{"--config", "p"}) {
		t.Fatalf("pos=%v flags=%v", pos, flags)
	}
	pos, flags = SplitPositionalAndFlags([]string{"-", "--quiet", "--", "--not-a-flag"}, nil)
	if !reflect.DeepEqual(pos, []string{"-", "--not-a-flag"}) || !reflect.DeepEqual(flags, []string{"--quiet"}) {
		t.Fatalf("pos=%v flags=%v", pos, flags)
	}
}

func parseToggle(t *testing.T, def bool, args ...string) bool {
	t.Helper()
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	tg := BindToggle(fs, "x", def, "enable x", "disable x")
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return tg.Value()
}

func TestToggleDefault(t *testing.T) {
	if !parseToggle(t, true) || parseToggle(t, false) {
		t.Fatal("default not honored")
	}
}

func TestToggleOn(t *testing.T) {
	if !parseToggle(t, false, "--x") {
		t.Fatal("--x should enable")
	}
	if parseToggle(t, true, "--no-x") {
		t.Fatal("--no-x should disable")
	}
}

func TestToggleNoWins(t *testing.T) {
	if parseToggle(t, false, "--x", "--no-x") {
		t.Fatal("--no-x must win over --x")
	}
}

func TestRunKeysSetListRm(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	spec := KeysSpec{Tool: "signals", Vendors: []keystore.Vendor{{Name: "openai", Category: "llm", EnvVars: []string{"OPENAI_API_KEY"}}}, VendorHelp: "  openai"}
	run := func(stdin string, args ...string) (string, error) {
		var out bytes.Buffer
		err := RunKeys(spec, append(args, "--config", cfgPath), strings.NewReader(stdin), &out, io.Discard)
		return out.String(), err
	}
	if out, err := run("sk-abcdefghijklmnopqrstuvwxyz\n", "set", "openai", "-"); err != nil || !strings.Contains(out, "Saved API key for openai (sk-a...wxyz)") {
		t.Fatalf("set: %q %v", out, err)
	}
	out, err := run("", "list")
	if err != nil || !strings.Contains(out, "sk-a...wxyz") || strings.Contains(out, "sk-abcdefghijklmnop") {
		t.Fatalf("list: %q %v", out, err)
	}
	out, _ = run("", "list", "--json")
	var statuses []keystore.KeyStatus
	if err := json.Unmarshal([]byte(out), &statuses); err != nil || len(statuses) != 1 || !statuses[0].Configured {
		t.Fatalf("list --json: %q %v", out, err)
	}
	if out, err := run("", "rm", "openai"); err != nil || !strings.Contains(out, "Removed API key for openai") {
		t.Fatalf("rm: %q %v", out, err)
	}
	if out, _ := run("", "list", "--json"); !strings.Contains(out, `"configured": false`) {
		t.Fatalf("after rm: %q", out)
	}
	if _, err := run("", "frob"); err == nil || !strings.Contains(err.Error(), "expected: list, set, rm") {
		t.Fatalf("unknown action: %v", err)
	}
	if _, err := run("", "set", "openai"); err == nil || !strings.Contains(err.Error(), "usage: signals keys set") {
		t.Fatalf("missing key: %v", err)
	}
}

func TestReadKeyFromStdin(t *testing.T) {
	if k, err := ReadKeyFromStdin(strings.NewReader("  k-1  \nignored")); err != nil || k != "k-1" {
		t.Fatalf("k=%q err=%v", k, err)
	}
	if _, err := ReadKeyFromStdin(strings.NewReader("")); err == nil {
		t.Fatal("expected error on empty stdin")
	}
}

func TestResolveVersion(t *testing.T) {
	withModule := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/tiga-labs/cli-tools", Version: v}}
	}
	tests := []struct {
		name    string
		ldflags string
		info    *debug.BuildInfo
		ok      bool
		want    string
	}{
		{"release build uses ldflags", "0.1.0", withModule("v0.1.0"), true, "0.1.0"},
		{"make build strips git describe v", "v0.1.0-3-g78f1ef2-dirty", withModule("(devel)"), true, "0.1.0-3-g78f1ef2-dirty"},
		{"go install uses module version", "dev", withModule("v0.1.0"), true, "0.1.0"},
		{"pseudo-version from local build", "dev", withModule("v0.1.1-0.20261027120000-78f1ef2abcde+dirty"), true, "0.1.1-0.20261027120000-78f1ef2abcde+dirty"},
		{"devel build stays dev", "dev", withModule("(devel)"), true, "dev"},
		{"no build info stays dev", "dev", nil, false, "dev"},
	}
	for _, tt := range tests {
		if got := ResolveVersion(tt.ldflags, tt.info, tt.ok); got != tt.want {
			t.Errorf("%s: ResolveVersion = %q, want %q", tt.name, got, tt.want)
		}
	}
}
