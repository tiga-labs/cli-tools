package enrich_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/tiga-labs/cli-tools/pkg/httpx"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/enrich"
	"github.com/tiga-labs/cli-tools/pkg/keystore"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonHTTPResponse(status int, body any) *http.Response {
	var raw []byte
	switch v := body.(type) {
	case string:
		raw = []byte(v)
	default:
		raw, _ = json.Marshal(v)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(raw)),
	}
}

func TestConfigKeyManagementAndPrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	cfg, err := enrich.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if err := cfg.SetKey("Findymail", "fm-config-key-1234"); err != nil {
		t.Fatalf("SetKey findymail failed: %v", err)
	}
	if err := cfg.SetKey("renidly", "rnd-config-key-5678"); err != nil {
		t.Fatalf("SetKey renidly failed: %v", err)
	}
	if err := enrich.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	reloaded, err := enrich.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig reload failed: %v", err)
	}
	if got := reloaded.GetKey(enrich.VendorFindymail); got != "fm-config-key-1234" {
		t.Fatalf("expected config key fm-config-key-1234, got %q", got)
	}

	// Env var should override config file
	t.Setenv("FINDYMAIL_API_KEY", "fm-env-override-9999")
	if got := reloaded.GetKey(enrich.VendorFindymail); got != "fm-env-override-9999" {
		t.Fatalf("expected env override fm-env-override-9999, got %q", got)
	}

	// Remove key
	if err := reloaded.RemoveKey("renidly"); err != nil {
		t.Fatalf("RemoveKey failed: %v", err)
	}
	if got := reloaded.GetKey(enrich.VendorRenidly); got != "" {
		t.Fatalf("expected empty renidly key after removal, got %q", got)
	}
}

func TestWaterfallOrderingAndEarlyStop(t *testing.T) {
	cfg := enrich.NewConfig(map[string]string{
		enrich.VendorFindymail: "fm-key",
		enrich.VendorProspeo:   "pr-key",
		enrich.VendorLusha:     "lu-key",
	})

	var calledHosts []string
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calledHosts = append(calledHosts, req.URL.Host+req.URL.Path)
		switch req.URL.Host {
		case "app.findymail.com":
			// Return empty email so waterfall continues to Prospeo
			return jsonHTTPResponse(200, map[string]any{
				"contact": map[string]any{"email": ""},
			}), nil
		case "api.prospeo.io":
			// Return both verified email and LinkedIn URL so Lusha is never called!
			return jsonHTTPResponse(200, map[string]any{
				"error": false,
				"person": map[string]any{
					"linkedin_url": "https://www.linkedin.com/in/janedoe",
					"email": map[string]any{
						"status": "VERIFIED",
						"email":  "jane@acme.com",
					},
				},
			}), nil
		case "api.lusha.com":
			t.Errorf("Lusha should not be called because Prospeo satisfied email and linkedin")
			return jsonHTTPResponse(404, "{}"), nil
		default:
			return jsonHTTPResponse(404, "{}"), nil
		}
	})
	defer func() { httpx.Transport = nil }()

	opts := enrich.DefaultRunOptions()
	opts.NeedEmail = true
	opts.NeedLinkedIn = true
	opts.NeedPhone = false

	row := enrich.ContactRecord{
		FirstName:   "Jane",
		LastName:    "Doe",
		CompanyName: "Acme",
		Domain:      "acme.com",
	}

	if err := enrich.EnrichContact(context.Background(), cfg, opts, &row); err != nil {
		t.Fatalf("EnrichContact failed: %v", err)
	}

	if row.Email != "jane@acme.com" || row.EmailSource != enrich.VendorProspeo {
		t.Fatalf("unexpected email result: email=%q source=%q", row.Email, row.EmailSource)
	}
	if row.PersonLinkedIn != "https://www.linkedin.com/in/janedoe" || row.PersonLinkedInSource != enrich.VendorProspeo {
		t.Fatalf("unexpected linkedin result: li=%q source=%q", row.PersonLinkedIn, row.PersonLinkedInSource)
	}
	if row.Status != "enriched" {
		t.Fatalf("expected status 'enriched', got %q", row.Status)
	}
}

