---
name: enrich-cli
description: "Run standalone waterfall contact enrichment from the command line using the `enrich` CLI tool. Use for enriching CSV files of contacts or looking up a single person across 9 data vendors (Findymail, Prospeo, Apollo, Lusha, Seamless, Dropcontact, Hunter, Datagma, LeadIQ), resolving LinkedIn or Sales Navigator URNs via Renidly, verifying emails with ZeroBounce, finding mobile phone numbers, cleaning messy person/company names with OpenAI, researching company LinkedIn URLs/domains, and managing vendor API keys (`enrich keys`). Trigger for requests such as 'enrich this CSV', 'find email/phone for this contact with enrich', 'run waterfall enrichment on a file', 'resolve Sales Nav URNs and find emails', or 'configure enrich API keys'."
---

# Enrich CLI Skill

`enrich` is a standalone, scriptable command-line tool that runs multi-vendor waterfall enrichment against CSV files of contacts or single individuals. It cascades through configured data providers in priority order and stops as soon as all requested target fields (`email`, `linkedin`, `phone`) are found.

**CLI basics.** Invoke `enrich` directly from the shell (`enrich person`, `enrich csv`, `enrich keys`). Keys are persisted locally in `~/.config/enrich/config.json` (or overridden via environment variables). Never print, echo, or log raw API keys—always use `enrich keys list` which masks them automatically.

**Related skills:** use **list-building** when discovering net-new accounts/people inside the Tiga platform, **signals** to score lists, and **sequence-runner** to enroll enriched contacts.

---

## Operating principles

1. **Check configured keys first.** Before running any enrichment, run `enrich keys list` (or `enrich keys list --json`) to see which waterfall and feature vendors have active API keys. Never assume a vendor key is present without checking.
2. **Inspect CSV headers before running.** Always inspect the first 5 rows (`head -n 5 input.csv`) to verify column names. `enrich csv` auto-detects common header names, but if a file uses unusual headers (e.g. `Work Mail`, `prospect_li`, `Employer`), pass explicit `--col-*` override flags.
3. **Only enable the target fields and features needed.** By default, each run seeks `--email` and `--linkedin` while keeping `--phone`, `--verify-email`, `--clean-names`, and `--research-company` off. Mobile phone reveals, ZeroBounce verification, and OpenAI name cleaning consume extra credits or latency—only enable them when the user's goal calls for them.
4. **Waterfall stops early.** For each contact, `enrich` evaluates which requested target fields are still missing before calling the next vendor. Once every enabled target field is populated (and verified, if `--verify-email` is on), the waterfall stops immediately for that contact.
5. **Approval gates.** Running `enrich keys list` and inspecting local CSV files are free and never require approval. Before running a paid enrichment against a CSV batch or enabling high-cost flags (`--phone`, `--verify-email`, `--clean-names`, `--resolve-urn`), stop and state:
   - How many rows will be processed
   - Which waterfall vendors will run and in what order
   - Which paid features/targets are enabled (`email`, `linkedin`, `phone`, `verify-email`, `clean-names`, `resolve-urn`, `research-company`)
   Wait for the user's explicit approval before executing the batch run.
6. **Always report found AND not-found.** After any CSV run, inspect the output file and report total rows processed, breakdown by `enrich_status` (`enriched`, `partial`, `not_found`, `error`), and attribution counts by `email_source`, `phone_source`, and `person_linkedin_source`.

---

## Vendors & capabilities

### Waterfall contact vendors (default execution order)

When `--vendors` is not passed, `enrich` automatically runs every waterfall vendor that has a configured key in this default priority order:

