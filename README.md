# enrich

**Find work emails, LinkedIn profiles, and phone numbers for your contacts, from the command line.**

You give `enrich` a person or a CSV of contacts. It asks your data vendors, such as Findymail, Prospeo and Apollo, one at a time. It stops as soon as it has what you asked for, so later vendors are not called and you spend credits only on what you need. Each found value records which vendor supplied it.

- Works on one contact (`enrich person`) or a whole CSV (`enrich csv`). The CSV keeps all your original columns and row order.
- Supports 9 contact-data vendors, plus email verification (ZeroBounce), LinkedIn URN resolution (Renidly) and AI name cleanup (OpenAI).
- A single binary for macOS and Linux, with nothing else to install.

---

## Install

```bash
curl -fsSL https://github.com/tiga-labs/cli-tools/releases/latest/download/install.sh | sh
```

The installer downloads the right binary for your machine and checks its SHA-256 checksum before installing it to `/usr/local/bin`. If it can't write to that directory, it asks for `sudo`.

Options:

```bash
# Install to a directory you own (no sudo), or pin a specific version
curl -fsSL https://github.com/tiga-labs/cli-tools/releases/latest/download/install.sh \
  | INSTALL_DIR="$HOME/.local/bin" VERSION=v0.1.0 sh
```

You can also install it these ways:
- **Manual download:** get `enrich-<os>-<arch>` from the [Releases page](https://github.com/tiga-labs/cli-tools/releases), `chmod +x` it, and put it on your `PATH`. Check it against `checksums.txt` on the same page.
- **With Go:** `go install github.com/tiga-labs/cli-tools/cmd/enrich@latest`

Supported platforms are macOS and Linux, on Intel/AMD (`amd64`) and ARM (`arm64`). To check the install:

```bash
enrich --version
```

---

## Quick start

### 1. Add an API key for at least one vendor

```bash
enrich keys set hunter -        # paste your key, then press Enter
enrich keys list                # see which vendors are configured (keys are masked)
```

The `-` makes `enrich` read the key from stdin, which keeps it out of your shell history. You can also run `enrich keys set hunter <key>`, or use environment variables (see [API keys](#api-keys-and-configuration)).

### 2. Enrich one person

```bash
enrich person --name "Jane Doe" --domain acme.com --format table
```

```text
First Name:         Jane
Last Name:          Doe
Email:              jane.doe@acme.com
Email Source:       hunter
Person LinkedIn:    https://www.linkedin.com/in/janedoe
Person LI Source:   prospeo
Domain:             acme.com
Status:             enriched
...
```

*(Example output. Results depend on your vendors and the contact.)*

You can identify a person in several ways: `--name` or `--first-name`/`--last-name`, plus a company (`--domain`, `--company`, `--website`), or just `--linkedin-url`, `--email-address` or `--urn`. Use `--format json` (the default), `table` or `csv`.

### 3. Enrich a CSV

```csv
First Name,Last Name,Company,Website
Jane,Doe,Acme,acme.com
John,Smith,Globex,globex.com
```

```bash
enrich csv -i contacts.csv -o enriched.csv
```

The output keeps every original column and row, in the same order, and adds enrichment columns at the end. Progress is printed to stderr as each row finishes. Add `--quiet` to hide it.

---

## Supported vendors

`enrich` uses only the vendors you've added keys for. By default it tries them in this order:

| # | Vendor | Finds email | Finds phone | Finds LinkedIn URL |
|---|---|:-:|:-:|:-:|
| 1 | `findymail` | ✓ | ✓ (needs LinkedIn URL) | |
| 2 | `prospeo` | ✓ (verified only) | ✓ (needs LinkedIn URL) | ✓ |
| 3 | `apollo` | ✓ | ✓ | ✓ |
| 4 | `lusha` | ✓ | ✓ | ✓ |
| 5 | `seamless` | ✓ | ✓ | ✓ |
| 6 | `dropcontact` | ✓ | ✓ | ✓ |
| 7 | `hunter` | ✓ | | |
| 8 | `datagma` | ✓ | | ✓ |
| 9 | `leadiq` | ✓ | | ✓ |

Feature vendors add extra steps:

| Vendor | Flag | What it does |
|---|---|---|
| `renidly` | `--resolve-urn` (on by default when a key is set) | Turns LinkedIn / Sales Navigator URNs and profile URLs into names, titles, and companies |
| `zerobounce` | `--verify-email` | Checks each found email; an undeliverable email is discarded and the next vendor is tried |
| `openai` | `--clean-names` | Cleans messy names before lookup (`DR. JANE DOE, PhD` → `Jane Doe`, `ACME INC` → `Acme`) |

> **Cost:** each vendor bills its own credits to your account. Phone lookups (`--phone`), email verification (`--verify-email`) and name cleaning (`--clean-names`) add extra calls. Try a few rows before running a large file.

---

## Common options

These work with both `enrich person` and `enrich csv`:

| Flag | Default | Meaning |
|---|---|---|
| `--email` / `--no-email` | on | Find a work email |
| `--linkedin` / `--no-linkedin` | on | Find the person's LinkedIn URL |
| `--phone` | off | Find a phone number (usually costs more) |
| `--vendors findymail,hunter` | all configured | Use only these vendors, in this order |
| `--exclude-vendors apollo` | none | Skip these vendors for this run |
| `--verify-email` | off | Verify emails with ZeroBounce |
| `--clean-names` | off | Clean names with OpenAI first |
| `--research-company` | off | Also look up the company's LinkedIn page and website |
| `--resolve-urn` / `--no-resolve-urn` | on | Resolve LinkedIn URNs via Renidly (needs a Renidly key) |
| `--concurrency 5` | 5 | CSV only: how many rows to process at once |
| `--quiet` | off | CSV only: hide progress output |

Run `enrich person --help` or `enrich csv --help` for the full list.

---

## Working with CSVs

**Column detection.** `enrich` recognizes common header names automatically. Matching ignores case, spaces, hyphens and underscores. For example:

| Field | Recognized headers (examples) |
|---|---|
| First / last name | `First Name`, `first`, `given_name` / `Last Name`, `surname` |
| Full name | `Name`, `Full Name`, `Contact Name` |
| Company | `Company`, `Company Name`, `Account`, `Organization` |
| Domain / website | `Domain`, `Company Domain` / `Website`, `Company URL` |
| Email / phone | `Email`, `Work Email` / `Phone`, `Mobile` |
| LinkedIn | `LinkedIn`, `LinkedIn URL`, `Profile URL` (person) · `Company LinkedIn` (company) |
| LinkedIn URN | `URN`, `LinkedIn URN`, `Sales Nav URN` |

If your file uses other names, map them yourself with `--col-*` flags:

```bash
enrich csv -i leads.csv -o out.csv --col-email "Work Mail" --col-company "Employer"
```

**Added columns:** `enriched_first_name`, `enriched_last_name`, `enriched_email`, `email_source`, `email_verified`, `enriched_phone`, `phone_source`, `enriched_person_linkedin`, `person_linkedin_source`, `enriched_title`, `enriched_company_name`, `enriched_domain`, `enriched_company_linkedin`, `company_linkedin_source`, `enrich_status`, `enrich_error`.

**`enrich_status`** tells you how each row went:

| Status | Meaning |
|---|---|
| `enriched` | Everything you asked for was found |
| `partial` | Some of what you asked for was found |
| `not_found` | Vendors were tried (or had too little input to try) and found nothing |
| `error` | Nothing was found and at least one vendor failed (auth, credits, timeout, …) |

`enrich_error` lists vendor failures, separated by `; `. A row can be `enriched` and still have an error from one vendor if another vendor then succeeded.

**Safety:**
- `enrich` refuses to write the output over its input file.
- The output is written to a temporary file and moved into place at the end, so a run that fails partway never leaves a half-written or emptied output file.
- If you press Ctrl-C, rows already finished are kept, and the rows not yet processed are marked `error`.

---

## API keys and configuration

`enrich keys` saves your keys in `~/.config/tiga/config.json` (or `$XDG_CONFIG_HOME/tiga/config.json`). Only your user can read the file.

```bash
enrich keys set <vendor> -     # add or replace a key (read from stdin)
enrich keys list               # show configured vendors with masked keys (--json for scripts)
enrich keys rm <vendor>        # remove a key
```

Environment variables override the config file, which is handy for CI:

| Vendor | Variable |
|---|---|
| Findymail | `FINDYMAIL_API_KEY` |
| Prospeo | `PROSPEO_API_KEY` |
| Apollo | `APOLLO_API_KEY` |
| Lusha | `LUSHA_API_KEY` |
| Seamless | `SEAMLESS_API_KEY` |
| Dropcontact | `DROPCONTACT_API_KEY` |
| Hunter | `HUNTER_API_KEY` |
| Datagma | `DATAGMA_API_KEY` |
| LeadIQ | `LEADIQ_API_KEY` |
| Renidly | `RENIDLY_API_KEY` |
| ZeroBounce | `ZEROBOUNCE_API_KEY` |
| OpenAI | `OPENAI_API_KEY` |

To use a different config file for a single run, pass `--config <path>`.

---

## Using with AI agents

[`skills/enrich-cli/SKILL.md`](skills/enrich-cli/SKILL.md) is a ready-made skill for Claude Code and other AI agents. It teaches the agent to check your keys, inspect a CSV, ask before spending credits, and report results.

---

## Contributing

See [DEVELOPMENT.md](DEVELOPMENT.md) for building from source, running tests (including live vendor tests), and publishing releases.
