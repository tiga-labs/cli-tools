//go:build e2e

// End-to-end tests that call real enrichment vendors and spend real credits.
//
// Opt-in only: requires both the `e2e` build tag and ENRICH_E2E=1. See `make test-e2e`.
//
//	ENRICH_E2E=1                 required to run anything
//	ENRICH_E2E_FIXTURE=<path>    contacts file (default testdata/e2e/contacts.json)
//	ENRICH_E2E_CONFIG=<path>     enrich config file (default ~/.config/enrich/config.json)
//	ENRICH_E2E_MAX_CONTACTS=<n>  cap on contacts used (default 3)
//	ENRICH_E2E_PHONE=1           also request phone numbers (expensive credits)
//
// API keys are resolved exactly as the CLI does: *_API_KEY env vars override the config file.
package enrich_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/enrich"
)

const (
	defaultFixturePath     = "testdata/e2e/contacts.json"
	defaultMaxContacts     = 3
	perContactTimeout      = 3 * time.Minute
	e2eDisabledExitMessage = "skipping e2e tests: set ENRICH_E2E=1 to call real vendors (spends credits)"
)

var (
	personLinkedInRe  = regexp.MustCompile(`^https://www\.linkedin\.com/in/[^/?]+$`)
	companyLinkedInRe = regexp.MustCompile(`^https://www\.linkedin\.com/(company|showcase|school)/[^/?]+$`)

	// benignErrRe matches "nothing found" / "not enough input" outcomes that are not integration faults.
	benignErrRe = regexp.MustCompile(`(?i)no[ _]match|not[ _]found|found no|insufficient identifiers|is required|required for|no website|no person name|no linkedin url|valid linkedin url required`)
	// faultErrRe matches errors that indicate a broken integration (auth, billing, schema, transport).
	faultErrRe = regexp.MustCompile(`(?i)non-2xx|\b40[123]\b|credits|timeout|timed out|deadline|rate limit|parse|unmarshal|EOF|error \(\d{3}\)|status \d{3}|subscription`)
)

type e2eContact struct {
	Label string `json:"label"`
	enrich.ContactRecord
}

var (
	e2eCfg       *enrich.Config
	e2eContacts  []e2eContact
	e2eWantPhone bool
	callCounter  = &countingTransport{base: http.DefaultTransport, counts: map[string]int{}}
)

// countingTransport records real HTTP calls per host so each run's credit spend is visible.
type countingTransport struct {
	base   http.RoundTripper
	mu     sync.Mutex
	counts map[string]int
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.counts[req.URL.Host]++
	c.mu.Unlock()
	return c.base.RoundTrip(req)
}

func (c *countingTransport) report() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	hosts := make([]string, 0, len(c.counts))
	total := 0
	for h, n := range c.counts {
		hosts = append(hosts, h)
		total += n
	}
	sort.Strings(hosts)
	var b strings.Builder
	fmt.Fprintf(&b, "e2e vendor HTTP calls: %d total\n", total)
	for _, h := range hosts {
		fmt.Fprintf(&b, "  %-40s %d\n", h, c.counts[h])
	}
	return b.String()
}

func TestMain(m *testing.M) {
	if os.Getenv("ENRICH_E2E") != "1" {
		fmt.Println(e2eDisabledExitMessage)
		os.Exit(0)
	}
	if err := setupE2E(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e setup failed: %v\n", err)
		os.Exit(1)
	}

	enrich.ProviderTransport = callCounter
	code := m.Run()
	enrich.ProviderTransport = nil

	fmt.Print(callCounter.report())
	os.Exit(code)
}

func setupE2E() error {
	cfg, err := enrich.LoadConfig(os.Getenv("ENRICH_E2E_CONFIG"))
	if err != nil {
		return err
	}
	e2eCfg = cfg

	path := os.Getenv("ENRICH_E2E_FIXTURE")
	if path == "" {
		path = defaultFixturePath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read fixture %s (copy testdata/e2e/contacts.example.json to contacts.json and fill it in): %w", path, err)
	}
	if err := json.Unmarshal(data, &e2eContacts); err != nil {
		return fmt.Errorf("parse fixture %s: %w", path, err)
	}
	if len(e2eContacts) == 0 {
		return fmt.Errorf("fixture %s has no contacts", path)
	}

	maxContacts := defaultMaxContacts
	if v := os.Getenv("ENRICH_E2E_MAX_CONTACTS"); v != "" {
		if maxContacts, err = strconv.Atoi(v); err != nil || maxContacts < 1 {
			return fmt.Errorf("invalid ENRICH_E2E_MAX_CONTACTS %q", v)
		}
	}
	if len(e2eContacts) > maxContacts {
		e2eContacts = e2eContacts[:maxContacts]
	}
	for i := range e2eContacts {
		if e2eContacts[i].Label == "" {
			e2eContacts[i].Label = fmt.Sprintf("contact-%d", i+1)
		}
	}

	e2eWantPhone = os.Getenv("ENRICH_E2E_PHONE") == "1"
	return nil
}