| Order | Vendor ID | Finds Email | Finds Phone | Finds LinkedIn URL | Best Input Identifiers |
|---|---|---|---|---|---|
| 1 | `findymail` | Yes | Yes (needs LinkedIn URL) | No | LinkedIn URL, or First + Last + Domain/Company |
| 2 | `prospeo` | Yes (verified) | Yes (needs LinkedIn URL) | Yes | First + Last + Domain/Company, or Email, or LinkedIn URL |
| 3 | `apollo` | Yes | Sync match only | Yes (+ Company info) | Name + Company/Domain, Email, or LinkedIn URL |
| 4 | `lusha` | Yes | Yes (mobile preferred) | Yes | LinkedIn URL, Email, or First + Last + Company/Domain |
| 5 | `seamless` | Yes (async poll) | Yes (mobile preferred) | Yes | LinkedIn URL, or Name + Company/Domain |
| 6 | `dropcontact` | Yes (async poll) | Yes | Yes (+ Company LI) | First + Last + Company/Website |
| 7 | `hunter` | Yes (score ≥ 50 / valid) | No | No | First + Last + Domain/Company |
| 8 | `datagma` | Yes (`VALID` status) | No | Yes | First + Last + Company/Domain |
| 9 | `leadiq` | Yes | No | Yes | First + Last + Company/Domain |

### Feature vendors (pre/post-processing)

| Vendor ID | Flag | Purpose |
|---|---|---|
| `renidly` | `--resolve-urn` (default: on when URN & key present), `--research-company` | Resolves LinkedIn profile URNs (`urn:li:fs_salesProfile:...`, `ACoA...`), public handles, and company slugs via `https://renidly.com` (`X-renidly-apikey`). |
| `zerobounce` | `--verify-email` | Validates discovered emails via ZeroBounce. If an email is invalid, `enrich` discards it and continues the waterfall to the next vendor. |
| `openai` | `--clean-names` | Uses `gpt-5.4-mini` to strip credentials (`MD`, `PhD`, `CPA`), middle initials, emojis, and legal suffixes (`Inc`, `LLC`, `Corp`) before querying waterfall vendors. |

---

## Choosing a workflow

| You have | Goal | Workflow |
|---|---|---|
| API keys to configure, remove, or audit | Manage vendor keys | **Workflow 1** |
| One person's name + company/domain (or LinkedIn URL) | Quick single-contact lookup | **Workflow 2** |
| A standard CSV of prospects (names + companies/domains) | Bulk waterfall email & LinkedIn enrichment | **Workflow 3** |
| A messy conference/scraped CSV with custom headers & dirty names | Clean names + custom column mapping + verified emails | **Workflow 4** |
| A Sales Navigator / LinkedIn URN export (`urn:li:fs_salesProfile:...`) | Resolve URNs via Renidly → waterfall enrich email/phone | **Workflow 5** |
| A cold-calling list needing direct dials / mobile numbers | Mobile phone waterfall | **Workflow 6** |
| Existing emails or LinkedIn URLs missing profile or company data | Reverse / cross-field lookup + company research | **Workflow 7** |
| Shell pipeline / `jq` / automated filtering | Stdin/stdout streaming & post-run reporting | **Workflow 8** |

---

## Workflow 1: Manage Vendor API Keys (`enrich keys`)

Keys are stored in `~/.config/enrich/config.json` (with `0600` permissions). Environment variables (`FINDYMAIL_API_KEY`, `PROSPEO_API_KEY`, `APOLLO_API_KEY`, `LUSHA_API_KEY`, `SEAMLESS_API_KEY`, `DROPCONTACT_API_KEY`, `HUNTER_API_KEY`, `DATAGMA_API_KEY`, `LEADIQ_API_KEY`, `RENIDLY_API_KEY`, `ZEROBOUNCE_API_KEY`, `OPENAI_API_KEY`) automatically take precedence over the config file.

1. **Audit configured keys** (safe—keys are masked):
   ```bash
   enrich keys list
   ```
   Or inspect programmatically as JSON:
   ```bash
   enrich keys list --json
   ```

2. **Add or update vendor keys:**
   ```bash
   enrich keys set findymail "fm_live_..."
   enrich keys set prospeo "pr_live_..."
   enrich keys set apollo "ap_live_..."
   enrich keys set lusha "lu_live_..."
   enrich keys set renidly "rnd-..."
   enrich keys set zerobounce "zb_..."
   enrich keys set openai "sk-..."
   ```