func TestRenidlyURNAndZeroBounceFallback(t *testing.T) {
	cfg := enrich.NewConfig(map[string]string{
		enrich.VendorRenidly:    "rnd-key",
		enrich.VendorZeroBounce: "zb-key",
		enrich.VendorFindymail:  "fm-key",
		enrich.VendorHunter:     "hu-key",
	})

	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "renidly.com":
			if got := req.Header.Get("X-renidly-apikey"); got != "rnd-key" {
				t.Errorf("expected X-renidly-apikey 'rnd-key', got %q", got)
			}
			return jsonHTTPResponse(200, map[string]any{
				"success":    true,
				"statusCode": 200,
				"data": map[string]any{
					"entityId":   "ACoAAABBBCCC",
					"handle":     "johnsmith",
					"first_name": "John",
					"last_name":  "Smith",
					"headline":   "VP of Engineering",
					"full_positions": []map[string]any{
						{
							"title":        "VP Engineering",
							"company_name": "Stripe",
							"company_slug": "stripe",
						},
					},
				},
			}), nil

		case "app.findymail.com":
			// Return a bouncing email first
			return jsonHTTPResponse(200, map[string]any{
				"contact": map[string]any{"email": "bad@stripe.com"},
			}), nil

		case "api.zerobounce.net":
			emailParam := req.URL.Query().Get("email")
			if emailParam == "bad@stripe.com" {
				return jsonHTTPResponse(200, map[string]any{
					"address": emailParam,
					"status":  "invalid",
				}), nil
			}
			return jsonHTTPResponse(200, map[string]any{
				"address": emailParam,
				"status":  "valid",
			}), nil

		case "api.hunter.io":
			return jsonHTTPResponse(200, map[string]any{
				"data": map[string]any{
					"email": "john.smith@stripe.com",
					"score": 95,
					"verification": map[string]any{
						"status": "valid",
					},
				},
			}), nil
		}
		return jsonHTTPResponse(404, "{}"), nil
	})
	defer func() { httpx.Transport = nil }()

	opts := enrich.DefaultRunOptions()
	opts.ResolveURN = true
	opts.VerifyEmail = true
	opts.NeedEmail = true
	opts.NeedLinkedIn = true

	row := enrich.ContactRecord{
		LinkedInURN: "urn:li:fs_salesProfile:(ACoAAABBBCCC,NAME_SEARCH,1234)",
	}

	if err := enrich.EnrichContact(context.Background(), cfg, opts, &row); err != nil {
		t.Fatalf("EnrichContact failed: %v", err)
	}

	if row.FirstName != "John" || row.LastName != "Smith" {
		t.Fatalf("expected Renidly to populate John Smith, got %q %q", row.FirstName, row.LastName)
	}
	if row.PersonLinkedIn != "https://www.linkedin.com/in/johnsmith" {
		t.Fatalf("expected LinkedIn URL from Renidly, got %q", row.PersonLinkedIn)
	}
	if row.CompanyName != "Stripe" || row.CompanyLinkedIn != "https://www.linkedin.com/company/stripe" {
		t.Fatalf("expected Stripe company details from Renidly, got name=%q li=%q", row.CompanyName, row.CompanyLinkedIn)
	}
	// Findymail's bad@stripe.com should have been rejected by ZeroBounce, falling through to Hunter's john.smith@stripe.com
	if row.Email != "john.smith@stripe.com" || row.EmailSource != enrich.VendorHunter || row.EmailVerified != "true" {
		t.Fatalf("expected verified email from Hunter, got email=%q source=%q verified=%q", row.Email, row.EmailSource, row.EmailVerified)
	}
}

