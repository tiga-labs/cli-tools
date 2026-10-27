package enrich

import (
	"context"
	"errors"
	"strings"

	"github.com/tiga-labs/cli-tools/pkg/freeemail"
	"github.com/tiga-labs/cli-tools/pkg/urlutil"
)

// EnrichContact executes the full enrichment lifecycle (pre-processors, waterfall, post-processors)
// for a single ContactRecord using the provided Config and RunOptions.
func EnrichContact(ctx context.Context, cfg *Config, opts RunOptions, row *ContactRecord) error {
	if ctx == nil {
		return errors.New("ctx is nil")
	}
	if row == nil {
		return errors.New("row is nil")
	}
	if cfg == nil {
		cfg = NewConfig(nil)
	}

	row.FirstName = strings.TrimSpace(row.FirstName)
	row.LastName = strings.TrimSpace(row.LastName)
	row.Email = strings.TrimSpace(row.Email)
	row.Phone = strings.TrimSpace(row.Phone)
	row.PersonLinkedIn = strings.TrimSpace(row.PersonLinkedIn)
	row.Title = strings.TrimSpace(row.Title)
	row.CompanyName = strings.TrimSpace(row.CompanyName)
	row.Domain = strings.TrimSpace(row.Domain)
	row.Website = strings.TrimSpace(row.Website)
	row.CompanyLinkedIn = strings.TrimSpace(row.CompanyLinkedIn)

	// errs collects real vendor failures; skipped calls (ErrSkipped) are not failures.
	var errs []error
	record := func(err error) {
		if err != nil && !errors.Is(err, ErrSkipped) {
			errs = append(errs, err)
		}
	}

	// 1. Pre-processor: Resolve LinkedIn URN / Handle via Renidly
	if opts.ResolveURN {
		renidlyKey := cfg.GetKey(VendorRenidly)
		hasURN := row.ExtractRapidURN() != ""
		missingDetails := row.PersonLinkedIn == "" || row.CompanyLinkedIn == "" || row.CompanyName == "" || row.FirstName == "" || row.LastName == ""
		if renidlyKey != "" && (hasURN || (row.PersonLinkedIn != "" && missingDetails)) {
			record(RenidlyEnrichPerson(ctx, renidlyKey, row))
		}
	}

	// 2. Pre-processor: Clean Person & Company Names via OpenAI
	if opts.CleanNames {
		openAIKey := cfg.GetKey(VendorOpenAI)
		if openAIKey != "" {
			if row.FirstName != "" || row.LastName != "" {
				cFirst, cLast, err := CleanFirstAndLastName(ctx, openAIKey, row.FirstName, row.LastName)
				if err != nil {
					record(err)
				} else {
					if cFirst != "" {
						row.FirstName = cFirst
					}
					if cLast != "" {
						row.LastName = cLast
					}
				}
			}
			if row.CompanyName != "" {
				cCompany, err := CleanAccountName(ctx, openAIKey, row.CompanyName)
				if err != nil {
					record(err)
				} else if cCompany != "" {
					row.CompanyName = cCompany
				}
			}
		}
	}

	// Normalize domain from website or email if missing
	populateDomain(row)

	// 3. Pre-processor: Company / Account Research
	if opts.ResearchCompany {
		resolveCompanyDetails(ctx, cfg, row)
		populateDomain(row)
	}

	// 4. Resolve Active Waterfall Providers
	activeVendors, err := ResolveActiveWaterfallVendors(cfg, opts.Vendors, opts.Exclude)
	if err != nil {
		row.Status = "error"
		row.Error = err.Error()
		return err
	}

	// 5. Evaluate initial NeedState (including ZeroBounce check if an email was already present)
	verifiedCache := map[string]string{}
	need := evaluateNeedState(ctx, cfg, opts, row, verifiedCache)

	// 6. Run Waterfall Loop
	if need.ShouldContinue && len(activeVendors) > 0 {
		for _, vendor := range activeVendors {
			select {
			case <-ctx.Done():
				row.Status = "error"
				row.Error = ctx.Err().Error()
				return ctx.Err()
			default:
			}

			apiKey := cfg.GetKey(vendor)
			if apiKey == "" {
				continue
			}

			switch vendor {
			case VendorFindymail:
				if need.StillNeedEmail {
					if row.PersonLinkedIn != "" {
						record(FindymailFindFromLinkedIn(ctx, apiKey, row))
					} else {
						record(FindymailFindFromName(ctx, apiKey, row))
					}
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
					if !need.ShouldContinue {
						break
					}
				}
				if need.StillNeedPhone && row.PersonLinkedIn != "" {
					record(FindymailFindPhoneFromLinkedIn(ctx, apiKey, row))
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
					if !need.ShouldContinue {
						break
					}
				}

			case VendorProspeo:
				if need.StillNeedEmail || need.StillNeedLI {
					record(ProspeoFindPerson(ctx, apiKey, row, need.StillNeedEmail, need.StillNeedLI))
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
					if !need.ShouldContinue {
						break
					}
				}
				if need.StillNeedPhone {
					record(ProspeoFindMobile(ctx, apiKey, row))
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
				}

			case VendorApollo:
				record(ApolloEnrichPerson(ctx, apiKey, row))
				need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
				if !need.ShouldContinue {
					break
				}
				record(ApolloSearchPerson(ctx, apiKey, row))
				need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)

			case VendorLusha:
				if need.StillNeedEmail || need.StillNeedPhone || need.StillNeedLI {
					record(LushaEnrichContact(ctx, apiKey, row, need.StillNeedEmail, need.StillNeedPhone))
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
				}

			case VendorSeamless:
				if need.StillNeedEmail || need.StillNeedPhone || need.StillNeedLI {
					record(SeamlessResearchContact(ctx, apiKey, row))
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
				}

			case VendorDropcontact:
				if need.StillNeedEmail || need.StillNeedPhone || need.StillNeedLI {
					record(DropcontactFindFromName(ctx, apiKey, row))
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
				}

			case VendorHunter:
				if need.StillNeedEmail {
					record(HunterFindFromName(ctx, apiKey, row))
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
				}

			case VendorDatagma:
				if need.StillNeedEmail {
					record(DatagmaFindFromName(ctx, apiKey, row))
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
					if !need.ShouldContinue {
						break
					}
				}
				if need.StillNeedLI {
					record(DatagmaEnrichPerson(ctx, apiKey, row))
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
				}

			case VendorLeadIQ:
				if need.StillNeedEmail || need.StillNeedLI {
					record(LeadIQFindByNameAndCompany(ctx, apiKey, row))
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
				}
			}

			if !need.ShouldContinue {
				break
			}
		}
	}

	// 7. Post-processor: Normalize URLs, domain, and optionally resolve company details from newly found email/company
	populateDomain(row)
	if opts.ResearchCompany && (row.CompanyLinkedIn == "" || row.Website == "") {
		resolveCompanyDetails(ctx, cfg, row)
		populateDomain(row)
	}
	if cleanedLI, err := urlutil.CleanPersonLinkedinUrl(row.PersonLinkedIn); err == nil {
		row.PersonLinkedIn = cleanedLI
	}
	if cleanedCompLI, err := urlutil.CleanCompanyLinkedinUrl(row.CompanyLinkedIn); err == nil {
		row.CompanyLinkedIn = cleanedCompLI
	}
	row.Email = strings.ToLower(strings.TrimSpace(row.Email))
	row.Domain = strings.ToLower(strings.TrimSpace(row.Domain))
	row.Website = strings.ToLower(strings.TrimSpace(row.Website))

	// 8. Finalize status & error
	finalizeStatus(opts, need, errs, row)
	row.Error = cfg.RedactSecrets(row.Error)
	return nil
}

