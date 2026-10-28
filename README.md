# tiga

`tiga` is a command-line tool for go-to-market work. It runs on your own vendor API keys and needs no Tiga account.

| Command | What it does |
|---|---|
| [`tiga enrich`](#enrich) | Finds work emails, LinkedIn profiles and phone numbers for contacts, across 9 data vendors |
| [`tiga signals`](#signals) | Runs AI research signals on contacts and accounts: funding, hiring, new roles, executive hires, LinkedIn posts |
| [`tiga keys`](#api-keys-and-configuration) | Manages the vendor API keys for both, in one key file (`~/.config/tiga/config.json`) |

`tiga` is a single binary for macOS and Linux.

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
- **Manual download:** get `tiga-<os>-<arch>` from the [Releases page](https://github.com/tiga-labs/cli-tools/releases), `chmod +x` it, and put it on your `PATH`. Check it against `checksums.txt` on the same page.
- **With Go:** `go install github.com/tiga-labs/cli-tools/cmd/tiga@latest`

Supported platforms are macOS and Linux, on Intel/AMD (`amd64`) and ARM (`arm64`). To check the install:

```bash
tiga --version
```

Run `tiga help` to see every command with its main flags, and `tiga help <command>` (for example `tiga help signals csv`) for the full flag list.

### Install the agent skills

`tiga` ships with skills that teach Claude Code and Codex how to use it. Install them with:

```bash
tiga skills install            # asks which agents to install for
tiga skills install --claude   # ~/.claude/skills/<skill>/SKILL.md
tiga skills install --codex    # ~/.agents/skills/<skill>/SKILL.md
tiga skills install --claude --codex --project   # into ./.claude/skills and ./.agents/skills instead
```

Run it again after upgrading `tiga` to update the skills. `tiga skills list` shows what is installed and whether each copy matches your `tiga`. `tiga skills uninstall` removes only the tiga skills.

---

## enrich

**Find work emails, LinkedIn profiles, and phone numbers for your contacts, from the command line.**

You give `tiga enrich` a person or a CSV of contacts. It asks your data vendors, such as Findymail, Prospeo and Apollo, one at a time. It stops as soon as it has what you asked for, so later vendors are not called and you spend credits only on what you need. Each found value records which vendor supplied it.

- Works on one contact (`tiga enrich person`) or a whole CSV (`tiga enrich csv`). The CSV keeps all your original columns and row order.
- Supports 9 contact-data vendors, plus email verification (ZeroBounce), LinkedIn URN resolution (Renidly) and AI name cleanup (OpenAI).
- A single binary for macOS and Linux, with nothing else to install.

### Quick start

#### 1. Add an API key for at least one vendor

```bash
tiga keys set hunter -        # paste your key, then press Enter
tiga keys list                # see which vendors are configured (keys are masked)
```

The `-` makes `enrich` read the key from stdin, which keeps it out of your shell history. You can also run `tiga keys set hunter <key>`, or use environment variables (see [API keys](#api-keys-and-configuration)).

#### 2. Enrich one person

```bash
tiga enrich person --name "Jane Doe" --domain acme.com --format table
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

#### 3. Enrich a CSV

```csv
First Name,Last Name,Company,Website
Jane,Doe,Acme,acme.com
John,Smith,Globex,globex.com
```

```bash
tiga enrich csv -i contacts.csv -o enriched.csv
```

The output keeps every original column and row, in the same order, and adds enrichment columns at the end. Progress is printed to stderr as each row finishes. Add `--quiet` to hide it.

---

### Supported vendors

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

### Common options

These work with both `tiga enrich person` and `tiga enrich csv`:

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

Run `tiga enrich person --help` or `tiga enrich csv --help` for the full list.

---

### Working with CSVs

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
tiga enrich csv -i leads.csv -o out.csv --col-email "Work Mail" --col-company "Employer"
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

### API keys and configuration

`tiga keys` saves your keys in `~/.config/tiga/config.json` (or `$XDG_CONFIG_HOME/tiga/config.json`). Only your user can read the file.

```bash
tiga keys set <vendor> -     # add or replace a key (read from stdin)
tiga keys list               # show configured vendors with masked keys (--json for scripts)
tiga keys rm <vendor>        # remove a key
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

### Using with AI agents

The `enrich-cli` skill ([source](skills/enrich-cli/SKILL.md)) is a ready-made skill for Claude Code, Codex and other AI agents. Install it with `tiga skills install` (see [Install the agent skills](#install-the-agent-skills)). It teaches the agent to check your keys, inspect a CSV, ask before spending credits, and report results.

---

## signals

**Ask AI research questions about your contacts and accounts, from the command line.**

You give `tiga signals` one record or a CSV, plus one or more signals. A signal is a question such as "Has {{.AccountName}} raised funding in the last 90 days?". For each record, `signals` researches the question with your OpenAI key (and, if you choose, LinkedIn data from linkdapi). It returns a status, an answer and the sources behind it.

- Works on one record (`tiga signals run`) or a whole CSV (`tiga signals csv`). The CSV keeps all your original columns and row order.
- Five signal types: open AI research (`gpt`), new role, LinkedIn posts, executive hires and job listings.
- **Only an OpenAI key is needed to start.** LinkedIn signal types need a linkdapi key, and never spend it unless you pass `--buy-linkedin`.
- Answers are stored on disk, so rerunning a file costs nothing for rows and signals that haven't changed.

### Quick start

```bash
tiga keys set openai -        # paste your key, then press Enter

tiga signals run --account-name Stripe --account-domain stripe.com \
  --prompt "Has {{.AccountName}} raised funding {{.Last90Days}}?" --format table
```

```text
Signal:  gpt (gpt)
Status:  2 not_found
Value:   **No—there is no evidence that Stripe raised a funding round after July 30, 2026.** ...
Source:  https://stripe.com/newsroom/news
```

*(Example output from 2026-10-28. Results depend on the question and on the web.)*

A CSV of accounts:

```bash
tiga signals csv accounts.csv --prompt "Is {{.AccountName}} ({{.AccountWebsite}}) hiring salespeople {{.Last30Days}}?" \
  --label "Hiring sales" --dry-run       # how many rows, and the most calls it can make: no vendor calls
tiga signals csv accounts.csv --prompt "Is {{.AccountName}} ({{.AccountWebsite}}) hiring salespeople {{.Last30Days}}?" \
  --label "Hiring sales" -o scored.csv
```

For each signal, the output adds `<key>_value`, `<key>_status`, `<key>_sources` (space-separated URLs) and `<key>_error`. The key is the label in lowercase with `_` (`hiring_sales`). A row is a hit when `<key>_status` is `1`.

### Signal types

| Type | For | Keys | Answers |
|---|---|---|---|
| `gpt` | person or account | OpenAI | Any question, researched on the web, with cited sources |
| `started_new_role` | person (`LinkedInUrl`) | linkdapi, plus OpenAI with `--exclude-advisor-roles` | Started a current role inside a window, such as `THREE_MONTHS` |
| `linkedin_posts` | person, or account with `--account` | linkdapi, OpenAI | Is a recent LinkedIn post about your question? Returns a summary and the post |
| `hired_executive` | account (`AccountLinkedInUrl`) | linkdapi, OpenAI | Did the company announce hiring one of these executive titles? |
| `hiring_for_role` | account (`AccountLinkedInUrl`) | linkdapi | Does the company list LinkedIn jobs for these titles (US, or one state with `--location`)? |

Signals come from inline flags (`--prompt`, `--type`, `--label`, …) or from JSON definition files (`-s funding.json`, repeatable), which use the same shape as the Tiga API's signal body. [`skills/signals-cli/SKILL.md`](skills/signals-cli/SKILL.md) has an example definition for every type.

**What prompts can use:**
- Any standard field (`{{.AccountName}}`, `{{.AccountWebsite}}`, `{{.FirstName}}`, …) and any CSV column (`{{.funding_stage}}`).
- Date fields: `{{.Last30Days}}`, `{{.Last90Days}}` and others.
- With `--buy-linkedin`, LinkedIn fields: `{{.PersonLi_Headline}}`, `{{.PersonLi_Experience}}`, `{{.CompanyLi_RecentPosts}}`, ….

`tiga signals csv --help` lists them all.

### Options and costs

| Flag | Default | Effect |
|---|---|---|
| `--web-search` / `--no-web-search` | on | Research on the web. With it off, answers have no sources. |
| `--bool` / `--no-bool` | on | One small extra OpenAI call per hit; a "no" answer becomes not found. `--no-bool` does not override a definition that turns classification on. |
| `--verify` | off | One extra OpenAI call per hit, to check the answer against its sources |
| `--buy-linkedin` | off | Allows paid linkdapi calls: about 1 credit per profile, post page or company lookup, and 2 per job search |
| `--refresh-linkedin` | off | Refetch LinkedIn data (paid again), and recompute stored answers for LinkedIn signals |
| `--no-cache` | off | Recompute stored answers |
| `--dry-run` | off | CSV only: print rows, cached rows and the most OpenAI requests and linkdapi credits the run can use, with no vendor calls. LinkedIn types also need `--buy-linkedin` here, which is free on a dry-run. |
| `--concurrency <n>` | 5 | CSV only: rows processed at once |

> **Cost:**
> - **OpenAI** bills each gpt answer: one research request per signal per row, which can run up to 10 web searches, plus one small classifier call per hit.
> - **LinkedIn post signals** (`linkedin_posts`, `hired_executive`) use up to 25 small classifier calls and up to 25 research-model calls per row.
> - **Before a large file,** run `--dry-run` and try a few rows first.
>
> Rows with identical inputs share one answer within a run, so 50 contacts at one company pay once for an account signal.

### Statuses

| Status | Meaning |
|---|---|
| `1` success | A hit: read the value and sources |
| `2` not_found | Ran, and the answer was no (or no evidence, or no such LinkedIn profile or company) |
| `3` missing_dependencies | A field the signal needs was empty; `_error` names it. Nothing was spent. |
| `4` failed | A vendor error; `_error` has the message (keys are redacted). Failed rows are retried on the next run. |
| `5` precondition_failed | The signal's precondition skipped the row |

**Results and safety:**
- Found and not-found answers are stored under `~/.cache/tiga/signals`, and LinkedIn data under `~/.cache/tiga/linkdapi`. `tiga signals cache clear [--linkedin]` empties them.
- `tiga signals csv` never writes over its input.
- On Ctrl-C, finished rows are kept.

### Keys

```bash
tiga keys set openai -
tiga keys set linkdapi -      # only for LinkedIn signal types
tiga keys list
```

Keys live in the same file as the enrich keys, and `tiga keys` manages both. `OPENAI_API_KEY` and `LINKDAPI_API_KEY` override it.

### Using with AI agents

The `signals-cli` skill ([source](skills/signals-cli/SKILL.md)) is a ready-made skill for Claude Code, Codex and other AI agents. Install it with `tiga skills install`. It teaches the agent to:
- check your keys
- dry-run before spending, and ask you first
- keep LinkedIn spend opt-in
- filter on status
- report what was found and not found

---

## Contributing

See [DEVELOPMENT.md](DEVELOPMENT.md) for building from source, running tests (including live vendor tests), and publishing releases.
