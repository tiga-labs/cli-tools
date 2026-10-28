package signals

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/linkdapi"
	"github.com/tiga-labs/cli-tools/pkg/resultcache"
)

func withResultCache(t *testing.T) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "signals")
	prev := resultcache.Dir
	resultcache.Dir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { resultcache.Dir = prev })
}

func cacheOpts() RunOptions {
	o := runOpts()
	o.ResultCache = true
	return o
}

func TestCacheKeyChangesWithInputs(t *testing.T) {
	base := gptDef(t, "Funding", "Has {{.AccountName}} raised?")
	r := mustRunner(t, []Signal{base}, runOpts())
	key := r.resultKey(base, acme)

	relabeled := base
	relabeled.Label = "Renamed"
	if r.resultKey(relabeled, acme) != key {
		t.Error("a label change must not change the key")
	}

	changes := map[string]string{
		"prompt": mustRunner(t, []Signal{gptDef(t, "Funding", "Did {{.AccountName}} raise?")}, runOpts()).resultKey(gptDef(t, "Funding", "Did {{.AccountName}} raise?"), acme),
		"input":  r.resultKey(base, NewRecord(map[string]string{"AccountName": "Globex"})),
	}
	withModel := base
	withModel.Config.Model = "gpt-x"
	changes["definition model"] = r.resultKey(withModel, acme)
	for name, opt := range map[string]func(*RunOptions){
		"--model":         func(o *RunOptions) { o.Model = "gpt-y" },
		"--bool off":      func(o *RunOptions) { o.Bool = false },
		"--bool on":       func(o *RunOptions) { o.Bool = true },
		"--verify":        func(o *RunOptions) { o.Verify = true },
		"--no-web-search": func(o *RunOptions) { o.NoWebSearch = true },
	} {
		o := runOpts()
		o.Bool = false
		opt(&o)
		changes[name] = mustRunner(t, []Signal{base}, o).resultKey(base, acme)
	}
	o := runOpts()
	o.Bool = false
	offKey := mustRunner(t, []Signal{base}, o).resultKey(base, acme)
	seen := map[string]string{"base": key}
	for name, k := range changes {
		if name == "--bool off" {
			if k != offKey {
				t.Error("same options must give the same key")
			}
			continue
		}
		for prev, pk := range seen {
			if k == pk {
				t.Errorf("%s gives the same key as %s", name, prev)
			}
		}
		seen[name] = k
	}

	hiring := hiringSignal(t, "")
	hr := mustRunner(t, []Signal{hiring}, liRunOpts())
	if hr.resultKey(hiring, acmeCo) == hr.resultKey(hiring, acmeCo.With("AccountName", "Acme Corp")) {
		t.Error("AccountName must be in the key for account types")
	}
	exact := hiring
	exact.Config.ExactMatchSearch = true
	if hr.resultKey(hiring, acmeCo) == hr.resultKey(exact, acmeCo) {
		t.Error("exact_match_search must change the key")
	}
}

func TestRunnerCachesHitAndNotFound(t *testing.T) {
	withResultCache(t)
	for name, body := range map[string]string{"hit": researchHit, "not found": outputText("No.")} {
		t.Run(name, func(t *testing.T) {
			withResultCache(t)
			stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, body)})
			s := gptDef(t, "Funding", "Has {{.AccountName}} raised?")
			first := mustRunner(t, []Signal{s}, cacheOpts()).Run(context.Background(), acme)[0]
			second := mustRunner(t, []Signal{s}, cacheOpts()).Run(context.Background(), acme)[0]
			if stub.count("/responses") != 1 || first.Cached || !second.Cached || second.Status != first.Status || second.Value != first.Value || len(second.Sources) != len(first.Sources) {
				t.Fatalf("first=%+v second=%+v calls=%d", first, second, stub.count("/responses"))
			}
		})
	}
}

