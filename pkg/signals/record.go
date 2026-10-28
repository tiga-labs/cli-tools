package signals

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tiga-labs/cli-tools/pkg/csvrun"
	"github.com/tiga-labs/cli-tools/pkg/urlutil"
)

// Standard merge-field names, as in the Tiga app and the signals skill.
var (
	PersonFields  = []string{"FirstName", "LastName", "EmailAddress", "LinkedInUrl", "Title", "Phone", "MobilePhone", "City", "State", "Country", "IntentData"}
	AccountFields = []string{"AccountName", "AccountIndustry", "AccountWebsite", "AccountDomain", "AccountLinkedInUrl", "AccountCity", "AccountRegion", "AccountCountry", "AccountIntentData"}
)

// fieldAliases maps each standard field to the normalized CSV headers auto-detected for it.
// "name" is deliberately absent: it means a company in an accounts CSV and a person in a
// contacts CSV, so it is only mapped by an explicit override.
var fieldAliases = map[string][]string{
	"FirstName":          {"first_name", "firstname", "first", "given_name"},
	"LastName":           {"last_name", "lastname", "last", "surname", "family_name"},
	"EmailAddress":       {"email", "email_address", "work_email", "business_email", "e_mail"},
	"LinkedInUrl":        {"person_linkedin", "linkedin", "linkedin_url", "person_linkedin_url", "li_url", "profile_url", "linkedin_profile", "linkedin_profile_url", "person_linkedin_profile"},
	"Title":              {"title", "job_title", "position", "role"},
	"Phone":              {"phone", "phone_number", "direct_phone", "telephone"},
	"MobilePhone":        {"mobile", "mobile_phone", "cell"},
	"City":               {"city"},
	"State":              {"state"},
	"Country":            {"country"},
	"IntentData":         {"intent", "intent_data"},
	"AccountName":        {"company", "company_name", "account", "account_name", "organization", "organization_name", "employer"},
	"AccountIndustry":    {"industry", "company_industry", "account_industry"},
	"AccountWebsite":     {"website", "company_website", "account_website", "website_url", "company_url"},
	"AccountDomain":      {"domain", "company_domain", "account_domain", "primary_domain", "company_domain_name"},
	"AccountLinkedInUrl": {"company_linkedin", "company_linkedin_url", "account_linkedin", "account_linkedin_url", "organization_linkedin_url", "linkedin_company_page", "company_linkedin_page", "linkedin_company_url", "linkedin_company"},
	"AccountCity":        {"company_city", "account_city"},
	"AccountRegion":      {"company_state", "account_state", "region", "account_region"},
	"AccountCountry":     {"company_country", "account_country"},
	"AccountIntentData":  {"account_intent", "account_intent_data"},
}

// aliasKey compares headers ignoring underscores, so camelCase export headers (profileUrl,
// companyName) match the snake_case aliases.
func aliasKey(normalizedHeader string) string {
	return strings.ReplaceAll(normalizedHeader, "_", "")
}

// socialDomains are hosts that identify a profile page, not the company's own website.
var socialDomains = []string{"linkedin.com", "facebook.com", "twitter.com", "x.com", "instagram.com", "youtube.com", "crunchbase.com"}

// fullNameColumns are the normalized headers split into FirstName/LastName when both are empty.
var fullNameColumns = []string{"full_name", "fullname", "contact_name", "person_name"}

func standardFields() []string {
	return append(slices.Clone(PersonFields), AccountFields...)
}

// ColumnMap maps a CSV header row to merge fields.
type ColumnMap struct {
	headers  []string
	standard map[string]int // standard field -> column index
}

// MapColumns resolves standard fields from headers: explicit overrides (standard field ->
// header) first, then header aliases. The first matching column wins.
func MapColumns(headers []string, overrides map[string]string) (ColumnMap, error) {
	m := ColumnMap{headers: headers, standard: map[string]int{}}
	std := standardFields()

	for field, header := range overrides {
		if !slices.Contains(std, field) {
			return ColumnMap{}, fmt.Errorf("unknown field %q in column override (expected one of: %s)", field, strings.Join(std, ", "))
		}
		target := strings.TrimSpace(header)
		idx := slices.IndexFunc(headers, func(h string) bool {
			return strings.TrimSpace(h) == target || csvrun.NormalizeHeader(h) == csvrun.NormalizeHeader(target)
		})
		if idx == -1 {
			return ColumnMap{}, fmt.Errorf("column override %q not found in CSV headers (%s)", header, strings.Join(headers, ", "))
		}
		m.standard[field] = idx
	}

	for i, h := range headers {
		key := aliasKey(csvrun.NormalizeHeader(h))
		for _, field := range std {
			if _, done := m.standard[field]; done {
				continue
			}
			if slices.ContainsFunc(fieldAliases[field], func(alias string) bool { return aliasKey(alias) == key }) {
				m.standard[field] = i
			}
		}
	}
	return m, nil
}