3. **Remove a vendor key** so it no longer runs by default:
   ```bash
   enrich keys rm datagma
   ```

---

## Workflow 2: Single Contact Lookup (`enrich person`)

Use `enrich person` when the user asks to look up one specific person in chat without creating a CSV.

### Use Case 2A: Find work email and LinkedIn URL from Name + Domain
```bash
enrich person \
  --first-name "Patrick" \
  --last-name "Collison" \
  --company "Stripe" \
  --domain "stripe.com" \
  --format table
```

### Use Case 2B: JSON output with specific vendor order & ZeroBounce verification
When a script or agent needs structured JSON and wants to restrict the waterfall to specific vendors with strict deliverability verification:
```bash
enrich person \
  --first-name "Jane" \
  --last-name "Doe" \
  --company "Acme Corp" \
  --domain "acme.com" \
  --verify-email \
  --vendors findymail,prospeo,hunter \
  --format json
```
Response shape:
```json
{
  "first_name": "Jane",
  "last_name": "Doe",
  "email": "jane.doe@acme.com",
  "phone": "",
  "person_linkedin": "https://www.linkedin.com/in/janedoe",
  "title": "VP of Sales",
  "company_name": "Acme Corp",
  "domain": "acme.com",
  "website": "acme.com",
  "company_linkedin": "https://www.linkedin.com/company/acme",
  "email_source": "findymail",
  "person_linkedin_source": "prospeo",
  "email_verified": "true",
  "status": "enriched"
}
```

### Use Case 2C: Full-Name splitting + Mobile Phone lookup
If the user provides an unsplit full name and wants both email and mobile phone:
```bash
enrich person \
  --name "Satya Nadella" \
  --domain "microsoft.com" \
  --email \
  --phone \
  --linkedin
```

---

## Workflow 3: Standard CSV Batch Waterfall (`enrich csv`)

Use `enrich csv` for standard prospect lists. It preserves **100% of the original CSV columns and row ordering**, appending 16 enrichment and provenance columns at the end of each row:
`enriched_first_name`, `enriched_last_name`, `enriched_email`, `email_source`, `email_verified`, `enriched_phone`, `phone_source`, `enriched_person_linkedin`, `person_linkedin_source`, `enriched_title`, `enriched_company_name`, `enriched_domain`, `enriched_company_linkedin`, `company_linkedin_source`, `enrich_status`, `enrich_error`.

The `enrich_error` column lists only real vendor failures, such as auth, credits, non-2xx responses or timeouts. Several failures are separated by `; `. A vendor that was skipped because the row lacked the inputs it needs is not an error. A vendor that found no match is not an error either. When every vendor was skipped or found nothing, the row's `enrich_status` is `not_found`. A row can be `enriched` or `partial` and still have a non-empty `enrich_error` from a vendor that failed before another one succeeded.

1. **Inspect the input file first:**
   ```bash
   head -n 5 prospects.csv
   wc -l prospects.csv
   ```
   Auto-detected headers (case-insensitive, spaces/hyphens/underscores normalized):
   - **First Name:** `first_name`, `firstname`, `first`, `given_name`
   - **Last Name:** `last_name`, `lastname`, `last`, `surname`, `family_name`
   - **Full Name (auto-split if first/last absent):** `full_name`, `fullname`, `name`, `contact_name`, `person_name`
   - **Email:** `email`, `email_address`, `work_email`, `business_email`, `e_mail`
   - **Phone:** `phone`, `phone_number`, `mobile`, `mobile_phone`, `direct_phone`, `telephone`
   - **Person LinkedIn:** `person_linkedin`, `linkedin`, `linkedin_url`, `person_linkedin_url`, `li_url`, `profile_url`, `linkedin_profile`, `linkedin_profile_url`
   - **LinkedIn URN:** `linkedin_urn`, `linkedin_profile_urn`, `urn`, `sales_nav_urn`, `entity_id`, `entityid`
   - **Company:** `company`, `company_name`, `account`, `account_name`, `organization`, `organization_name`, `employer`
   - **Domain:** `domain`, `company_domain`, `account_domain`, `primary_domain`
   - **Website:** `website`, `company_website`, `account_website`, `website_url`, `company_url`
   - **Company LinkedIn:** `company_linkedin`, `company_linkedin_url`, `account_linkedin`, `account_linkedin_url`, `organization_linkedin_url`
   - **Title:** `title`, `job_title`, `position`, `role`, `headline`

