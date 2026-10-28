package signals

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

const companyIDJSON = `{"success":true,"statusCode":200,"data":{"id":"42","universalName":"acme"}}`

// companyPostsJSON is a synthetic company-posts page; each post is (text, age in days).
func companyPostsJSON(posts ...any) string { return postsJSON(posts...) }

var acmeCo = NewRecord(map[string]string{"AccountName": "Acme", "AccountLinkedInUrl": "https://www.linkedin.com/company/acme"})

func accountPostsSignal(t *testing.T, extra string) Signal {
	t.Helper()
	return parseSignal(t, `{"label":"Co posts","computed_config":{"type":"linkedin_posts","is_account_insight":true,"prompt":"Did they announce funding?"`+extra+`}}`)
}

func hiredExecSignal(t *testing.T) Signal {
	t.Helper()
	return parseSignal(t, `{"label":"Exec hire","computed_config":{"type":"hired_executive","is_account_insight":true,"prompt":"CFO, CRO"}}`)
}

func categoryVerdict(category string) *http.Response {
	return jsonResponse(200, `{"choices":[{"message":{"content":"{\"category\":\"`+category+`\",\"reason\":\"x\"}"}}]}`)
}

// accountStub serves the company id, one page of posts (later pages empty), and OpenAI.
func accountStub(t *testing.T, page0 string, responses, chat func(int) *http.Response) *stubOpenAI {
	t.Helper()
	withLiCache(t)
	if chat == nil {
		chat = always(500, `{}`)
	}
	return newStubOpenAI(t, map[string]func(int) *http.Response{
		"/universal-name-to-id": always(200, companyIDJSON),
		"/companies/company/posts": func(n int) *http.Response {
			if n == 1 {
				return jsonResponse(200, page0)
			}
			return jsonResponse(200, companyPostsJSON())
		},
		"/responses":        responses,
		"/chat/completions": chat,
	})
}

func TestAccountPostsFirstMatchWins(t *testing.T) {
	stub := accountStub(t, companyPostsJSON("Lunch", 1, "We raised $20M!", 2), func(n int) *http.Response {
		switch n {
		case 1:
			return jsonFound(false, "")
		case 2:
			return jsonFound(true, "yes")
		default:
			return jsonFound(true, "Acme raised $20M.")
		}
	}, nil)
	res := RunAccountPosts(context.Background(), accountPostsSignal(t, ""), acmeCo, liClient(), postsOpts())
	if res.Status != StatusSuccess || res.Value != "Acme raised $20M." || res.Sources[0].Title != "LinkedIn Post" {
		t.Fatalf("res = %+v", res)
	}
	if in := stub.last("/responses").body["input"].(string); !strings.Contains(in, `You are looking at a LinkedIn post from the company "Acme".`) || !strings.Contains(in, `"Did they announce funding?"`) {
		t.Fatalf("input = %s", in)
	}
}

func TestAccountPostsPaging(t *testing.T) {
	// Page n holds posts aged 10n+11 and 10n+12 days. With no date_range all 3 pages are read;
	// with TWO_WEEKS, page 1's 22-day-old post is outside the window, so paging stops there.
	withLiCache(t)
	page := func(ages ...int) string {
		var p []any
		for _, a := range ages {
			p = append(p, fmt.Sprintf("post %d", a), a)
		}
		return companyPostsJSON(p...)
	}
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{
		"/universal-name-to-id": always(200, companyIDJSON),
		"/companies/company/posts": func(n int) *http.Response {
			return jsonResponse(200, page(n*10+1, n*10+2)) // page n has posts aged 10n+1, 10n+2 days
		},
		"/responses": func(int) *http.Response { return jsonFound(false, "") },
	})
	RunAccountPosts(context.Background(), accountPostsSignal(t, ""), acmeCo, liClient(), postsOpts())
	if stub.count("/companies/company/posts") != 3 {
		t.Fatalf("no date_range: pages = %d", stub.count("/companies/company/posts"))
	}
	withLiCache(t)
	stub.mu.Lock()
	stub.counts = map[string]int{}
	stub.mu.Unlock()
	RunAccountPosts(context.Background(), accountPostsSignal(t, `,"date_range":"TWO_WEEKS"`), acmeCo, liClient(), postsOpts())
	if stub.count("/companies/company/posts") != 2 {
		t.Fatalf("TWO_WEEKS: pages = %d (page 1 holds a 22-day-old post, which ends paging)", stub.count("/companies/company/posts"))
	}
}

func TestHiredExecutiveOnlyHiredCategory(t *testing.T) {
	stub := accountStub(t, companyPostsJSON("Our CEO spoke at Dreamforce", 1),
		func(int) *http.Response { return jsonFound(true, "would say found") },
		func(int) *http.Response { return categoryVerdict("executive_spoke_at_event") })
	if res := RunAccountPosts(context.Background(), hiredExecSignal(t), acmeCo, liClient(), postsOpts()); res.Status != StatusNotFound || stub.count("/responses") != 0 {
		t.Fatalf("res = %+v gen=%d", res, stub.count("/responses"))
	}
}

