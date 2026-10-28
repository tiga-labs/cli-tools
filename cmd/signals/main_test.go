package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/httpx"
	"github.com/tiga-labs/cli-tools/pkg/linkdapi"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

const researchHit = `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Yes, Acme raised $10M.","annotations":[{"type":"url_citation","url":"https://a.com","title":"A"},{"type":"url_citation","url":"https://b.com","title":"B"}]}]}]}`

// stub serves body (with status) for every request and records the decoded request bodies.
type stub struct {
	mu     sync.Mutex
	bodies []map[string]any
	counts map[string]int
}

const classifierYes = `{"choices":[{"message":{"content":"{\"containsResult\":true,\"doesNotContainResult\":false,\"explanation\":\"x\"}"}}]}`

func installStub(t *testing.T, status int, body string) *stub {
	t.Helper()
	s := &stub{counts: map[string]int{}}
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(req.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		s.mu.Lock()
		s.counts[req.URL.Path]++
		s.mu.Unlock()
		respBody := body
		if strings.HasSuffix(req.URL.Path, "/chat/completions") && status == 200 {
			respBody = classifierYes
		} else {
			s.mu.Lock()
			s.bodies = append(s.bodies, m)
			s.mu.Unlock()
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(respBody))}, nil
	})
	t.Cleanup(func() { httpx.Transport = nil })
	return s
}

func (s *stub) paths() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for k, v := range s.counts {
		out[k] = v
	}
	return out
}

func (s *stub) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

func (s *stub) inputs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, b := range s.bodies {
		in, _ := b["input"].(string)
		out = append(out, in)
	}
	return out
}

