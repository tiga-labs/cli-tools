package signals

import (
	"strings"
	"testing"

	"github.com/tiga-labs/cli-tools/pkg/urlutil"
)

func mustMap(t *testing.T, headers []string, overrides map[string]string) ColumnMap {
	t.Helper()
	m, err := MapColumns(headers, overrides)
	if err != nil {
		t.Fatalf("MapColumns: %v", err)
	}
	return m
}

func TestMapColumnsAliases(t *testing.T) {
	m := mustMap(t, []string{"First Name", "Last Name", "Work Email", "Company", "Company LinkedIn URL", "Website", "funding-stage"}, nil)
	r := m.Record([]string{"Jane", "Doe", "jane@acme.com", "Acme", "https://www.linkedin.com/company/acme/", "https://acme.com", "Series B"})
	want := map[string]string{
		"FirstName":          "Jane",
		"LastName":           "Doe",
		"EmailAddress":       "jane@acme.com",
		"AccountName":        "Acme",
		"AccountDomain":      "acme.com",
		"AccountLinkedInUrl": "https://www.linkedin.com/company/acme",
		"funding_stage":      "Series B",
		"funding-stage":      "Series B",
	}
	for k, v := range want {
		if got := r.Get(k); got != v {
			t.Errorf("Get(%q) = %q, want %q", k, got, v)
		}
	}
}

func TestMapColumnsNameIsNotAutoMapped(t *testing.T) {
	headers := []string{"name", "domain"}
	row := []string{"Acme Corp", "acme.com"}
	r := mustMap(t, headers, nil).Record(row)
	if r.Get("AccountName") != "" || r.Get("FirstName") != "" || r.Get("name") != "Acme Corp" {
		t.Fatalf("fields = %v", r.Fields())
	}
	r = mustMap(t, headers, map[string]string{"AccountName": "name"}).Record(row)
	if r.Get("AccountName") != "Acme Corp" {
		t.Fatalf("override not applied: %v", r.Fields())
	}
}

func TestMapColumnsOverrideErrors(t *testing.T) {
	if _, err := MapColumns([]string{"name"}, map[string]string{"AccountName": "nope"}); err == nil || !strings.Contains(err.Error(), "not found in CSV headers") {
		t.Fatalf("err = %v", err)
	}
	if _, err := MapColumns([]string{"name"}, map[string]string{"Bogus": "name"}); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("err = %v", err)
	}
}

func TestMapColumnsDuplicateHeadersFirstWins(t *testing.T) {
	r := mustMap(t, []string{"First Name", "first_name"}, nil).Record([]string{"A", "B"})
	if r.Get("FirstName") != "A" || r.Get("first_name") != "A" || r.Get("First Name") != "A" {
		t.Fatalf("fields = %v", r.Fields())
	}
}

func TestRecordShortRow(t *testing.T) {
	r := mustMap(t, []string{"company", "domain"}, nil).Record([]string{"Acme"})
	if r.Get("AccountName") != "Acme" || r.Get("AccountDomain") != "" {
		t.Fatalf("fields = %v", r.Fields())
	}
}

func TestRecordNormalizesURLs(t *testing.T) {
	r := NewRecord(map[string]string{
		"LinkedInUrl":        "linkedin.com/in/jane-doe/?trk=x",
		"AccountWebsite":     "https://www.Acme.com/about",
		"AccountLinkedInUrl": "not a url",
	})
	wantLI, _ := urlutil.CleanPersonLinkedinUrl("linkedin.com/in/jane-doe/?trk=x")
	if r.Get("LinkedInUrl") != wantLI || wantLI == "" {
		t.Fatalf("LinkedInUrl = %q, want %q", r.Get("LinkedInUrl"), wantLI)
	}
	if r.Get("AccountDomain") != "acme.com" {
		t.Fatalf("AccountDomain = %q", r.Get("AccountDomain"))
	}
	if r.Get("AccountLinkedInUrl") != "not a url" {
		t.Fatalf("AccountLinkedInUrl = %q", r.Get("AccountLinkedInUrl"))
	}
	if r := NewRecord(map[string]string{"AccountDomain": "acme.com"}); r.Get("AccountWebsite") != "https://acme.com" {
		t.Fatalf("AccountWebsite = %q", r.Get("AccountWebsite"))
	}
}

