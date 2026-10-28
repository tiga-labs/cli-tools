package salesnav

import (
	"errors"
	"strings"
	"testing"
)

const apiURL = "https://www.linkedin.com/sales-api/salesApiPeopleSearch?q=peopleSearchQuery"

func TestCheckPage(t *testing.T) {
	tests := []struct {
		name   string
		in     FetchResult
		kind   StopKind
		status int
		reason string // exact, or a prefix when prefix is set
		prefix bool
	}{
		{"no session", FetchResult{NoSession: true}, StopNotLoggedIn, 0, "LinkedIn is not logged in in this Chrome profile", false},
		{"401", FetchResult{Status: 401}, StopWarning, 401, "LinkedIn answered 401: the session expired or was logged out", false},
		{"403", FetchResult{Status: 403}, StopWarning, 403, "LinkedIn answered 403: it refused the request", false},
		{"429", FetchResult{Status: 429}, StopWarning, 429, "LinkedIn answered 429: too many requests", false},
		{"999", FetchResult{Status: 999}, StopWarning, 999, "LinkedIn answered 999: it flagged the request as automated", false},
		{"checkpoint redirect", FetchResult{Status: 200, Redirected: true, URL: "https://www.linkedin.com/checkpoint/challenge?x=1"}, StopWarning, 200,
			"LinkedIn redirected to www.linkedin.com/checkpoint/challenge (login or security check)", false},
		{"login page without redirect flag", FetchResult{Status: 200, URL: "https://www.linkedin.com/uas/login", ContentType: "text/html"}, StopWarning, 200,
			"LinkedIn redirected to www.linkedin.com/uas/login (login or security check)", false},
		{"500", FetchResult{Status: 500, URL: apiURL}, StopFailed, 500, "LinkedIn answered 500", false},
		{"302", FetchResult{Status: 302, URL: apiURL}, StopFailed, 302, "LinkedIn answered 302", false},
		{"html with 200", FetchResult{Status: 200, URL: apiURL, ContentType: "text/html", Body: "<html>"}, StopFailed, 200,
			"LinkedIn sent a non-JSON response (text/html)", false},
		{"broken json", FetchResult{Status: 200, URL: apiURL, ContentType: "application/json", Body: "{"}, StopFailed, 200,
			"could not read LinkedIn's response: ", true},
	}
	for _, tt := range tests {
		_, err := CheckPage(tt.in, Source{})
		var stop *Stop
		if !errors.As(err, &stop) {
			t.Errorf("%s: err = %v, want *Stop", tt.name, err)
			continue
		}
		if stop.Kind != tt.kind || stop.Status != tt.status {
			t.Errorf("%s: kind %d status %d, want kind %d status %d", tt.name, stop.Kind, stop.Status, tt.kind, tt.status)
		}
		if tt.prefix && !strings.HasPrefix(stop.Error(), tt.reason) || !tt.prefix && stop.Error() != tt.reason {
			t.Errorf("%s: reason %q, want %q", tt.name, stop.Error(), tt.reason)
		}
	}
}

func TestCheckPageOK(t *testing.T) {
	body := string(readFixture(t, "list-page.json"))
	page, err := CheckPage(FetchResult{Status: 200, URL: apiURL, ContentType: "application/json; charset=utf-8", Body: body}, Source{})
	if err != nil || len(page.Leads) != 25 {
		t.Fatalf("got %d leads, err %v", len(page.Leads), err)
	}
	// Some runtimes leave Response.url empty; that is not a redirect.
	if _, err := CheckPage(FetchResult{Status: 200, ContentType: "application/json", Body: body}, Source{}); err != nil {
		t.Fatalf("empty URL: %v", err)
	}
}
