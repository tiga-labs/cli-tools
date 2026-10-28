//go:build e2e

// End-to-end tests that call the real OpenAI API and spend real credits.
//
// Opt-in only: requires both the `e2e` build tag and SIGNALS_E2E=1. See `make test-e2e-signals`.
//
//	SIGNALS_E2E=1                required to run anything
//	SIGNALS_E2E_CONFIG=<path>    signals config file (default ~/.config/tiga/config.json)
//
// The OpenAI key is resolved exactly as the CLI does: OPENAI_API_KEY overrides the config file.
// Each run makes about 4 calls: 2 web-search research calls and 1–2 classifier calls.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// stripeFundingPrompt is the S5 live-check example. On 2026-10-28 the research answer was a
// sourced "No — no evidence Stripe raised funding after July 30, 2026", which is status 1
// without classification and status 2 with it (the default). If Stripe announces a raise,
// TestE2EStripeFundingDefaultClassifiesNo will fail and its expectation must be revisited.
const stripeFundingPrompt = "Has {{.AccountName}} raised funding {{.Last90Days}}?"

func e2eRun(t *testing.T, extra ...string) runOutput {
	t.Helper()
	if os.Getenv("SIGNALS_E2E") != "1" {
		t.Skip("skipping e2e tests: set SIGNALS_E2E=1 to call OpenAI (spends credits)")
	}
	args := []string{"run", "--account-name", "Stripe", "--account-domain", "stripe.com", "--prompt", stripeFundingPrompt}
	if cfg := os.Getenv("SIGNALS_E2E_CONFIG"); cfg != "" {
		args = append(args, "--config", cfg)
	}
	args = append(args, extra...)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	var stdout, stderr bytes.Buffer
	if err := runCLI(ctx, args, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("signals %v: %v (stderr: %s)", extra, err, stderr.String())
	}
	var out runOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil || len(out.Results) != 1 {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}
	r := out.Results[0]
	t.Logf("status=%d %s sources=%d\nvalue: %s", r.Status, r.StatusName, len(r.Sources), r.Value)
	if r.Error != "" {
		t.Fatalf("error: %s", r.Error)
	}
	if strings.TrimSpace(r.Value) == "" || len(r.Sources) == 0 {
		t.Fatalf("want a sourced answer, got %+v", r)
	}
	for _, src := range r.Sources {
		if u, _ := src["url"].(string); !strings.HasPrefix(u, "http") {
			t.Fatalf("source without URL: %v", src)
		}
	}
	return out
}

// Without classification, any sourced answer is a hit, even a "no" (gandalf's behaviour).
func TestE2EStripeFundingNoBoolIsSourcedHit(t *testing.T) {
	if r := e2eRun(t, "--no-bool").Results[0]; r.Status != 1 {
		t.Fatalf("want status 1, got %d %s", r.Status, r.StatusName)
	}
}

// With classification on (the default), the "no evidence of a raise" answer is status 2,
// and the research text and sources are kept as evidence.
func TestE2EStripeFundingDefaultClassifiesNo(t *testing.T) {
	if r := e2eRun(t).Results[0]; r.Status != 2 {
		t.Fatalf("want status 2 (not found) for a \"no\" answer, got %d %s: %s", r.Status, r.StatusName, r.Value)
	}
}
