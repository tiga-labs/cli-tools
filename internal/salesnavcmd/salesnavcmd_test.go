package salesnavcmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tiga-labs/cli-tools/pkg/chromeattach"
	"github.com/tiga-labs/cli-tools/pkg/salesnav"
	"github.com/tiga-labs/cli-tools/pkg/salesnavout"
)

const listURL = "https://www.linkedin.com/sales/lists/people/7"

func runCmd(t *testing.T, args ...string) error {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return Run(context.Background(), args, &stdout, &stderr)
}

func emptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("left files behind: %v", entries)
	}
}

func TestRunUsage(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	usage := "usage: tiga salesnav-list-import <lead-list-or-saved-search-url> [-o out.csv] [--max N] [--resume | --force]"
	if err := runCmd(t); err == nil || err.Error() != usage {
		t.Errorf("no URL: err = %v", err)
	}
	if err := runCmd(t, listURL, "--resume", "--force"); err == nil || err.Error() != "--resume and --force cannot be used together" {
		t.Errorf("both flags: err = %v", err)
	}
	if err := runCmd(t, "https://www.linkedin.com/sales/search/people?query=(x)"); !errors.Is(err, salesnav.ErrUnsavedSearch) {
		t.Errorf("unsaved search: err = %v", err)
	}
	if err := runCmd(t, listURL, "--max", "7001"); err == nil || err.Error() != salesnav.ValidateMax(7001).Error() {
		t.Errorf("max 7001: err = %v", err)
	}
	emptyDir(t, dir)
}

func TestRunExistingOutput(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("salesnav-list-7.csv", []byte("x\n"), 0o644)
	err := runCmd(t, listURL, "--chrome-user-data-dir", t.TempDir())
	if !errors.Is(err, salesnavout.ErrExists) {
		t.Fatalf("err = %v, want ErrExists before any Chrome access", err)
	}
}

func TestRunNoChrome(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	err := runCmd(t, listURL, "--chrome-user-data-dir", t.TempDir())
	if !errors.Is(err, chromeattach.ErrDebuggingOff) {
		t.Fatalf("err = %v, want ErrDebuggingOff", err)
	}
	emptyDir(t, dir)
}

func TestProfileName(t *testing.T) {
	dir := t.TempDir()
	if got := profileName(dir); got != "Default" {
		t.Errorf("missing Local State: %q", got)
	}
	os.WriteFile(filepath.Join(dir, "Local State"), []byte(`{"profile":{"info_cache":{"Default":{"name":"Person 1"}}}}`), 0o644)
	if got := profileName(dir); got != "Person 1" {
		t.Errorf("profileName = %q", got)
	}
	os.WriteFile(filepath.Join(dir, "Local State"), []byte(`{garbled`), 0o644)
	if got := profileName(dir); got != "Default" {
		t.Errorf("garbled Local State: %q", got)
	}
}
