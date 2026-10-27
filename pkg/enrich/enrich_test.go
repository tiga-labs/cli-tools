package enrich_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"github.com/tiga-labs/cli-tools/pkg/enrich"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	cfg := &enrich.Config{
		Keys: map[string]string{
			enrich.VendorFindymail: "fm-key",
			enrich.VendorProspeo:   "pr-key",
			enrich.VendorLusha:     "lu-key",
		},
	}

	var calledHosts []string
	enrich.ProviderTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
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
	defer func() { enrich.ProviderTransport = nil }()

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
	cfg := &enrich.Config{
		Keys: map[string]string{
			enrich.VendorRenidly:    "rnd-key",
			enrich.VendorZeroBounce: "zb-key",
			enrich.VendorFindymail:  "fm-key",
			enrich.VendorHunter:     "hu-key",
		},
	}

	enrich.ProviderTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
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
	defer func() { enrich.ProviderTransport = nil }()

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
	cfg := &enrich.Config{
		Keys: map[string]string{
			enrich.VendorFindymail: "fm-key",
		},
	}

	enrich.ProviderTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body map[string]string
		_ = json.NewDecoder(req.Body).Decode(&body)
		name := strings.ToLower(body["name"])
		domain := body["domain"]
		email := strings.ReplaceAll(name, " ", ".") + "@" + domain
		return jsonHTTPResponse(200, map[string]any{
			"contact": map[string]any{"email": email},
		}), nil
	})
	defer func() { enrich.ProviderTransport = nil }()

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

	cfg := &enrich.Config{
		Keys: map[string]string{
			enrich.VendorOpenAI:      "oa-key",
			enrich.VendorApollo:      "ap-key",
			enrich.VendorLusha:       "lu-key",
			enrich.VendorSeamless:    "sm-key",
			enrich.VendorDropcontact: "dc-key",
			enrich.VendorDatagma:     "dg-key",
			enrich.VendorLeadIQ:      "lq-key",
		},
	}

	enrich.ProviderTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
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
	defer func() { enrich.ProviderTransport = nil }()

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
	enrich.ProviderTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection refused")
	})
	defer func() { enrich.ProviderTransport = nil }()

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
	cfg := &enrich.Config{Keys: map[string]string{enrich.VendorHunter: secret}}
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
	enrich.ProviderTimeout = 50 * time.Millisecond
	enrich.ProviderTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done() // simulate a vendor that never responds
		return nil, req.Context().Err()
	})
	defer func() {
		enrich.ProviderTransport = nil
		enrich.ProviderTimeout = 60 * time.Second
	}()

	cfg := &enrich.Config{Keys: map[string]string{enrich.VendorFindymail: "fm-key"}}
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
		t.Fatalf("stalled request was not bounded by ProviderTimeout (took %s)", elapsed)
	}
	if row.Status != "error" || row.Error == "" {
		t.Fatalf("expected timeout error on row, got status=%q error=%q", row.Status, row.Error)
	}
}

func TestProcessCSVStreamsInOrderAndReportsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	enrich.ProviderTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body map[string]string
		_ = json.NewDecoder(req.Body).Decode(&body)
		if body["name"] == "Bob Two" {
			cancel() // simulate Ctrl-C while row 2 is in flight
		}
		return jsonHTTPResponse(200, map[string]any{
			"contact": map[string]any{"email": strings.ReplaceAll(strings.ToLower(body["name"]), " ", ".") + "@acme.com"},
		}), nil
	})
	defer func() { enrich.ProviderTransport = nil }()

	inputCSV := "ID,Full Name,Domain\n1,Alice One,acme.com\n2,Bob Two,acme.com\n3,Cara Three,acme.com\n4,Dan Four,acme.com\n"
	cfg := &enrich.Config{Keys: map[string]string{enrich.VendorFindymail: "fm-key"}}
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