2. **After user approval, run the batch waterfall:**
   ```bash
   enrich csv \
     -i prospects.csv \
     -o prospects_enriched.csv \
     --concurrency 10 \
     --email \
     --linkedin \
     --exclude-vendors seamless,dropcontact
   ```
   *Tip:* Excluding async-polling vendors (`seamless`, `dropcontact`) speeds up large batches when fast synchronous vendors (`findymail`, `prospeo`, `apollo`, `lusha`, `hunter`) are sufficient.

---

## Workflow 4: Messy Conference / Scraped Lists (`--clean-names` + `--col-*` + `--verify-email`)

Conference attendee exports, scraped directories, and webinar signups often have:
- Non-standard column headers (e.g. `Speaker Name`, `Organization / Hospital`, `Corp URL`)
- Dirty names with credentials, titles, or emojis (`"👨‍⚕️ Dr. JOHN F. SMITH, MD, PhD"`)
- Legal entity suffixes that hurt vendor match rates (`"THE NEW YORK PRESBYTERIAN HOSPITAL INC."`)

Combine **explicit column mapping**, **OpenAI name cleaning**, and **ZeroBounce verification** in one command:

```bash
enrich csv \
  -i conference_attendees.csv \
  -o conference_enriched.csv \
  --col-name "Speaker Name" \
  --col-company "Organization / Hospital" \
  --col-website "Corp URL" \
  --col-title "Role" \
  --clean-names \
  --research-company \
  --verify-email \
  --concurrency 5
```

**What happens per row:**
1. `Speaker Name` is split into first and last name.
2. `--clean-names` calls OpenAI (`gpt-5.4-mini`) to clean `"👨‍⚕️ Dr. JOHN F. SMITH, MD, PhD"` → `"John"` `"Smith"` and `"THE NEW YORK PRESBYTERIAN HOSPITAL INC."` → `"New York Presbyterian Hospital"`.
3. `--research-company` resolves the company's domain and LinkedIn URL via Renidly, Apollo, or homepage link scraping.
4. Each waterfall vendor searches using the cleaned name and domain.
5. `--verify-email` checks any discovered email with ZeroBounce; if a vendor returns a dead/invalid address, `enrich` discards it and automatically continues to the next vendor in the waterfall.

---

## Workflow 5: Sales Navigator / LinkedIn URN Exports (`--resolve-urn` with Renidly)

Exports from LinkedIn Sales Navigator or scraping tools often contain only a raw profile URN (e.g. `urn:li:fs_salesProfile:(ACoAAAAKXBwBikfbNJww68eYvcu2dqDYJhHbp4g,NAME_SEARCH,xyz)` or `ACoAAAA...`) without a public `/in/` handle, company name, or domain.

Requires the `renidly` API key (`enrich keys set renidly <key>`).

```bash
enrich csv \
  -i sales_nav_export.csv \
  -o sales_nav_enriched.csv \
  --col-urn "Sales Nav URN" \
  --resolve-urn \
  --research-company \
  --email \
  --linkedin \
  --verify-email \
  --concurrency 5
```

**What happens per row:**
1. `enrich` extracts the raw token (`ACoAAAAKXBwBikfbNJww68eYvcu2dqDYJhHbp4g`) from the Sales Navigator URN.
2. `--resolve-urn` calls the Renidly API (`/api/v2/person/enrich` and `/api/v2/person/employment-history`) to resolve the person's `first_name`, `last_name`, `title`, public `https://www.linkedin.com/in/<handle>`, `company_name`, and `company_linkedin`.
3. `--research-company` resolves the company's website/domain from the company LinkedIn slug via Renidly (`/api/data/v1/companies/company` / `/api/v2/organization/enrich`) and Apollo.
4. With the public LinkedIn URL, full name, and company domain now populated, the waterfall vendors (`findymail`, `prospeo`, `apollo`, etc.) find and verify the person's work email.