func requireKey(t *testing.T, vendor string) string {
	t.Helper()
	key := e2eCfg.GetKey(vendor)
	if key == "" {
		t.Skipf("no API key configured for %s", vendor)
	}
	return key
}

func contactCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), perContactTimeout)
	t.Cleanup(cancel)
	return ctx
}

// foundSomething reports whether enrichment added an email, LinkedIn URL, or phone not present in the input.
func foundSomething(input, row enrich.ContactRecord) bool {
	return (row.Email != "" && input.Email == "") ||
		(row.PersonLinkedIn != "" && input.PersonLinkedIn == "") ||
		(row.Phone != "" && input.Phone == "")
}

// assertShape checks structural invariants of an enriched row without depending on live data values.
func assertShape(t *testing.T, input, row enrich.ContactRecord) {
	t.Helper()

	switch row.Status {
	case "enriched", "partial", "not_found", "error":
	default:
		t.Errorf("unexpected status %q", row.Status)
	}

	if row.Error != "" {
		if e2eCfg.RedactSecrets(row.Error) != row.Error {
			t.Errorf("row error leaks an API key")
		}
		switch {
		case benignErrRe.MatchString(row.Error):
			t.Logf("benign vendor outcome: %s", row.Error)
		case faultErrRe.MatchString(row.Error):
			t.Errorf("vendor integration fault: %s", row.Error)
		default:
			t.Logf("unclassified vendor error (review): %s", row.Error)
		}
	}

	if row.Email != "" {
		if _, err := mail.ParseAddress(row.Email); err != nil {
			t.Errorf("email %q does not parse: %v", row.Email, err)
		}
		if row.Email != strings.ToLower(row.Email) {
			t.Errorf("email %q is not lowercased", row.Email)
		}
		if input.Email == "" && row.EmailSource == "" {
			t.Errorf("email %q found without EmailSource", row.Email)
		}
	}
	if row.PersonLinkedIn != "" {
		if !personLinkedInRe.MatchString(row.PersonLinkedIn) {
			t.Errorf("person LinkedIn %q is not canonical", row.PersonLinkedIn)
		}
		if input.PersonLinkedIn == "" && row.PersonLinkedInSource == "" {
			t.Errorf("person LinkedIn %q found without PersonLinkedInSource", row.PersonLinkedIn)
		}
	}
	if row.Phone != "" {
		if !row.HasValidPhone() {
			t.Errorf("phone %q is not a plausible number", row.Phone)
		}
		if input.Phone == "" && row.PhoneSource == "" {
			t.Errorf("phone %q found without PhoneSource", row.Phone)
		}
	}
	if row.CompanyLinkedIn != "" && !companyLinkedInRe.MatchString(row.CompanyLinkedIn) {
		t.Errorf("company LinkedIn %q is not canonical", row.CompanyLinkedIn)
	}
}

func logRow(t *testing.T, vendor, label string, row enrich.ContactRecord) {
	t.Helper()
	t.Logf("%s | %s | status=%s | email=%s (%s) | linkedin=%s (%s) | phone=%s (%s) | error=%s",
		vendor, label, row.Status,
		row.Email, row.EmailSource,
		row.PersonLinkedIn, row.PersonLinkedInSource,
		row.Phone, row.PhoneSource,
		row.Error)
}

func TestE2EWaterfallVendor(t *testing.T) {
	for _, vendor := range enrich.DefaultWaterfallOrder {
		t.Run(vendor, func(t *testing.T) {
			requireKey(t, vendor)

			opts := enrich.DefaultRunOptions()
			opts.Vendors = []string{vendor}
			opts.ResolveURN = false
			opts.ResearchCompany = false
			opts.VerifyEmail = false
			opts.NeedPhone = e2eWantPhone

			anyFound := false
			for _, c := range e2eContacts {
				t.Run(c.Label, func(t *testing.T) {
					input := c.ContactRecord
					row := c.ContactRecord
					if err := enrich.EnrichContact(contactCtx(t), e2eCfg, opts, &row); err != nil {
						t.Fatalf("EnrichContact returned error: %v", err)
					}
					logRow(t, vendor, c.Label, row)
					assertShape(t, input, row)
					if foundSomething(input, row) {
						anyFound = true
					}
				})
			}
			if !anyFound {
				t.Logf("WARNING: %s found nothing for any contact; check for a silent API/schema change", vendor)
			}
		})
	}
}

