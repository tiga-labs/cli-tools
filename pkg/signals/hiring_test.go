package signals

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type job struct{ title, location string }

func jobsJSON(jobs ...job) string {
	var items []string
	for i, j := range jobs {
		t, _ := json.Marshal(j.title + "  ")
		l, _ := json.Marshal(j.location)
		items = append(items, `{"id":"`+itoa(i)+`","url":"https://www.linkedin.com/jobs/view/`+itoa(i)+`","title":`+string(t)+`,"location":`+string(l)+`}`)
	}
	return `{"success":true,"statusCode":200,"data":{"total":` + itoa(len(jobs)) + `,"jobs":[` + strings.Join(items, ",") + `]}}`
}

func hiringSignal(t *testing.T, extra string) Signal {
	t.Helper()
	return parseSignal(t, `{"label":"Hiring","computed_config":{"type":"hiring_for_role","is_account_insight":true,"hiring_for_role":{"job_titles":["Account Executive","SDR"]`+extra+`}}}`)
}

// hiringStub serves the company id and, per search keyword, a jobs body.
func hiringStub(t *testing.T, byKeyword map[string]string) *stubOpenAI {
	t.Helper()
	withLiCache(t)
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/universal-name-to-id": always(200, companyIDJSON)})
	stub.handlers["/search/jobs"] = func(n int) *http.Response {
		stub.mu.Lock()
		kw := stub.reqs[len(stub.reqs)-1].url.Query().Get("keyword")
		stub.mu.Unlock()
		if body, ok := byKeyword[kw]; ok {
			return jsonResponse(200, body)
		}
		return jsonResponse(200, jobsJSON())
	}
	return stub
}

func TestHiringFirstTitleWins(t *testing.T) {
	titles := []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9", "Account Executive"}
	b, _ := json.Marshal(titles)
	s := parseSignal(t, `{"label":"Hiring","computed_config":{"type":"hiring_for_role","is_account_insight":true,"hiring_for_role":{"job_titles":`+string(b)+`}}}`)
	stub := hiringStub(t, map[string]string{"account executive": jobsJSON(job{"Account Executive", "Austin, TX (On-site)"})})
	res := RunHiringForRole(context.Background(), s, acmeCo, liClient(), liNow)
	if res.Status != StatusSuccess || !strings.Contains(res.Value, `with the term "Account Executive"`) || stub.count("/search/jobs") != 10 {
		t.Fatalf("res = %+v searches=%d", res, stub.count("/search/jobs"))
	}
}

func TestHiringNoneFound(t *testing.T) {
	hiringStub(t, nil)
	res := RunHiringForRole(context.Background(), hiringSignal(t, ""), acmeCo, liClient(), liNow)
	if res.Status != StatusNotFound || len(res.Sources) != 0 || res.Value != "" {
		t.Fatalf("res = %+v", res)
	}
}

func TestHiringValueText(t *testing.T) {
	hiringStub(t, map[string]string{"account executive": jobsJSON(job{"Account Executive", "Redmond, WA (Hybrid)"}, job{"AE II", "Seattle, Washington, United States"}, job{"AE", "Tokyo, Tokyo, Japan"}, job{"AE", "Washington, DC (On-site)"})})
	res := RunHiringForRole(context.Background(), hiringSignal(t, `,"location":"WA"`), acmeCo, liClient(), liNow)
	if res.Status != StatusSuccess || res.Value != `As of Wednesday, October 28, 2026 Acme has 2 jobs with the term "Account Executive" located in Washington listed on LinkedIn.` {
		t.Fatalf("res = %+v", res)
	}
	if len(res.Sources) != 2 || res.Sources[0].Title != "Job Listing (1)" || res.Sources[0].URL != "https://www.linkedin.com/jobs/view/0" {
		t.Fatalf("sources = %+v", res.Sources)
	}

	// No location: US only (DC counts), and a full page says "at least".
	var page []job
	for range 19 {
		page = append(page, job{"Account Executive", "London, England, United Kingdom"})
	}
	page = append(page, job{"Account Executive", "Washington, DC (On-site)"})
	hiringStub(t, map[string]string{"account executive": jobsJSON(page...)})
	res = RunHiringForRole(context.Background(), hiringSignal(t, ""), acmeCo, liClient(), liNow)
	if res.Status != StatusSuccess || res.Value != `As of Wednesday, October 28, 2026 Acme has at least 1 job with the term "Account Executive" listed on LinkedIn.` {
		t.Fatalf("res = %+v", res)
	}
}

func TestHiringExactMatch(t *testing.T) {
	hiringStub(t, map[string]string{"account executive": jobsJSON(job{"Customer Success Account Manager", "Austin, TX"}, job{"Senior Account Executive", "United States (Remote)"})})
	res := RunHiringForRole(context.Background(), hiringSignal(t, ""), acmeCo, liClient(), liNow)
	if !strings.Contains(res.Value, "has 2 jobs") {
		t.Fatalf("loose: res = %+v", res)
	}
	withLiCache(t)
	s := parseSignal(t, `{"label":"Hiring","computed_config":{"type":"hiring_for_role","is_account_insight":true,"exact_match_search":true,"hiring_for_role":{"job_titles":["Account Executive"]}}}`)
	res = RunHiringForRole(context.Background(), s, acmeCo, liClient(), liNow)
	if !strings.Contains(res.Value, "has 1 job ") || res.Sources[0].URL != "https://www.linkedin.com/jobs/view/1" {
		t.Fatalf("exact: res = %+v", res)
	}
}

