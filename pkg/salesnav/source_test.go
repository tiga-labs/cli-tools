package salesnav

import (
	"errors"
	"testing"
)

func TestParseURL(t *testing.T) {
	const listID = "7352369367017299971"
	list := func(u string) Source { return Source{Kind: KindList, ID: listID, URL: u} }
	const search = "https://www.linkedin.com/sales/search/people?lastViewedAt=1752770598757&savedSearchId=1929545378&sessionId=RGnoY2t0R72UfGqkItwziQ%3D%3D"
	const searchNoLVA = "https://www.linkedin.com/sales/search/people?savedSearchId=1929545378&sessionId=RGnoY2t0R72UfGqkItwziQ%3D%3D"

	ok := []struct {
		in   string
		want Source
	}{
		{"https://www.linkedin.com/sales/lists/people/" + listID + "?sortCriteria=CREATED_TIME&sortOrder=DESCENDING",
			list("https://www.linkedin.com/sales/lists/people/" + listID + "?sortCriteria=CREATED_TIME&sortOrder=DESCENDING")},
		{"https://www.linkedin.com/sales/lists/people/" + listID + "/", list("https://www.linkedin.com/sales/lists/people/" + listID + "/")},
		{"https://www.linkedin.com/sales/lists/people/" + listID, list("https://www.linkedin.com/sales/lists/people/" + listID)},
		{"https://linkedin.com/sales/lists/people/" + listID, list("https://linkedin.com/sales/lists/people/" + listID)},
		{"https://WWW.LinkedIn.com/sales/lists/people/" + listID, list("https://WWW.LinkedIn.com/sales/lists/people/" + listID)},
		{"  http://www.linkedin.com/sales/lists/people/" + listID + "  ", list("http://www.linkedin.com/sales/lists/people/" + listID)},
		{search, Source{Kind: KindSavedSearch, ID: "1929545378", SessionID: "RGnoY2t0R72UfGqkItwziQ==", LastViewedAt: "1752770598757", URL: search}},
		{searchNoLVA, Source{Kind: KindSavedSearch, ID: "1929545378", SessionID: "RGnoY2t0R72UfGqkItwziQ==", URL: searchNoLVA}},
	}
	for _, tt := range ok {
		got, err := ParseURL(tt.in)
		if err != nil {
			t.Errorf("ParseURL(%q) error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseURL(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}

	bad := []struct {
		in   string
		want error
	}{
		{"https://www.linkedin.com/sales/search/people?savedSearchId=1929545378", ErrMissingSession},
		{"https://www.linkedin.com/sales/search/people?query=(filters:List())", ErrUnsavedSearch},
		{"https://www.linkedin.com/sales/lists/company/123", ErrAccountList},
		{"https://www.linkedin.com/sales/lists/people/123)%26start=0", ErrNotSalesNav},
		{"https://www.linkedin.com/sales/lists/people/abc", ErrNotSalesNav},
		{"https://www.linkedin.com/sales/search/people?savedSearchId=1;x&sessionId=a", ErrNotSalesNav},
		{"https://www.linkedin.com/sales/search/people?savedSearchId=1&sessionId=a%22b", ErrNotSalesNav},
		{"https://www.linkedin.com/sales/search/people?savedSearchId=1&sessionId=a&lastViewedAt=12x", ErrNotSalesNav},
		{"https://evil.com/sales/lists/people/1", ErrNotSalesNav},
		{"https://www.linkedin.com.evil.com/sales/lists/people/1", ErrNotSalesNav},
		{"not a url", ErrNotSalesNav},
		{"", ErrNotSalesNav},
	}
	for _, tt := range bad {
		if _, err := ParseURL(tt.in); !errors.Is(err, tt.want) {
			t.Errorf("ParseURL(%q) error = %v, want %v", tt.in, err, tt.want)
		}
	}
}

func TestSlug(t *testing.T) {
	if got := (Source{Kind: KindList, ID: "7352369367017299971"}).Slug(); got != "list-7352369367017299971" {
		t.Errorf("list Slug = %q", got)
	}
	if got := (Source{Kind: KindSavedSearch, ID: "1929545378"}).Slug(); got != "search-1929545378" {
		t.Errorf("search Slug = %q", got)
	}
}
