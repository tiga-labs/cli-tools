package salesnav

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParsePageList(t *testing.T) {
	src := Source{Kind: KindList, ID: "1", URL: "https://www.linkedin.com/sales/lists/people/1"}
	page, err := ParsePage(readFixture(t, "list-page.json"), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Leads) != 25 || page.Start != 0 || page.Count != 25 || page.Total != 98 {
		t.Fatalf("got %d leads, start %d count %d total %d", len(page.Leads), page.Start, page.Count, page.Total)
	}
	want := Lead{
		FirstName:       "First1",
		LastName:        "Last1",
		FullName:        "First1 Last1",
		Title:           "Co-CEO & Co-Founder",
		CompanyName:     "poolside",
		CompanyLinkedIn: "https://www.linkedin.com/company/94859563",
		Location:        "City 1, State, United States",
		LinkedInURN:     "urn:li:fs_salesProfile:(ACwAAREDACTED1,NAME_SEARCH,N3S6)",
		SalesNavURL:     "https://www.linkedin.com/sales/lead/ACwAAREDACTED1,NAME_SEARCH,N3S6",
		Degree:          "2",
		DateAddedToList: "2025-08-15T00:39:17Z",
		SourceURL:       src.URL,
	}
	if got := page.Leads[1]; got != want {
		t.Errorf("lead[1] =\n%+v\nwant\n%+v", got, want)
	}
	noPos := page.Leads[19]
	if noPos.FullName != "First19 Last19" || noPos.Title != "" || noPos.CompanyName != "" || noPos.CompanyLinkedIn != "" {
		t.Errorf("lead[19] (no positions) = %+v", noPos)
	}
}

func TestParsePageSavedSearch(t *testing.T) {
	page, err := ParsePage(readFixture(t, "search-page.json"), Source{Kind: KindSavedSearch, ID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Leads) != 25 || page.Total != 987 {
		t.Fatalf("got %d leads, total %d", len(page.Leads), page.Total)
	}
	l := page.Leads[1]
	if l.Title != "Owner/ Engineer" || l.CompanyName != "Waller Engineering, PLLC" ||
		l.CompanyLinkedIn != "https://www.linkedin.com/company/108142003" || l.Degree != "3" ||
		l.DateAddedToList != "" || l.LinkedInURN != "urn:li:fs_salesProfile:(ACwAAREDACTED1,NAME_SEARCH,86OM)" {
		t.Errorf("lead[1] = %+v", l)
	}
	var outOfNetwork bool
	for _, l := range page.Leads {
		if l.Degree == "-1" {
			outOfNetwork = true
		}
	}
	if !outOfNetwork {
		t.Error("no lead with degree -1; the fixture has some")
	}
}

func parseOne(t *testing.T, element string) Lead {
	t.Helper()
	page, err := ParsePage([]byte(`{"paging":{"start":0,"count":25,"total":1},"elements":[`+element+`]}`), Source{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Leads) != 1 {
		t.Fatalf("got %d leads", len(page.Leads))
	}
	return page.Leads[0]
}

func TestParsePageEdgeCases(t *testing.T) {
	l := parseOne(t, `{"currentPositions":[{"current":false,"title":"Old","companyName":"OldCo","companyUrn":"urn:li:fs_salesCompany:1"}]}`)
	if l.Title != "" || l.CompanyName != "" || l.CompanyLinkedIn != "" {
		t.Errorf("only past positions: %+v", l)
	}
	l = parseOne(t, `{"currentPositions":[{"current":true,"title":"CEO","companyName":"Solo"}]}`)
	if l.CompanyName != "Solo" || l.CompanyLinkedIn != "" {
		t.Errorf("no companyUrn: %+v", l)
	}
	l = parseOne(t, `{"currentPositions":[{"current":true,"title":"CEO","companyUrn":"urn:li:fs_salesCompany:42"}]}`)
	if l.CompanyName != "" || l.CompanyLinkedIn != "https://www.linkedin.com/company/42" {
		t.Errorf("companyUrn without companyName: %+v", l)
	}
	l = parseOne(t, `{"currentPositions":[{"current":false,"title":"Past"},{"current":true,"title":"First","companyName":"A"},{"current":true,"title":"Second","companyName":"B"}]}`)
	if l.Title != "First" || l.CompanyName != "A" {
		t.Errorf("first current position should win: %+v", l)
	}
	l = parseOne(t, `{"firstName":" Ann\n","lastName":"\tLee ","fullName":"  Ann Lee ","entityUrn":"garbage"}`)
	if l.FirstName != "Ann" || l.LastName != "Lee" || l.FullName != "Ann Lee" {
		t.Errorf("names not trimmed: %+v", l)
	}
	if l.LinkedInURN != "garbage" || l.SalesNavURL != "" {
		t.Errorf("malformed URN: %+v", l)
	}
	if l.Degree != "" || l.DateAddedToList != "" {
		t.Errorf("absent degree/date: %+v", l)
	}
}

func TestParsePageErrors(t *testing.T) {
	if _, err := ParsePage([]byte("<html>"), Source{}); err == nil {
		t.Error("HTML body: want error")
	}
	if _, err := ParsePage([]byte(`{"elements":[]}`), Source{}); err == nil || !strings.Contains(err.Error(), "missing paging") {
		t.Errorf("no paging: err = %v", err)
	}
	page, err := ParsePage([]byte(`{"elements":[],"paging":{"start":0,"count":25,"total":0}}`), Source{})
	if err != nil || len(page.Leads) != 0 {
		t.Errorf("empty page: %d leads, err %v", len(page.Leads), err)
	}
}

func TestRowOrder(t *testing.T) {
	want := []string{"first_name", "last_name", "full_name", "title", "company_name", "company_linkedin", "location", "linkedin_urn", "sales_nav_url", "degree", "date_added_to_list", "source_url"}
	if !reflect.DeepEqual(CSVHeader, want) {
		t.Fatalf("CSVHeader = %v", CSVHeader)
	}
	row := Lead{FirstName: "a", Degree: "d", SourceURL: "z"}.Row()
	if len(row) != 12 || row[0] != "a" || row[9] != "d" || row[11] != "z" {
		t.Fatalf("Row = %q", row)
	}
}