func TestRunnerDoesNotCacheFailures(t *testing.T) {
	withResultCache(t)
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(500, `{}`)})
	s := gptDef(t, "Funding", "Has {{.AccountName}} raised?")
	for range 2 {
		mustRunner(t, []Signal{s}, cacheOpts()).Run(context.Background(), acme)
	}
	if stub.count("/responses") != 2 {
		t.Fatalf("a failed result was cached: calls=%d", stub.count("/responses"))
	}
	files, _ := filepathGlob(t)
	if len(files) != 0 {
		t.Fatalf("missing/precondition/failed results stored: %v", files)
	}
	mustRunner(t, []Signal{s}, cacheOpts()).Run(context.Background(), NewRecord(nil)) // status 3
	pre := parseSignal(t, `{"label":"P","computed_config":{"type":"gpt","prompt":"{{.AccountName}}","has_precondition":true,"precondition":{"property":"AccountIndustry","property_type":"text","operator":"IS_NOT_EMPTY"}}}`)
	mustRunner(t, []Signal{pre}, cacheOpts()).Run(context.Background(), acme) // status 5
	if files, _ := filepathGlob(t); len(files) != 0 {
		t.Fatalf("status 3/5 stored: %v", files)
	}
}

func filepathGlob(t *testing.T) ([]string, error) {
	dir, _ := resultcache.Dir()
	return filepath.Glob(filepath.Join(dir, "*.json"))
}

func TestRunnerCacheConcurrent(t *testing.T) {
	withResultCache(t)
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, researchHit)})
	s := gptDef(t, "Funding", "Has {{.AccountName}} raised?")
	mustRunner(t, []Signal{s}, cacheOpts()).Run(context.Background(), acme) // warm
	r := mustRunner(t, []Signal{s}, cacheOpts())
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if res := r.Run(context.Background(), acme)[0]; !res.Cached {
				t.Error("expected a cached result")
			}
		}()
	}
	wg.Wait()
	if stub.count("/responses") != 1 {
		t.Fatalf("calls = %d", stub.count("/responses"))
	}
}

func TestRunnerNoCacheRereadsAndRewrites(t *testing.T) {
	withResultCache(t)
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": func(n int) *http.Response {
		if n == 1 {
			return jsonResponse(200, researchHit)
		}
		return jsonResponse(200, strings.Replace(researchHit, "$10M", "$99M", 1))
	}})
	s := gptDef(t, "Funding", "Has {{.AccountName}} raised?")
	mustRunner(t, []Signal{s}, cacheOpts()).Run(context.Background(), acme)
	noCache := runOpts()
	noCache.ResultCache = true
	noCache.NoCache = true
	forced := mustRunner(t, []Signal{s}, noCache).Run(context.Background(), acme)[0]
	after := mustRunner(t, []Signal{s}, cacheOpts()).Run(context.Background(), acme)[0]
	if forced.Cached || !strings.Contains(forced.Value, "$99M") || !after.Cached || !strings.Contains(after.Value, "$99M") || stub.count("/responses") != 2 {
		t.Fatalf("forced=%+v after=%+v calls=%d", forced, after, stub.count("/responses"))
	}
}

func TestRunnerRefreshLinkedInSkipsCacheForLinkedInSignals(t *testing.T) {
	withResultCache(t)
	withLiCache(t)
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{
		"/profile/full": always(200, profileJSON(position(42, "Acme", "VP Sales", ymdJSON(2026, 9, 1), current))),
		"/responses":    always(200, researchHit),
	})
	opt := liRunOpts()
	opt.ResultCache = true
	sigs := []Signal{snrSignal(t, ""), gptDef(t, "Funding", "Has {{.AccountName}} raised?")}
	rec := jane.With("AccountName", "Acme")
	mustRunner(t, sigs, opt).Run(context.Background(), rec)
	opt.RefreshLinkedIn = true
	res := mustRunner(t, sigs, opt).Run(context.Background(), rec)
	if res[0].Cached || !res[1].Cached || stub.count("/profile/full") != 2 || stub.count("/responses") != 1 {
		t.Fatalf("res=%+v profile=%d responses=%d", res, stub.count("/profile/full"), stub.count("/responses"))
	}
}

