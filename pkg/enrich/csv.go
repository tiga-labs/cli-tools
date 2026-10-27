package enrich

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
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
	if ctx == nil {
		return errors.New("ctx is nil")
	}
	if r == nil || w == nil {
		return errors.New("reader and writer are required")
	}

	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1 // allow variable trailing fields
	reader.LazyQuotes = true

	allRows, err := reader.ReadAll()
	if err != nil {
		return fmt.Errorf("failed to read CSV: %w", err)
	}
	if len(allRows) == 0 {
		return errors.New("CSV input is empty")
	}

	headers := allRows[0]
	dataRows := allRows[1:]

	colIdx, err := resolveColumnIndices(headers, overrides)
	if err != nil {
		return err
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 5
	}

	total := len(dataRows)
	enrichedRecords := make([]ContactRecord, total)

	writer := csv.NewWriter(w)
	outHeader := make([]string, 0, len(headers)+len(AppendedCSVHeaders))
	outHeader = append(outHeader, headers...)
	outHeader = append(outHeader, AppendedCSVHeaders...)
	if err := writer.Write(outHeader); err != nil {
		return err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}

	type jobItem struct {
		index int
		row   []string
	}

	jobs := make(chan jobItem, total)
	for i, row := range dataRows {
		jobs <- jobItem{index: i, row: row}
	}
	close(jobs)

	// done receives the index of each finished row; buffered so workers never block on the writer.
	done := make(chan int, total)

	numWorkers := concurrency
	if total < numWorkers {
		numWorkers = total
	}
	if numWorkers == 0 {
		numWorkers = 1
	}

	var wg sync.WaitGroup
	for wIdx := 0; wIdx < numWorkers; wIdx++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				rec := extractContactFromCSVRow(item.row, colIdx)
				if err := ctx.Err(); err != nil {
					// Keep output aligned with input: emit cancelled rows without calling vendors.
					rec.Status = "error"
					rec.Error = err.Error()
				} else {
					_ = EnrichContact(ctx, cfg, opts, &rec)
				}
				enrichedRecords[item.index] = rec
				done <- item.index
			}
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()

	// Write rows in original order as soon as every earlier row has finished, so
	// completed (paid) results reach the output even if the run is interrupted.
	finished := make([]bool, total)
	next := 0
	completedCount := 0
	var writeErr error
	for idx := range done {
		finished[idx] = true
		completedCount++
		if onProgress != nil {
			onProgress(completedCount, total, enrichedRecords[idx])
		}
		if writeErr != nil {
			continue
		}
		for next < total && finished[next] {
			if err := writer.Write(csvOutputRow(headers, dataRows[next], enrichedRecords[next])); err != nil {
				writeErr = err
				break
			}
			next++
		}
		writer.Flush()
		if err := writer.Error(); err != nil && writeErr == nil {
			writeErr = err
		}
	}
	if writeErr != nil {
		return writeErr
	}
	return ctx.Err()
}

func csvOutputRow(headers, origRow []string, rec ContactRecord) []string {
	// Pad origRow if it had fewer columns than headers
	padded := make([]string, len(headers))
	copy(padded, origRow)

	appended := []string{
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

	outRow := make([]string, 0, len(padded)+len(appended))
	outRow = append(outRow, padded...)
	return append(outRow, appended...)
}

func normalizeHeaderKey(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimPrefix(h, "\ufeff") // strip UTF-8 BOM if present
	h = strings.ReplaceAll(h, "-", "_")
	h = strings.ReplaceAll(h, " ", "_")
	return h
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
		targetNorm := normalizeHeaderKey(target)
		for i, h := range headers {
			if strings.EqualFold(strings.TrimSpace(h), target) || normalizeHeaderKey(h) == targetNorm {
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
		norm := normalizeHeaderKey(h)
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
