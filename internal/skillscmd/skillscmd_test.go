package skillscmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiga-labs/cli-tools/skills"
)

func cli(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr)
	return stdout.String(), err
}

func fakeTerminal(t *testing.T, tty bool) {
	t.Helper()
	old := isTerminal
	isTerminal = func() bool { return tty }
	t.Cleanup(func() { isTerminal = old })
}

func TestBundlesBothSkills(t *testing.T) {
	names := strings.Join(skills.Names(), ",")
	if names != "enrich-cli,salesnav-cli,signals-cli" {
		t.Fatalf("bundled skills = %s", names)
	}
}

func TestInstallBothAgents(t *testing.T) {
	base := t.TempDir()
	out, err := cli(t, "", "install", "--claude", "--codex", "--dir", base)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".claude/skills", ".agents/skills"} {
		for _, name := range skills.Names() {
			want, _ := skills.Read(name)
			got, err := os.ReadFile(filepath.Join(base, dir, name, "SKILL.md"))
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("%s/%s: err=%v, content matches=%v", dir, name, err, bytes.Equal(got, want))
			}
		}
	}
	want := 2 * len(skills.Names()) // every skill, for Claude Code and Codex
	if n := strings.Count(out, "installed"); n != want {
		t.Errorf("want %d installed lines, got %d:\n%s", want, n, out)
	}

	// A second run changes nothing; an edited copy is updated.
	if out, _ := cli(t, "", "install", "--claude", "--codex", "--dir", base); strings.Count(out, "unchanged") != want {
		t.Errorf("rerun:\n%s", out)
	}
	edited := filepath.Join(base, ".claude/skills/enrich-cli/SKILL.md")
	if err := os.WriteFile(edited, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ := cli(t, "", "list", "--dir", base); !strings.Contains(out, "differs") {
		t.Errorf("list should flag the edited copy:\n%s", out)
	}
	if out, _ := cli(t, "", "install", "--claude", "--dir", base); !strings.Contains(out, "updated") || strings.Count(out, "unchanged") != len(skills.Names())-1 {
		t.Errorf("update run:\n%s", out)
	}
}

func TestInstallProjectUsesWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if _, err := cli(t, "", "install", "--codex", "--project"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agents/skills/signals-cli/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude")); !os.IsNotExist(err) {
		t.Errorf("--codex alone must not write .claude: %v", err)
	}
}

func TestInstallPromptsOnTerminal(t *testing.T) {
	fakeTerminal(t, true)
	base := t.TempDir()
	out, err := cli(t, "1\n", "install", "--dir", base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[1] Claude Code") {
		t.Errorf("no prompt:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(base, ".claude/skills/enrich-cli/SKILL.md")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(base, ".agents")); !os.IsNotExist(err) {
		t.Errorf("choice 1 must not install for Codex: %v", err)
	}
	if _, err := cli(t, "9\n", "install", "--dir", base); err == nil {
		t.Error("an unknown choice must fail")
	}
}

func TestInstallWithoutAgentFailsOffTerminal(t *testing.T) {
	fakeTerminal(t, false)
	_, err := cli(t, "", "install", "--dir", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "--claude and/or --codex") {
		t.Fatalf("err = %v", err)
	}
}

func TestUninstallLeavesOtherSkills(t *testing.T) {
	base := t.TempDir()
	if _, err := cli(t, "", "install", "--claude", "--dir", base); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(base, ".claude/skills/mine/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cli(t, "", "uninstall", "--claude", "--dir", base)
	if err != nil || strings.Count(out, "removed") != len(skills.Names()) {
		t.Fatalf("err=%v out=\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(base, ".claude/skills/enrich-cli")); !os.IsNotExist(err) {
		t.Errorf("enrich-cli folder should be gone: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("other skill removed: %v", err)
	}
}
