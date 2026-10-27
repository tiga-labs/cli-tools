package enrich

import (
	"context"
	"fmt"
	"github.com/tiga-labs/cli-tools/pkg/csvrun"
	"io"
	"strings"
	"sync"
)

// ColumnOverrides allows the caller to explicitly specify which input CSV column headers map to contact fields.
type ColumnOverrides struct {
	FirstName       string
	LastName        string
	FullName        string
	Email           string
	Phone           string
	PersonLinkedIn  string
	LinkedInURN     string
	CompanyName     string
	Domain          string
	Website         string
	CompanyLinkedIn string
	Title           string
}

type resolvedColumnIndices struct {
	firstName       int
	lastName        int
	fullName        int
	email           int
	phone           int
	personLinkedIn  int
	linkedInURN     int
	companyName     int
	domain          int
	website         int
	companyLinkedIn int
	title           int
}

// AppendedCSVHeaders are the columns appended to every output CSV row.
var AppendedCSVHeaders = []string{
	"enriched_first_name",
	"enriched_last_name",
	"enriched_email",
	"email_source",
	"email_verified",
	"enriched_phone",
	"phone_source",
	"enriched_person_linkedin",
	"person_linkedin_source",
	"enriched_title",
	"enriched_company_name",
	"enriched_domain",
	"enriched_company_linkedin",
	"company_linkedin_source",
	"enrich_status",
	"enrich_error",
}

// ProgressCallback is optionally invoked as each CSV row finishes enrichment.
type ProgressCallback func(completed, total int, record ContactRecord)

// ProcessCSV reads all rows from r, maps columns using auto-detection + overrides,
// runs EnrichContact concurrently across a worker pool, and writes the original columns
// plus AppendedCSVHeaders to w in the exact original row order.
func ProcessCSV(
	ctx context.Context,
	r io.Reader,
	w io.Writer,
	cfg *Config,
	opts RunOptions,
	overrides ColumnOverrides,
	onProgress ProgressCallback,
) error {
	// records holds finished rows for the progress callback, keyed by row index.
	records := make(map[int]ContactRecord)
	var mu sync.Mutex
	runOpts := csvrun.Options{Concurrency: opts.Concurrency, AppendedHeaders: AppendedCSVHeaders}
	if onProgress != nil {
		runOpts.OnRowDone = func(completed, total, index int) {
			mu.Lock()
			rec := records[index]
			delete(records, index)
			mu.Unlock()
			onProgress(completed, total, rec)
		}
	}
	return csvrun.Process(ctx, r, w, runOpts, func(headers []string) (csvrun.RowFunc, error) {
		colIdx, err := resolveColumnIndices(headers, overrides)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, index int, row []string) []string {
			rec := extractContactFromCSVRow(row, colIdx)
			if err := ctx.Err(); err != nil {
				// Keep output aligned with input: emit cancelled rows without calling vendors.
				rec.Status = "error"
				rec.Error = err.Error()
			} else {
				_ = EnrichContact(ctx, cfg, opts, &rec)
			}
			if onProgress != nil {
				mu.Lock()
				records[index] = rec
				mu.Unlock()
			}
			return csvAppendedCells(rec)
		}, nil
	})
}

func csvAppendedCells(rec ContactRecord) []string {
	return []string{
		rec.FirstName,
		rec.LastName,
		rec.Email,
		rec.EmailSource,
		rec.EmailVerified,
		rec.Phone,
		rec.PhoneSource,
		rec.PersonLinkedIn,
		rec.PersonLinkedInSource,
		rec.Title,
		rec.CompanyName,
		rec.Domain,
		rec.CompanyLinkedIn,
		rec.CompanyLinkedInSource,
		rec.Status,
		rec.Error,
	}
}

