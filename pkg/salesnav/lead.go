package salesnav

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CSVHeader is the output column order. The first columns use names `tiga enrich` maps
// automatically.
var CSVHeader = []string{
	"first_name", "last_name", "full_name", "title", "company_name", "company_linkedin",
	"location", "linkedin_urn", "sales_nav_url", "degree", "date_added_to_list", "source_url",
}

// Lead is one Sales Navigator lead as written to the CSV.
type Lead struct {
	FirstName, LastName, FullName string
	Title, CompanyName            string
	CompanyLinkedIn               string
	Location                      string
	// LinkedInURN is the Sales Nav entity URN; enrich resolves it to a profile URL.
	LinkedInURN string
	SalesNavURL string
	// Degree is the connection degree as text: 1, 2, 3, or -1 for out of network.
	Degree string
	// DateAddedToList is RFC 3339 UTC; empty for saved searches.
	DateAddedToList string
	SourceURL       string
}

// Row returns the lead's values in CSVHeader order.
func (l Lead) Row() []string {
	return []string{
		l.FirstName, l.LastName, l.FullName, l.Title, l.CompanyName, l.CompanyLinkedIn,
		l.Location, l.LinkedInURN, l.SalesNavURL, l.Degree, l.DateAddedToList, l.SourceURL,
	}
}

// Page is one parsed sales-api response.
type Page struct {
	Leads               []Lead
	Start, Count, Total int
}

type rawLead struct {
	FirstName         string `json:"firstName"`
	LastName          string `json:"lastName"`
	FullName          string `json:"fullName"`
	EntityURN         string `json:"entityUrn"`
	GeoRegion         string `json:"geoRegion"`
	Degree            int    `json:"degree"`
	DateAddedToListAt int64  `json:"dateAddedToListAt"`
	CurrentPositions  []struct {
		Title       string `json:"title"`
		CompanyName string `json:"companyName"`
		CompanyURN  string `json:"companyUrn"`
		Current     bool   `json:"current"`
	} `json:"currentPositions"`
}

var (
	// urn:li:fs_salesProfile:(<id>,NAME_SEARCH,<token>) → "<id>,NAME_SEARCH,<token>"
	profileURNInner = regexp.MustCompile(`^urn:li:fs_salesProfile:\(([^()\s]+)\)$`)
	companyURNID    = regexp.MustCompile(`\d+`)
)

// ParsePage parses one sales-api response body. src supplies the source_url column.
func ParsePage(body []byte, src Source) (Page, error) {
	var resp struct {
		Elements []rawLead `json:"elements"`
		Paging   *struct {
			Start int `json:"start"`
			Count int `json:"count"`
			Total int `json:"total"`
		} `json:"paging"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Page{}, fmt.Errorf("parse Sales Navigator response: %w", err)
	}
	if resp.Paging == nil {
		return Page{}, errors.New("parse Sales Navigator response: missing paging")
	}
	page := Page{Start: resp.Paging.Start, Count: resp.Paging.Count, Total: resp.Paging.Total}
	for _, r := range resp.Elements {
		page.Leads = append(page.Leads, toLead(r, src))
	}
	return page, nil
}

// toLead applies the mapping the Tiga server uses for Sales Nav imports
// (gandalf BulkUploadAndEnrichSN): title and company come from the first current position.
func toLead(r rawLead, src Source) Lead {
	l := Lead{
		FirstName:   strings.TrimSpace(r.FirstName),
		LastName:    strings.TrimSpace(r.LastName),
		FullName:    strings.TrimSpace(r.FullName),
		Location:    strings.TrimSpace(r.GeoRegion),
		LinkedInURN: strings.TrimSpace(r.EntityURN),
		SourceURL:   src.URL,
	}
	if m := profileURNInner.FindStringSubmatch(l.LinkedInURN); m != nil {
		l.SalesNavURL = "https://www.linkedin.com/sales/lead/" + m[1]
	}
	if r.Degree != 0 {
		l.Degree = strconv.Itoa(r.Degree)
	}
	if r.DateAddedToListAt > 0 {
		l.DateAddedToList = time.UnixMilli(r.DateAddedToListAt).UTC().Format(time.RFC3339)
	}
	for _, p := range r.CurrentPositions {
		if !p.Current {
			continue
		}
		l.Title = strings.TrimSpace(p.Title)
		l.CompanyName = strings.TrimSpace(p.CompanyName)
		if id := companyURNID.FindString(p.CompanyURN); id != "" {
			l.CompanyLinkedIn = "https://www.linkedin.com/company/" + id
		}
		break
	}
	return l
}
