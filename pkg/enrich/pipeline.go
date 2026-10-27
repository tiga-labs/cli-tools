package enrich

import (
	"context"
	"errors"
	"github.com/tiga-labs/cli-tools/pkg/freeemail"
	"github.com/tiga-labs/cli-tools/pkg/urlutil"
	"strings"
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
		cfg = &Config{Keys: make(map[string]string)}
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

	var lastErr error

	// 1. Pre-processor: Resolve LinkedIn URN / Handle via Renidly
	if opts.ResolveURN {
		renidlyKey := cfg.GetKey(VendorRenidly)
		hasURN := row.ExtractRapidURN() != ""
		missingDetails := row.PersonLinkedIn == "" || row.CompanyLinkedIn == "" || row.CompanyName == "" || row.FirstName == "" || row.LastName == ""
		if renidlyKey != "" && (hasURN || (row.PersonLinkedIn != "" && missingDetails)) {
			if err := RenidlyEnrichPerson(ctx, renidlyKey, row); err != nil {
				lastErr = err
			}
		}
	}

	// 2. Pre-processor: Clean Person & Company Names via OpenAI
	if opts.CleanNames {
		openAIKey := cfg.GetKey(VendorOpenAI)
		if openAIKey != "" {
			if row.FirstName != "" || row.LastName != "" {
				cFirst, cLast, err := CleanFirstAndLastName(ctx, openAIKey, row.FirstName, row.LastName)
				if err != nil {
					lastErr = err
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
					lastErr = err
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
	activeVendors, err := cfg.ResolveActiveWaterfallVendors(opts.Vendors, opts.Exclude)
	if err != nil {
		row.Status = "error"
		row.Error = err.Error()
		return err
	}

	// 5. Evaluate initial NeedState (including ZeroBounce check if an email was already present)
	verifiedCache := map[string]bool{}
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
						if err := FindymailFindFromLinkedIn(ctx, apiKey, row); err != nil {
							lastErr = err
						}
					} else {
						if err := FindymailFindFromName(ctx, apiKey, row); err != nil {
							lastErr = err
						}
					}
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
					if !need.ShouldContinue {
						break
					}
				}
				if need.StillNeedPhone && row.PersonLinkedIn != "" {
					if err := FindymailFindPhoneFromLinkedIn(ctx, apiKey, row); err != nil {
						lastErr = err
					}
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
					if !need.ShouldContinue {
						break
					}
				}

			case VendorProspeo:
				if need.StillNeedLI {
					if err := ProspeoFindLinkedInURL(ctx, apiKey, row); err != nil {
						lastErr = err
					}
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
					if !need.ShouldContinue {
						break
					}
				}
				if need.StillNeedEmail {
					if err := ProspeoFindFromName(ctx, apiKey, row); err != nil {
						lastErr = err
					}
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
					if !need.ShouldContinue {
						break
					}
				}
				if need.StillNeedPhone {
					if err := ProspeoFindMobile(ctx, apiKey, row); err != nil {
						lastErr = err
					}
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
					if !need.ShouldContinue {
						break
					}
				}

			case VendorApollo:
				if err := ApolloEnrichPerson(ctx, apiKey, row, 0); err != nil {
					lastErr = err
				}
				need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
				if !need.ShouldContinue {
					break
				}
				if err := ApolloSearchPerson(ctx, apiKey, row, 0); err != nil {
					lastErr = err
				}
				need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)

			case VendorLusha:
				if need.StillNeedEmail || need.StillNeedPhone || need.StillNeedLI {
					if err := LushaEnrichContact(ctx, apiKey, row, need.StillNeedEmail, need.StillNeedPhone); err != nil {
						lastErr = err
					}
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
				}

			case VendorSeamless:
				if need.StillNeedEmail || need.StillNeedPhone || need.StillNeedLI {
					if err := SeamlessResearchContact(ctx, apiKey, row); err != nil {
						lastErr = err
					}
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
				}

			case VendorDropcontact:
				if need.StillNeedEmail || need.StillNeedPhone || need.StillNeedLI {
					if err := DropcontactFindFromName(ctx, apiKey, row); err != nil {
						lastErr = err
					}
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
				}

			case VendorHunter:
				if need.StillNeedEmail {
					if err := HunterFindFromName(ctx, apiKey, row); err != nil {
						lastErr = err
					}
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
				}

			case VendorDatagma:
				if need.StillNeedEmail {
					if err := DatagmaFindFromName(ctx, apiKey, row); err != nil {
						lastErr = err
					}
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
					if !need.ShouldContinue {
						break
					}
				}
				if need.StillNeedLI {
					if err := DatagmaEnrichPerson(ctx, apiKey, row); err != nil {
						lastErr = err
					}
					need = evaluateNeedState(ctx, cfg, opts, row, verifiedCache)
				}

			case VendorLeadIQ:
				if need.StillNeedEmail || need.StillNeedLI {
					if err := LeadIQFindByNameAndCompany(ctx, apiKey, row); err != nil {
						lastErr = err
					}
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
	finalizeStatus(opts, need, lastErr, row)
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

func evaluateNeedState(ctx context.Context, cfg *Config, opts RunOptions, row *ContactRecord, verifiedCache map[string]bool) NeedState {
	populateDomain(row)
	state := NeedState{}

	if opts.NeedEmail {
		stillNeedEmail := true
		if row.HasValidEmailSyntax() {
			emailKey := strings.ToLower(strings.TrimSpace(row.Email))
			zbKey := cfg.GetKey(VendorZeroBounce)
			if opts.VerifyEmail && zbKey != "" {
				if valid, alreadyChecked := verifiedCache[emailKey]; alreadyChecked {
					if valid {
						stillNeedEmail = false
						row.EmailVerified = "true"
					} else {
						row.Email = ""
						row.EmailSource = ""
						row.EmailVerified = "false"
					}
				} else {
					valid, err := ZeroBounceValidateEmail(ctx, zbKey, row.Email)
					if err != nil {
						// If ZeroBounce errors transiently, keep the syntax-valid email rather than discarding
						stillNeedEmail = false
						row.EmailVerified = "error"
					} else {
						verifiedCache[emailKey] = valid
						if valid {
							stillNeedEmail = false
							row.EmailVerified = "true"
						} else {
							row.Email = ""
							row.EmailSource = ""
							row.EmailVerified = "false"
						}
					}
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

func finalizeStatus(opts RunOptions, need NeedState, lastErr error, row *ContactRecord) {
	if lastErr != nil {
		row.Error = lastErr.Error()
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

	if lastErr != nil {
		row.Status = "error"
	} else {
		row.Status = "not_found"
	}
}
