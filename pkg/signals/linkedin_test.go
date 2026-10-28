package signals

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/linkdapi"
)

var liNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// withLiCache points the linkdapi cache at a temp dir for one test.
func withLiCache(t *testing.T) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "linkdapi")
	prev := linkdapi.CacheDir
	linkdapi.CacheDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { linkdapi.CacheDir = prev })
}

// profileJSON is a synthetic /profile/full body with the given fullPositions entries.
func profileJSON(positions ...string) string {
	return `{"success":true,"statusCode":200,"message":"ok","data":{"urn":"ACoAAAtest","username":"jane-doe","firstName":"Jane","lastName":"Doe",` +
		`"headline":"VP Sales at Acme","summary":"I sell things.","skills":[{"name":"Sales"}],"languages":[],"educations":[],` +
		`"fullPositions":[` + strings.Join(positions, ",") + `]}}`
}

func position(companyID int, company, title, start, end string) string {
	return `{"companyId":` + itoa(companyID) + `,"companyName":"` + company + `","companyURL":"","title":"` + title + `","employmentType":"","start":` + start + `,"end":` + end + `}`
}

func itoa(i int) string { return strconv.Itoa(i) }

const current = `{"year":0,"month":0,"day":0}`

func ymdJSON(y, m, d int) string {
	return `{"year":` + itoa(y) + `,"month":` + itoa(m) + `,"day":` + itoa(d) + `}`
}

func snrSignal(t *testing.T, extra string) Signal {
	t.Helper()
	return parseSignal(t, `{"label":"New role","computed_config":{"type":"started_new_role","started_new_role_config":{"started_new_job_within":"THREE_MONTHS"`+extra+`}}}`)
}

var jane = NewRecord(map[string]string{"FirstName": "Jane", "LastName": "Doe", "LinkedInUrl": "https://www.linkedin.com/in/jane-doe"})

func liClient() *linkdapi.Client { return linkdapi.New("ld-test", false) }

func TestStartedNewRoleWindow(t *testing.T) {
	withLiCache(t)
	newStubOpenAI(t, map[string]func(int) *http.Response{"/profile/full": always(200, profileJSON(
		position(42, "Acme ", "VP Sales", ymdJSON(2026, 9, 1), current),
		position(42, "Acme", "Director", ymdJSON(2024, 0, 0), ymdJSON(2026, 8, 31)),
	))})
	res := RunStartedNewRole(context.Background(), snrSignal(t, ""), jane, liClient(), "", liNow)
	if res.Status != StatusSuccess || res.Value != `2026-09-01 - Jane Doe started role "VP Sales" at Acme` {
		t.Fatalf("res = %+v", res)
	}
	if len(res.Sources) != 1 || res.Sources[0].URL != "https://www.linkedin.com/in/jane-doe" || res.Sources[0].Title != "LinkedIn Profile" {
		t.Fatalf("sources = %+v", res.Sources)
	}

	for name, positions := range map[string][]string{
		"too old": {position(42, "Acme", "VP Sales", ymdJSON(2026, 1, 1), current)},
		"ended":   {position(42, "Acme", "VP Sales", ymdJSON(2026, 9, 1), ymdJSON(2026, 10, 1))},
		"none":    nil,
	} {
		withLiCache(t)
		newStubOpenAI(t, map[string]func(int) *http.Response{"/profile/full": always(200, profileJSON(positions...))})
		if res := RunStartedNewRole(context.Background(), snrSignal(t, ""), jane, liClient(), "", liNow); res.Status != StatusNotFound {
			t.Errorf("%s: res = %+v", name, res)
		}
	}
}

func TestStartedNewRoleReportsMatchingRole(t *testing.T) {
	withLiCache(t)
	newStubOpenAI(t, map[string]func(int) *http.Response{"/profile/full": always(200, profileJSON(
		position(42, "Acme", "Board Member", ymdJSON(2018, 0, 0), current),
		position(42, "Acme", "CEO", ymdJSON(2026, 9, 15), current),
	))})
	res := RunStartedNewRole(context.Background(), snrSignal(t, ""), jane, liClient(), "", liNow)
	if res.Status != StatusSuccess || !strings.Contains(res.Value, `"CEO"`) || !strings.HasPrefix(res.Value, "2026-09-15") {
		t.Fatalf("res = %+v", res)
	}
}

