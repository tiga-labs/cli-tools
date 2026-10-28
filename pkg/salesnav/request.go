package salesnav

import (
	"encoding/json"
	"strconv"
	"strings"
)

// PageSize is how many leads one sales-api request returns.
const PageSize = 25

// The requests below are copied verbatim from the Tiga Chrome extension (gandalf
// frontend/deku linkedin.js getPageOfData / getSalesNavSavedSearch and core.js
// linkedInRequest), quirks included, so LinkedIn sees the same traffic it already does.
const listDecoration = "%28entityUrn%2CprofilePictureDisplayImage%2CfirstName%2ClastName%2CfullName%2Cdegree%2CblockThirdPartyDataSharing%2CcrmStatus%2CgeoRegion%2ClistCount%2CdateAddedToListAt%2CpendingInvitation%2Csaved%2CleadAssociatedAccount~fs_salesCompany%28entityUrn%2Cname%2CflagshipCompanyUrl%2Cwebsite%29%2CoutreachActivity%2Cmemorialized%2CsavedAccount~fs_salesCompany%28entityUrn%2Cname%29%2CnotificationUrnOnLeadList%2CuniquePositionCompanyCount%2CcurrentPositions*%28title%2CcompanyName%2Ccurrent%2CcompanyUrn%29%2CmostRecentEntityNote%28body%2ClastModifiedAt%2CnoteId%2Cseat%2Centity%2CownerInfo%2Cownership%2Cvisibility%29%29"

const liTrack = `{"clientVersion":"1.13.27374","mpVersion":"1.13.27374","osName":"web","timezoneOffset":-4,"timezone":"America/New_York","deviceFormFactor":"DESKTOP","mpName":"voyager-web","displayDensity":2,"displayWidth":2841,"displayHeight":1664}`

// sessionCookieRE finds the JSESSIONID cookie (LinkedIn's CSRF token) in document.cookie.
// It is anchored to a cookie boundary so a lookalike name or value cannot match first.
const sessionCookieRE = `(?:^|;\s*)JSESSIONID="?([^";]+)"?`

// PageURL is the sales-api request for the page of leads starting at offset start.
func (s Source) PageURL(start int) string {
	st := strconv.Itoa(start)
	if s.Kind == KindSavedSearch {
		q := "start=" + st + "&count=" + strconv.Itoa(PageSize) + "&savedSearchId=" + s.ID
		if s.LastViewedAt != "" {
			q += "&lastViewedAt=" + s.LastViewedAt
		}
		q += "&trackingParam=(sessionId:" + s.SessionID + ")&decorationId=com.linkedin.sales.deco.desktop.searchv2.LeadSearchResult-14"
		return "https://www.linkedin.com/sales-api/salesApiLeadSearch?q=savedSearchId&" + q
	}
	q := "query=(spotlightParam:(selectedType:ALL),doFetchSpotlights:true,doFetchHits:true," +
		"doFetchFilters:false," +
		"pivotParam:(com.linkedin.sales.search.LeadListPivotRequest:(list:urn%3Ali%3Afs_salesList%3A" + s.ID + ",sortCriteria:CREATED_TIME,sortOrder:DESCENDING))," +
		"list:(scope:LEAD,includeAll:false,excludeAll:false,includedValues:List((id:" + s.ID + "))))" +
		"&start=" + st + "&count=" + strconv.Itoa(PageSize) + "&decoration=" + listDecoration
	return "https://www.linkedin.com/sales-api/salesApiPeopleSearch?q=peopleSearchQuery&" + q
}

// FetchResult is what the FetchJS expression resolves to.
type FetchResult struct {
	// NoSession is set when the page has no LinkedIn session cookie; nothing was fetched.
	NoSession   bool   `json:"noSession"`
	Status      int    `json:"status"`
	Redirected  bool   `json:"redirected"`
	URL         string `json:"url"`
	ContentType string `json:"contentType"`
	Body        string `json:"body"`
}

// FetchJS is a JavaScript expression that fetches pageURL from inside a LinkedIn tab with
// the page's own session, and resolves to a FetchResult. The CSRF token is read from the
// page's JSESSIONID cookie; if there is none, it returns without sending a request.
func FetchJS(pageURL string) string {
	return strings.NewReplacer("<PAGE_URL>", jsQuote(pageURL), "<TRACK>", jsQuote(liTrack), "<COOKIE_RE>", sessionCookieRE).Replace(`(async () => {
  const m = document.cookie.match(/<COOKIE_RE>/);
  if (!m) return { noSession: true };
  const csrf = m[1];
  const r = await fetch(<PAGE_URL>, { method: "get", headers: {
    "Csrf-Token": csrf,
    "Origin": "https://www.linkedin.com",
    "Referer": "https://linkedin.com/feed/",
    "X-Li-Lang": "en_US'",
    "X-Li-Page-Instance": "urn:li:page:d_flagship3_feed;zVgrqhZVR4WstYEkJpKfmQ==",
    "X-Li-Track": <TRACK>,
    "X-Restli-Protocol-Version": "2.0.0"
  }});
  return { status: r.status, redirected: r.redirected, url: r.url, contentType: r.headers.get("content-type") || "", body: await r.text() };
})()`)
}

// jsQuote returns s as a JavaScript string literal. JSON strings are valid JS strings, and
// json.Marshal escapes <, > and & so the result cannot close a surrounding script.
func jsQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
