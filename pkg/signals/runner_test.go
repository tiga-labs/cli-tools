package signals

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/tiga-labs/cli-tools/pkg/keystore"
)

func parseSignal(t *testing.T, body string) Signal {
	t.Helper()
	s, err := Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func gptDef(t *testing.T, label, prompt string) Signal {
	t.Helper()
	b, _ := jsonString(prompt)
	l, _ := jsonString(label)
	return parseSignal(t, `{"label":`+l+`,"computed_config":{"type":"gpt","prompt":`+b+`}}`)
}

func runOpts() RunOptions {
	return RunOptions{OpenAIKey: "sk-test", Now: gptOpts().Now}
}

func mustRunner(t *testing.T, sigs []Signal, opt RunOptions) *Runner {
	t.Helper()
	r, err := NewRunner(sigs, opt)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestVendorsRegistry(t *testing.T) {
	cfg := keystore.New(Vendors, nil)
	if got, err := cfg.NormalizeVendor("LinkdAPI"); err != nil || got != VendorLinkdAPI {
		t.Fatalf("NormalizeVendor = %q, %v", got, err)
	}
	t.Setenv("LINKDAPI_API_KEY", "")
	t.Setenv("LINKDAPI_KEY", "ld-env")
	if got := cfg.GetKey(VendorLinkdAPI); got != "ld-env" {
		t.Fatalf("GetKey = %q", got)
	}
	if Vendors[0].Name != VendorOpenAI {
		t.Fatalf("order = %v", Vendors)
	}
}

func TestNewRunnerErrors(t *testing.T) {
	gpt := gptDef(t, "Funding", "Has {{.AccountName}} raised?")
	snr := parseSignal(t, `{"label":"Exec","computed_config":{"type":"hired_executive","is_account_insight":true,"prompt":"CFO"}}`)
	cases := []struct {
		name string
		sigs []Signal
		opt  RunOptions
		want string
	}{
		{"none", nil, runOpts(), "at least one signal is required"},
		{"type", []Signal{snr}, runOpts(), `signal "Exec": type hired_executive is not available in this version yet`},
		{"key", []Signal{gpt}, RunOptions{}, "signals keys set openai"},
	}
	for _, c := range cases {
		if _, err := NewRunner(c.sigs, c.opt); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
}

func TestNewRunnerDuplicateKeys(t *testing.T) {
	_, err := NewRunner([]Signal{gptDef(t, "Funding", "a"), gptDef(t, "funding!", "b")}, runOpts())
	if err == nil || !strings.Contains(err.Error(), `"Funding"`) || !strings.Contains(err.Error(), `"funding!"`) || !strings.Contains(err.Error(), `"funding"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunnerRunsInOrder(t *testing.T) {
	newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, researchHit)})
	r := mustRunner(t, []Signal{gptDef(t, "A", "a {{.AccountName}}"), gptDef(t, "B", "b {{.AccountName}}")}, runOpts())
	res := r.Run(context.Background(), acme)
	if len(res) != 2 || res[0].Status != StatusSuccess || res[1].Status != StatusSuccess {
		t.Fatalf("res = %+v", res)
	}
	if keys := []string{r.Signals()[0].Key(), r.Signals()[1].Key()}; keys[0] != "a" || keys[1] != "b" {
		t.Fatalf("keys = %v", keys)
	}
}

func TestRunnerChainPassesHit(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, researchHit)})
	r := mustRunner(t, []Signal{gptDef(t, "Funding", "Has {{.AccountName}} raised?"), gptDef(t, "Tier", "Given {{.funding}}, tier?")}, runOpts())
	res := r.Run(context.Background(), acme)
	if res[1].Status != StatusSuccess {
		t.Fatalf("res = %+v", res)
	}
	if input := stub.last("/responses").body["input"].(string); input != "Given Yes, Acme raised $10M., tier?" {
		t.Fatalf("input = %q", input)
	}
}

func TestRunnerChainSkipsOnMiss(t *testing.T) {
	for name, resp := range map[string]func(int) *http.Response{
		"not_found": always(200, outputText("Yes")), // no citations -> status 2
		"failed":    always(500, `{}`),
	} {
		t.Run(name, func(t *testing.T) {
			stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": resp})
			r := mustRunner(t, []Signal{gptDef(t, "Funding", "Has {{.AccountName}} raised?"), gptDef(t, "Tier", "Given {{.funding}}, tier?")}, runOpts())
			res := r.Run(context.Background(), acme)
			if res[0].Status == StatusSuccess || res[1].Status != StatusMissingDependencies || res[1].Error != "missing: funding" {
				t.Fatalf("res = %+v", res)
			}
			if stub.count("/responses") != 1 {
				t.Fatalf("calls = %d", stub.count("/responses"))
			}
		})
	}
}

func TestRunnerSharesIdenticalWork(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, researchHit)})
	r := mustRunner(t, []Signal{gptDef(t, "Funding", "Has {{.AccountName}} raised?")}, runOpts())
	var wg sync.WaitGroup
	results := make([][]Result, 10)
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = r.Run(context.Background(), NewRecord(map[string]string{"AccountName": "Acme", "FirstName": "P" + string(rune('a'+i))}))
		}()
	}
	wg.Wait()
	if stub.count("/responses") != 1 {
		t.Fatalf("calls = %d", stub.count("/responses"))
	}
	for _, res := range results {
		if res[0].Status != StatusSuccess || res[0].Value != results[0][0].Value {
			t.Fatalf("res = %+v", res)
		}
	}
	r.Run(context.Background(), NewRecord(map[string]string{"AccountName": "Globex"}))
	if stub.count("/responses") != 2 {
		t.Fatalf("different input must call again: %d", stub.count("/responses"))
	}
}

func TestRunnerShareKeyIncludesPrecondition(t *testing.T) {
	s := parseSignal(t, `{"label":"F","computed_config":{"type":"gpt","prompt":"{{.AccountName}}","has_precondition":true,"precondition":{"property":"AccountIndustry","property_type":"text","operator":"IS_NOT_EMPTY"}}}`)
	a := shareKey(s, NewRecord(map[string]string{"AccountName": "Acme", "AccountIndustry": "Software"}))
	b := shareKey(s, NewRecord(map[string]string{"AccountName": "Acme"}))
	if a == b {
		t.Fatal("precondition field must be part of the share key")
	}
}

func TestRunnerRedacts(t *testing.T) {
	newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(401, `{"error":{"message":"Incorrect API key provided: sk-test"}}`)})
	opt := runOpts()
	opt.Redact = func(s string) string { return strings.ReplaceAll(s, "sk-test", "[REDACTED]") }
	res := mustRunner(t, []Signal{gptDef(t, "F", "{{.AccountName}}")}, opt).Run(context.Background(), acme)
	if res[0].Status != StatusFailed || strings.Contains(res[0].Error, "sk-test") || !strings.Contains(res[0].Error, "[REDACTED]") {
		t.Fatalf("res = %+v", res)
	}
}

func TestRunnerNoWebSearch(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, outputText(`{"output":"x","found":true}`))})
	opt := runOpts()
	opt.NoWebSearch = true
	mustRunner(t, []Signal{gptDef(t, "F", "{{.AccountName}}")}, opt).Run(context.Background(), acme)
	if _, ok := stub.last("/responses").body["tools"]; ok {
		t.Fatal("tools sent with NoWebSearch")
	}
}

func TestRunnerPlan(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{})
	opt := runOpts()
	opt.Bool = true
	r := mustRunner(t, []Signal{gptDef(t, "Funding", "Has {{.AccountName}} raised?"), gptDef(t, "Tier", "Given {{.funding}}")}, opt)
	recs := []Record{acme, NewRecord(nil), NewRecord(map[string]string{"AccountName": "Acme", "FirstName": "Z"})}
	plan := r.Plan(recs)
	p := plan[0]
	if p.Key != "funding" || p.Type != TypeGPT || p.Model != DefaultGPTModel || !p.WebSearch || !p.Bool ||
		p.RowsReady != 2 || p.RowsMissingDeps != 1 || p.ResearchCalls != 1 || p.MaxOpenAICalls != 2 {
		t.Fatalf("plan[0] = %+v", p)
	}
	// Tier assumes funding hits, so its missing row is the one with no AccountName (funding missing).
	if plan[1].RowsReady != 2 || plan[1].RowsMissingDeps != 1 {
		t.Fatalf("plan[1] = %+v", plan[1])
	}
	if stub.calls() != 0 {
		t.Fatal("Plan made HTTP calls")
	}
}

func TestRunnerChainMissShadowsInputColumn(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, outputText("Yes"))}) // no citations -> status 2
	r := mustRunner(t, []Signal{gptDef(t, "Funding", "Has {{.AccountName}} raised?"), gptDef(t, "Tier", "Given {{.funding}}, tier?")}, runOpts())
	res := r.Run(context.Background(), NewRecord(map[string]string{"AccountName": "Acme", "funding": "stale column value"}))
	if res[1].Status != StatusMissingDependencies || stub.count("/responses") != 1 {
		t.Fatalf("res = %+v calls=%d", res, stub.count("/responses"))
	}
}

func TestRunnerPlanPreconditionOnChainedValue(t *testing.T) {
	newStubOpenAI(t, map[string]func(int) *http.Response{})
	tier := parseSignal(t, `{"label":"Tier","computed_config":{"type":"gpt","prompt":"Tier for {{.AccountName}}","has_precondition":true,"precondition":{"property":"funding","property_type":"text","operator":"ILIKE","value":"Yes"}}}`)
	r := mustRunner(t, []Signal{gptDef(t, "Funding", "Has {{.AccountName}} raised?"), tier}, runOpts())
	plan := r.Plan([]Record{acme, NewRecord(map[string]string{"AccountName": "Globex"})})
	if plan[1].RowsReady != 2 || plan[1].RowsPreconditionFailed != 0 || plan[1].ResearchCalls != 2 {
		t.Fatalf("plan[1] = %+v", plan[1])
	}
}