func TestStartedNewRoleUsesProfileNameWhenRecordHasNone(t *testing.T) {
	withLiCache(t)
	newStubOpenAI(t, map[string]func(int) *http.Response{"/profile/full": always(200, profileJSON(position(42, "Acme", "VP Sales", ymdJSON(2026, 9, 1), current)))})
	rec := NewRecord(map[string]string{"LinkedInUrl": "https://www.linkedin.com/in/jane-doe"})
	if res := RunStartedNewRole(context.Background(), snrSignal(t, ""), rec, liClient(), "", liNow); !strings.Contains(res.Value, "Jane Doe started") {
		t.Fatalf("res = %+v", res)
	}
}

func excludeVerdict(exclude bool) string {
	v := "false"
	if exclude {
		v = "true"
	}
	return `{"choices":[{"message":{"content":"{\"should_exclude_title\":` + v + `}"}}]}`
}

func TestStartedNewRoleAdvisorExcluded(t *testing.T) {
	withLiCache(t)
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{
		"/profile/full": always(200, profileJSON(
			position(0, "Freelance", "Advisor", ymdJSON(2026, 8, 1), current),
			position(42, "Acme", "VP Sales", ymdJSON(2026, 9, 1), current),
		)),
		"/chat/completions": func(n int) *http.Response { return jsonResponse(200, excludeVerdict(n == 1)) },
	})
	res := RunStartedNewRole(context.Background(), snrSignal(t, `,"should_exclude_advisor_roles":true`), jane, liClient(), "sk-test", liNow)
	if res.Status != StatusSuccess || !strings.Contains(res.Value, "VP Sales") || stub.count("/chat/completions") != 2 {
		t.Fatalf("res = %+v chat=%d", res, stub.count("/chat/completions"))
	}
	body := stub.last("/chat/completions").body
	msgs, _ := body["messages"].([]any)
	if body["model"] != ClassifierModel || len(msgs) != 2 || msgs[1].(map[string]any)["content"] != `Role: "VP Sales"` ||
		!strings.Contains(msgs[0].(map[string]any)["content"].(string), `Role: "Guest Lecturer"`) {
		t.Fatalf("classifier body = %v", body)
	}

	withLiCache(t)
	newStubOpenAI(t, map[string]func(int) *http.Response{
		"/profile/full":     always(200, profileJSON(position(42, "Acme", "VP Sales", ymdJSON(2026, 9, 1), current))),
		"/chat/completions": always(500, `{}`),
	})
	if res := RunStartedNewRole(context.Background(), snrSignal(t, `,"should_exclude_advisor_roles":true`), jane, liClient(), "sk-test", liNow); res.Status != StatusFailed {
		t.Fatalf("classifier errors must fail, res = %+v", res)
	}
}

func TestStartedNewRoleNotFoundProfile(t *testing.T) {
	withLiCache(t)
	newStubOpenAI(t, map[string]func(int) *http.Response{"/profile/full": always(404, `{"success":false,"statusCode":404,"message":"not found"}`)})
	res := RunStartedNewRole(context.Background(), snrSignal(t, ""), jane, liClient(), "", liNow)
	if res.Status != StatusNotFound || res.Error != "linkdapi: profile not found" {
		t.Fatalf("res = %+v", res)
	}
}

