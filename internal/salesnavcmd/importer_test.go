package salesnavcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/salesnav"
	"github.com/tiga-labs/cli-tools/pkg/salesnavout"
)

const apiURL = "https://www.linkedin.com/sales-api/salesApiPeopleSearch?q=peopleSearchQuery"

type minRand struct{}

func (minRand) Int64N(int64) int64 { return 0 }

// fakeTab answers Eval with scripted fetch results, one per call.
type fakeTab struct {
	mu      sync.Mutex
	results []salesnav.FetchResult
	evalErr map[int]error
	block   map[int]bool // block until ctx is done
	exprs   []string
	closed  bool
}

func (t *fakeTab) WaitLoaded(context.Context) error { return nil }

func (t *fakeTab) Eval(ctx context.Context, expr string, out any) error {
	t.mu.Lock()
	i := len(t.exprs)
	t.exprs = append(t.exprs, expr)
	t.mu.Unlock()
	if t.block[i] {
		<-ctx.Done()
		return ctx.Err()
	}
	if err := t.evalErr[i]; err != nil {
		return err
	}
	if i >= len(t.results) {
		return fmt.Errorf("fake: no scripted result for fetch %d", i+1)
	}
	*(out.(*salesnav.FetchResult)) = t.results[i]
	return nil
}

func (t *fakeTab) Close() error { t.closed = true; return nil }

type fakeBrowser struct {
	tab    *fakeTab
	opened []string
	closed bool
}

func (b *fakeBrowser) OpenTab(_ context.Context, url string) (tab, error) {
	b.opened = append(b.opened, url)
	return b.tab, nil
}

func (b *fakeBrowser) Close() error { b.closed = true; return nil }

// harness holds one runImport call's fakes and outputs.
type harness struct {
	t              *testing.T
	tab            *fakeTab
	browser        *fakeBrowser
	sleeps         []time.Duration
	cancelAtSleep  int // cancel the run on this sleep call (1-based); 0 = never
	cancel         context.CancelFunc
	connectErr     error
	connected      bool
	stdout, stderr bytes.Buffer
}

func newHarness(t *testing.T, results ...salesnav.FetchResult) *harness {
	tab := &fakeTab{results: results, evalErr: map[int]error{}, block: map[int]bool{}}
	return &harness{t: t, tab: tab, browser: &fakeBrowser{tab: tab}}
}

func (h *harness) deps() deps {
	return deps{
		connect: func(context.Context) (browser, error) {
			h.connected = true
			if h.connectErr != nil {
				return nil, h.connectErr
			}
			return h.browser, nil
		},
		sleep: func(ctx context.Context, d time.Duration) error {
			h.sleeps = append(h.sleeps, d)
			if len(h.sleeps) == h.cancelAtSleep {
				h.cancel()
				return ctx.Err()
			}
			return ctx.Err()
		},
		rand:        minRand{},
		profileName: func() string { return "Person 1" },
	}
}

func (h *harness) run(j job) error {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	defer cancel()
	return runImport(ctx, j, h.deps(), &h.stdout, &h.stderr)
}