func TestHiredExecutiveHit(t *testing.T) {
	stub := accountStub(t, companyPostsJSON("Welcome Jane Roe, our new CFO", 3),
		func(int) *http.Response { return jsonFound(true, "Acme hired Jane Roe as CFO.") },
		func(int) *http.Response { return categoryVerdict("executive_hired") })
	res := RunAccountPosts(context.Background(), hiredExecSignal(t), acmeCo, liClient(), postsOpts())
	if res.Status != StatusSuccess || res.Value != "Acme hired Jane Roe as CFO." || !strings.Contains(res.Sources[0].URL, "activity") {
		t.Fatalf("res = %+v", res)
	}
	chat := stub.last("/chat/completions").body
	rf, _ := json.Marshal(chat["response_format"])
	for _, want := range []string{`"name":"hired_executive_post_classification"`, `"strict":true`, `"executive_hired"`, `"not_about_executive"`, `"executive_received_award"`} {
		if !strings.Contains(string(rf), want) {
			t.Errorf("response_format missing %s: %s", want, rf)
		}
	}
	if chat["model"] != ClassifierModel {
		t.Fatalf("model = %v", chat["model"])
	}
	gen := stub.last("/responses").body["input"].(string)
	if !strings.Contains(gen, `has hired any of these executive titles:`) || !strings.Contains(gen, `"CFO, CRO"`) || !strings.Contains(gen, "Welcome Jane Roe") {
		t.Fatalf("generation input = %s", gen)
	}
}

func TestAccountPostsBadCompanyURL(t *testing.T) {
	withLiCache(t)
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{})
	rec := NewRecord(map[string]string{"AccountName": "Acme", "AccountLinkedInUrl": "https://www.linkedin.com/in/jane-doe"})
	res := RunAccountPosts(context.Background(), accountPostsSignal(t, ""), rec, liClient(), postsOpts())
	if res.Status != StatusMissingDependencies || res.Error != "missing: AccountLinkedInUrl (not a LinkedIn company URL)" || stub.calls() != 0 {
		t.Fatalf("res = %+v calls=%d", res, stub.calls())
	}
}

func TestAccountPostsCompanyNotFound(t *testing.T) {
	withLiCache(t)
	newStubOpenAI(t, map[string]func(int) *http.Response{"/universal-name-to-id": always(404, `{"success":false,"statusCode":404}`)})
	res := RunAccountPosts(context.Background(), accountPostsSignal(t, ""), acmeCo, liClient(), postsOpts())
	if res.Status != StatusNotFound || res.Error != "linkdapi: company not found" {
		t.Fatalf("res = %+v", res)
	}
}

func TestAccountNameFallsBackToSlug(t *testing.T) {
	stub := accountStub(t, companyPostsJSON("hello", 1), func(int) *http.Response { return jsonFound(false, "") }, nil)
	rec := NewRecord(map[string]string{"AccountLinkedInUrl": "https://www.linkedin.com/company/acme"})
	RunAccountPosts(context.Background(), accountPostsSignal(t, ""), rec, liClient(), postsOpts())
	if in := stub.last("/responses").body["input"].(string); !strings.Contains(in, `from the company "acme"`) {
		t.Fatalf("input = %s", in)
	}
}

func TestRunnerOneCompanyFetch(t *testing.T) {
	stub := accountStub(t, companyPostsJSON("Welcome our new CFO", 1),
		func(int) *http.Response { return jsonFound(true, "hit") },
		func(int) *http.Response { return categoryVerdict("executive_hired") })
	r := mustRunner(t, []Signal{accountPostsSignal(t, ""), hiredExecSignal(t), gptDef(t, "Recent", "Summarize {{.CompanyLi_RecentPosts}}")}, liRunOpts())
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Run(context.Background(), acmeCo.With("FirstName", fmt.Sprint("p", i)))
		}()
	}
	wg.Wait()
	if stub.count("/universal-name-to-id") != 1 || stub.count("/companies/company/posts") != 2 {
		t.Fatalf("id=%d posts pages=%d (page 1 is empty, which ends paging)", stub.count("/universal-name-to-id"), stub.count("/companies/company/posts"))
	}
}

func TestCompanyLiPostsInGPT(t *testing.T) {
	stub := accountStub(t, companyPostsJSON("We raised $20M!", 1, "Old news", 40), func(int) *http.Response { return jsonResponse(200, researchHit) }, nil)
	res := mustRunner(t, []Signal{gptDef(t, "Recent", "Summarize {{.CompanyLi_RecentPosts}}")}, liRunOpts()).Run(context.Background(), acmeCo)
	in := stub.last("/responses").body["input"].(string)
	if res[0].Status != StatusSuccess || !strings.HasPrefix(in, "Summarize 2026-10-27: We raised $20M!") || strings.Contains(in, "Old news") || stub.count("/companies/company/posts") != 1 {
		t.Fatalf("res=%+v input=%q", res, in)
	}
}

func TestNewRunnerAccountTypesNeedBuy(t *testing.T) {
	withLiCache(t)
	for _, s := range []Signal{accountPostsSignal(t, ""), hiredExecSignal(t)} {
		if _, err := NewRunner([]Signal{s}, runOpts()); err == nil || !strings.Contains(err.Error(), "--buy-linkedin") {
			t.Errorf("%s: err = %v", s.Label, err)
		}
		if _, err := NewRunner([]Signal{s}, liRunOpts()); err != nil {
			t.Errorf("%s with buy: err = %v", s.Label, err)
		}
	}
}

func TestPlanAccountPostsCalls(t *testing.T) {
	withLiCache(t)
	newStubOpenAI(t, map[string]func(int) *http.Response{})
	other := NewRecord(map[string]string{"AccountLinkedInUrl": "https://www.linkedin.com/company/globex"})
	p := mustRunner(t, []Signal{hiredExecSignal(t)}, liRunOpts()).Plan([]Record{acmeCo, acmeCo, other})[0]
	if p.RowsReady != 3 || p.ResearchCalls != 2 || p.LinkdAPICalls != 2*(1+3) || p.MaxOpenAICalls != 2*2*maxPostsClassified {
		t.Fatalf("plan = %+v", p)
	}
}