func TestE2EFeatureVendors(t *testing.T) {
	t.Run(enrich.VendorRenidly, func(t *testing.T) {
		key := requireKey(t, enrich.VendorRenidly)
		ran := false
		for _, c := range e2eContacts {
			if c.PersonLinkedIn == "" && c.LinkedInURN == "" {
				continue
			}
			ran = true
			t.Run(c.Label, func(t *testing.T) {
				// Start from identifiers only so we can see what Renidly fills in.
				row := enrich.ContactRecord{PersonLinkedIn: c.PersonLinkedIn, LinkedInURN: c.LinkedInURN}
				if err := enrich.RenidlyEnrichPerson(contactCtx(t), key, &row); err != nil {
					t.Fatalf("RenidlyEnrichPerson: %v", err)
				}
				t.Logf("renidly | %s | name=%s %s | title=%s | company=%s | company_li=%s",
					c.Label, row.FirstName, row.LastName, row.Title, row.CompanyName, row.CompanyLinkedIn)
				if row.FirstName == "" && row.LastName == "" && row.Title == "" {
					t.Errorf("renidly returned no name or title")
				}
				if row.CompanyLinkedIn != "" && !companyLinkedInRe.MatchString(row.CompanyLinkedIn) {
					t.Errorf("company LinkedIn %q is not canonical", row.CompanyLinkedIn)
				}
			})
		}
		if !ran {
			t.Skip("no fixture contacts with person_linkedin or linkedin_urn")
		}
	})

	t.Run(enrich.VendorZeroBounce, func(t *testing.T) {
		key := requireKey(t, enrich.VendorZeroBounce)
		ran := false
		for _, c := range e2eContacts {
			if c.Email == "" {
				continue
			}
			ran = true
			t.Run(c.Label, func(t *testing.T) {
				valid, err := enrich.ZeroBounceValidateEmail(contactCtx(t), key, c.Email)
				if err != nil {
					t.Fatalf("ZeroBounceValidateEmail: %v", err)
				}
				t.Logf("zerobounce | %s | %s valid=%v", c.Label, c.Email, valid)
			})
		}
		if !ran {
			t.Skip("no fixture contacts with email")
		}
	})

	t.Run(enrich.VendorOpenAI, func(t *testing.T) {
		key := requireKey(t, enrich.VendorOpenAI)
		for _, c := range e2eContacts {
			t.Run(c.Label, func(t *testing.T) {
				ctx := contactCtx(t)
				if c.FirstName != "" || c.LastName != "" {
					first, last, err := enrich.CleanFirstAndLastName(ctx, key, c.FirstName, c.LastName)
					if err != nil {
						t.Fatalf("CleanFirstAndLastName: %v", err)
					}
					if first == "" && last == "" {
						t.Errorf("cleaned name is empty")
					}
					t.Logf("openai | %s | %q %q -> %q %q", c.Label, c.FirstName, c.LastName, first, last)
				}
				if c.CompanyName != "" {
					company, err := enrich.CleanAccountName(ctx, key, c.CompanyName)
					if err != nil {
						t.Fatalf("CleanAccountName: %v", err)
					}
					if company == "" {
						t.Errorf("cleaned company name is empty")
					}
					t.Logf("openai | %s | %q -> %q", c.Label, c.CompanyName, company)
				}
			})
		}
	})

	t.Run("website-scrape", func(t *testing.T) {
		ran := false
		for _, c := range e2eContacts {
			site := c.Website
			if site == "" {
				site = c.Domain
			}
			if site == "" {
				continue
			}
			ran = true
			t.Run(c.Label, func(t *testing.T) {
				li, err := enrich.ScrapeCompanyLinkedInFromWebsite(contactCtx(t), site)
				if err != nil {
					t.Fatalf("ScrapeCompanyLinkedInFromWebsite(%s): %v", site, err)
				}
				t.Logf("website-scrape | %s | %s -> %q", c.Label, site, li)
				if li != "" && !companyLinkedInRe.MatchString(li) {
					t.Errorf("scraped company LinkedIn %q is not canonical", li)
				}
			})
		}
		if !ran {
			t.Skip("no fixture contacts with domain or website")
		}
	})
}

func TestE2EFullWaterfall(t *testing.T) {
	hasWaterfallKey := false
	for _, v := range enrich.DefaultWaterfallOrder {
		if e2eCfg.GetKey(v) != "" {
			hasWaterfallKey = true
			break
		}
	}
	if !hasWaterfallKey {
		t.Skip("no waterfall vendor keys configured")
	}

	opts := enrich.DefaultRunOptions()
	opts.ResolveURN = true
	opts.ResearchCompany = true
	opts.VerifyEmail = e2eCfg.GetKey(enrich.VendorZeroBounce) != ""
	opts.NeedPhone = e2eWantPhone

	for _, c := range e2eContacts {
		t.Run(c.Label, func(t *testing.T) {
			input := c.ContactRecord
			row := c.ContactRecord
			if err := enrich.EnrichContact(contactCtx(t), e2eCfg, opts, &row); err != nil {
				t.Fatalf("EnrichContact returned error: %v", err)
			}
			logRow(t, "waterfall", c.Label, row)
			assertShape(t, input, row)
			if row.Email != "" {
				switch row.EmailVerified {
				case "true", "error", "skipped":
				default:
					t.Errorf("email %q has unexpected EmailVerified %q", row.Email, row.EmailVerified)
				}
			}
		})
	}
}