func TestCSVBatchPreservesColumnsAndOrder(t *testing.T) {
	cfg := enrich.NewConfig(map[string]string{
		enrich.VendorFindymail: "fm-key",
	})

	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body map[string]string
		_ = json.NewDecoder(req.Body).Decode(&body)
		name := strings.ToLower(body["name"])
		domain := body["domain"]
		email := strings.ReplaceAll(name, " ", ".") + "@" + domain
		return jsonHTTPResponse(200, map[string]any{
			"contact": map[string]any{"email": email},
		}), nil
	})
	defer func() { httpx.Transport = nil }()

	inputCSV := strings.Join([]string{
		"Custom ID,Full Name,Company,Website,Notes",
		"row-1,Alice Wonderland,Acme Corp,https://www.acme.com,priority lead",
		"row-2,Bob Builder,BuildIt Inc,buildit.io,follow up",
		"row-3,Charlie Chaplin,Silent Films,silentfilms.org,vip",
	}, "\n")

	var outBuf bytes.Buffer
	opts := enrich.DefaultRunOptions()
	opts.NeedEmail = true
	opts.NeedLinkedIn = false
	opts.Concurrency = 3

	err := enrich.ProcessCSV(
		context.Background(),
		strings.NewReader(inputCSV),
		&outBuf,
		cfg,
		opts,
		enrich.ColumnOverrides{},
		nil,
	)
	if err != nil {
		t.Fatalf("ProcessCSV failed: %v", err)
	}

	outRows, err := csv.NewReader(&outBuf).ReadAll()
	if err != nil {
		t.Fatalf("failed to parse output CSV: %v", err)
	}
	if len(outRows) != 4 {
		t.Fatalf("expected 4 rows (1 header + 3 data), got %d", len(outRows))
	}

	// Verify original columns are preserved intact at indices 0..4
	if outRows[1][0] != "row-1" || outRows[1][4] != "priority lead" {
		t.Fatalf("row 1 original columns corrupted: %v", outRows[1])
	}
	if outRows[2][0] != "row-2" || outRows[2][4] != "follow up" {
		t.Fatalf("row 2 original columns corrupted: %v", outRows[2])
	}
	if outRows[3][0] != "row-3" || outRows[3][4] != "vip" {
		t.Fatalf("row 3 original columns corrupted: %v", outRows[3])
	}

	// Find index of enriched_email and email_source
	headerIdx := map[string]int{}
	for i, h := range outRows[0] {
		headerIdx[h] = i
	}
	emailCol := headerIdx["enriched_email"]
	sourceCol := headerIdx["email_source"]
	statusCol := headerIdx["enrich_status"]

	if outRows[1][emailCol] != "alice.wonderland@acme.com" || outRows[1][sourceCol] != "findymail" || outRows[1][statusCol] != "enriched" {
		t.Fatalf("unexpected enriched values for row 1: %v", outRows[1])
	}
	if outRows[2][emailCol] != "bob.builder@buildit.io" {
		t.Fatalf("unexpected enriched email for row 2: %q", outRows[2][emailCol])
	}
	if outRows[3][emailCol] != "charlie.chaplin@silentfilms.org" {
		t.Fatalf("unexpected enriched email for row 3: %q", outRows[3][emailCol])
	}
}