---

## Workflow 6: Cold Calling / Mobile Phone Waterfall (`--phone`)

When building a call blitz list where direct dials / mobile numbers are the primary objective:

- Phone enrichment is **off by default** because vendors charge higher credit rates for mobile reveals. Pass `--phone` to enable it.
- Only 5 waterfall vendors return phone numbers in the stateless CLI (`lusha`, `prospeo`, `findymail`, `seamless`, `dropcontact`). Note that `prospeo` and `findymail` require a `person_linkedin` URL to look up a phone number—so keep `--linkedin` enabled (or place a LinkedIn-finding vendor like `prospeo` or `apollo` first) if your input CSV lacks LinkedIn URLs.

### Use Case 6A: Mobile phones only (when CSV already has LinkedIn URLs)
```bash
enrich csv \
  -i linkedin_leads.csv \
  -o call_list.csv \
  --phone \
  --no-email \
  --no-linkedin \
  --vendors lusha,prospeo,findymail,seamless,dropcontact \
  --concurrency 5
```

### Use Case 6B: Full omni-channel enrichment (Verified Email + LinkedIn URL + Mobile Phone)
```bash
enrich csv \
  -i target_executives.csv \
  -o executives_omnichannel.csv \
  --email \
  --linkedin \
  --phone \
  --verify-email \
  --vendors prospeo,findymail,lusha,apollo,seamless \
  --concurrency 5
```

---

## Workflow 7: Reverse & Cross-Field Enrichment

### Use Case 7A: LinkedIn URLs only → Find Verified Work Emails
When the input CSV only has a column of LinkedIn profile URLs (`https://www.linkedin.com/in/...`):
```bash
enrich csv \
  -i linkedin_urls_only.csv \
  -o linkedin_with_emails.csv \
  --resolve-urn \
  --research-company \
  --email \
  --no-linkedin \
  --verify-email
```
*(When `renidly` is configured, `--resolve-urn` also hydrates missing first/last names, job titles, and current company from the LinkedIn handle before the email vendors run.)*

### Use Case 7B: Work Emails + Names → Find Person & Company LinkedIn URLs
When you have inbound signup emails (`first_name`, `last_name`, `email`) and only need LinkedIn URLs and company firmographics without spending email-finder credits:
```bash
enrich csv \
  -i inbound_signups.csv \
  -o signups_with_linkedin.csv \
  --no-email \
  --linkedin \
  --research-company \
  --vendors prospeo,apollo,lusha,datagma,leadiq
```

---

## Workflow 8: Unix Pipelines & Post-Run Reporting

`enrich csv` reads from `stdin` when `-i` is omitted (or `-`) and writes CSV to `stdout` when `-o` is omitted (or `-`). Progress logs are always written to `stderr` (and can be silenced with `--quiet`), making `enrich` safe for shell pipelines.

### Use Case 8A: Enrich only a subset of rows via pipe
Enrich only the first 20 rows (or rows matching a filter) without creating intermediate input files:
```bash
head -n 21 large_list.csv | enrich csv --quiet --vendors findymail,prospeo > sample_20_enriched.csv
```

### Use Case 8B: Post-run summary report script
Always run a quick summary after completing a CSV batch to report found vs. not-found counts and vendor attribution to the user:
```bash
python3 -c '
import csv, collections, sys

with open("prospects_enriched.csv", newline="", encoding="utf-8") as f:
    rows = list(csv.DictReader(f))

status_counts = collections.Counter(r["enrich_status"] for r in rows)
email_sources = collections.Counter(r["email_source"] for r in rows if r["enriched_email"])
phone_sources = collections.Counter(r["phone_source"] for r in rows if r["enriched_phone"])

print(f"Total rows: {len(rows)}")
print("Status breakdown:", dict(status_counts))
print("Emails found by vendor:", dict(email_sources))
print("Phones found by vendor:", dict(phone_sources))

not_found = [f"{r.get(\"enriched_first_name\",\"\")} {r.get(\"enriched_last_name\",\"\")} ({r.get(\"enriched_company_name\",\"\")})".strip()
             for r in rows if r["enrich_status"] in ("not_found", "error")]
if not_found:
    print(f"\nUnresolved contacts ({len(not_found)}):")
    for item in not_found[:25]:
        print(" -", item)
'
```

