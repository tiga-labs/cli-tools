package signals

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, body string) Signal {
	t.Helper()
	s, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return s
}

func parseErr(t *testing.T, body string) error {
	t.Helper()
	_, err := Parse([]byte(body))
	if err == nil {
		t.Fatalf("expected error for %s", body)
	}
	return err
}

func TestParseAPIBody(t *testing.T) {
	s := mustParse(t, `{
		"label": "Recent Funding News",
		"is_computed_column": true,
		"type": "text",
		"computed_config": {
			"type": "gpt",
			"prompt": "Has {{.AccountName}} raised funding in the last 90 days? Answer yes or no with a brief reason.",
			"required_dependencies": ["AccountName"],
			"is_account_insight": true,
			"api": "Agentic",
			"can_use_web_search": true,
			"expiration_in_days": 90,
			"temperature": 0.4,
			"word_limit": 50
		}
	}`)
	if s.Key() != "recent_funding_news" {
		t.Fatalf("Key = %q", s.Key())
	}
	if !reflect.DeepEqual(s.Config.RequiredDependencies, []string{"AccountName"}) {
		t.Fatalf("deps = %v", s.Config.RequiredDependencies)
	}
	if s.Config.ExpirationInDays != 90 || !s.WebSearch() {
		t.Fatalf("expiration=%d websearch=%v", s.Config.ExpirationInDays, s.WebSearch())
	}
}

func TestParseIgnoresUnknownAndAPIFields(t *testing.T) {
	mustParse(t, `{
		"label": "Hiring SDRs",
		"is_computed_column": true,
		"type": "text",
		"computed_config": {
			"type": "hiring_for_role",
			"is_account_insight": true,
			"api": "Agentic",
			"email_settings": "never_create",
			"signal_found_color": null,
			"default_value": "x",
			"hiring_for_role": {"job_titles": ["SDR"], "should_check_indeed": true, "should_check_linkedin": true}
		}
	}`)
}

func TestParseRejectsBareComputedConfig(t *testing.T) {
	err := parseErr(t, `{"type":"gpt","prompt":"x"}`)
	if !strings.Contains(err.Error(), "computed_config") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateUnsupportedType(t *testing.T) {
	for _, typ := range []string{"search", "foo"} {
		err := parseErr(t, fmt.Sprintf(`{"computed_config":{"type":%q,"prompt":"x"}}`, typ))
		if !strings.Contains(err.Error(), "not supported by the signals CLI yet") || !strings.Contains(err.Error(), "supported: gpt") {
			t.Fatalf("%s: err = %v", typ, err)
		}
	}
}

func TestValidateScope(t *testing.T) {
	if err := parseErr(t, `{"computed_config":{"type":"started_new_role","is_account_insight":true}}`); !strings.Contains(err.Error(), "person") {
		t.Fatalf("err = %v", err)
	}
	if err := parseErr(t, `{"computed_config":{"type":"hired_executive","prompt":"x","is_account_insight":false}}`); !strings.Contains(err.Error(), "account") {
		t.Fatalf("err = %v", err)
	}
	mustParse(t, `{"computed_config":{"type":"linkedin_posts","prompt":"x","is_account_insight":true}}`)
	mustParse(t, `{"computed_config":{"type":"linkedin_posts","prompt":"x","is_account_insight":false}}`)
}

func TestValidateRequiresPrompt(t *testing.T) {
	if err := parseErr(t, `{"computed_config":{"type":"gpt","prompt":"  "}}`); !strings.Contains(err.Error(), "prompt") {
		t.Fatalf("err = %v", err)
	}
}

func hiringBody(n int) string {
	titles := make([]string, n)
	for i := range titles {
		titles[i] = fmt.Sprintf("%q", fmt.Sprintf("Title %d", i))
	}
	return `{"computed_config":{"type":"hiring_for_role","is_account_insight":true,"hiring_for_role":{"job_titles":[` + strings.Join(titles, ",") + `]}}}`
}

func TestValidateHiringTitleLimit(t *testing.T) {
	if err := parseErr(t, hiringBody(11)); err.Error() != "hiring_for_role accepts at most 10 job_titles (got 11)" {
		t.Fatalf("err = %v", err)
	}
	parseErr(t, hiringBody(0))
	mustParse(t, hiringBody(10))
}

func TestValidateWindows(t *testing.T) {
	parseErr(t, `{"computed_config":{"type":"linkedin_posts","prompt":"x","date_range":"LAST_WEEK"}}`)
	mustParse(t, `{"computed_config":{"type":"linkedin_posts","prompt":"x","date_range":""}}`)
	s := mustParse(t, `{"computed_config":{"type":"started_new_role"}}`)
	if s.Config.StartedNewRoleConfig.StartedNewJobWithin != "THIS_YEAR" {
		t.Fatalf("window = %q", s.Config.StartedNewRoleConfig.StartedNewJobWithin)
	}
}

func TestValidatePrecondition(t *testing.T) {
	pre := func(p string) string {
		return `{"computed_config":{"type":"gpt","prompt":"x","has_precondition":true,"precondition":` + p + `}}`
	}
	mustParse(t, pre(`{"property":"Title","property_type":"text","operator":"ILIKE","value":"VP"}`))
	parseErr(t, pre(`{"property":"Title","property_type":"number","operator":"ILIKE"}`))
	mustParse(t, pre(`{"property":"FundedOn","property_type":"date","operator":"THREE_MONTHS"}`))
	parseErr(t, pre(`{"property":"","property_type":"text","operator":"="}`))
}

func TestDeriveDependencies(t *testing.T) {
	got := DeriveDependencies(Config{Type: TypeGPT, Prompt: "{{.AccountName}} {{.AccountWebsite}} {{.AccountName}} {{.CurrentDate}} {{.funding_stage}}"})
	if !reflect.DeepEqual(got, []string{"AccountName", "AccountWebsite", "funding_stage"}) {
		t.Fatalf("got %v", got)
	}
	if got := DeriveDependencies(Config{Type: TypeGPT, Prompt: "no fields"}); got == nil || len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
	if got := DeriveDependencies(Config{Type: TypeLinkedInPosts}); !reflect.DeepEqual(got, []string{"LinkedInUrl"}) {
		t.Fatalf("got %v", got)
	}
	if got := DeriveDependencies(Config{Type: TypeHiringForRole, IsAccountInsight: true}); !reflect.DeepEqual(got, []string{"AccountLinkedInUrl"}) {
		t.Fatalf("got %v", got)
	}
	s := mustParse(t, `{"computed_config":{"type":"gpt","prompt":"{{.Title}}","required_dependencies":["Bogus"]}}`)
	if !reflect.DeepEqual(s.Config.RequiredDependencies, []string{"Title"}) {
		t.Fatalf("deps = %v", s.Config.RequiredDependencies)
	}
}

func TestKeyDeterministicSlug(t *testing.T) {
	cases := map[string]string{
		"Funding?":             "funding",
		"funding":              "funding",
		"ICP Fit — Score (v2)": "icp_fit_score_v2",
		"!!!":                  "signal",
	}
	for label, want := range cases {
		if got := (Signal{Label: label}).Key(); got != want {
			t.Errorf("Key(%q) = %q, want %q", label, got, want)
		}
	}
	if s := mustParse(t, `{"computed_config":{"type":"gpt","prompt":"x"}}`); s.Key() != "gpt" {
		t.Fatalf("Key = %q", s.Key())
	}
}

func TestLoadFileErrorNamesPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v", err)
	}
}