func TestAllVendorsAndNameCleaningAndCompanyResearch(t *testing.T) {
	enrich.DropcontactPollDelay = 0
	enrich.SeamlessPollDelay = 0

	cfg := enrich.NewConfig(map[string]string{
		enrich.VendorOpenAI:      "oa-key",
		enrich.VendorApollo:      "ap-key",
		enrich.VendorLusha:       "lu-key",
		enrich.VendorSeamless:    "sm-key",
		enrich.VendorDropcontact: "dc-key",
		enrich.VendorDatagma:     "dg-key",
		enrich.VendorLeadIQ:      "lq-key",
	})

	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "api.openai.com":
			return jsonHTTPResponse(200, map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"content": `{"cleaned_first_name":"Jane","cleaned_last_name":"Doe","cleaned_company_name":"Acme"}`,
						},
					},
				},
			}), nil

		case "api.apollo.io":
			if strings.Contains(req.URL.Path, "organizations/enrich") {
				return jsonHTTPResponse(200, map[string]any{
					"organization": map[string]any{
						"name":         "Acme",
						"linkedin_url": "https://www.linkedin.com/company/acme",
						"industry":     "Software",
					},
				}), nil
			}
			// Return no email on match/search so it falls through to other vendors
			return jsonHTTPResponse(200, map[string]any{"person": map[string]any{}}), nil

		case "api.lusha.com":
			// Return phone number only
			return jsonHTTPResponse(200, map[string]any{
				"data": map[string]any{
					"phoneNumbers": []map[string]any{
						{"phone": "+1-555-234-5678", "type": "mobile", "doNotCall": false},
					},
				},
			}), nil

		case "api.seamless.ai":
			if strings.HasSuffix(req.URL.Path, "/research") {
				return jsonHTTPResponse(200, map[string]any{
					"success":    true,
					"requestIds": []string{"req-123"},
				}), nil
			}
			return jsonHTTPResponse(200, map[string]any{
				"success": true,
				"data": []map[string]any{
					{
						"requestId": "req-123",
						"status":    "done",
						"contact":   map[string]any{},
					},
				},
			}), nil

		case "api.dropcontact.com":
			if req.Method == http.MethodPost {
				return jsonHTTPResponse(200, map[string]any{
					"success":    true,
					"request_id": "dc-123",
				}), nil
			}
			return jsonHTTPResponse(200, map[string]any{
				"success": true,
				"data": []map[string]any{
					{
						"linkedin": "https://www.linkedin.com/in/janedoe-dc",
					},
				},
			}), nil

		case "gateway.datagma.net":
			return jsonHTTPResponse(200, map[string]any{"status": "INVALID"}), nil

		case "api.leadiq.com":
			return jsonHTTPResponse(200, map[string]any{
				"data": map[string]any{
					"searchPeople": map[string]any{
						"totalResults": 1,
						"results": []map[string]any{
							{
								"currentPositions": []map[string]any{
									{
										"emails": []map[string]any{
											{"value": "jane.doe@acme.com", "status": "VERIFIED"},
										},
									},
								},
							},
						},
					},
				},
			}), nil
		}
		return jsonHTTPResponse(404, "{}"), nil
	})
	defer func() { httpx.Transport = nil }()

	opts := enrich.DefaultRunOptions()
	opts.CleanNames = true
	opts.ResearchCompany = true
	opts.NeedEmail = true
	opts.NeedLinkedIn = true
	opts.NeedPhone = true

	row := enrich.ContactRecord{
		FirstName:   "Dr. JANE",
		LastName:    "DOE, PhD",
		CompanyName: "ACME INC",
		Domain:      "acme.com",
	}

	if err := enrich.EnrichContact(context.Background(), cfg, opts, &row); err != nil {
		t.Fatalf("EnrichContact failed: %v", err)
	}

	if row.FirstName != "Jane" || row.LastName != "Doe" || row.CompanyName != "Acme" {
		t.Fatalf("expected OpenAI cleaned names, got %q %q @ %q", row.FirstName, row.LastName, row.CompanyName)
	}
	if row.CompanyLinkedIn != "https://www.linkedin.com/company/acme" || row.CompanyLinkedInSource != enrich.VendorApollo {
		t.Fatalf("expected Apollo company LinkedIn, got %q (%q)", row.CompanyLinkedIn, row.CompanyLinkedInSource)
	}
	if row.Phone != "+1-555-234-5678" || row.PhoneSource != enrich.VendorLusha {
		t.Fatalf("expected Lusha phone, got %q (%q)", row.Phone, row.PhoneSource)
	}
	if row.PersonLinkedIn != "https://www.linkedin.com/in/janedoe-dc" || row.PersonLinkedInSource != enrich.VendorDropcontact {
		t.Fatalf("expected Dropcontact LinkedIn, got %q (%q)", row.PersonLinkedIn, row.PersonLinkedInSource)
	}
	if row.Email != "jane.doe@acme.com" || row.EmailSource != enrich.VendorLeadIQ {
		t.Fatalf("expected LeadIQ email, got %q (%q)", row.Email, row.EmailSource)
	}
	if row.Status != "enriched" {
		t.Fatalf("expected status 'enriched', got %q", row.Status)
	}
}

func TestQueryParamKeyVendorsDoNotLeakKeyOnTransportError(t *testing.T) {
	const secret = "sk-very-secret-key-123"
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection refused")
	})
	defer func() { httpx.Transport = nil }()

	ctx := context.Background()
	newRow := func() *enrich.ContactRecord {
		return &enrich.ContactRecord{FirstName: "Jane", LastName: "Doe", Domain: "acme.com"}
	}
	_, zbErr := enrich.ZeroBounceValidateEmail(ctx, secret, "jane@acme.com")
	calls := map[string]error{
		"hunter":         enrich.HunterFindFromName(ctx, secret, newRow()),
		"datagma find":   enrich.DatagmaFindFromName(ctx, secret, newRow()),
		"datagma enrich": enrich.DatagmaEnrichPerson(ctx, secret, newRow()),
		"zerobounce":     zbErr,
	}
	for name, err := range calls {
		if err == nil {
			t.Fatalf("%s: expected transport error, got nil", name)
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("%s: error leaks API key: %v", name, err)
		}
		if !strings.Contains(err.Error(), "[REDACTED]") || !strings.Contains(err.Error(), "connection refused") {
			t.Errorf("%s: expected redacted URL and original cause, got: %v", name, err)
		}
	}

	// End-to-end: the key must not reach row.Error (written to CSV/JSON output).
	cfg := enrich.NewConfig(map[string]string{enrich.VendorHunter: secret})
	opts := enrich.DefaultRunOptions()
	opts.NeedLinkedIn = false
	row := newRow()
	if err := enrich.EnrichContact(ctx, cfg, opts, row); err != nil {
		t.Fatalf("EnrichContact failed: %v", err)
	}
	if row.Error == "" || strings.Contains(row.Error, secret) {
		t.Fatalf("expected redacted row error, got %q", row.Error)
	}
}

