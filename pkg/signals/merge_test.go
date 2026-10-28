package signals

import (
	"reflect"
	"testing"
	"time"
)

var mergeNow = time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)

func TestDateFields(t *testing.T) {
	d := DateFields(mergeNow)
	want := map[string]string{
		"CurrentDate": "2026-10-27",
		"Last7Days":   "after:2026-10-20",
		"Last14Days":  "after:2026-10-13",
		"Last30Days":  "after:2026-09-27",
		"Last60Days":  "after:2026-08-28",
		"Last90Days":  "after:2026-07-29",
		"Last180Days": "after:2026-04-30",
	}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("DateFields = %v", d)
	}
}

func TestRenderReplacesAndBlanks(t *testing.T) {
	got := Render("Has {{.AccountName}} raised? {{.Missing}}|{{ .AccountName }}|{{notafield}}", map[string]string{"AccountName": "Acme"})
	if got != "Has Acme raised? |Acme|{{notafield}}" {
		t.Fatalf("Render = %q", got)
	}
}

func TestRenderRawHeaderNames(t *testing.T) {
	got := Render("{{.funding-stage}} {{.2025 revenue}}", map[string]string{"funding-stage": "Series B", "2025 revenue": "$4M"})
	if got != "Series B $4M" {
		t.Fatalf("Render = %q", got)
	}
}

func TestMergeFieldsDateCannotBeOverridden(t *testing.T) {
	r := NewRecord(map[string]string{"CurrentDate": "1999-01-01", "AccountName": "Acme"})
	f := r.MergeFields(mergeNow)
	if f["CurrentDate"] != "2026-10-27" || f["AccountName"] != "Acme" {
		t.Fatalf("MergeFields = %v", f)
	}
}

func gptSignal(t *testing.T, prompt string) Signal {
	t.Helper()
	s, err := FromInline(Inline{Type: TypeGPT, Prompt: prompt, Account: true})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMissingDependenciesWhitespaceIsMissing(t *testing.T) {
	s := gptSignal(t, "{{.AccountName}} {{.AccountWebsite}}")
	r := NewRecord(map[string]string{"AccountName": "  ", "AccountWebsite": "https://a.com"})
	if got := MissingDependencies(s, r); !reflect.DeepEqual(got, []string{"AccountName"}) {
		t.Fatalf("missing = %v", got)
	}
}

func TestMissingDependenciesReportsUnknownField(t *testing.T) {
	if got := MissingDependencies(gptSignal(t, "{{.funding_stage}}"), NewRecord(nil)); !reflect.DeepEqual(got, []string{"funding_stage"}) {
		t.Fatalf("missing = %v", got)
	}
}

func TestMissingDependenciesLinkedInType(t *testing.T) {
	s, err := FromInline(Inline{Type: TypeLinkedInPosts, Prompt: "Did {{.FirstName}} post?"})
	if err != nil {
		t.Fatal(err)
	}
	if got := MissingDependencies(s, NewRecord(map[string]string{"FirstName": "Jane"})); !reflect.DeepEqual(got, []string{"LinkedInUrl"}) {
		t.Fatalf("missing = %v", got)
	}
}

func TestMissingDependenciesDateFieldNeverMissing(t *testing.T) {
	if got := MissingDependencies(gptSignal(t, "since {{.Last30Days}}"), NewRecord(nil)); len(got) != 0 {
		t.Fatalf("missing = %v", got)
	}
}
