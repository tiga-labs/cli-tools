package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tiga-labs/cli-tools/pkg/urlutil"
)

const (
	renidlyLiveBaseURL = "https://renidly.com/api/v2"
	renidlyDataBaseURL = "https://renidly.com/api/data/v1"
)

// RenidlyEnvelope is the standard 5-field JSON envelope returned by all Renidly endpoints.
type RenidlyEnvelope struct {
	Success    bool            `json:"success"`
	StatusCode int             `json:"statusCode"`
	Message    string          `json:"message"`
	ErrorCode  string          `json:"error_code,omitempty"`
	Errors     any             `json:"errors"`
	Data       json.RawMessage `json:"data"`
}

// RenidlyPersonProfile holds normalized fields from either Renidly's Live or Data person endpoints.
type RenidlyPersonProfile struct {
	ID            string                  `json:"id"`
	EntityID      string                  `json:"entityId"`
	Handle        string                  `json:"handle"`
	Username      string                  `json:"username"`
	PublicID      string                  `json:"publicIdentifier"`
	FirstName     string                  `json:"first_name"`
	FirstNameAlt  string                  `json:"firstName"`
	LastName      string                  `json:"last_name"`
	LastNameAlt   string                  `json:"lastName"`
	Headline      string                  `json:"headline"`
	Title         string                  `json:"title"`
	GeoCity       string                  `json:"geo_city"`
	GeoCountry    string                  `json:"geo_country"`
	Location      *RenidlyPersonLocation  `json:"location,omitempty"`
	FullPositions []RenidlyPersonPosition `json:"full_positions,omitempty"`
	PositionsAlt  []RenidlyPersonPosition `json:"fullPositions,omitempty"`
	Positions     []RenidlyPersonPosition `json:"positions,omitempty"`
}

type RenidlyPersonLocation struct {
	City    string `json:"city"`
	Region  string `json:"region"`
	Country string `json:"country"`
	Full    string `json:"full"`
}

type RenidlyPersonPosition struct {
	Title            string `json:"title"`
	CompanyName      string `json:"company_name"`
	CompanyNameAlt   string `json:"companyName"`
	OrganizationName string `json:"organization_name"`
	OrgNameAlt       string `json:"organizationName"`
	CompanySlug      string `json:"company_slug"`
	CompanyUsername  string `json:"companyUsername"`
	OrganizationSlug string `json:"organization_slug"`
	CompanyURL       string `json:"company_url"`
	CompanyURLAlt    string `json:"companyURL"`
	OrganizationURL  string `json:"organization_url"`
	OrgURLAlt        string `json:"organizationUrl"`
	End              any    `json:"end,omitempty"`
}

type RenidlyEmploymentHistoryData struct {
	Positions []RenidlyPersonPosition `json:"positions"`
	History   []RenidlyPersonPosition `json:"employmentHistory"`
}

// RenidlyCompanyProfile holds normalized company fields from Renidly's Live or Data organization endpoints.
type RenidlyCompanyProfile struct {
	ID            any    `json:"id"`
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	URL           string `json:"url"`
	Website       string `json:"website"`
	Industry      string `json:"industry"`
	StaffCount    int    `json:"staff_count"`
	StaffCountAlt int    `json:"staffCount"`
}

// RenidlyEnrichPerson resolves a person's LinkedIn URN, entityId, or public handle via Renidly
// and populates missing fields on row (FirstName, LastName, Title, PersonLinkedIn, CompanyName, CompanyLinkedIn).
func RenidlyEnrichPerson(ctx context.Context, apiKey string, row *ContactRecord) error {
	if ctx == nil {
		return errors.New("ctx is nil")
	}
	if row == nil {
		return errors.New("row is nil")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return errors.New("renidly API key is not configured")
	}

	urn := row.ExtractRapidURN()
	handle := urlutil.LinkedinUrlToLinkedinId(row.PersonLinkedIn)

	if urn == "" && handle == "" {
		return nil
	}

	// Build query parameters for /api/v2/person/enrich
	params := map[string]string{}
	if strings.HasPrefix(urn, "prsn_") {
		// Data API opaque ID
		return renidlyEnrichFromDataAPI(ctx, apiKey, urn, "", row)
	}
	if urn != "" {
		params["entityId"] = urn
	}
	if handle != "" {
		params["handle"] = handle
	}

	env, err := renidlyGet(ctx, apiKey, renidlyLiveBaseURL+"/person/enrich", params)
	if err != nil {
		return err
	}
	if !env.Success || len(env.Data) == 0 || string(env.Data) == "null" {
		// Fallback to Data API if handle is available
		if handle != "" {
			return renidlyEnrichFromDataAPI(ctx, apiKey, "", handle, row)
		}
		return nil
	}

	var profile RenidlyPersonProfile
	if err := json.Unmarshal(env.Data, &profile); err != nil {
		return fmt.Errorf("renidly: failed to parse person profile: %w", err)
	}

	// If positions aren't embedded in live /person/enrich and we still need company/title,
	// try /person/employment-history when entityId is known.
	positions := profile.allPositions()
	entityID := firstNonEmpty(profile.EntityID, profile.ID, urn)
	if len(positions) == 0 && entityID != "" && (row.CompanyName == "" || row.CompanyLinkedIn == "" || row.Title == "") {
		if empEnv, empErr := renidlyGet(ctx, apiKey, renidlyLiveBaseURL+"/person/employment-history", map[string]string{"entityId": entityID}); empErr == nil && empEnv.Success && len(empEnv.Data) > 0 {
			var empData RenidlyEmploymentHistoryData
			if json.Unmarshal(empEnv.Data, &empData) == nil {
				positions = append(positions, empData.Positions...)
				positions = append(positions, empData.History...)
			} else {
				var posSlice []RenidlyPersonPosition
				if json.Unmarshal(empEnv.Data, &posSlice) == nil {
					positions = append(positions, posSlice...)
				}
			}
		}
	}

	applyRenidlyPersonToRow(profile, positions, row)
	return nil
}