func TestProviderTimeoutBoundsStalledRequests(t *testing.T) {
	httpx.Timeout = 50 * time.Millisecond
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done() // simulate a vendor that never responds
		return nil, req.Context().Err()
	})
	defer func() {
		httpx.Transport = nil
		httpx.Timeout = 60 * time.Second
	}()

	cfg := enrich.NewConfig(map[string]string{enrich.VendorFindymail: "fm-key"})
	opts := enrich.DefaultRunOptions()
	opts.NeedLinkedIn = false
	row := enrich.ContactRecord{FirstName: "Jane", LastName: "Doe", Domain: "acme.com"}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := enrich.EnrichContact(ctx, cfg, opts, &row); err != nil {
		t.Fatalf("EnrichContact failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("stalled request was not bounded by httpx.Timeout (took %s)", elapsed)
	}
	if row.Status != "error" || row.Error == "" {
		t.Fatalf("expected timeout error on row, got status=%q error=%q", row.Status, row.Error)
	}
}

func TestProcessCSVStreamsInOrderAndReportsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body map[string]string
		_ = json.NewDecoder(req.Body).Decode(&body)
		if body["name"] == "Bob Two" {
			cancel() // simulate Ctrl-C while row 2 is in flight
		}
		return jsonHTTPResponse(200, map[string]any{
			"contact": map[string]any{"email": strings.ReplaceAll(strings.ToLower(body["name"]), " ", ".") + "@acme.com"},
		}), nil
	})
	defer func() { httpx.Transport = nil }()

	inputCSV := "ID,Full Name,Domain\n1,Alice One,acme.com\n2,Bob Two,acme.com\n3,Cara Three,acme.com\n4,Dan Four,acme.com\n"
	cfg := enrich.NewConfig(map[string]string{enrich.VendorFindymail: "fm-key"})
	opts := enrich.DefaultRunOptions()
	opts.NeedLinkedIn = false
	opts.Concurrency = 1

	var outBuf bytes.Buffer
	err := enrich.ProcessCSV(ctx, strings.NewReader(inputCSV), &outBuf, cfg, opts, enrich.ColumnOverrides{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from ProcessCSV, got %v", err)
	}

	outRows, err := csv.NewReader(&outBuf).ReadAll()
	if err != nil {
		t.Fatalf("failed to parse output CSV: %v", err)
	}
	if len(outRows) != 5 {
		t.Fatalf("expected header + 4 rows, got %d rows: %v", len(outRows), outRows)
	}
	headerIdx := map[string]int{}
	for i, h := range outRows[0] {
		headerIdx[h] = i
	}
	for i, want := range []string{"1", "2", "3", "4"} {
		if outRows[i+1][0] != want {
			t.Fatalf("row order not preserved: row %d has ID %q", i+1, outRows[i+1][0])
		}
	}
	if got := outRows[1][headerIdx["enriched_email"]]; got != "alice.one@acme.com" {
		t.Fatalf("expected row 1 enriched before cancel, got email %q", got)
	}
	for _, r := range outRows[3:] {
		if r[headerIdx["enrich_status"]] != "error" || !strings.Contains(r[headerIdx["enrich_error"]], "context canceled") {
			t.Fatalf("expected cancelled row to be marked error, got %v", r)
		}
	}
}

// hostCounter wraps a handler and counts requests per host.
type hostCounter struct {
	counts  map[string]int
	handler func(req *http.Request) (*http.Response, error)
}

