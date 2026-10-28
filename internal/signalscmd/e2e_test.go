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
package signalscmd

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
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // never answer from stored results
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

// e2eLinkedIn runs one account LinkedIn signal on Microsoft with a fresh linkdapi cache, so the
// calls really reach linkdapi. It needs a linkdapi key (LINKDAPI_API_KEY / LINKDAPI_KEY or the
// config file) and spends linkdapi credits.
func e2eLinkedIn(t *testing.T, args ...string) runOutput {
	t.Helper()
	if os.Getenv("SIGNALS_E2E") != "1" {
		t.Skip("skipping e2e tests: set SIGNALS_E2E=1 to call OpenAI and linkdapi (spends credits)")
	}
	if os.Getenv("LINKDAPI_API_KEY") == "" && os.Getenv("LINKDAPI_KEY") == "" && os.Getenv("SIGNALS_E2E_CONFIG") == "" {
		t.Skip("skipping LinkedIn e2e tests: no linkdapi key")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	args = append([]string{"run", "--account-name", "Microsoft", "--account-linkedin", "https://www.linkedin.com/company/microsoft", "--buy-linkedin"}, args...)
	if cfg := os.Getenv("SIGNALS_E2E_CONFIG"); cfg != "" {
		args = append(args, "--config", cfg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	var stdout, stderr bytes.Buffer
	if err := runCLI(ctx, args, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("signals %v: %v (stderr: %s)", args, err, stderr.String())
	}
	var out runOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil || len(out.Results) != 1 {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}
	r := out.Results[0]
	t.Logf("status=%d %s sources=%d\nvalue: %s\nerror: %s", r.Status, r.StatusName, len(r.Sources), r.Value, r.Error)
	return out
}

// Microsoft always lists US software engineering jobs (S7 probe, 2026-10-28). About 3 linkdapi
// credits: the company id lookup and one 2-credit job search.
func TestE2EHiringForRoleMicrosoft(t *testing.T) {
	r := e2eLinkedIn(t, "--type", "hiring_for_role", "--job-title", "Software Engineer").Results[0]
	if r.Status != 1 || !strings.Contains(r.Value, "Microsoft has") || len(r.Sources) == 0 {
		t.Fatalf("want status 1 with job sources, got %+v", r)
	}
	for _, src := range r.Sources {
		if u, _ := src["url"].(string); !strings.HasPrefix(u, "https://www.linkedin.com/jobs/view/") {
			t.Fatalf("source is not a job listing: %v", src)
		}
	}
}

// Microsoft posts about AI constantly. Up to 4 linkdapi credits (id + up to 3 post pages) and
// up to 50 small OpenAI calls; the two-week window keeps it to the newest page or two.
func TestE2EAccountPostsMicrosoft(t *testing.T) {
	r := e2eLinkedIn(t, "--type", "linkedin_posts", "--account", "--date-range", "TWO_WEEKS", "--label", "AI posts",
		"--prompt", "Is this post about AI, Copilot, or artificial intelligence?").Results[0]
	if r.Status != 1 || len(r.Sources) != 1 {
		t.Fatalf("want status 1 with one post source, got %+v", r)
	}
	if u, _ := r.Sources[0]["url"].(string); !strings.Contains(u, "linkedin.com/feed/update/") {
		t.Fatalf("source is not a LinkedIn post: %v", r.Sources[0])
	}
}

// The no-web-search JSON path (also used by --verify and the LinkedIn post steps). OpenAI
// rejects json_object requests whose input lacks the word "json" (found live, 2026-10-28).
// One research call plus one classifier call; no sources are expected (D12).
func TestE2ENoWebSearchJSONPath(t *testing.T) {
	if os.Getenv("SIGNALS_E2E") != "1" {
		t.Skip("skipping e2e tests: set SIGNALS_E2E=1 to call OpenAI (spends credits)")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // never answer from stored results
	args := []string{"run", "--no-web-search", "--account-name", "Stripe", "--prompt", "Is {{.AccountName}} a payments company?"}
	if cfg := os.Getenv("SIGNALS_E2E_CONFIG"); cfg != "" {
		args = append(args, "--config", cfg)
	}
	var stdout, stderr bytes.Buffer
	if err := runCLI(context.Background(), args, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("%v (stderr: %s)", err, stderr.String())
	}
	var out runOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil || len(out.Results) != 1 {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}
	r := out.Results[0]
	t.Logf("status=%d %s value: %s error: %s", r.Status, r.StatusName, r.Value, r.Error)
	if r.Status != 1 || r.Error != "" || len(r.Sources) != 0 {
		t.Fatalf("want an unsourced hit, got %+v", r)
	}
}