func TestHiringSourcesCapped(t *testing.T) {
	var page []job
	for range 15 {
		page = append(page, job{"Account Executive", "Austin, TX"})
	}
	hiringStub(t, map[string]string{"account executive": jobsJSON(page...)})
	if res := RunHiringForRole(context.Background(), hiringSignal(t, ""), acmeCo, liClient(), liNow); len(res.Sources) != 10 || !strings.Contains(res.Value, "has 15 jobs") {
		t.Fatalf("res = %+v", res)
	}
}

func TestValidateHiringLocation(t *testing.T) {
	_, err := Parse([]byte(`{"label":"H","computed_config":{"type":"hiring_for_role","is_account_insight":true,"hiring_for_role":{"job_titles":["AE"],"location":"XX"}}}`))
	if err == nil || err.Error() != `invalid hiring_for_role.location "XX" (expected a US state code such as WA)` {
		t.Fatalf("err = %v", err)
	}
	if _, err := Parse([]byte(`{"label":"H","computed_config":{"type":"hiring_for_role","is_account_insight":true,"hiring_for_role":{"job_titles":["AE"],"location":"wa"}}}`)); err != nil {
		t.Fatalf("lowercase code: %v", err)
	}
}

func TestHiringNeedsNoOpenAI(t *testing.T) {
	withLiCache(t)
	o := liRunOpts()
	o.OpenAIKey = ""
	if _, err := NewRunner([]Signal{hiringSignal(t, "")}, o); err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, err := NewRunner([]Signal{hiringSignal(t, "")}, runOpts()); err == nil || !strings.Contains(err.Error(), "--buy-linkedin") {
		t.Fatalf("err = %v", err)
	}
}

func TestPlanHiringCalls(t *testing.T) {
	withLiCache(t)
	newStubOpenAI(t, map[string]func(int) *http.Response{})
	p := mustRunner(t, []Signal{hiringSignal(t, "")}, liRunOpts()).Plan([]Record{acmeCo, acmeCo})[0]
	// One company: 1 id lookup + 2 titles; each search costs 2 credits.
	if p.LinkdAPICalls != 3 || p.LinkdAPICredits != 1+2*2 || p.MaxOpenAICalls != 0 || p.ResearchCalls != 1 {
		t.Fatalf("plan = %+v", p)
	}
}

func TestJobInLocation(t *testing.T) {
	cases := []struct {
		loc, state string
		want       bool
	}{
		{"Redmond, WA (Hybrid)", "WA", true},
		{"Seattle, Washington, United States", "WA", true},
		{"Washington, England, United Kingdom", "WA", false},
		{"Washington, England, United Kingdom", "", false},
		{"Tbilisi, Tbilisi, Georgia", "GA", false},
		{"Tbilisi, Tbilisi, Georgia (On-site)", "", false},
		{"Atlanta, GA", "GA", true},
		{"Washington, DC (On-site)", "WA", false},
		{"Washington, DC (On-site)", "DC", true},
		{"Washington, D.C.", "DC", true},
		{"Greater Seattle Area", "WA", true},
		{"San Francisco Bay Area", "CA", true},
		{"New York City Metropolitan Area", "", true},
		{"Washington DC-Baltimore Area", "DC", true},
		{"Washington DC-Baltimore Area", "WA", false},
		{"United States (Remote)", "", true},
		{"United States (Remote)", "WA", false},
		{"London, England, United Kingdom", "", false},
		{"Tokyo, Tokyo, Japan (Hybrid)", "", false},
	}
	for _, c := range cases {
		if got := jobInLocation(c.loc, c.state); got != c.want {
			t.Errorf("jobInLocation(%q, %q) = %v, want %v", c.loc, c.state, got, c.want)
		}
	}
}

func TestHiringShareKeyIncludesAccountName(t *testing.T) {
	// Rows that share a result must not carry another row's company name into the value.
	stub := hiringStub(t, map[string]string{"account executive": jobsJSON(job{"Account Executive", "Austin, TX"})})
	r := mustRunner(t, []Signal{hiringSignal(t, "")}, liRunOpts())
	a := r.Run(context.Background(), acmeCo)
	b := r.Run(context.Background(), acmeCo.With("AccountName", "Acme Corp"))
	if !strings.Contains(a[0].Value, " Acme has ") || !strings.Contains(b[0].Value, " Acme Corp has ") || stub.count("/search/jobs") != 1 {
		t.Fatalf("a=%q b=%q searches=%d", a[0].Value, b[0].Value, stub.count("/search/jobs"))
	}
}