func newHostCounter(handler func(req *http.Request) (*http.Response, error)) *hostCounter {
	return &hostCounter{counts: map[string]int{}, handler: handler}
}

func (h *hostCounter) RoundTrip(req *http.Request) (*http.Response, error) {
	h.counts[req.URL.Host]++
	return h.handler(req)
}

func TestSkippedVendorIsNotFoundNotError(t *testing.T) {
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Errorf("no vendor should be called for a row without inputs, got %s", req.URL)
		return jsonHTTPResponse(500, "{}"), nil
	})
	defer func() { httpx.Transport = nil }()

	cfg := enrich.NewConfig(map[string]string{enrich.VendorHunter: "hu-key"})
	opts := enrich.DefaultRunOptions()
	opts.NeedLinkedIn = false
	row := enrich.ContactRecord{Title: "CTO"} // no name, company, or domain

	if err := enrich.EnrichContact(context.Background(), cfg, opts, &row); err != nil {
		t.Fatalf("EnrichContact failed: %v", err)
	}
	if row.Status != "not_found" || row.Error != "" {
		t.Fatalf("expected not_found with no error, got status=%q error=%q", row.Status, row.Error)
	}
}

func TestProspeoNoMatchIsNotFound(t *testing.T) {
	counter := newHostCounter(func(req *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(400, `{"error":true,"error_code":"NO_MATCH"}`), nil
	})
	httpx.Transport = counter
	defer func() { httpx.Transport = nil }()

	cfg := enrich.NewConfig(map[string]string{enrich.VendorProspeo: "pr-key"})
	row := enrich.ContactRecord{FirstName: "Jane", LastName: "Doe", Domain: "acme.com"}
	if err := enrich.EnrichContact(context.Background(), cfg, enrich.DefaultRunOptions(), &row); err != nil {
		t.Fatalf("EnrichContact failed: %v", err)
	}
	if row.Status != "not_found" || row.Error != "" {
		t.Fatalf("expected not_found with no error, got status=%q error=%q", row.Status, row.Error)
	}
	if got := counter.counts["api.prospeo.io"]; got != 1 {
		t.Fatalf("expected 1 Prospeo call, got %d", got)
	}
}

func TestMultipleVendorErrorsAreAllReported(t *testing.T) {
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(500, `{"message":"boom"}`), nil
	})
	defer func() { httpx.Transport = nil }()

	cfg := enrich.NewConfig(map[string]string{
		enrich.VendorFindymail: "fm-key",
		enrich.VendorHunter:    "hu-key",
	})
	opts := enrich.DefaultRunOptions()
	opts.NeedLinkedIn = false
	row := enrich.ContactRecord{FirstName: "Jane", LastName: "Doe", Domain: "acme.com"}
	if err := enrich.EnrichContact(context.Background(), cfg, opts, &row); err != nil {
		t.Fatalf("EnrichContact failed: %v", err)
	}
	if row.Status != "error" || !strings.Contains(row.Error, "findymail") || !strings.Contains(row.Error, "hunter") || !strings.Contains(row.Error, "; ") {
		t.Fatalf("expected both vendor errors joined, got status=%q error=%q", row.Status, row.Error)
	}
}

func TestProspeoEmailAndLinkedInInOneCall(t *testing.T) {
	counter := newHostCounter(func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		if body["only_verified_email"] == true {
			t.Errorf("only_verified_email must be off when the LinkedIn URL is also needed")
		}
		return jsonHTTPResponse(200, map[string]any{
			"person": map[string]any{
				"linkedin_url": "https://www.linkedin.com/in/janedoe",
				"email":        map[string]any{"status": "VERIFIED", "email": "jane@acme.com"},
			},
		}), nil
	})
	httpx.Transport = counter
	defer func() { httpx.Transport = nil }()

	cfg := enrich.NewConfig(map[string]string{enrich.VendorProspeo: "pr-key"})
	row := enrich.ContactRecord{FirstName: "Jane", LastName: "Doe", Domain: "acme.com"}
	if err := enrich.EnrichContact(context.Background(), cfg, enrich.DefaultRunOptions(), &row); err != nil {
		t.Fatalf("EnrichContact failed: %v", err)
	}
	if got := counter.counts["api.prospeo.io"]; got != 1 {
		t.Fatalf("expected 1 Prospeo call for email + LinkedIn, got %d", got)
	}
	if row.Email != "jane@acme.com" || row.EmailSource != enrich.VendorProspeo ||
		row.PersonLinkedIn != "https://www.linkedin.com/in/janedoe" || row.PersonLinkedInSource != enrich.VendorProspeo {
		t.Fatalf("expected email and LinkedIn from Prospeo, got %+v", row)
	}
}

