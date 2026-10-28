package salesnav

import (
	"net/url"
	"strconv"
	"strings"
)

// StopKind says why an import has to stop.
type StopKind int

const (
	// StopNotLoggedIn: the tab has no LinkedIn session; nothing was sent.
	StopNotLoggedIn StopKind = iota + 1
	// StopWarning: LinkedIn pushed back (401, 403, 429, 999, or a login/security redirect).
	StopWarning
	// StopFailed: any other unexpected response.
	StopFailed
)

// Stop is returned by CheckPage when the import must stop now. There are no retries:
// pushing on after a warning is how accounts get restricted.
type Stop struct {
	Kind   StopKind
	Status int
	Reason string
}

func (s *Stop) Error() string { return s.Reason }

var warningStatus = map[int]string{
	401: "the session expired or was logged out",
	403: "it refused the request",
	429: "too many requests",
	999: "it flagged the request as automated",
}

// CheckPage turns one fetch result into a page of leads, or a *Stop.
func CheckPage(r FetchResult, src Source) (Page, error) {
	if r.NoSession {
		return Page{}, &Stop{Kind: StopNotLoggedIn, Reason: "LinkedIn is not logged in in this Chrome profile"}
	}
	if why, ok := warningStatus[r.Status]; ok {
		return Page{}, &Stop{Kind: StopWarning, Status: r.Status, Reason: "LinkedIn answered " + strconv.Itoa(r.Status) + ": " + why}
	}
	if r.URL != "" || r.Redirected {
		u, err := url.Parse(r.URL)
		if r.Redirected || err != nil || !strings.HasPrefix(u.Path, "/sales-api/") {
			target := r.URL
			if err == nil {
				target = u.Host + u.Path
			}
			return Page{}, &Stop{Kind: StopWarning, Status: r.Status, Reason: "LinkedIn redirected to " + target + " (login or security check)"}
		}
	}
	if r.Status != 200 {
		return Page{}, &Stop{Kind: StopFailed, Status: r.Status, Reason: "LinkedIn answered " + strconv.Itoa(r.Status)}
	}
	if !strings.Contains(strings.ToLower(r.ContentType), "json") {
		return Page{}, &Stop{Kind: StopFailed, Status: r.Status, Reason: "LinkedIn sent a non-JSON response (" + r.ContentType + ")"}
	}
	page, err := ParsePage([]byte(r.Body), src)
	if err != nil {
		return Page{}, &Stop{Kind: StopFailed, Status: r.Status, Reason: "could not read LinkedIn's response: " + err.Error()}
	}
	return page, nil
}