func TestStartedNewRoleBadURL(t *testing.T) {
	withLiCache(t)
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{})
	rec := NewRecord(map[string]string{"LinkedInUrl": "https://www.linkedin.com/company/acme"})
	res := RunStartedNewRole(context.Background(), snrSignal(t, ""), rec, liClient(), "", liNow)
	if res.Status != StatusMissingDependencies || res.Error != "missing: LinkedInUrl (not a LinkedIn profile URL)" || stub.calls() != 0 {
		t.Fatalf("res = %+v calls=%d", res, stub.calls())
	}
	if res := RunStartedNewRole(context.Background(), snrSignal(t, ""), NewRecord(nil), liClient(), "", liNow); res.Status != StatusMissingDependencies || res.Error != "missing: LinkedInUrl" {
		t.Fatalf("res = %+v", res)
	}
}

func TestPersonLiFields(t *testing.T) {
	p := linkdapi.Profile{
		Username: "jane-doe", FirstName: "Jane", LastName: "Doe", Headline: "VP Sales", About: "I sell.",
		Skills: []string{"Sales", "SaaS"}, Languages: []string{"English"}, Education: []string{"UT, BBA"},
		Experience: []linkdapi.Experience{{CompanyName: "Acme", Roles: []linkdapi.Role{
			{Title: "VP Sales", Start: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Current: true},
			{Title: "Director", Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)},
		}}},
	}
	posts := []linkdapi.Post{
		{Text: "New quarter!", Date: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)},
		{Text: "Old news", Date: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
	}
	f := PersonLiFields(p, posts, liNow)
	want := map[string]string{
		"PersonLi_FirstName":           "Jane",
		"PersonLi_LastName":            "Doe",
		"PersonLi_Headline":            "VP Sales",
		"PersonLi_About":               "I sell.",
		"PersonLi_Summary":             "I sell.",
		"PersonLi_Skills":              "Sales, SaaS",
		"PersonLi_Languages":           "English",
		"PersonLi_Education":           "UT, BBA",
		"PersonLi_Experience":          "Acme\nVP Sales\nfrom: September/2026 to: Present\nDirector\nfrom: January/2024 - to: August/2026",
		"PersonLi_LatestNewJobDate":    "2024-01-01",
		"PersonLi_LatestPromotionDate": "2026-09-01",
		"PersonLi_RecentRoleChange":    "false",
		"PersonLi_Posts":               "2026-10-20: New quarter!\n2026-08-01: Old news",
		"PersonLi_RecentPosts":         "2026-10-20: New quarter!",
	}
	for k, v := range want {
		if f[k] != v {
			t.Errorf("%s = %q, want %q", k, f[k], v)
		}
	}
	if _, ok := PersonLiFields(p, nil, liNow)["PersonLi_Posts"]; ok {
		t.Error("posts fields must be absent when posts were not fetched")
	}
}

func liRunOpts() RunOptions {
	o := runOpts()
	o.LinkdAPIKey = "ld-test"
	o.BuyLinkedIn = true
	return o
}

func TestNewRunnerLinkedInNeedsBuy(t *testing.T) {
	withLiCache(t)
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{})
	snr := snrSignal(t, "")
	if _, err := NewRunner([]Signal{snr}, runOpts()); err == nil || !strings.Contains(err.Error(), `signal "New role": started_new_role needs LinkedIn data; add --buy-linkedin`) {
		t.Fatalf("err = %v", err)
	}
	noKey := liRunOpts()
	noKey.LinkdAPIKey = ""
	if _, err := NewRunner([]Signal{snr}, noKey); err == nil || !strings.Contains(err.Error(), "signals keys set linkdapi") {
		t.Fatalf("err = %v", err)
	}
	for _, body := range []string{
		`{"label":"Exec","computed_config":{"type":"hired_executive","is_account_insight":true,"prompt":"CFO"}}`,
		`{"label":"Hiring","computed_config":{"type":"hiring_for_role","is_account_insight":true,"hiring_for_role":{"job_titles":["AE"]}}}`,
		`{"label":"Co posts","computed_config":{"type":"linkedin_posts","is_account_insight":true,"prompt":"funding"}}`,
	} {
		if _, err := NewRunner([]Signal{parseSignal(t, body)}, liRunOpts()); err == nil || !strings.Contains(err.Error(), "not available in this version yet") {
			t.Errorf("%s: err = %v", body, err)
		}
	}
	// started_new_role without exclusion needs no OpenAI key.
	noOpenAI := liRunOpts()
	noOpenAI.OpenAIKey = ""
	if _, err := NewRunner([]Signal{snr}, noOpenAI); err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, err := NewRunner([]Signal{snrSignal(t, `,"should_exclude_advisor_roles":true`)}, noOpenAI); err == nil || !strings.Contains(err.Error(), "OpenAI") {
		t.Fatalf("err = %v", err)
	}
	if stub.calls() != 0 {
		t.Fatal("NewRunner made HTTP calls")
	}
}