func TestDeriveDependenciesLinkedInTypesIncludePromptFields(t *testing.T) {
	got := DeriveDependencies(Config{Type: TypeLinkedInPosts, Prompt: "Has {{.FirstName}} {{.LastName}} posted about AI?"})
	if !reflect.DeepEqual(got, []string{"LinkedInUrl", "FirstName", "LastName"}) {
		t.Fatalf("person posts: %v", got)
	}
	got = DeriveDependencies(Config{Type: TypeHiredExecutive, IsAccountInsight: true, Prompt: "Did {{.AccountName}} hire a CRO?"})
	if !reflect.DeepEqual(got, []string{"AccountLinkedInUrl", "AccountName"}) {
		t.Fatalf("hired_executive: %v", got)
	}
}

func TestValidatePreconditionValue(t *testing.T) {
	pre := func(p string) string {
		return `{"computed_config":{"type":"gpt","prompt":"x","has_precondition":true,"precondition":` + p + `}}`
	}
	for _, bad := range []string{
		`{"property":"Employees","property_type":"number","operator":">=","value":""}`,
		`{"property":"Employees","property_type":"number","operator":">=","value":"1,000"}`,
		`{"property":"Title","property_type":"text","operator":"ILIKE","value":""}`,
	} {
		if err := parseErr(t, pre(bad)); !strings.Contains(err.Error(), "precondition.value") {
			t.Fatalf("%s: err = %v", bad, err)
		}
	}
	mustParse(t, pre(`{"property":"Title","property_type":"text","operator":"IS_NOT_EMPTY"}`))
	mustParse(t, pre(`{"property":"Employees","property_type":"number","operator":">=","value":"100"}`))
}

func TestDeriveDependenciesAllowsSpacesInBraces(t *testing.T) {
	if got := DeriveDependencies(Config{Type: TypeGPT, Prompt: "{{ .AccountName }} and {{.AccountName}}"}); !reflect.DeepEqual(got, []string{"AccountName"}) {
		t.Fatalf("got %v", got)
	}
}