func TestResultTTL(t *testing.T) {
	r := mustRunner(t, []Signal{gptDef(t, "F", "{{.AccountName}}")}, liRunOpts())
	day := 24 * time.Hour
	cases := map[string]struct {
		s    Signal
		want time.Duration
	}{
		"gpt default":   {gptDef(t, "F", "{{.AccountName}}"), 30 * day},
		"gpt 5 days":    {parseSignal(t, `{"label":"F","computed_config":{"type":"gpt","prompt":"{{.AccountName}}","expiration_in_days":5}}`), 5 * day},
		"profile":       {snrSignal(t, ""), linkdapi.ProfileTTL},
		"posts":         {postsSignal(t, ""), linkdapi.PostsTTL},
		"hiring":        {hiringSignal(t, ""), linkdapi.JobsTTL},
		"gpt PersonLi":  {gptDef(t, "P", "{{.PersonLi_Headline}}"), linkdapi.ProfileTTL},
		"gpt CompanyLi": {gptDef(t, "C", "{{.CompanyLi_Posts}}"), linkdapi.PostsTTL},
	}
	for name, c := range cases {
		if got := r.resultTTL(c.s); got != c.want {
			t.Errorf("%s: ttl = %v, want %v", name, got, c.want)
		}
	}
}

func TestCachedHitFeedsChain(t *testing.T) {
	withResultCache(t)
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, researchHit)})
	sigs := []Signal{gptDef(t, "Funding", "Has {{.AccountName}} raised?"), gptDef(t, "Tier", "Given {{.funding}}, tier?")}
	mustRunner(t, sigs, cacheOpts()).Run(context.Background(), acme)
	res := mustRunner(t, sigs, cacheOpts()).Run(context.Background(), acme)
	if !res[0].Cached || !res[1].Cached || stub.count("/responses") != 2 {
		t.Fatalf("res=%+v calls=%d", res, stub.count("/responses"))
	}
}

func TestPlanCachedRows(t *testing.T) {
	withResultCache(t)
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, researchHit)})
	sigs := []Signal{gptDef(t, "Funding", "Has {{.AccountName}} raised?"), gptDef(t, "Tier", "Given {{.funding}}, tier?")}
	mustRunner(t, sigs, cacheOpts()).Run(context.Background(), acme)
	before := stub.calls()
	plan := mustRunner(t, sigs, cacheOpts()).Plan([]Record{acme, acme, NewRecord(map[string]string{"AccountName": "Globex"})})
	if plan[0].CachedRows != 2 || plan[0].ResearchCalls != 1 || plan[1].CachedRows != 2 || plan[1].ResearchCalls != 1 || stub.calls() != before {
		t.Fatalf("plan = %+v / %+v", plan[0], plan[1])
	}
}

func TestCacheKeyIncludesWindowDayForLinkedInTypes(t *testing.T) {
	// A started_new_role hit must not be served after its role falls out of the window.
	snr := snrSignal(t, "")
	r1 := mustRunner(t, []Signal{snr}, liRunOpts())
	o := liRunOpts()
	o.Now = func() time.Time { return liNow.AddDate(0, 0, 5) }
	r2 := mustRunner(t, []Signal{snr}, o)
	if r1.resultKey(snr, jane) == r2.resultKey(snr, jane) {
		t.Error("started_new_role key must change with the window day")
	}
	// gpt keys stay the same across days (R1).
	g := gptDef(t, "F", "{{.AccountName}}")
	og := runOpts()
	og.Now = func() time.Time { return liNow.AddDate(0, 0, 5) }
	if mustRunner(t, []Signal{g}, runOpts()).resultKey(g, acme) != mustRunner(t, []Signal{g}, og).resultKey(g, acme) {
		t.Error("gpt keys must not change by day")
	}
}

func TestCacheKeyIncludesLogicVersion(t *testing.T) {
	g := gptDef(t, "F", "{{.AccountName}}")
	r := mustRunner(t, []Signal{g}, runOpts())
	before := r.resultKey(g, acme)
	prev := cacheLogicVersion
	cacheLogicVersion = prev + "-test"
	defer func() { cacheLogicVersion = prev }()
	if r.resultKey(g, acme) == before {
		t.Error("changing the runner logic version must change every key")
	}
}

func TestRunnerCacheConcurrentCold(t *testing.T) {
	withResultCache(t)
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": func(int) *http.Response {
		time.Sleep(20 * time.Millisecond) // keep the first execution in flight while the others arrive
		return jsonResponse(200, researchHit)
	}})
	r := mustRunner(t, []Signal{gptDef(t, "Funding", "Has {{.AccountName}} raised?")}, cacheOpts())
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); r.Run(context.Background(), acme) }()
	}
	wg.Wait()
	files, _ := filepathGlob(t)
	if stub.count("/responses") != 1 || len(files) != 1 {
		t.Fatalf("calls=%d files=%d", stub.count("/responses"), len(files))
	}
}