func TestZeroBounceErrorIsCheckedOnce(t *testing.T) {
	counter := newHostCounter(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "app.findymail.com":
			return jsonHTTPResponse(200, map[string]any{"contact": map[string]any{"email": "jane@acme.com"}}), nil
		case "api.zerobounce.net":
			return jsonHTTPResponse(500, `{"error":"internal"}`), nil
		}
		// Prospeo and Lusha find nothing, so the waterfall keeps going for the LinkedIn URL.
		return jsonHTTPResponse(404, "{}"), nil
	})
	httpx.Transport = counter
	defer func() { httpx.Transport = nil }()

	cfg := enrich.NewConfig(map[string]string{
		enrich.VendorFindymail:  "fm-key",
		enrich.VendorProspeo:    "pr-key",
		enrich.VendorLusha:      "lu-key",
		enrich.VendorZeroBounce: "zb-key",
	})
	opts := enrich.DefaultRunOptions()
	opts.VerifyEmail = true
	row := enrich.ContactRecord{FirstName: "Jane", LastName: "Doe", Domain: "acme.com"}
	if err := enrich.EnrichContact(context.Background(), cfg, opts, &row); err != nil {
		t.Fatalf("EnrichContact failed: %v", err)
	}
	if counter.counts["api.prospeo.io"] == 0 || counter.counts["api.lusha.com"] == 0 {
		t.Fatalf("expected waterfall to continue past ZeroBounce error, calls=%v", counter.counts)
	}
	if got := counter.counts["api.zerobounce.net"]; got != 1 {
		t.Fatalf("expected 1 ZeroBounce call, got %d (calls=%v)", got, counter.counts)
	}
	if row.Email != "jane@acme.com" || row.EmailVerified != "error" {
		t.Fatalf("expected email kept with EmailVerified=error, got email=%q verified=%q", row.Email, row.EmailVerified)
	}
}

func withFastRetries(t *testing.T) {
	t.Helper()
	prev := httpx.RetryMaxWait
	httpx.RetryMaxWait = 5 * time.Millisecond
	t.Cleanup(func() {
		httpx.RetryMaxWait = prev
		httpx.Transport = nil
	})
}

func TestRateLimitedRequestIsRetried(t *testing.T) {
	withFastRetries(t)
	calls := 0
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return jsonHTTPResponse(429, "{}"), nil
		}
		return jsonHTTPResponse(200, map[string]any{
			"data": map[string]any{"email": "jane@acme.com", "score": 90},
		}), nil
	})

	row := enrich.ContactRecord{FirstName: "Jane", LastName: "Doe", Domain: "acme.com"}
	if err := enrich.HunterFindFromName(context.Background(), "hu-key", &row); err != nil {
		t.Fatalf("HunterFindFromName failed: %v", err)
	}
	if calls != 2 || row.Email != "jane@acme.com" {
		t.Fatalf("expected 1 retry then success, got calls=%d email=%q", calls, row.Email)
	}
}

func TestRateLimitRetriesAreBoundedAndCapRetryAfter(t *testing.T) {
	withFastRetries(t)
	calls := 0
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		resp := jsonHTTPResponse(429, "{}")
		resp.Header.Set("Retry-After", "120") // must be capped by httpx.RetryMaxWait
		return resp, nil
	})

	start := time.Now()
	row := enrich.ContactRecord{FirstName: "Jane", LastName: "Doe", Domain: "acme.com"}
	err := enrich.ApolloEnrichPerson(context.Background(), "ap-key", &row)
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("expected 429 error after retries, got %v", err)
	}
	if want := 1 + httpx.RetryCount; calls != want {
		t.Fatalf("expected %d calls (1 + %d retries), got %d", want, httpx.RetryCount, calls)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Retry-After was not capped (took %s)", elapsed)
	}
}