func listSource(t *testing.T) salesnav.Source {
	src, err := salesnav.ParseURL("https://www.linkedin.com/sales/lists/people/7")
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func searchSource(t *testing.T) salesnav.Source {
	src, err := salesnav.ParseURL("https://www.linkedin.com/sales/search/people?savedSearchId=5&sessionId=abc%3D%3D")
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func newJob(t *testing.T, src salesnav.Source, max int) job {
	t.Helper()
	path := filepath.Join(t.TempDir(), "out.csv")
	w, info, err := salesnavout.Open(salesnavout.Options{Path: path, Source: src, Max: max})
	if err != nil {
		t.Fatal(err)
	}
	return job{src: src, out: path, max: max, w: w, info: info}
}

// fetchOK is a 200 sales-api page at start with the given total and lead URN ids.
func fetchOK(start, total int, ids ...string) salesnav.FetchResult {
	type el struct {
		FirstName string `json:"firstName"`
		EntityURN string `json:"entityUrn"`
	}
	body := struct {
		Paging   map[string]int `json:"paging"`
		Elements []el           `json:"elements"`
	}{Paging: map[string]int{"start": start, "count": 25, "total": total}, Elements: []el{}}
	for _, id := range ids {
		body.Elements = append(body.Elements, el{FirstName: "F" + id, EntityURN: "urn:li:fs_salesProfile:(" + id + ",NAME_SEARCH,x)"})
	}
	b, _ := json.Marshal(body)
	return salesnav.FetchResult{Status: 200, URL: apiURL, ContentType: "application/json", Body: string(b)}
}

func ids(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return out
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) {
		t.Fatalf("err = %v, want an error with an exit code", err)
	}
	return coded.ExitCode()
}

func TestImportHappyPath(t *testing.T) {
	src := listSource(t)
	j := newJob(t, src, 1000)
	h := newHarness(t, fetchOK(0, 30, ids("a", 25)...), fetchOK(25, 30, ids("b", 5)...))
	if err := h.run(j); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, h.stderr.String())
	}
	stem := strings.TrimSuffix(j.out, ".csv")
	wantOut := fmt.Sprintf("Done: 30 leads in %s.\nNext: tiga enrich csv -i %s -o %s-enriched.csv\n", j.out, j.out, stem)
	if h.stdout.String() != wantOut {
		t.Errorf("stdout =\n%s\nwant\n%s", h.stdout.String(), wantOut)
	}
	if exists(salesnavout.StatePath(j.out)) {
		t.Error("state file left after a completed run")
	}
	if want := []time.Duration{5 * time.Second, 2 * time.Second}; fmt.Sprint(h.sleeps) != fmt.Sprint(want) {
		t.Errorf("sleeps = %v, want %v", h.sleeps, want)
	}
	if len(h.tab.exprs) != 2 || h.tab.exprs[0] != salesnav.FetchJS(src.PageURL(0)) || h.tab.exprs[1] != salesnav.FetchJS(src.PageURL(25)) {
		t.Errorf("fetches = %d, not the expected page requests", len(h.tab.exprs))
	}
	if h.browser.opened[0] != "https://www.linkedin.com/sales/home" || !h.tab.closed || !h.browser.closed {
		t.Errorf("tab/browser lifecycle: opened %v, tab closed %t, browser closed %t", h.browser.opened, h.tab.closed, h.browser.closed)
	}
	if !strings.Contains(h.stderr.String(), "page 1: 25 new leads, 25 total (max 1000); next page in 2s\n") {
		t.Errorf("stderr missing progress line: %s", h.stderr.String())
	}
}

func TestImportMax(t *testing.T) {
	j := newJob(t, listSource(t), 30)
	h := newHarness(t, fetchOK(0, 98, ids("a", 25)...), fetchOK(25, 98, ids("b", 25)...))
	if err := h.run(j); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout.String(), "Done: 30 leads in ") || !strings.Contains(h.stdout.String(), "Stopped at --max 30.\n") {
		t.Errorf("stdout = %s", h.stdout.String())
	}
	if len(h.tab.exprs) != 2 {
		t.Errorf("fetches = %d, want 2", len(h.tab.exprs))
	}
}

func TestImportStopWarning(t *testing.T) {
	src := searchSource(t)
	j := newJob(t, src, 1000)
	h := newHarness(t, fetchOK(0, 98, ids("a", 25)...), salesnav.FetchResult{Status: 403})
	err := h.run(j)
	if exitCodeOf(t, err) != 2 {
		t.Fatalf("exit code %d, want 2", exitCodeOf(t, err))
	}
	want := fmt.Sprintf("Stopped: LinkedIn answered 403: it refused the request. 25 leads saved in %s.\nWait before trying again (hours, not minutes). Resume with: tiga salesnav-list-import '%s' -o %s --resume", j.out, src.URL, j.out)
	if err.Error() != want {
		t.Errorf("err =\n%s\nwant\n%s", err, want)
	}
	if !exists(salesnavout.StatePath(j.out)) || len(h.tab.exprs) != 2 || !h.tab.closed {
		t.Errorf("state kept %t, fetches %d, tab closed %t", exists(salesnavout.StatePath(j.out)), len(h.tab.exprs), h.tab.closed)
	}
}

