// Package salesnav turns a Sales Navigator lead list or saved-search URL into the exact
// sales-api page requests the Tiga Chrome extension makes, and parses the responses into
// leads. It does no network or browser work itself.
package salesnav

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// Input errors. Their text is shown to the user as-is.
var (
	ErrNotSalesNav    = errors.New("not a Sales Navigator lead list or saved search URL. Open the list or saved search in Sales Navigator and copy the URL from the address bar.")
	ErrAccountList    = errors.New("this is a Sales Navigator account list. salesnav-list-import works with lead lists and saved lead searches.")
	ErrUnsavedSearch  = errors.New("this is an unsaved Sales Navigator search. Save it in Sales Navigator, open it from Saved searches, and copy the URL from the address bar.")
	ErrMissingSession = errors.New("this saved-search URL has no sessionId. Open the saved search in Sales Navigator and copy the URL from the address bar.")
)

// Kind is what a Sales Navigator URL points at.
type Kind int

const (
	KindList Kind = iota + 1
	KindSavedSearch
)

// Source is a validated lead list or saved search. Every field that reaches a request
// has been checked, so it can be put into a URL as-is.
type Source struct {
	Kind Kind
	// ID is the lead list id or the savedSearchId.
	ID string
	// SessionID (saved searches) is the URL's sessionId, decoded.
	SessionID string
	// LastViewedAt (saved searches) may be empty.
	LastViewedAt string
	// URL is the input as given, trimmed. It becomes the source_url column.
	URL string
}

var (
	digits    = regexp.MustCompile(`^[0-9]+$`)
	sessionID = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{1,200}$`)
)

const (
	listPrefix   = "/sales/lists/people/"
	accountLists = "/sales/lists/company"
	searchPath   = "/sales/search/people"
)

// ParseURL validates a lead list or saved-search URL copied from the address bar.
func ParseURL(raw string) (Source, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return Source{}, ErrNotSalesNav
	}
	if host := strings.ToLower(u.Hostname()); host != "linkedin.com" && host != "www.linkedin.com" {
		return Source{}, ErrNotSalesNav
	}
	path := strings.TrimSuffix(u.Path, "/")

	switch {
	case strings.HasPrefix(path, listPrefix):
		id := strings.TrimPrefix(path, listPrefix)
		if !digits.MatchString(id) {
			return Source{}, ErrNotSalesNav
		}
		return Source{Kind: KindList, ID: id, URL: raw}, nil

	case strings.HasPrefix(path, accountLists):
		return Source{}, ErrAccountList

	case path == searchPath:
		// url.Query silently drops pairs it cannot parse (e.g. containing ';'), which would
		// make a tampered savedSearchId look missing; reject the URL instead.
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return Source{}, ErrNotSalesNav
		}
		id, sess, lva := q.Get("savedSearchId"), q.Get("sessionId"), q.Get("lastViewedAt")
		if id == "" {
			return Source{}, ErrUnsavedSearch
		}
		if !digits.MatchString(id) || (lva != "" && !digits.MatchString(lva)) {
			return Source{}, ErrNotSalesNav
		}
		if sess == "" {
			return Source{}, ErrMissingSession
		}
		if !sessionID.MatchString(sess) {
			return Source{}, ErrNotSalesNav
		}
		return Source{Kind: KindSavedSearch, ID: id, SessionID: sess, LastViewedAt: lva, URL: raw}, nil
	}
	return Source{}, ErrNotSalesNav
}

// Slug names the source for a default output file: list-<id> or search-<id>.
func (s Source) Slug() string {
	if s.Kind == KindSavedSearch {
		return "search-" + s.ID
	}
	return "list-" + s.ID
}