func resolveColumnIndices(headers []string, overrides ColumnOverrides) (resolvedColumnIndices, error) {
	idx := resolvedColumnIndices{
		firstName:       -1,
		lastName:        -1,
		fullName:        -1,
		email:           -1,
		phone:           -1,
		personLinkedIn:  -1,
		linkedInURN:     -1,
		companyName:     -1,
		domain:          -1,
		website:         -1,
		companyLinkedIn: -1,
		title:           -1,
	}

	findExplicit := func(colName string) (int, error) {
		if strings.TrimSpace(colName) == "" {
			return -1, nil
		}
		target := strings.TrimSpace(colName)
		targetNorm := csvrun.NormalizeHeader(target)
		for i, h := range headers {
			if strings.EqualFold(strings.TrimSpace(h), target) || csvrun.NormalizeHeader(h) == targetNorm {
				return i, nil
			}
		}
		return -1, fmt.Errorf("column override %q not found in CSV headers (%s)", colName, strings.Join(headers, ", "))
	}

	var err error
	if idx.firstName, err = findExplicit(overrides.FirstName); err != nil {
		return idx, err
	}
	if idx.lastName, err = findExplicit(overrides.LastName); err != nil {
		return idx, err
	}
	if idx.fullName, err = findExplicit(overrides.FullName); err != nil {
		return idx, err
	}
	if idx.email, err = findExplicit(overrides.Email); err != nil {
		return idx, err
	}
	if idx.phone, err = findExplicit(overrides.Phone); err != nil {
		return idx, err
	}
	if idx.personLinkedIn, err = findExplicit(overrides.PersonLinkedIn); err != nil {
		return idx, err
	}
	if idx.linkedInURN, err = findExplicit(overrides.LinkedInURN); err != nil {
		return idx, err
	}
	if idx.companyName, err = findExplicit(overrides.CompanyName); err != nil {
		return idx, err
	}
	if idx.domain, err = findExplicit(overrides.Domain); err != nil {
		return idx, err
	}
	if idx.website, err = findExplicit(overrides.Website); err != nil {
		return idx, err
	}
	if idx.companyLinkedIn, err = findExplicit(overrides.CompanyLinkedIn); err != nil {
		return idx, err
	}
	if idx.title, err = findExplicit(overrides.Title); err != nil {
		return idx, err
	}

	// Auto-detect any remaining unmapped fields
	for i, h := range headers {
		norm := csvrun.NormalizeHeader(h)
		switch norm {
		case "first_name", "firstname", "first", "given_name":
			if idx.firstName == -1 {
				idx.firstName = i
			}
		case "last_name", "lastname", "last", "surname", "family_name":
			if idx.lastName == -1 {
				idx.lastName = i
			}
		case "full_name", "fullname", "name", "contact_name", "person_name":
			if idx.fullName == -1 {
				idx.fullName = i
			}
		case "email", "email_address", "work_email", "business_email", "e_mail":
			if idx.email == -1 {
				idx.email = i
			}
		case "phone", "phone_number", "mobile", "mobile_phone", "direct_phone", "telephone":
			if idx.phone == -1 {
				idx.phone = i
			}
		case "person_linkedin", "linkedin", "linkedin_url", "person_linkedin_url", "li_url", "profile_url", "linkedin_profile", "linkedin_profile_url":
			if idx.personLinkedIn == -1 {
				idx.personLinkedIn = i
			}
		case "linkedin_urn", "linkedin_profile_urn", "urn", "sales_nav_urn", "entity_id", "entityid":
			if idx.linkedInURN == -1 {
				idx.linkedInURN = i
			}
		case "company", "company_name", "account", "account_name", "organization", "organization_name", "employer":
			if idx.companyName == -1 {
				idx.companyName = i
			}
		case "domain", "company_domain", "account_domain", "primary_domain":
			if idx.domain == -1 {
				idx.domain = i
			}
		case "website", "company_website", "account_website", "website_url", "company_url":
			if idx.website == -1 {
				idx.website = i
			}
		case "company_linkedin", "company_linkedin_url", "account_linkedin", "account_linkedin_url", "organization_linkedin_url":
			if idx.companyLinkedIn == -1 {
				idx.companyLinkedIn = i
			}
		case "title", "job_title", "position", "role", "headline":
			if idx.title == -1 {
				idx.title = i
			}
		}
	}

	return idx, nil
}

func extractContactFromCSVRow(row []string, idx resolvedColumnIndices) ContactRecord {
	getCell := func(col int) string {
		if col < 0 || col >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[col])
	}

	rec := ContactRecord{
		FirstName:       getCell(idx.firstName),
		LastName:        getCell(idx.lastName),
		Email:           getCell(idx.email),
		Phone:           getCell(idx.phone),
		PersonLinkedIn:  getCell(idx.personLinkedIn),
		LinkedInURN:     getCell(idx.linkedInURN),
		CompanyName:     getCell(idx.companyName),
		Domain:          getCell(idx.domain),
		Website:         getCell(idx.website),
		CompanyLinkedIn: getCell(idx.companyLinkedIn),
		Title:           getCell(idx.title),
	}

	// If FirstName and LastName are empty but FullName is present, split FullName
	if rec.FirstName == "" && rec.LastName == "" {
		full := getCell(idx.fullName)
		if full != "" {
			parts := strings.Fields(full)
			if len(parts) == 1 {
				rec.FirstName = parts[0]
			} else if len(parts) > 1 {
				rec.FirstName = parts[0]
				rec.LastName = strings.Join(parts[1:], " ")
			}
		}
	}

	return rec
}