func TestImportEvalErrorNothingSaved(t *testing.T) {
	j := newJob(t, listSource(t), 1000)
	h := newHarness(t)
	h.tab.evalErr[0] = errors.New("javascript error: TypeError: Failed to fetch")
	err := h.run(j)
	if exitCodeOf(t, err) != 2 {
		t.Fatalf("exit code %d, want 2", exitCodeOf(t, err))
	}
	want := "Stopped: the fetch in Chrome failed: javascript error: TypeError: Failed to fetch. No leads were saved.\nWait before trying again (hours, not minutes), then run the same command again."
	if err.Error() != want {
		t.Errorf("err =\n%s\nwant\n%s", err, want)
	}
	if exists(j.out) || exists(salesnavout.StatePath(j.out)) {
		t.Error("empty new output not removed")
	}
}

func TestImportPageMismatch(t *testing.T) {
	j := newJob(t, listSource(t), 1000)
	h := newHarness(t, fetchOK(0, 98, ids("a", 25)...), fetchOK(0, 98, ids("a", 25)...), fetchOK(50, 98))
	err := h.run(j)
	if exitCodeOf(t, err) != 2 || !strings.Contains(err.Error(), "different page than requested") {
		t.Fatalf("err = %v", err)
	}
	if len(h.tab.exprs) != 2 {
		t.Errorf("fetches = %d, want 2 (no request after the mismatch)", len(h.tab.exprs))
	}
}

func TestImportEndedEarly(t *testing.T) {
	j := newJob(t, searchSource(t), 1000)
	h := newHarness(t, fetchOK(0, 3000, ids("a", 25)...), fetchOK(25, 3000))
	if err := h.run(j); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout.String(), "LinkedIn stopped returning results before its total of 3000. Sales Nav limits saved-search results; use a lead list to import more.\n") {
		t.Errorf("saved search stdout = %s", h.stdout.String())
	}
	j = newJob(t, listSource(t), 1000)
	h = newHarness(t, fetchOK(0, 3000, ids("a", 25)...), fetchOK(25, 3000))
	if err := h.run(j); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout.String(), "LinkedIn stopped returning leads before its total of 3000; the list may have changed during the import.\n") {
		t.Errorf("list stdout = %s", h.stdout.String())
	}
}

func TestImportNotLoggedInFirstPageNewRun(t *testing.T) {
	j := newJob(t, listSource(t), 1000)
	h := newHarness(t, salesnav.FetchResult{NoSession: true})
	err := h.run(j)
	if exitCodeOf(t, err) != 1 {
		t.Fatalf("exit code %d, want 1", exitCodeOf(t, err))
	}
	want := `Sales Nav is not logged in in your Chrome profile "Person 1". tiga can only use Chrome's first profile; log into Sales Nav there and run this again.`
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
	if exists(j.out) || exists(salesnavout.StatePath(j.out)) {
		t.Error("empty new output not removed")
	}

	j = newJob(t, listSource(t), 1000)
	h = newHarness(t, fetchOK(0, 98, ids("a", 25)...), salesnav.FetchResult{NoSession: true})
	err = h.run(j)
	if exitCodeOf(t, err) != 2 || !strings.HasPrefix(err.Error(), "Stopped: LinkedIn is not logged in in this Chrome profile. 25 leads saved") {
		t.Errorf("later not-logged-in: %v", err)
	}
	if !exists(j.out) {
		t.Error("output with 25 leads removed")
	}
}

func TestImportConnectFailsNewRun(t *testing.T) {
	j := newJob(t, listSource(t), 1000)
	h := newHarness(t)
	boom := errors.New("boom")
	h.connectErr = boom
	if err := h.run(j); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if exists(j.out) || exists(salesnavout.StatePath(j.out)) {
		t.Error("empty new output not removed")
	}
	if len(h.browser.opened) != 0 {
		t.Error("opened a tab after connect failed")
	}
}

