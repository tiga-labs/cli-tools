package salesnav

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestPageURLList(t *testing.T) {
	got := Source{Kind: KindList, ID: "7352369367017299971"}.PageURL(25)
	want := "https://www.linkedin.com/sales-api/salesApiPeopleSearch?q=peopleSearchQuery&query=(spotlightParam:(selectedType:ALL),doFetchSpotlights:true,doFetchHits:true,doFetchFilters:false,pivotParam:(com.linkedin.sales.search.LeadListPivotRequest:(list:urn%3Ali%3Afs_salesList%3A7352369367017299971,sortCriteria:CREATED_TIME,sortOrder:DESCENDING)),list:(scope:LEAD,includeAll:false,excludeAll:false,includedValues:List((id:7352369367017299971))))&start=25&count=25&decoration=%28entityUrn%2CprofilePictureDisplayImage%2CfirstName%2ClastName%2CfullName%2Cdegree%2CblockThirdPartyDataSharing%2CcrmStatus%2CgeoRegion%2ClistCount%2CdateAddedToListAt%2CpendingInvitation%2Csaved%2CleadAssociatedAccount~fs_salesCompany%28entityUrn%2Cname%2CflagshipCompanyUrl%2Cwebsite%29%2CoutreachActivity%2Cmemorialized%2CsavedAccount~fs_salesCompany%28entityUrn%2Cname%29%2CnotificationUrnOnLeadList%2CuniquePositionCompanyCount%2CcurrentPositions*%28title%2CcompanyName%2Ccurrent%2CcompanyUrn%29%2CmostRecentEntityNote%28body%2ClastModifiedAt%2CnoteId%2Cseat%2Centity%2CownerInfo%2Cownership%2Cvisibility%29%29"
	if got != want {
		t.Fatalf("PageURL =\n%s\nwant\n%s", got, want)
	}
}

func TestPageURLSavedSearch(t *testing.T) {
	src := Source{Kind: KindSavedSearch, ID: "1929545378", SessionID: "RGnoY2t0R72UfGqkItwziQ==", LastViewedAt: "1752770598757"}
	want := "https://www.linkedin.com/sales-api/salesApiLeadSearch?q=savedSearchId&start=0&count=25&savedSearchId=1929545378&lastViewedAt=1752770598757&trackingParam=(sessionId:RGnoY2t0R72UfGqkItwziQ==)&decorationId=com.linkedin.sales.deco.desktop.searchv2.LeadSearchResult-14"
	if got := src.PageURL(0); got != want {
		t.Fatalf("PageURL =\n%s\nwant\n%s", got, want)
	}
	src.LastViewedAt = ""
	if got := src.PageURL(0); strings.Contains(got, "lastViewedAt") {
		t.Fatalf("PageURL without lastViewedAt = %s", got)
	}
}

func jsString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFetchJSHeaders(t *testing.T) {
	const u = "https://www.linkedin.com/sales-api/salesApiLeadSearch?q=savedSearchId&start=0"
	js := FetchJS(u)
	track := `{"clientVersion":"1.13.27374","mpVersion":"1.13.27374","osName":"web","timezoneOffset":-4,"timezone":"America/New_York","deviceFormFactor":"DESKTOP","mpName":"voyager-web","displayDensity":2,"displayWidth":2841,"displayHeight":1664}`
	for _, want := range []string{
		`"Csrf-Token": csrf`,
		`"Origin": "https://www.linkedin.com"`,
		`"Referer": "https://linkedin.com/feed/"`,
		`"X-Li-Lang": "en_US'"`,
		`"X-Li-Page-Instance": "urn:li:page:d_flagship3_feed;zVgrqhZVR4WstYEkJpKfmQ=="`,
		`"X-Restli-Protocol-Version": "2.0.0"`,
		`"X-Li-Track": ` + jsString(t, track),
	} {
		if !strings.Contains(js, want) {
			t.Errorf("FetchJS missing %s", want)
		}
	}
	if n := strings.Count(js, jsString(t, u)); n != 1 {
		t.Errorf("page URL appears %d times as a JS string, want 1", n)
	}
}

func TestFetchJSNoSessionBeforeFetch(t *testing.T) {
	js := FetchJS("https://www.linkedin.com/x")
	ns, f := strings.Index(js, "noSession: true"), strings.Index(js, "fetch(")
	if ns < 0 || f < 0 || ns > f {
		t.Fatalf("noSession return at %d, fetch at %d: must return before fetching", ns, f)
	}
}

func TestFetchJSQuotesURL(t *testing.T) {
	u := "https://x/\"</script>\\"
	js := FetchJS(u)
	if !strings.Contains(js, jsString(t, u)) {
		t.Fatalf("URL not JSON-quoted in %s", js)
	}
	if strings.Contains(js, `"</script>`) {
		t.Fatalf("raw quote + </script> leaked into the expression")
	}
}

// TestSessionCookieRegex runs the same pattern FetchJS embeds (Go and JS regexps agree on
// this syntax) against cookie strings where a lookalike comes first.
func TestSessionCookieRegex(t *testing.T) {
	re := regexp.MustCompile(sessionCookieRE)
	tests := []struct{ cookie, want string }{
		{`JSESSIONID="ajax:123"`, "ajax:123"},
		{`lang=v=2; JSESSIONID="ajax:123"; li_at=x`, "ajax:123"},
		{`li_JSESSIONID=wrong; JSESSIONID="ajax:123"`, "ajax:123"},
		{`note=JSESSIONID=wrong; JSESSIONID="ajax:123"`, "ajax:123"},
		{`li_JSESSIONID=wrong`, ""},
	}
	for _, tt := range tests {
		got := ""
		if m := re.FindStringSubmatch(tt.cookie); m != nil {
			got = m[1]
		}
		if got != tt.want {
			t.Errorf("cookie %q → %q, want %q", tt.cookie, got, tt.want)
		}
	}
	if !strings.Contains(FetchJS("u"), "document.cookie.match(/"+sessionCookieRE+"/)") {
		t.Error("FetchJS does not use sessionCookieRE")
	}
}