func renidlyEnrichFromDataAPI(ctx context.Context, apiKey, id, handle string, row *ContactRecord) error {
	params := map[string]string{}
	if id != "" {
		params["id"] = id
	} else if handle != "" {
		params["handle"] = handle
	} else {
		return nil
	}

	env, err := renidlyGet(ctx, apiKey, renidlyDataBaseURL+"/people/profile", params)
	if err != nil {
		return err
	}
	if !env.Success || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}

	var profile RenidlyPersonProfile
	if err := json.Unmarshal(env.Data, &profile); err != nil {
		return fmt.Errorf("renidly: failed to parse data person profile: %w", err)
	}

	applyRenidlyPersonToRow(profile, profile.allPositions(), row)
	return nil
}

func (p RenidlyPersonProfile) allPositions() []RenidlyPersonPosition {
	if len(p.FullPositions) > 0 {
		return p.FullPositions
	}
	if len(p.PositionsAlt) > 0 {
		return p.PositionsAlt
	}
	return p.Positions
}

func applyRenidlyPersonToRow(profile RenidlyPersonProfile, positions []RenidlyPersonPosition, row *ContactRecord) {
	firstName := firstNonEmpty(profile.FirstName, profile.FirstNameAlt)
	lastName := firstNonEmpty(profile.LastName, profile.LastNameAlt)
	handle := firstNonEmpty(profile.Handle, profile.Username, profile.PublicID)

	if row.FirstName == "" && firstName != "" {
		row.FirstName = strings.TrimSpace(firstName)
	}
	if row.LastName == "" && lastName != "" {
		row.LastName = strings.TrimSpace(lastName)
	}
	if row.PersonLinkedIn == "" && handle != "" {
		row.PersonLinkedIn = "https://www.linkedin.com/in/" + strings.TrimSpace(handle)
		row.PersonLinkedInSource = VendorRenidly
	}

	if row.City == "" {
		if profile.GeoCity != "" {
			row.City = profile.GeoCity
		} else if profile.Location != nil && profile.Location.City != "" {
			row.City = profile.Location.City
		}
	}
	if row.Country == "" {
		if profile.GeoCountry != "" {
			row.Country = profile.GeoCountry
		} else if profile.Location != nil && profile.Location.Country != "" {
			row.Country = profile.Location.Country
		}
	}

	if len(positions) > 0 {
		pos := positions[0]
		compName := firstNonEmpty(pos.CompanyName, pos.CompanyNameAlt, pos.OrganizationName, pos.OrgNameAlt)
		compSlug := firstNonEmpty(pos.CompanySlug, pos.CompanyUsername, pos.OrganizationSlug)
		compURL := firstNonEmpty(pos.CompanyURL, pos.CompanyURLAlt, pos.OrganizationURL, pos.OrgURLAlt)

		if row.Title == "" && pos.Title != "" {
			row.Title = strings.TrimSpace(pos.Title)
		}
		if row.CompanyName == "" && compName != "" {
			row.CompanyName = strings.TrimSpace(compName)
		}
		if row.CompanyLinkedIn == "" {
			if compURL != "" && strings.Contains(compURL, "linkedin.com/") {
				if cleaned, err := urlutil.CleanCompanyLinkedinUrl(compURL); err == nil {
					row.CompanyLinkedIn = cleaned
					row.CompanyLinkedInSource = VendorRenidly
				} else {
					row.CompanyLinkedIn = compURL
					row.CompanyLinkedInSource = VendorRenidly
				}
			} else if compSlug != "" {
				row.CompanyLinkedIn = "https://www.linkedin.com/company/" + strings.TrimSpace(compSlug)
				row.CompanyLinkedInSource = VendorRenidly
			}
		}
	}

	if row.Title == "" && profile.Title != "" {
		row.Title = strings.TrimSpace(profile.Title)
	} else if row.Title == "" && profile.Headline != "" {
		row.Title = strings.TrimSpace(profile.Headline)
	}
}