func TestImportInterruptDuringWait(t *testing.T) {
	src := listSource(t)
	j := newJob(t, src, 1000)
	h := newHarness(t, fetchOK(0, 98, ids("a", 25)...))
	h.cancelAtSleep = 2
	err := h.run(j)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	want := fmt.Sprintf("Interrupted. 25 leads saved in %s. Resume with: tiga salesnav-list-import '%s' -o %s --resume\n", j.out, src.URL, j.out)
	if !strings.Contains(h.stderr.String(), want) {
		t.Errorf("stderr = %s\nwant line %s", h.stderr.String(), want)
	}
	if !exists(salesnavout.StatePath(j.out)) || !h.tab.closed || len(h.tab.exprs) != 1 {
		t.Errorf("state kept %t, tab closed %t, fetches %d", exists(salesnavout.StatePath(j.out)), h.tab.closed, len(h.tab.exprs))
	}
}

func TestImportInterruptDuringFetch(t *testing.T) {
	j := newJob(t, listSource(t), 1000)
	h := newHarness(t, fetchOK(0, 98, ids("a", 25)...))
	h.tab.block[1] = true
	go func() {
		for {
			h.tab.mu.Lock()
			n := len(h.tab.exprs)
			h.tab.mu.Unlock()
			if n == 2 {
				h.cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	err := h.run(j)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !h.tab.closed || !exists(salesnavout.StatePath(j.out)) {
		t.Errorf("tab closed %t, state kept %t", h.tab.closed, exists(salesnavout.StatePath(j.out)))
	}
}

func TestImportResume(t *testing.T) {
	src := listSource(t)
	path := filepath.Join(t.TempDir(), "out.csv")
	w, _, _ := salesnavout.Open(salesnavout.Options{Path: path, Source: src, Max: 1000})
	first, _ := salesnav.ParsePage([]byte(fetchOK(0, 98, ids("a", 25)...).Body), src)
	w.Write(first.Leads, 1000)
	w.Checkpoint(25, 98)
	w.Close()
	w, info, err := salesnavout.Open(salesnavout.Options{Path: path, Source: src, Resume: true, Max: 50})
	if err != nil {
		t.Fatal(err)
	}
	j := job{src: info.Source, out: path, max: info.Max, w: w, info: info}
	h := newHarness(t, fetchOK(25, 120, ids("b", 25)...))
	if err := h.run(j); err != nil {
		t.Fatal(err)
	}
	if h.tab.exprs[0] != salesnav.FetchJS(src.PageURL(25)) {
		t.Error("resume did not start at offset 25")
	}
	note := "Note: LinkedIn now reports 120 leads (98 when this import started); leads added or removed since then shift the pages, so some may be missed.\n"
	if strings.Count(h.stderr.String(), note) != 1 {
		t.Errorf("stderr = %s", h.stderr.String())
	}
	if h.sleeps[0] != 5*time.Second {
		t.Errorf("first sleep = %s, want FirstWait", h.sleeps[0])
	}
}

func TestImportResumeAlreadyAtMax(t *testing.T) {
	src := listSource(t)
	path := filepath.Join(t.TempDir(), "out.csv")
	w, _, _ := salesnavout.Open(salesnavout.Options{Path: path, Source: src, Max: 30})
	page, _ := salesnav.ParsePage([]byte(fetchOK(0, 98, ids("a", 25)...).Body), src)
	w.Write(page.Leads, 30)
	page, _ = salesnav.ParsePage([]byte(fetchOK(25, 98, ids("b", 25)...).Body), src)
	w.Write(page.Leads, 30)
	w.Checkpoint(50, 98)
	w.Close()
	w, info, _ := salesnavout.Open(salesnavout.Options{Path: path, Source: src, Resume: true})
	h := newHarness(t)
	if err := h.run(job{src: info.Source, out: path, max: info.Max, w: w, info: info}); err != nil {
		t.Fatal(err)
	}
	if h.connected {
		t.Error("connected to Chrome with nothing left to fetch")
	}
	if !strings.Contains(h.stdout.String(), "Done: 30 leads in ") || exists(salesnavout.StatePath(path)) {
		t.Errorf("stdout = %s, state kept %t", h.stdout.String(), exists(salesnavout.StatePath(path)))
	}
}

// The resume hint is built from the validated source and shell-quoted, so nothing pasted
// into the URL or -o can run when the hint is pasted back (S5 review I1, I2).
func TestResumeHintShellSafe(t *testing.T) {
	src, err := salesnav.ParseURL("https://www.linkedin.com/sales/lists/people/123?x=';touch /tmp/pwn;'")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "my leads.csv")
	w, info, _ := salesnavout.Open(salesnavout.Options{Path: path, Source: src, Max: 1000})
	j := job{src: src, out: path, max: 1000, w: w, info: info, chromeDir: "/opt/my chrome"}
	h := newHarness(t, fetchOK(0, 98, ids("a", 25)...), salesnav.FetchResult{Status: 429})
	err = h.run(j)
	want := "Resume with: tiga salesnav-list-import 'https://www.linkedin.com/sales/lists/people/123' -o '" + path + "' --resume --chrome-user-data-dir '/opt/my chrome'"
	if !strings.HasSuffix(err.Error(), want) {
		t.Errorf("hint =\n%s\nwant suffix\n%s", err, want)
	}
	if strings.Contains(err.Error(), "touch") {
		t.Error("pasted URL text reached the hint")
	}
}

func TestShellQuote(t *testing.T) {
	tests := map[string]string{
		"out.csv":         "out.csv",
		"/tmp/a-b_c.csv":  "/tmp/a-b_c.csv",
		"my leads.csv":    "'my leads.csv'",
		"it's.csv":        `'it'\''s.csv'`,
		"https://x/y?a&b": "'https://x/y?a&b'",
	}
	for in, want := range tests {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

// A checkpoint failure (e.g. disk full) still says how to continue (S5 review I3).
func TestImportCheckpointErrorGivesNextStep(t *testing.T) {
	src := listSource(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "out.csv")
	w, info, _ := salesnavout.Open(salesnavout.Options{Path: path, Source: src, Max: 1000})
	os.Chmod(dir, 0o500) // the CSV stays writable; the next checkpoint cannot be created
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	h := newHarness(t, fetchOK(0, 98, ids("a", 25)...))
	err := h.run(job{src: src, out: path, max: 1000, w: w, info: info})
	if exitCodeOf(t, err) != 1 {
		t.Fatalf("exit code %d, want 1", exitCodeOf(t, err))
	}
	want := "25 leads saved in " + path + ".\nFix the problem (for example free up disk space), then resume with: tiga salesnav-list-import 'https://www.linkedin.com/sales/lists/people/7' -o " + path + " --resume"
	if !strings.HasSuffix(err.Error(), want) {
		t.Errorf("err =\n%s\nwant suffix\n%s", err, want)
	}
	if len(h.tab.exprs) != 1 {
		t.Errorf("fetches = %d, want 1", len(h.tab.exprs))
	}
}

// Ctrl-C landing as a wait ends must not send the next fetch (S5 review Minor 1).
func TestImportNoFetchAfterCancel(t *testing.T) {
	j := newJob(t, listSource(t), 1000)
	h := newHarness(t, fetchOK(0, 98, ids("a", 25)...), fetchOK(25, 98, ids("b", 25)...))
	d := h.deps()
	calls := 0
	d.sleep = func(_ context.Context, _ time.Duration) error {
		calls++
		if calls == 2 {
			h.cancel() // the signal arrives, but the timer fired first
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	err := runImport(ctx, j, d, &h.stdout, &h.stderr)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(h.tab.exprs) != 1 {
		t.Errorf("fetches = %d, want 1 (no request after Ctrl-C)", len(h.tab.exprs))
	}
}