func TestRunnerPersonLiInGPT(t *testing.T) {
	withLiCache(t)
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{
		"/profile/full": always(200, profileJSON(position(42, "Acme", "VP Sales", ymdJSON(2026, 9, 1), current))),
		"/responses":    always(200, researchHit),
	})
	r := mustRunner(t, []Signal{gptDef(t, "Pitch", "Pitch {{.PersonLi_Headline}}")}, liRunOpts())
	res := r.Run(context.Background(), jane)
	if res[0].Status != StatusSuccess || stub.last("/responses").body["input"] != "Pitch VP Sales at Acme" || stub.count("/profile/full") != 1 || stub.count("/posts/all") != 0 {
		t.Fatalf("res=%+v input=%v", res, stub.last("/responses").body["input"])
	}

	// Without --buy-linkedin the field must come from the record (status 3 otherwise), with no fetch.
	stub = newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, researchHit)})
	res = mustRunner(t, []Signal{gptDef(t, "Pitch", "Pitch {{.PersonLi_Headline}}")}, runOpts()).Run(context.Background(), jane)
	if res[0].Status != StatusMissingDependencies || stub.calls() != 0 {
		t.Fatalf("res=%+v calls=%d", res, stub.calls())
	}
}

func TestRunnerOneProfilePerPerson(t *testing.T) {
	withLiCache(t)
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{
		"/profile/full": always(200, profileJSON(position(42, "Acme", "VP Sales", ymdJSON(2026, 9, 1), current))),
		"/responses":    always(200, researchHit),
	})
	opt := liRunOpts()
	opt.Now = func() time.Time { return liNow }
	r := mustRunner(t, []Signal{snrSignal(t, ""), gptDef(t, "Pitch", "Pitch {{.PersonLi_Headline}} {{.FirstName}}")}, opt)
	var wg sync.WaitGroup
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := jane.With("FirstName", "Jane"+string(rune('A'+i)))
			if res := r.Run(context.Background(), rec); res[0].Status != StatusSuccess || res[1].Status != StatusSuccess {
				t.Errorf("res = %+v", res)
			}
		}()
	}
	wg.Wait()
	if stub.count("/profile/full") != 1 {
		t.Fatalf("profile calls = %d", stub.count("/profile/full"))
	}
}

func TestPlanLinkdAPICalls(t *testing.T) {
	withLiCache(t)
	newStubOpenAI(t, map[string]func(int) *http.Response{"/profile/full": always(200, profileJSON())})
	if _, err := liClient().Profile(context.Background(), "jane-doe"); err != nil { // warm the cache
		t.Fatal(err)
	}
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{})
	r := mustRunner(t, []Signal{snrSignal(t, "")}, liRunOpts())
	recs := []Record{
		jane,
		NewRecord(map[string]string{"LinkedInUrl": "https://www.linkedin.com/in/john-roe"}),
		NewRecord(map[string]string{"LinkedInUrl": "https://www.linkedin.com/in/john-roe"}),
		NewRecord(map[string]string{"LinkedInUrl": "https://www.linkedin.com/company/acme"}),
	}
	p := r.Plan(recs)[0]
	if p.LinkdAPICalls != 1 || p.RowsReady != 3 || p.RowsMissingDeps != 1 || p.MaxOpenAICalls != 0 || p.Bool || stub.calls() != 0 {
		t.Fatalf("plan = %+v calls=%d", p, stub.calls())
	}
}