func populateDomain(row *ContactRecord) {
	if row.Website == "" && row.Domain != "" {
		row.Website = row.Domain
	}
	if row.Domain == "" && row.Website != "" {
		if d := urlutil.GetDomainFromWebsite(row.Website); d != "" {
			row.Domain = d
		} else if h, err := urlutil.HostFromWebsite(row.Website); err == nil && h != "" {
			row.Domain = h
		}
	}
	if row.Domain == "" && strings.Contains(row.Email, "@") {
		parts := strings.Split(row.Email, "@")
		if len(parts) == 2 && !freeemail.IsDomainFreeEmail(strings.ToLower(parts[1])) {
			row.Domain = strings.ToLower(strings.TrimSpace(parts[1]))
			if row.Website == "" {
				row.Website = row.Domain
			}
		}
	}
}

func resolveCompanyDetails(ctx context.Context, cfg *Config, row *ContactRecord) {
	// 1. If we have a company LinkedIn URL/slug and a Renidly key, resolve company firmographics/website
	if row.CompanyLinkedIn != "" {
		if renidlyKey := cfg.GetKey(VendorRenidly); renidlyKey != "" {
			_ = RenidlyEnrichCompany(ctx, renidlyKey, row)
		}
	}

	// 2. If we have a domain, try Apollo Org Enrich if company LinkedIn or company name is missing
	if row.Domain != "" && (row.CompanyLinkedIn == "" || row.CompanyName == "") {
		if apolloKey := cfg.GetKey(VendorApollo); apolloKey != "" {
			if orgData, err := ApolloEnrichAccount(ctx, apolloKey, row.Domain); err == nil {
				if row.CompanyLinkedIn == "" && orgData.Organization.LinkedinURL != "" {
					row.CompanyLinkedIn = orgData.Organization.LinkedinURL
					row.CompanyLinkedInSource = VendorApollo
				}
				if row.CompanyName == "" && orgData.Organization.Name != "" {
					row.CompanyName = orgData.Organization.Name
				}
				if row.Industry == "" && orgData.Organization.Industry != "" {
					row.Industry = orgData.Organization.Industry
				}
				if row.EmployeeCount == 0 && orgData.Organization.EstimatedNumEmployees > 0 {
					row.EmployeeCount = orgData.Organization.EstimatedNumEmployees
				}
			}
		}
	}

	// 3. Scrape website homepage for LinkedIn company link if still missing
	websiteOrDomain := firstNonEmpty(row.Website, row.Domain)
	if row.CompanyLinkedIn == "" && websiteOrDomain != "" {
		if liURL, err := ScrapeCompanyLinkedInFromWebsite(ctx, websiteOrDomain); err == nil && liURL != "" {
			row.CompanyLinkedIn = liURL
			row.CompanyLinkedInSource = "website"
		}
	}
}