func TestRecordFullNameSplit(t *testing.T) {
	r := mustMap(t, []string{"full_name", "company"}, nil).Record([]string{"Ada Lovelace King", "Analytical"})
	if r.Get("FirstName") != "Ada" || r.Get("LastName") != "Lovelace King" {
		t.Fatalf("fields = %v", r.Fields())
	}
}

func TestRecordWithIsCopyOnWrite(t *testing.T) {
	r := NewRecord(map[string]string{"AccountName": "Acme"})
	r2 := r.With("funding", "yes")
	if r.Get("funding") != "" || r2.Get("funding") != "yes" || r2.Get("AccountName") != "Acme" {
		t.Fatalf("r=%v r2=%v", r.Fields(), r2.Fields())
	}
}

func TestRecordCleansDomainColumn(t *testing.T) {
	for _, raw := range []string{"https://www.acme.com/", "www.Acme.com", "acme.com"} {
		r := NewRecord(map[string]string{"AccountDomain": raw})
		if r.Get("AccountDomain") != "acme.com" || r.Get("AccountWebsite") != "https://acme.com" {
			t.Fatalf("%q: domain=%q website=%q", raw, r.Get("AccountDomain"), r.Get("AccountWebsite"))
		}
	}
	if r := NewRecord(map[string]string{"AccountDomain": "N/A"}); r.Get("AccountDomain") != "N/A" {
		t.Fatalf("uncleanable domain must be kept: %q", r.Get("AccountDomain"))
	}
}

func TestRecordSocialWebsiteIsNotADomain(t *testing.T) {
	r := NewRecord(map[string]string{"AccountWebsite": "https://www.linkedin.com/company/acme/"})
	if r.Get("AccountDomain") != "" {
		t.Fatalf("AccountDomain = %q", r.Get("AccountDomain"))
	}
	if r.Get("AccountLinkedInUrl") != "https://www.linkedin.com/company/acme" {
		t.Fatalf("AccountLinkedInUrl = %q", r.Get("AccountLinkedInUrl"))
	}
	r = NewRecord(map[string]string{"AccountWebsite": "facebook.com/acme"})
	if r.Get("AccountDomain") != "" {
		t.Fatalf("facebook domain = %q", r.Get("AccountDomain"))
	}
}

func TestMapColumnsExportHeaders(t *testing.T) {
	// HubSpot
	r := mustMap(t, []string{"Company name", "Company Domain Name", "LinkedIn Company Page"}, nil).
		Record([]string{"Acme", "acme.com", "https://www.linkedin.com/company/acme"})
	if r.Get("AccountName") != "Acme" || r.Get("AccountDomain") != "acme.com" || r.Get("AccountLinkedInUrl") != "https://www.linkedin.com/company/acme" {
		t.Fatalf("hubspot: %v", r.Fields())
	}
	// Sales Nav / Phantombuster camelCase
	r = mustMap(t, []string{"firstName", "lastName", "profileUrl", "companyName", "linkedinCompanyUrl"}, nil).
		Record([]string{"Jane", "Doe", "https://www.linkedin.com/in/jane", "Acme", "https://www.linkedin.com/company/acme"})
	if r.Get("FirstName") != "Jane" || r.Get("LinkedInUrl") != "https://www.linkedin.com/in/jane" || r.Get("AccountName") != "Acme" || r.Get("AccountLinkedInUrl") == "" {
		t.Fatalf("camelCase: %v", r.Fields())
	}
}