// RenidlyEnrichCompany resolves an organization by its LinkedIn company slug or ID via Renidly
// and populates missing company details (CompanyName, Website, Domain, CompanyLinkedIn, Industry, EmployeeCount).
func RenidlyEnrichCompany(ctx context.Context, apiKey string, row *ContactRecord) error {
	if ctx == nil || row == nil {
		return nil
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}

	slugOrID := urlutil.LinkedinCompanyUrlToLinkedinId(row.CompanyLinkedIn)
	if slugOrID == "" {
		return nil
	}

	// Try Data API /companies/company?slug=... first since it accepts slug directly
	params := map[string]string{"slug": slugOrID}
	if strings.HasPrefix(slugOrID, "org_") {
		params = map[string]string{"id": slugOrID}
	}

	env, err := renidlyGet(ctx, apiKey, renidlyDataBaseURL+"/companies/company", params)
	if err != nil {
		return err
	}

	// If not found on Data API and slugOrID is numeric, try Live API /organization/enrich?id=...
	if !env.Success || len(env.Data) == 0 || string(env.Data) == "null" {
		liveParams := map[string]string{"id": slugOrID}
		if !isDigitsOnly(slugOrID) {
			// Resolve slug to numeric id first
			resEnv, resErr := renidlyGet(ctx, apiKey, renidlyLiveBaseURL+"/organization/resolve-slug", map[string]string{"slug": slugOrID})
			if resErr != nil || !resEnv.Success {
				return nil
			}
			var resolved struct {
				ID any `json:"id"`
			}
			if json.Unmarshal(resEnv.Data, &resolved) != nil || resolved.ID == nil {
				return nil
			}
			liveParams["id"] = fmt.Sprintf("%v", resolved.ID)
		}
		env, err = renidlyGet(ctx, apiKey, renidlyLiveBaseURL+"/organization/enrich", liveParams)
		if err != nil || !env.Success || len(env.Data) == 0 || string(env.Data) == "null" {
			return nil
		}
	}

	var comp RenidlyCompanyProfile
	if err := json.Unmarshal(env.Data, &comp); err != nil {
		return nil
	}

	if row.CompanyName == "" && comp.Name != "" {
		row.CompanyName = strings.TrimSpace(comp.Name)
	}
	if row.Website == "" && comp.Website != "" {
		row.Website = strings.TrimSpace(comp.Website)
	}
	if row.Domain == "" && row.Website != "" {
		if host, err := urlutil.HostFromWebsite(row.Website); err == nil && host != "" {
			row.Domain = host
		}
	}
	if comp.Slug != "" {
		row.CompanyLinkedIn = "https://www.linkedin.com/company/" + strings.TrimSpace(comp.Slug)
		if row.CompanyLinkedInSource == "" {
			row.CompanyLinkedInSource = VendorRenidly
		}
	} else if comp.URL != "" && strings.Contains(comp.URL, "linkedin.com/") {
		if cleaned, cleanErr := urlutil.CleanCompanyLinkedinUrl(comp.URL); cleanErr == nil {
			row.CompanyLinkedIn = cleaned
		}
	}
	if row.Industry == "" && comp.Industry != "" {
		row.Industry = comp.Industry
	}
	if row.EmployeeCount == 0 {
		if comp.StaffCount > 0 {
			row.EmployeeCount = comp.StaffCount
		} else if comp.StaffCountAlt > 0 {
			row.EmployeeCount = comp.StaffCountAlt
		}
	}

	return nil
}

func renidlyGet(ctx context.Context, apiKey, endpoint string, queryParams map[string]string) (RenidlyEnvelope, error) {
	var env RenidlyEnvelope
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetHeader("X-renidly-apikey", apiKey).
		SetHeader("Accept", "application/json").
		SetQueryParams(queryParams).
		SetResult(&env).
		Get(endpoint)
	if err != nil {
		return env, fmt.Errorf("renidly request failed: %w", err)
	}

	if resp.StatusCode() == 401 || resp.StatusCode() == 403 {
		return env, fmt.Errorf("renidly auth/permission error (status %d): %s", resp.StatusCode(), firstNonEmpty(env.Message, string(resp.Body())))
	}
	if resp.StatusCode() == 402 {
		return env, errors.New("renidly insufficient credits")
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return env, fmt.Errorf("renidly non-2xx status %d: %s", resp.StatusCode(), firstNonEmpty(env.Message, string(resp.Body())))
	}

	return env, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func isDigitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