---

## Complete CLI Flag Reference

### `enrich person` & `enrich csv` shared flags
| Flag | Default | Description |
|---|---|---|
| `--email` / `--no-email` | `true` | Seek work email address in the waterfall |
| `--linkedin` / `--no-linkedin` | `true` | Seek person LinkedIn profile URL in the waterfall |
| `--phone` / `--no-phone` | `false` | Seek phone / mobile number in the waterfall |
| `--vendors <list>` | *(all configured)* | Comma-separated waterfall vendor order (e.g. `findymail,prospeo,apollo`) |
| `--exclude-vendors <list>` | `""` | Comma-separated waterfall vendors to skip for this run |
| `--resolve-urn` / `--no-resolve-urn` | `true` | Resolve LinkedIn URN / handle via Renidly (`renidly` key) |
| `--clean-names` / `--no-clean-names` | `false` | Clean person & company names via OpenAI (`openai` key) |
| `--research-company` / `--no-research-company` | `false` | Resolve company LinkedIn/website via Renidly, Apollo, and website scraping |
| `--verify-email` / `--no-verify-email` | `false` | Validate discovered emails via ZeroBounce (`zerobounce` key) |
| `--config <path>` | `~/.config/enrich/config.json` | Custom path to key configuration file |

### `enrich person` specific flags
| Flag | Description |
|---|---|
| `--first-name <str>` | Contact's first name |
| `--last-name <str>` | Contact's last name |
| `--name <str>` | Contact's full name (auto-split if `--first-name`/`--last-name` omitted) |
| `--company <str>` | Company / account name |
| `--domain <str>` | Company domain (e.g. `acme.com`) |
| `--website <str>` | Company website URL |
| `--email-address <str>` | Existing email address |
| `--phone-number <str>` | Existing phone number |
| `--linkedin-url <str>` | Existing person LinkedIn URL |
| `--company-linkedin <str>` | Existing company LinkedIn URL |
| `--urn <str>` | LinkedIn profile URN or Renidly `entityId` |
| `--title <str>` | Job title |
| `--format <fmt>` | Output format: `json` (default), `table`, or `csv` |

### `enrich csv` specific flags
| Flag | Description |
|---|---|
| `-i`, `--input <path>` | Input CSV file path (or `-` / omit for `stdin`; also accepts first positional arg) |
| `-o`, `--output <path>` | Output CSV file path (or `-` / omit for `stdout`) |
| `--concurrency <int>` | Number of parallel enrichment workers (default: `5`) |
| `--quiet` | Suppress per-row progress logs on `stderr` |
| `--col-first-name <hdr>` | Override CSV column header for First Name |
| `--col-last-name <hdr>` | Override CSV column header for Last Name |
| `--col-name <hdr>` | Override CSV column header for Full Name |
| `--col-email <hdr>` | Override CSV column header for Email |
| `--col-phone <hdr>` | Override CSV column header for Phone |
| `--col-linkedin <hdr>` | Override CSV column header for Person LinkedIn URL |
| `--col-urn <hdr>` | Override CSV column header for LinkedIn URN / Entity ID |
| `--col-company <hdr>` | Override CSV column header for Company Name |
| `--col-domain <hdr>` | Override CSV column header for Company Domain |
| `--col-website <hdr>` | Override CSV column header for Company Website |
| `--col-company-linkedin <hdr>` | Override CSV column header for Company LinkedIn URL |
| `--col-title <hdr>` | Override CSV column header for Job Title |
