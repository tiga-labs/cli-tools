package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func cli(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := runCLI(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func TestVersion(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		out, _, err := cli(t, "", arg)
		if err != nil || !strings.HasPrefix(out, "tiga ") {
			t.Errorf("%s: out=%q err=%v", arg, out, err)
		}
	}
}

func TestHelpListsCommandGroups(t *testing.T) {
	out, _, err := cli(t, "", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"enrich", "signals", "keys", "version"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q:\n%s", want, out)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	if _, _, err := cli(t, "", "bogus"); err == nil || !strings.Contains(err.Error(), `unknown command "bogus"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestDispatchesToToolGroups(t *testing.T) {
	cases := map[string]string{"enrich": "tiga enrich <command>", "signals": "tiga signals <command>"}
	for group, want := range cases {
		out, _, err := cli(t, "", group, "--help")
		if err != nil || !strings.Contains(out, want) {
			t.Errorf("%s --help: err=%v out=%q", group, err, out)
		}
		if _, _, err := cli(t, "", group, "keys", "list"); err == nil || !strings.Contains(err.Error(), "tiga keys") {
			t.Errorf("%s keys: err = %v, want a pointer to tiga keys", group, err)
		}
	}
}

func TestKeysCoverBothTools(t *testing.T) {
	for _, env := range []string{"OPENAI_API_KEY", "OPENAI_KEY", "LINKDAPI_API_KEY", "LINKDAPI_KEY", "FINDYMAIL_API_KEY", "FINDY_MAIL", "FINDYMAIL_KEY"} {
		t.Setenv(env, "")
	}
	cfg := filepath.Join(t.TempDir(), "config.json")
	if _, _, err := cli(t, "ld-secret-123456\n", "keys", "set", "linkdapi", "-", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cli(t, "", "keys", "set", "findymail", "fm-secret-654321", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	out, _, err := cli(t, "", "keys", "list", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"findymail", "zerobounce", "linkdapi", "...3456", "...4321"} {
		if !strings.Contains(out, want) {
			t.Errorf("keys list missing %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "openai"); n != 1 {
		t.Errorf("openai listed %d times, want 1:\n%s", n, out)
	}
}
