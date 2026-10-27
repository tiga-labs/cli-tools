package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/tiga-labs/cli-tools/pkg/enrich"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCLIKeysAndPersonAndCSV(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	// 1. Set key via CLI
	var stdout, stderr bytes.Buffer
	err := runCLI(context.Background(), []string{"keys", "set", "findymail", "fm-secret-123456", "--config", cfgPath}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("keys set failed: %v (stderr: %s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Saved API key for findymail") {
		t.Fatalf("unexpected keys set output: %s", stdout.String())
	}

	// 2. List keys via CLI
	stdout.Reset()
	stderr.Reset()
	err = runCLI(context.Background(), []string{"keys", "list", "--config", cfgPath}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("keys list failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "findymail") || !strings.Contains(stdout.String(), "fm-s...3456") {
		t.Fatalf("unexpected keys list output:\n%s", stdout.String())
	}

	// 3. Single person enrichment via CLI
	enrich.ProviderTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		raw, _ := json.Marshal(map[string]any{
			"contact": map[string]any{"email": "ada@analyticalengine.org"},
		})
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(raw)),
		}, nil
	})
	defer func() { enrich.ProviderTransport = nil }()

	stdout.Reset()
	stderr.Reset()
	err = runCLI(context.Background(), []string{
		"person",
		"--config", cfgPath,
		"--first-name", "Ada",
		"--last-name", "Lovelace",
		"--domain", "analyticalengine.org",
		"--no-linkedin",
		"--format", "json",
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("person command failed: %v (stderr: %s)", err, stderr.String())
	}

	var rec enrich.ContactRecord
	if err := json.Unmarshal(stdout.Bytes(), &rec); err != nil {
		t.Fatalf("failed to unmarshal person JSON output: %v (output: %s)", err, stdout.String())
	}
	if rec.Email != "ada@analyticalengine.org" || rec.EmailSource != "findymail" || rec.Status != "enriched" {
		t.Fatalf("unexpected enriched person record: %+v", rec)
	}

	// 4. CSV enrichment via stdin/stdout pipe
	csvIn := "First Name,Last Name,Domain\nGrace,Hopper,navy.mil\n"
	stdout.Reset()
	stderr.Reset()
	err = runCLI(context.Background(), []string{
		"csv",
		"--config", cfgPath,
		"--no-linkedin",
		"--quiet",
	}, strings.NewReader(csvIn), &stdout, &stderr)
	if err != nil {
		t.Fatalf("csv command failed: %v (stderr: %s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ada@analyticalengine.org") || !strings.Contains(stdout.String(), "Grace,Hopper,navy.mil") {
		t.Fatalf("unexpected CSV stdout:\n%s", stdout.String())
	}
}

func TestCSVRefusesSameInputOutput(t *testing.T) {
	tmpDir := t.TempDir()
	csvPath := filepath.Join(tmpDir, "contacts.csv")
	original := []byte("First Name,Last Name,Domain\nGrace,Hopper,navy.mil\n")
	if err := os.WriteFile(csvPath, original, 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := runCLI(context.Background(), []string{
		"csv", "-i", csvPath, "-o", csvPath,
		"--config", filepath.Join(tmpDir, "config.json"), "--quiet",
	}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "same file") {
		t.Fatalf("expected same-file error, got %v", err)
	}
	if got, _ := os.ReadFile(csvPath); !bytes.Equal(got, original) {
		t.Fatalf("input CSV was modified:\n%s", got)
	}
}

func TestCSVFailureLeavesExistingOutputIntact(t *testing.T) {
	tmpDir := t.TempDir()
	inPath := filepath.Join(tmpDir, "in.csv")
	outPath := filepath.Join(tmpDir, "out.csv")
	if err := os.WriteFile(inPath, []byte("First Name,Last Name\nGrace,Hopper\n"), 0644); err != nil {
		t.Fatal(err)
	}
	previous := []byte("previous,results\n")
	if err := os.WriteFile(outPath, previous, 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := runCLI(context.Background(), []string{
		"csv", "-i", inPath, "-o", outPath,
		"--config", filepath.Join(tmpDir, "config.json"), "--quiet",
		"--col-email", "No Such Column",
	}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "not found in CSV headers") {
		t.Fatalf("expected column override error, got %v", err)
	}
	if got, _ := os.ReadFile(outPath); !bytes.Equal(got, previous) {
		t.Fatalf("existing output was overwritten by failed run:\n%s", got)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(tmpDir, ".enrich-*")); len(leftovers) > 0 {
		t.Fatalf("temp output files left behind: %v", leftovers)
	}
}

func TestCSVWritesOutputFile(t *testing.T) {
	tmpDir := t.TempDir()
	inPath := filepath.Join(tmpDir, "in.csv")
	outPath := filepath.Join(tmpDir, "out.csv")
	if err := os.WriteFile(inPath, []byte("First Name,Last Name\nGrace,Hopper\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := runCLI(context.Background(), []string{
		"csv", "-i", inPath, "-o", outPath,
		"--config", filepath.Join(tmpDir, "config.json"), "--quiet", "--no-linkedin",
	}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("csv command failed: %v (stderr: %s)", err, stderr.String())
	}
	got, err := os.ReadFile(outPath)
	if err != nil || !strings.Contains(string(got), "Grace,Hopper") || !strings.Contains(string(got), "enrich_status") {
		t.Fatalf("unexpected output file (err=%v):\n%s", err, got)
	}
	if info, _ := os.Stat(outPath); info.Mode().Perm() != 0644 {
		t.Fatalf("expected output mode 0644, got %v", info.Mode().Perm())
	}
}