func evaluateNeedState(ctx context.Context, cfg *Config, opts RunOptions, row *ContactRecord, verifiedCache map[string]string) NeedState {
	populateDomain(row)
	state := NeedState{}

	if opts.NeedEmail {
		stillNeedEmail := true
		if row.HasValidEmailSyntax() {
			emailKey := strings.ToLower(strings.TrimSpace(row.Email))
			zbKey := cfg.GetKey(VendorZeroBounce)
			if opts.VerifyEmail && zbKey != "" {
				// Cache the verdict per address (including API errors) so each email is checked once per row.
				verdict, alreadyChecked := verifiedCache[emailKey]
				if !alreadyChecked {
					valid, err := ZeroBounceValidateEmail(ctx, zbKey, row.Email)
					switch {
					case err != nil:
						verdict = "error"
					case valid:
						verdict = "true"
					default:
						verdict = "false"
					}
					verifiedCache[emailKey] = verdict
				}
				switch verdict {
				case "true":
					stillNeedEmail = false
					row.EmailVerified = "true"
				case "error":
					// A transient ZeroBounce failure keeps the syntax-valid email rather than discarding it.
					stillNeedEmail = false
					row.EmailVerified = "error"
				default:
					row.Email = ""
					row.EmailSource = ""
					row.EmailVerified = "false"
				}
			} else {
				stillNeedEmail = false
				if row.EmailVerified == "" {
					row.EmailVerified = "skipped"
				}
			}
		} else {
			row.Email = ""
			row.EmailSource = ""
		}

		if stillNeedEmail {
			state.StillNeedEmail = true
			state.ShouldContinue = true
		}
	}

	if opts.NeedLinkedIn {
		if cleaned, err := urlutil.CleanPersonLinkedinUrl(row.PersonLinkedIn); err == nil && cleaned != "" {
			row.PersonLinkedIn = cleaned
		} else {
			row.PersonLinkedIn = ""
			row.PersonLinkedInSource = ""
			state.StillNeedLI = true
			state.ShouldContinue = true
		}
	}

	if opts.NeedPhone {
		if !row.HasValidPhone() {
			row.Phone = ""
			row.PhoneSource = ""
			state.StillNeedPhone = true
			state.ShouldContinue = true
		}
	}

	return state
}

func finalizeStatus(opts RunOptions, need NeedState, errs []error, row *ContactRecord) {
	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, err := range errs {
			msgs[i] = err.Error()
		}
		row.Error = strings.Join(msgs, "; ")
	}

	if !need.ShouldContinue {
		row.Status = "enriched"
		return
	}

	// Check if at least one requested target or useful field was found
	foundAny := (opts.NeedEmail && !need.StillNeedEmail) ||
		(opts.NeedLinkedIn && !need.StillNeedLI) ||
		(opts.NeedPhone && !need.StillNeedPhone)

	if foundAny {
		row.Status = "partial"
		return
	}

	if len(errs) > 0 {
		row.Status = "error"
	} else {
		row.Status = "not_found"
	}
}
