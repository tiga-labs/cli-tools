package enrich

import (
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

const (
	VendorFindymail   = "findymail"
	VendorProspeo     = "prospeo"
	VendorApollo      = "apollo"
	VendorLusha       = "lusha"
	VendorSeamless    = "seamless"
	VendorDropcontact = "dropcontact"
	VendorHunter      = "hunter"
	VendorDatagma     = "datagma"
	VendorLeadIQ      = "leadiq"

	VendorRenidly    = "renidly"
	VendorZeroBounce = "zerobounce"
	VendorOpenAI     = "openai"
)

// DefaultWaterfallOrder defines the default sequence in which waterfall vendors are queried.
var DefaultWaterfallOrder = []string{
	VendorFindymail,
	VendorProspeo,
	VendorApollo,
	VendorLusha,
	VendorSeamless,
	VendorDropcontact,
	VendorHunter,
	VendorDatagma,
	VendorLeadIQ,
}

// AllSupportedVendors includes all 9 waterfall providers plus the 3 feature providers.
var AllSupportedVendors = []string{
	VendorFindymail,
	VendorProspeo,
	VendorApollo,
	VendorLusha,
	VendorSeamless,
	VendorDropcontact,
	VendorHunter,
	VendorDatagma,
	VendorLeadIQ,
	VendorRenidly,
	VendorZeroBounce,
	VendorOpenAI,
}

// ProviderTransport, when non-nil (in tests), intercepts HTTP calls made by vendor clients.
var ProviderTransport http.RoundTripper

// ProviderTimeout bounds each vendor HTTP request so a stalled connection cannot hang a worker.
var ProviderTimeout = 60 * time.Second

func newProviderClient() *resty.Client {
	client := resty.New().SetTimeout(ProviderTimeout)
	if ProviderTransport != nil {
		client.SetTransport(ProviderTransport)
	}
	return client
}

// ContactRecord holds all input, enriched, and provenance fields for a single contact.
type ContactRecord struct {
	FirstName       string `json:"first_name"`
	LastName        string `json:"last_name"`
	Email           string `json:"email"`
	Phone           string `json:"phone"`
	PersonLinkedIn  string `json:"person_linkedin"`
	LinkedInURN     string `json:"linkedin_urn,omitempty"`
	Title           string `json:"title"`
	CompanyName     string `json:"company_name"`
	Domain          string `json:"domain"`
	Website         string `json:"website"`
	CompanyLinkedIn string `json:"company_linkedin"`
	Industry        string `json:"industry,omitempty"`
	EmployeeCount   int    `json:"employee_count,omitempty"`
	City            string `json:"city,omitempty"`
	Region          string `json:"region,omitempty"`
	Country         string `json:"country,omitempty"`

	EmailSource           string `json:"email_source,omitempty"`
	PhoneSource           string `json:"phone_source,omitempty"`
	PersonLinkedInSource  string `json:"person_linkedin_source,omitempty"`
	CompanyLinkedInSource string `json:"company_linkedin_source,omitempty"`
	EmailVerified         string `json:"email_verified,omitempty"`
	Status                string `json:"status"`
	Error                 string `json:"error,omitempty"`
}

// ExtractRapidURN extracts the raw URN or profile token from Sales Navigator or fsd_profile URNs,
// or returns LinkedInURN trimmed if it is already a raw token/entityId.
func (c *ContactRecord) ExtractRapidURN() string {
	raw := strings.TrimSpace(c.LinkedInURN)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "urn:li:fs_salesProfile:") || strings.Contains(raw, "urn:li:fsd_profile:") {
		re := regexp.MustCompile(`\(([^()]*)\)`)
		matches := re.FindAllStringSubmatch(raw, -1)
		if len(matches) > 0 && len(matches[0]) > 0 {
			split := strings.Split(matches[0][0], ",")
			if len(split) > 0 {
				return strings.TrimSpace(strings.ReplaceAll(split[0], "(", ""))
			}
		}
	}
	return raw
}

var phoneDigitsRe = regexp.MustCompile(`\d`)

// HasValidPhone reports whether Phone contains a plausible phone number (at least 7 digits).
func (c *ContactRecord) HasValidPhone() bool {
	trimmed := strings.TrimSpace(c.Phone)
	if trimmed == "" {
		return false
	}
	digits := phoneDigitsRe.FindAllString(trimmed, -1)
	return len(digits) >= 7
}

// HasValidEmailSyntax reports whether Email is non-empty, not a placeholder, and parses as RFC-5322.
func (c *ContactRecord) HasValidEmailSyntax() bool {
	trimmed := strings.TrimSpace(c.Email)
	if trimmed == "" || strings.EqualFold(trimmed, ApolloPlaceholderEmail) || !strings.Contains(trimmed, "@") {
		return false
	}
	_, err := mail.ParseAddress(trimmed)
	return err == nil
}

// RunOptions configures which features and vendors are enabled for an enrichment run.
type RunOptions struct {
	NeedEmail    bool     `json:"need_email"`
	NeedLinkedIn bool     `json:"need_linkedin"`
	NeedPhone    bool     `json:"need_phone"`
	Vendors      []string `json:"vendors"`
	Exclude      []string `json:"exclude,omitempty"`

	ResolveURN      bool `json:"resolve_urn"`
	CleanNames      bool `json:"clean_names"`
	ResearchCompany bool `json:"research_company"`
	VerifyEmail     bool `json:"verify_email"`

	Concurrency int `json:"concurrency"`
}

// DefaultRunOptions returns the standard per-run feature defaults.
func DefaultRunOptions() RunOptions {
	return RunOptions{
		NeedEmail:       true,
		NeedLinkedIn:    true,
		NeedPhone:       false,
		ResolveURN:      true,
		CleanNames:      false,
		ResearchCompany: false,
		VerifyEmail:     false,
		Concurrency:     5,
	}
}

// NeedState tracks which fields are still missing during the waterfall loop.
type NeedState struct {
	ShouldContinue bool
	StillNeedEmail bool
	StillNeedLI    bool
	StillNeedPhone bool
}