func TestTransportErrorIsNotRetried(t *testing.T) {
	withFastRetries(t)
	calls := 0
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("connection reset")
	})

	row := enrich.ContactRecord{FirstName: "Jane", LastName: "Doe", Domain: "acme.com"}
	if err := enrich.DropcontactFindFromName(context.Background(), "dc-key", &row); err == nil {
		t.Fatalf("expected transport error")
	}
	if calls != 1 {
		t.Fatalf("transport errors must not be retried (non-idempotent submit), got %d calls", calls)
	}
}

func TestMaskKeyHidesMostOfShortKeys(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"short", "****"},
		{"elevenchars", "****"},
		{"hu-secret-123456", "...3456"},
		{"sk-live-0123456789abcdefWXYZ", "sk-l...WXYZ"},
	}
	for _, tt := range tests {
		if got := keystore.MaskKey(tt.in); got != tt.want {
			t.Errorf("MaskKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSaveConfigIsAtomicAndEnforcesMode(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.json")
	if err := os.WriteFile(realPath, []byte(`{"keys":{}}`), 0644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(dir, "config.json")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatal(err)
	}

	cfg := enrich.NewConfig(map[string]string{enrich.VendorHunter: "hu-key-123"})
	if err := enrich.SaveConfig(linkPath, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	if info, err := os.Lstat(linkPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlinked config was replaced by a regular file (err=%v)", err)
	}
	info, err := os.Stat(realPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected existing 0644 config to be saved as 0600, got %v", info.Mode().Perm())
	}
	reloaded, err := enrich.LoadConfig(linkPath)
	if err != nil || reloaded.Keys[enrich.VendorHunter] != "hu-key-123" {
		t.Fatalf("reload failed: %v %+v", err, reloaded)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".*.tmp-*")); len(leftovers) > 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
}

func TestApolloHashesNormalizedEmail(t *testing.T) {
	var got string
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		got = req.URL.Query().Get("hashed_email")
		return jsonHTTPResponse(200, map[string]any{"person": map[string]any{}}), nil
	})
	defer func() { httpx.Transport = nil }()

	row := enrich.ContactRecord{Email: "Jane.Doe@Acme.com"}
	if err := enrich.ApolloEnrichPerson(context.Background(), "ap-key", &row); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("jane.doe@acme.com"))
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("hashed_email = %q, want sha256 of lowercased email %q", got, want)
	}
}

func TestFindymailSendsBareLinkedInHandle(t *testing.T) {
	var sent string
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body map[string]string
		_ = json.NewDecoder(req.Body).Decode(&body)
		sent = body["linkedin_url"]
		return jsonHTTPResponse(200, map[string]any{"contact": map[string]any{}}), nil
	})
	defer func() { httpx.Transport = nil }()

	row := enrich.ContactRecord{PersonLinkedIn: "https://www.linkedin.com/in/JaneDoe/?trk=abc"}
	if err := enrich.FindymailFindFromLinkedIn(context.Background(), "fm-key", &row); err != nil {
		t.Fatal(err)
	}
	if sent != "janedoe" {
		t.Fatalf("expected bare handle %q, got %q", "janedoe", sent)
	}
}

func TestScrapeStopsReadingHugePages(t *testing.T) {
	const link = `<a href="https://www.linkedin.com/company/acme">LinkedIn</a>`
	var page string
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(page))}, nil
	})
	defer func() { httpx.Transport = nil }()

	page = "<html><body>" + link + "</body></html>"
	if got, err := enrich.ScrapeCompanyLinkedInFromWebsite(context.Background(), "acme.com"); err != nil || got != "https://www.linkedin.com/company/acme" {
		t.Fatalf("expected link from small page, got %q, %v", got, err)
	}

	// A link beyond the read cap is never seen, proving the body is not read in full.
	page = "<html><body>" + strings.Repeat("x", 3<<20) + link + "</body></html>"
	if got, err := enrich.ScrapeCompanyLinkedInFromWebsite(context.Background(), "acme.com"); err != nil || got != "" {
		t.Fatalf("expected no link past the read cap, got %q, %v", got, err)
	}
}

func TestResolveWaterfallVendorsNilConfigHonorsEnv(t *testing.T) {
	t.Setenv("FINDYMAIL_API_KEY", "fm-env-key")
	got, err := enrich.ResolveActiveWaterfallVendors(nil, nil, nil)
	if err != nil || len(got) == 0 || got[0] != enrich.VendorFindymail {
		t.Fatalf("got %v, %v", got, err)
	}
}