// Record builds the merge fields for one data row. Missing trailing cells count as empty.
func (m ColumnMap) Record(row []string) Record {
	cell := func(i int) string {
		if i < len(row) {
			return row[i]
		}
		return ""
	}

	fields := map[string]string{}
	// Lowest precedence first: raw header names, then normalized names (first column wins).
	for i, h := range m.headers {
		if raw := strings.TrimSpace(h); raw != "" {
			if _, ok := fields[raw]; !ok {
				fields[raw] = cell(i)
			}
		}
	}
	normalized := map[string]string{}
	for i, h := range m.headers {
		if norm := csvrun.NormalizeHeader(h); norm != "" {
			if _, ok := normalized[norm]; !ok {
				normalized[norm] = cell(i)
			}
		}
	}
	maps.Copy(fields, normalized)
	for field, i := range m.standard {
		fields[field] = cell(i)
	}
	return normalizeRecord(fields)
}

// Record is one person or account's merge fields. It is immutable; With returns a copy.
type Record struct {
	fields map[string]string
}

// NewRecord builds a Record from standard (or custom) field names, normalized like a CSV row.
func NewRecord(fields map[string]string) Record {
	return normalizeRecord(maps.Clone(fields))
}

func normalizeRecord(fields map[string]string) Record {
	if fields == nil {
		fields = map[string]string{}
	}
	for k, v := range fields {
		fields[k] = strings.TrimSpace(v)
	}

	// A LinkedIn (or other social) page in the website column is not the company's website:
	// never derive a domain from it, and use a LinkedIn company page as AccountLinkedInUrl.
	websiteIsSocial := slices.Contains(socialDomains, urlutil.GetDomainFromWebsite(fields["AccountWebsite"]))
	if websiteIsSocial && fields["AccountLinkedInUrl"] == "" {
		if _, err := urlutil.CleanCompanyLinkedinUrl(fields["AccountWebsite"]); err == nil {
			fields["AccountLinkedInUrl"] = fields["AccountWebsite"]
		}
	}

	if v := fields["LinkedInUrl"]; v != "" {
		if clean, err := urlutil.CleanPersonLinkedinUrl(v); err == nil {
			fields["LinkedInUrl"] = clean
		}
	}
	if v := fields["AccountLinkedInUrl"]; v != "" {
		if clean, err := urlutil.CleanCompanyLinkedinUrl(v); err == nil {
			fields["AccountLinkedInUrl"] = clean
		}
	}
	// Domains are cleaned like websites ("https://www.Acme.com/" -> "acme.com"); a value that
	// does not parse as a domain is kept as given.
	if v := fields["AccountDomain"]; v != "" {
		if clean := urlutil.GetDomainFromWebsite(v); clean != "" {
			fields["AccountDomain"] = clean
		}
	}
	if fields["AccountDomain"] == "" && fields["AccountWebsite"] != "" && !websiteIsSocial {
		fields["AccountDomain"] = urlutil.GetDomainFromWebsite(fields["AccountWebsite"])
	}
	if fields["AccountWebsite"] == "" && fields["AccountDomain"] != "" && strings.Contains(fields["AccountDomain"], ".") {
		fields["AccountWebsite"] = "https://" + fields["AccountDomain"]
	}
	if fields["FirstName"] == "" && fields["LastName"] == "" {
		for _, col := range fullNameColumns {
			if full := fields[col]; full != "" {
				first, last, _ := strings.Cut(full, " ")
				fields["FirstName"], fields["LastName"] = first, strings.TrimSpace(last)
				break
			}
		}
	}
	return Record{fields: fields}
}

// Get returns a merge field's value, or "" if it is not set.
func (r Record) Get(name string) string {
	return r.fields[name]
}

// With returns a copy of r with name set to value (used to feed one signal's output to the next).
func (r Record) With(name, value string) Record {
	fields := maps.Clone(r.fields)
	if fields == nil {
		fields = map[string]string{}
	}
	fields[name] = strings.TrimSpace(value)
	return Record{fields: fields}
}

// Fields returns a copy of all merge fields.
func (r Record) Fields() map[string]string {
	return maps.Clone(r.fields)
}