// testConfig writes a config with an OpenAI key and clears the env vars that would override it.
func testConfig(t *testing.T, key string) string {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_KEY", "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // each test gets its own result and linkdapi cache
	path := filepath.Join(t.TempDir(), "config.json")
	if key != "" {
		if err := runCLI(context.Background(), []string{"keys", "set", "openai", key, "--config", path}, strings.NewReader(""), io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func cli(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := runCLI(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

type runOutput struct {
	Results []struct {
		Key        string           `json:"key"`
		Label      string           `json:"label"`
		Type       string           `json:"type"`
		Value      string           `json:"value"`
		Status     int              `json:"status"`
		StatusName string           `json:"status_name"`
		Sources    []map[string]any `json:"sources"`
		Error      string           `json:"error"`
	} `json:"results"`
}

func decodeRun(t *testing.T, out string) runOutput {
	t.Helper()
	var r runOutput
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return r
}

func writeSignal(t *testing.T, label, prompt string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"label": label, "computed_config": map[string]any{"type": "gpt", "prompt": prompt}})
	path := filepath.Join(t.TempDir(), label+".json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestKeysSetListRm(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	out, _, err := cli(t, "keys", "list", "--config", cfg)
	if err != nil || !strings.Contains(out, "openai") || !strings.Contains(out, "linkdapi") || !strings.Contains(out, "...3456") || strings.Contains(out, "sk-abc") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if _, _, err := cli(t, "keys", "rm", "openai", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := cli(t, "keys", "list", "--config", cfg); strings.Contains(out, "...3456") {
		t.Fatalf("key not removed:\n%s", out)
	}
}

func TestRunInlineJSON(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	installStub(t, 200, researchHit)
	out, _, err := cli(t, "run", "--config", cfg, "--prompt", "Has {{.AccountName}} raised?", "--account-name", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	r := decodeRun(t, out)
	if len(r.Results) != 1 {
		t.Fatalf("results = %+v", r)
	}
	got := r.Results[0]
	if got.Key != "gpt" || got.Type != "gpt" || got.Status != 1 || got.StatusName != "success" || len(got.Sources) != 2 || got.Value != "Yes, Acme raised $10M." {
		t.Fatalf("result = %+v", got)
	}
}

func TestRunSignalFiles(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	stub := installStub(t, 200, researchHit)
	a, b := writeSignal(t, "Alpha", "alpha {{.AccountName}}"), writeSignal(t, "Beta", "beta {{.AccountName}}")
	out, _, err := cli(t, "run", "--config", cfg, "-s", a, "--signal", b, "--label", "Gamma", "--prompt", "gamma {{.AccountName}}", "--account-name", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	r := decodeRun(t, out)
	if len(r.Results) != 3 || r.Results[0].Key != "alpha" || r.Results[1].Key != "beta" || r.Results[2].Key != "gamma" {
		t.Fatalf("results = %+v", r)
	}
	if in := stub.inputs(); len(in) != 3 || in[0] != "alpha Acme" {
		t.Fatalf("inputs = %v", in)
	}
}

func TestRunFieldFlag(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	stub := installStub(t, 200, researchHit)
	if _, _, err := cli(t, "run", "--config", cfg, "--prompt", "{{.AccountName}} at {{.funding_stage}}", "--account-name", "Acme", "--field", "funding_stage=Series B"); err != nil {
		t.Fatal(err)
	}
	if in := stub.inputs(); len(in) != 1 || in[0] != "Acme at Series B" {
		t.Fatalf("inputs = %v", in)
	}
}

func TestRunNoOpenAIKey(t *testing.T) {
	cfg := testConfig(t, "")
	stub := installStub(t, 200, researchHit)
	_, _, err := cli(t, "run", "--config", cfg, "--prompt", "{{.AccountName}}", "--account-name", "Acme")
	if err == nil || !strings.Contains(err.Error(), "signals keys set openai") || stub.calls() != 0 {
		t.Fatalf("err = %v calls=%d", err, stub.calls())
	}
}

func TestRunNoRecord(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	if _, _, err := cli(t, "run", "--config", cfg, "--prompt", "{{.AccountName}}"); err == nil || !strings.Contains(err.Error(), "no record given") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	if _, _, err := cli(t, "bogus"); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunRedactsKeyInError(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	installStub(t, 401, `{"error":{"message":"Incorrect API key provided: sk-abcdef123456"}}`)
	out, _, err := cli(t, "run", "--config", cfg, "--prompt", "{{.AccountName}}", "--account-name", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	r := decodeRun(t, out)
	if strings.Contains(out, "sk-abcdef123456") || r.Results[0].Status != 4 || r.Results[0].Error == "" {
		t.Fatalf("out = %s", out)
	}
}

func TestRunFormatTable(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	installStub(t, 200, researchHit)
	out, _, err := cli(t, "run", "--config", cfg, "--format", "table", "--prompt", "{{.AccountName}}", "--account-name", "Acme")
	if err != nil || !strings.Contains(out, "Status:") || !strings.Contains(out, "success") || !strings.Contains(out, "https://a.com") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestRunNoWebSearchFlag(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	stub := installStub(t, 200, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"output\":\"yes\",\"found\":true}"}]}]}`)
	if _, _, err := cli(t, "run", "--config", cfg, "--no-web-search", "--prompt", "{{.AccountName}}", "--account-name", "Acme"); err != nil {
		t.Fatal(err)
	}
	if _, ok := stub.bodies[0]["tools"]; ok {
		t.Fatal("tools sent with --no-web-search")
	}
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readCSVString(t *testing.T, s string) [][]string {
	t.Helper()
	r := csv.NewReader(strings.NewReader(s))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("read csv %q: %v", s, err)
	}
	return rows
}

func TestCSVOrderAndQuoting(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(req.Body)
		if strings.Contains(string(raw), "Acme") {
			time.Sleep(50 * time.Millisecond) // the first row finishes last
		}
		body := researchHit
		if strings.HasSuffix(req.URL.Path, "/chat/completions") {
			body = classifierYes
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() { httpx.Transport = nil })
	in := writeFile(t, "in.csv", "\ufeffcompany,domain\nAcme,acme.com\nGlobex\n\"Initech, Inc\nEast\",initech.com\nHooli,hooli.com\nUmbrella,umbrella.com\n")
	a, b := writeSignal(t, "Funding", "Has {{.AccountName}} raised?"), writeSignal(t, "Hiring", "Is {{.AccountName}} hiring?")
	out, _, err := cli(t, "csv", in, "--config", cfg, "-s", a, "-s", b, "--quiet")
	if err != nil {
		t.Fatal(err)
	}
	rows := readCSVString(t, out)
	wantHeader := []string{"\ufeffcompany", "domain", "funding_value", "funding_status", "funding_sources", "funding_error", "hiring_value", "hiring_status", "hiring_sources", "hiring_error"}
	if strings.Join(rows[0], "|") != strings.Join(wantHeader, "|") {
		t.Fatalf("header = %q", rows[0])
	}
	wantNames := []string{"Acme", "Globex", "Initech, Inc\nEast", "Hooli", "Umbrella"}
	if len(rows) != 6 {
		t.Fatalf("rows = %d", len(rows))
	}
	for i, name := range wantNames {
		row := rows[i+1]
		if row[0] != name || len(row) != 10 || row[3] != "1" || row[4] != "https://a.com https://b.com" || row[7] != "1" {
			t.Fatalf("row %d = %q", i+1, row)
		}
	}
}

func TestCSVColOverride(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	stub := installStub(t, 200, researchHit)
	in := writeFile(t, "in.csv", "name\nAcme Corp\n")
	if _, _, err := cli(t, "csv", in, "--config", cfg, "--prompt", "Has {{.AccountName}} raised?", "--col", "AccountName=name", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if in := stub.inputs(); len(in) != 1 || in[0] != "Has Acme Corp raised?" {
		t.Fatalf("inputs = %v", in)
	}
}

func TestCSVColumnClash(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	stub := installStub(t, 200, researchHit)
	in := writeFile(t, "in.csv", "company,GPT Value\nAcme,x\n")
	_, _, err := cli(t, "csv", in, "--config", cfg, "--prompt", "{{.AccountName}}", "--quiet")
	if err == nil || !strings.Contains(err.Error(), "gpt_value") || stub.calls() != 0 {
		t.Fatalf("err = %v calls=%d", err, stub.calls())
	}
}

func TestCSVRedactsKeyInError(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	installStub(t, 401, `{"error":{"message":"Incorrect API key provided: sk-abcdef123456"}}`)
	in := writeFile(t, "in.csv", "company\nAcme\n")
	out, _, err := cli(t, "csv", in, "--config", cfg, "--prompt", "{{.AccountName}}", "--quiet")
	if err != nil {
		t.Fatal(err)
	}
	rows := readCSVString(t, out)
	if strings.Contains(out, "sk-abcdef123456") || rows[1][2] != "4" || rows[1][4] == "" {
		t.Fatalf("out = %s", out)
	}
}

func TestCSVMissingDepsRow(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	installStub(t, 200, researchHit)
	in := writeFile(t, "in.csv", "company,domain\n,acme.com\n")
	out, _, err := cli(t, "csv", in, "--config", cfg, "--prompt", "{{.AccountName}}", "--quiet")
	if err != nil {
		t.Fatal(err)
	}
	if row := readCSVString(t, out)[1]; row[3] != "3" || row[5] != "missing: AccountName" {
		t.Fatalf("row = %q", row)
	}
}

func TestCSVDryRunNoHTTP(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	stub := installStub(t, 200, researchHit)
	in := writeFile(t, "in.csv", "\ufeffcompany,domain\nAcme,a.com\n,b.com\nAcme,a.com\n")
	out, _, err := cli(t, "csv", in, "--config", cfg, "--prompt", "{{.AccountName}}", "--verify", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Rows    int `json:"rows"`
		Signals []struct {
			Key             string `json:"key"`
			RowsReady       int    `json:"rows_ready"`
			RowsMissingDeps int    `json:"rows_missing_deps"`
			ResearchCalls   int    `json:"research_calls"`
			MaxOpenAICalls  int    `json:"max_openai_calls"`
		} `json:"signals"`
	}
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	p := plan.Signals[0]
	if plan.Rows != 3 || p.Key != "gpt" || p.RowsReady != 2 || p.RowsMissingDeps != 1 || p.ResearchCalls != 1 || p.MaxOpenAICalls != 3 || stub.calls() != 0 {
		t.Fatalf("plan = %+v calls=%d", plan, stub.calls())
	}
}

func TestCSVOutputFile(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	installStub(t, 200, researchHit)
	in := writeFile(t, "in.csv", "company\nAcme\n")
	outPath := filepath.Join(t.TempDir(), "out.csv")
	if _, _, err := cli(t, "csv", "-i", in, "-o", outPath, "--config", cfg, "--prompt", "{{.AccountName}}", "--quiet"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil || !strings.Contains(string(data), "gpt_value") {
		t.Fatalf("out file: %v %s", err, data)
	}
	if _, _, err := cli(t, "csv", "-i", in, "-o", in, "--config", cfg, "--prompt", "{{.AccountName}}", "--quiet"); err == nil {
		t.Fatal("writing over the input must fail")
	}
}

func TestCSVProgressAndQuiet(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	installStub(t, 200, researchHit)
	in := writeFile(t, "in.csv", "company\nAcme\nGlobex\n")
	_, stderr, err := cli(t, "csv", in, "--config", cfg, "--prompt", "{{.AccountName}}")
	if err != nil || !strings.Contains(stderr, "[2/2]") || !strings.Contains(stderr, "gpt=success") {
		t.Fatalf("err=%v stderr=%q", err, stderr)
	}
	_, stderr, _ = cli(t, "csv", in, "--config", cfg, "--prompt", "{{.AccountName}}", "--quiet")
	if stderr != "" {
		t.Fatalf("quiet stderr = %q", stderr)
	}
}

func TestCSVStdin(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	installStub(t, 200, researchHit)
	var stdout bytes.Buffer
	err := runCLI(context.Background(), []string{"csv", "-", "--config", cfg, "--prompt", "{{.AccountName}}", "--quiet"}, strings.NewReader("company\nAcme\n"), &stdout, io.Discard)
	if err != nil || !strings.Contains(stdout.String(), "gpt_status") {
		t.Fatalf("err=%v out=%s", err, stdout.String())
	}
}

func TestRunBadFormatFailsBeforeHTTP(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	stub := installStub(t, 200, researchHit)
	_, _, err := cli(t, "run", "--config", cfg, "--format", "csv", "--prompt", "{{.AccountName}}", "--account-name", "Acme")
	if err == nil || !strings.Contains(err.Error(), "unsupported --format") || stub.calls() != 0 {
		t.Fatalf("err = %v calls=%d", err, stub.calls())
	}
}

func TestRunInterruptedExitsNonZero(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	installStub(t, 200, researchHit)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout bytes.Buffer
	err := runCLI(ctx, []string{"run", "--config", cfg, "--prompt", "{{.AccountName}}", "--account-name", "Acme"}, strings.NewReader(""), &stdout, io.Discard)
	if !errors.Is(err, context.Canceled) || !strings.Contains(stdout.String(), `"results"`) {
		t.Fatalf("err = %v out=%s", err, stdout.String())
	}
}

func TestOrphanInlineFlagsError(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	a := writeSignal(t, "Alpha", "{{.AccountName}}")
	for _, flags := range [][]string{{"--label", "Renamed"}, {"--word-limit", "5"}, {"--account"}, {"--date-range", "LAST_90_DAYS"}} {
		args := append([]string{"run", "--config", cfg, "-s", a, "--account-name", "Acme"}, flags...)
		if _, _, err := cli(t, args...); err == nil || !strings.Contains(err.Error(), "--prompt") {
			t.Errorf("%v: err = %v", flags, err)
		}
	}
}

func TestCSVRequiresInput(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	if _, _, err := cli(t, "csv", "--config", cfg, "--prompt", "{{.AccountName}}"); err == nil || !strings.Contains(err.Error(), "input CSV") {
		t.Fatalf("err = %v", err)
	}
	in := writeFile(t, "in.csv", "company\nAcme\n")
	if _, _, err := cli(t, "csv", in, "extra.csv", "--config", cfg, "--prompt", "{{.AccountName}}"); err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("err = %v", err)
	}
}

func TestBoolIsOnByDefault(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	stub := installStub(t, 200, researchHit)
	if _, _, err := cli(t, "run", "--config", cfg, "--prompt", "{{.AccountName}}", "--account-name", "Acme"); err != nil {
		t.Fatal(err)
	}
	if stub.paths()["/v1/chat/completions"] != 1 {
		t.Fatalf("classifier not called by default: %v", stub.paths())
	}
	stub = installStub(t, 200, researchHit)
	if _, _, err := cli(t, "run", "--config", cfg, "--no-bool", "--prompt", "{{.AccountName}}", "--account-name", "Acme"); err != nil {
		t.Fatal(err)
	}
	if stub.paths()["/v1/chat/completions"] != 0 {
		t.Fatalf("--no-bool still classified: %v", stub.paths())
	}
}

const liProfile = `{"success":true,"statusCode":200,"message":"ok","data":{"urn":"ACoAAAtest","username":"jane-doe","firstName":"Jane","lastName":"Doe","headline":"VP Sales at Acme",
	"fullPositions":[{"companyId":42,"companyName":"Acme","title":"VP Sales","start":{"year":2026,"month":9,"day":1},"end":{"year":0,"month":0,"day":0}}]}}`

const liPosts = `{"success":true,"statusCode":200,"message":"ok","data":{"cursor":"","posts":[{"urn":"urn:li:activity:1","url":"https://www.linkedin.com/feed/update/urn:li:activity:1","text":"We are hiring AEs!","author":{"name":"Jane Doe"},"postedAt":{"relativeDay":"2d"},"resharedPostContent":null}]}}`

// liConfig adds a linkdapi key to cfg, points the linkdapi cache at a temp dir, and serves
// liProfile for /profile/full (researchHit otherwise). It returns a per-path call counter.
func liConfig(t *testing.T, cfg string) func(string) int {
	t.Helper()
	t.Setenv("LINKDAPI_API_KEY", "")
	t.Setenv("LINKDAPI_KEY", "")
	if _, _, err := cli(t, "keys", "set", "linkdapi", "ld-secret-123456", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "linkdapi")
	prev := linkdapi.CacheDir
	linkdapi.CacheDir = func() (string, error) { return dir, nil }
	var mu sync.Mutex
	counts := map[string]int{}
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		counts[req.URL.Path]++
		mu.Unlock()
		body := researchHit
		switch {
		case strings.HasSuffix(req.URL.Path, "/profile/full"):
			body = liProfile
		case strings.HasSuffix(req.URL.Path, "/posts/all"):
			body = liPosts
		case strings.HasSuffix(req.URL.Path, "/chat/completions"):
			body = classifierYes
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() { httpx.Transport = nil; linkdapi.CacheDir = prev })
	return func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return counts[path]
	}
}

func TestRunStartedNewRoleInline(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	calls := liConfig(t, cfg)
	out, _, err := cli(t, "run", "--config", cfg, "--type", "started_new_role", "--started-within", "TWELVE_MONTHS",
		"--linkedin-url", "https://www.linkedin.com/in/jane-doe", "--buy-linkedin")
	if err != nil {
		t.Fatal(err)
	}
	r := decodeRun(t, out)
	if r.Results[0].Status != 1 || !strings.Contains(r.Results[0].Value, `started role "VP Sales" at Acme`) || calls("/api/v1/profile/full") != 1 {
		t.Fatalf("out = %s", out)
	}
	// A second run reads the disk cache: no linkdapi call.
	if _, _, err := cli(t, "run", "--config", cfg, "--type", "started_new_role", "--linkedin-url", "https://www.linkedin.com/in/jane-doe", "--buy-linkedin"); err != nil || calls("/api/v1/profile/full") != 1 {
		t.Fatalf("err=%v calls=%d", err, calls("/api/v1/profile/full"))
	}
	// --refresh-linkedin refetches.
	if _, _, err := cli(t, "run", "--config", cfg, "--type", "started_new_role", "--linkedin-url", "https://www.linkedin.com/in/jane-doe", "--buy-linkedin", "--refresh-linkedin"); err != nil || calls("/api/v1/profile/full") != 2 {
		t.Fatalf("err=%v calls=%d", err, calls("/api/v1/profile/full"))
	}
}

func TestRunLinkedInWithoutBuyFails(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	calls := liConfig(t, cfg)
	_, _, err := cli(t, "run", "--config", cfg, "--type", "started_new_role", "--linkedin-url", "https://www.linkedin.com/in/jane-doe")
	if err == nil || !strings.Contains(err.Error(), "--buy-linkedin") || calls("/api/v1/profile/full") != 0 {
		t.Fatalf("err = %v", err)
	}
	_, _, err = cli(t, "run", "--config", cfg, "-s", writeSignal(t, "Alpha", "{{.AccountName}}"), "--account-name", "Acme", "--started-within", "ONE_MONTH")
	if err == nil || !strings.Contains(err.Error(), "--started-within") {
		t.Fatalf("orphan --started-within: err = %v", err)
	}
}

func TestCSVDryRunLinkdAPICalls(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	calls := liConfig(t, cfg)
	in := writeFile(t, "in.csv", "linkedin\nhttps://www.linkedin.com/in/jane-doe\nhttps://www.linkedin.com/in/jane-doe\n")
	out, _, err := cli(t, "csv", in, "--config", cfg, "--type", "started_new_role", "--buy-linkedin", "--dry-run")
	if err != nil || !strings.Contains(out, `"linkdapi_calls": 1`) || calls("/api/v1/profile/full") != 0 {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestCSVDryRunNeedsNoKeys(t *testing.T) {
	cfg := testConfig(t, "")
	t.Setenv("LINKDAPI_API_KEY", "")
	t.Setenv("LINKDAPI_KEY", "")
	stub := installStub(t, 200, researchHit)
	in := writeFile(t, "in.csv", "company,linkedin\nAcme,https://www.linkedin.com/in/jane-doe\n")
	out, _, err := cli(t, "csv", in, "--config", cfg, "-s", writeSignal(t, "Alpha", "{{.AccountName}}"), "--type", "started_new_role", "--buy-linkedin", "--dry-run")
	if err != nil || !strings.Contains(out, `"rows": 1`) || stub.calls() != 0 {
		t.Fatalf("err=%v out=%s", err, out)
	}
	if _, _, err := cli(t, "csv", in, "--config", cfg, "--prompt", "{{.AccountName}}"); err == nil {
		t.Fatal("a real run without keys must still fail")
	}
}

func TestRunPersonPostsInline(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	calls := liConfig(t, cfg)
	out, _, err := cli(t, "run", "--config", cfg, "--type", "linkedin_posts", "--prompt", "Is Jane hiring?", "--linkedin-url", "https://www.linkedin.com/in/jane-doe", "--buy-linkedin")
	if err != nil {
		t.Fatal(err)
	}
	r := decodeRun(t, out)
	if r.Results[0].Type != "linkedin_posts" || calls("/api/v1/profile/full") != 1 || calls("/api/v1/posts/all") != 1 || calls("/v1/responses") == 0 {
		t.Fatalf("out=%s", out)
	}
}

func TestRunHiringInline(t *testing.T) {
	cfg := testConfig(t, "")
	t.Setenv("LINKDAPI_API_KEY", "")
	t.Setenv("LINKDAPI_KEY", "")
	if _, _, err := cli(t, "keys", "set", "linkdapi", "ld-secret-123456", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "linkdapi")
	prev := linkdapi.CacheDir
	linkdapi.CacheDir = func() (string, error) { return dir, nil }
	var keywords []string
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"success":true,"statusCode":200,"data":{"id":"42","universalName":"acme"}}`
		if strings.HasSuffix(req.URL.Path, "/search/jobs") {
			keywords = append(keywords, req.URL.Query().Get("keyword"))
			body = `{"success":true,"statusCode":200,"data":{"total":2,"jobs":[` +
				`{"id":"1","url":"https://www.linkedin.com/jobs/view/1","title":"Account Executive","location":"Seattle, WA"},` +
				`{"id":"2","url":"https://www.linkedin.com/jobs/view/2","title":"Account Manager","location":"Seattle, WA"}]}}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() { httpx.Transport = nil; linkdapi.CacheDir = prev })

	out, _, err := cli(t, "run", "--config", cfg, "--type", "hiring_for_role", "--job-title", "Account Executive", "--job-title", "SDR",
		"--location", "WA", "--exact-match", "--account-name", "Acme", "--account-linkedin", "https://www.linkedin.com/company/acme", "--buy-linkedin")
	if err != nil {
		t.Fatal(err)
	}
	r := decodeRun(t, out)
	if r.Results[0].Status != 1 || !strings.Contains(r.Results[0].Value, `Acme has 1 job with the term "Account Executive" located in Washington`) || len(keywords) != 1 {
		t.Fatalf("out=%s keywords=%v", out, keywords)
	}
	if _, _, err := cli(t, "run", "--config", cfg, "-s", writeSignal(t, "Alpha", "{{.AccountName}}"), "--account-name", "Acme", "--job-title", "AE"); err == nil || !strings.Contains(err.Error(), "--job-title") {
		t.Fatalf("orphan --job-title: err = %v", err)
	}
}

func TestCSVRerunIsFree(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	stub := installStub(t, 200, researchHit)
	in := writeFile(t, "in.csv", "company\nAcme\nGlobex\nInitech\n")
	a, b := writeSignal(t, "Funding", "Has {{.AccountName}} raised?"), writeSignal(t, "Hiring", "Is {{.AccountName}} hiring?")
	first, _, err := cli(t, "csv", in, "--config", cfg, "-s", a, "-s", b, "--quiet")
	if err != nil {
		t.Fatal(err)
	}
	calls := stub.calls()
	second, stderr, err := cli(t, "csv", in, "--config", cfg, "-s", a, "-s", b)
	if err != nil || stub.calls() != calls || second != first {
		t.Fatalf("rerun: err=%v new calls=%d same output=%v", err, stub.calls()-calls, second == first)
	}
	if !strings.Contains(stderr, "funding=success(cached)") {
		t.Fatalf("progress = %q", stderr)
	}
	// Editing one signal re-runs only that signal: 3 rows, one research call each.
	b2 := writeSignal(t, "Hiring", "Is {{.AccountName}} hiring engineers?")
	seen := len(stub.inputs())
	if _, _, err := cli(t, "csv", in, "--config", cfg, "-s", a, "-s", b2, "--quiet"); err != nil {
		t.Fatal(err)
	}
	var research []string
	for _, input := range stub.inputs()[seen:] {
		if input != "" { // classifier calls carry no "input"
			research = append(research, input)
		}
	}
	if len(research) != 3 {
		t.Fatalf("want 3 research calls for the edited signal, got %d: %q", len(research), research)
	}
	for _, input := range research {
		if !strings.Contains(input, "hiring engineers") || strings.Contains(input, "raised") {
			t.Fatalf("an unchanged signal was re-run: %q", input)
		}
	}
	// --no-cache forces every signal again.
	before := stub.calls()
	cli(t, "csv", in, "--config", cfg, "-s", a, "-s", b, "--quiet", "--no-cache")
	if stub.calls() == before {
		t.Fatal("--no-cache made no calls")
	}
}

func TestRunCachedFlag(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	installStub(t, 200, researchHit)
	args := []string{"run", "--config", cfg, "--prompt", "{{.AccountName}}", "--account-name", "Acme"}
	first, _, _ := cli(t, args...)
	second, _, _ := cli(t, args...)
	if !strings.Contains(first, `"cached": false`) || !strings.Contains(second, `"cached": true`) {
		t.Fatalf("first=%s second=%s", first, second)
	}
}

func TestCacheClear(t *testing.T) {
	cfg := testConfig(t, "sk-abcdef123456")
	stub := installStub(t, 200, researchHit)
	args := []string{"run", "--config", cfg, "--prompt", "{{.AccountName}}", "--account-name", "Acme"}
	cli(t, args...)
	out, _, err := cli(t, "cache", "clear")
	if err != nil || !strings.Contains(out, "Removed 1 cached result") {
		t.Fatalf("err=%v out=%q", err, out)
	}
	before := stub.calls()
	cli(t, args...)
	if stub.calls() == before {
		t.Fatal("cleared cache still hit")
	}
	if out, _, err := cli(t, "cache", "clear", "--linkedin"); err != nil || !strings.Contains(out, "LinkedIn") {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

func TestRunHelpListsFieldsAndStatuses(t *testing.T) {
	for _, cmd := range []string{"run", "csv"} {
		_, stderr, err := cli(t, cmd, "--help")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Usage:", "signals " + cmd, "AccountLinkedInUrl", "PersonLi_", "1 success", "3 missing_dependencies", "THREE_MONTHS"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%s --help missing %q", cmd, want)
			}
		}
		if strings.Contains(stderr, "LAST_90_DAYS") {
			t.Errorf("%s --help still shows the invalid LAST_90_DAYS example", cmd)
		}
	}
}
