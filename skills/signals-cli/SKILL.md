---
name: signals-cli
description: "Run AI research signals from the command line with the `tiga signals` command, on one contact or account or on a CSV of them, using the user's own OpenAI key (and optionally a linkdapi key for LinkedIn data). Use it to answer questions like 'which of these accounts raised funding recently?', 'is this company hiring AEs?', 'did this person start a new role?', 'has this company hired a new CFO?', or 'what is this person posting about on LinkedIn?'. Also use it to filter a list down to accounts where a signal hit, to score or tier a list by stacking several signals, and to manage signals API keys (`tiga keys`). Trigger for requests such as 'run a signal on this CSV', 'score these accounts', 'check if these companies are hiring', 'research this account with signals', or 'set up signals keys'. This is the local CLI; it does not use the Tiga API or a Tiga account."
---

# Signals CLI Skill

`tiga signals` is a standalone command that runs AI signals on contacts and accounts. A signal is a question about a record, such as "Has {{.AccountName}} raised funding in the last 90 days?". The answer comes back with a status and its sources. `signals` runs on the user's own vendor keys and keeps everything on the user's machine.

**CLI basics.** The tool is installed as the `tiga` binary. The commands are:
- `tiga signals run` for one record
- `tiga signals csv` for a CSV of records
- `tiga keys` to manage vendor keys
- `tiga help` for every command and its main flags (`tiga help signals csv` for the full list)
- `tiga skills install` to update this skill after upgrading `tiga`
- `tiga signals cache clear` to remove stored results

Keys live in `~/.config/tiga/config.json`, and environment variables override that file. Never print, echo or log a raw API key. Use `tiga keys list`, which masks them.

**Related skills:**
- **enrich-cli** fills in missing emails, LinkedIn URLs and company domains before a signals run.
- **signals** (the Tiga platform skill) runs the same signal types inside a Tiga account.

---

## Operating principles

1. **Check keys first.** Run `tiga keys list` before anything else.
   - Every type uses OpenAI, except `started_new_role` without advisor exclusion and `hiring_for_role`.
   - The LinkedIn types need a linkdapi key.

   Never assume a key exists. If a needed key is missing, ask the user to run `tiga keys set openai -` (or `linkdapi`) in their own terminal. Never ask them to paste a key into the chat, and never read keys from files or the environment yourself.
2. **Inspect the CSV before running.** Run `head -n 5 input.csv`.
   - `tiga signals csv` maps common header names to the standard fields, ignoring case, spaces, `-` and `_`. Examples:
     - **Account:** `Company` / `Company Name` / `Account` → `AccountName`; `Website` → `AccountWebsite`; `Domain` → `AccountDomain`; `Company LinkedIn` / `LinkedIn Company Page` → `AccountLinkedInUrl`; `Industry` → `AccountIndustry`
     - **Person:** `First Name`, `Last Name`, `Email`, `Title`; `LinkedIn` / `Profile URL` / `Person LinkedIn` → `LinkedInUrl`

     `tiga signals csv --help` lists all fields. If unsure, check `rows_missing_deps` in the dry-run. An extra `--col` is harmless.
   - Every column is also available as `{{.column_name}}` (lowercased, with spaces as `_`).
   - Use `--col Field=Header` for an unusual header, such as `--col AccountName="Company Legal Name"`.
   - **A bare `linkedin` column maps to the person's `LinkedInUrl`.** In an accounts CSV, pass `--col AccountLinkedInUrl=linkedin`, or the account LinkedIn types report every row as missing. Check `rows_missing_deps` in the dry-run.
   - **A bare `name` column is never mapped,** because it could be a person or a company. Pass `--col AccountName=name` or `--col FirstName=...` as fits.
3. **Dry-run, then ask.** Before any paid CSV run:
   1. Run the same command with `--dry-run`. It makes no vendor calls and needs no keys.
   2. Tell the user how many rows there are, the signals and their types, which paid flags are on, and, per signal, `rows_ready`, `cached_rows`, `max_openai_calls` and `linkdapi_credits`. Two notes when you report them:
      - **They are upper bounds.** Each gpt research request can run up to 10 web searches, which OpenAI bills on top of the request.
      - **Where the linkdapi credits come from**, per company or person not already cached:
        - `hiring_for_role`: 1 for the company id, plus 2 per job title
        - company post types: 1, plus up to 3 post pages
        - person types: 1 for the profile, plus 1 for posts
   3. Wait for the user's explicit approval.
   4. Rows that miss a needed field (for example an account with no company LinkedIn URL) are status 3 and cost nothing. Say which rows they are, and ask whether to fill them first (with **enrich-cli**) or skip them.

   A single `tiga signals run` on one record is cheap. Still say which paid flags are on.
4. **LinkedIn data is opt-in.** `--buy-linkedin` spends linkdapi credits (about 1 per profile, post page or company lookup, and 2 per job search).
   - Use it on a real run only after the user agrees to that spend. Without it, LinkedIn types stop at startup with an error. That is expected; don't work around it.
   - **On a `--dry-run`, `--buy-linkedin` is free and required** for LinkedIn types to be planned, so include it there without asking.
5. **Filter on status, not text.** A hit is `_status == 1` (`status: 1` in `run` JSON).
   - Bool classification is **on by default**: a sourced "No, they did not…" answer is status 2, not 1.
   - Only use `--no-bool` when the user wants every sourced answer kept. Say so first, because it makes "no" answers count as hits.
   - `--no-bool` and `--no-verify` do not override a definition that sets `should_classify_output_as_bool` or `advanced_model_verify`.
6. **Sources matter.**
   - With web search on (the default), a gpt hit always has at least one source URL. A hit without sources is downgraded to status 2.
   - With `--no-web-search`, hits have no sources. Tell the user these are the model's reasoning over the row's own data, not researched facts.
7. **Report found and not found.** After every CSV run, report counts per signal by status, and show a few example values with their sources. Explain any status 3 or 4 rows (see Troubleshooting).
8. **Reruns are free.** Found and not-found results are stored on disk, so a rerun of an unchanged signal on an unchanged row makes no vendor calls. Editing a signal's prompt re-runs that signal, plus any later signal that uses its answer (`{{.<key>}}`).
   - Use `--no-cache` only when the user wants fresh answers.
   - Use `--refresh-linkedin` only when they want fresh LinkedIn data (it pays linkdapi again).
   - Run `tiga signals cache clear` only when the user asks. With `--linkedin`, the next run pays linkdapi again for every profile and company.

---

## Signal types

| Type | Scope | Needs | What it answers |
|---|---|---|---|
| `gpt` | person or account | OpenAI | Any question, researched on the web (or reasoned without it with `--no-web-search`). |
| `started_new_role` | person | linkdapi + `--buy-linkedin` (OpenAI only with `--exclude-advisor-roles`) | Did the person start a current role inside the window? |
| `linkedin_posts` | person, or account with `--account` | linkdapi + `--buy-linkedin`, OpenAI | Is one of their recent LinkedIn posts about the prompt? Returns a summary and the post URL. Each row can use up to 25 small classifier calls and 25 research-model calls. |
| `hired_executive` | account | linkdapi + `--buy-linkedin`, OpenAI | Did the company announce an executive hire for the titles in the prompt? |
| `hiring_for_role` | account | linkdapi + `--buy-linkedin` | Does the company list LinkedIn jobs for any of the titles? US-only by default; `--location WA` for one state. |

**What each type reads from the row:**
- **Person LinkedIn types** need `LinkedInUrl`, a `linkedin.com/in/...` URL.
- **Account LinkedIn types** need `AccountLinkedInUrl`, a `linkedin.com/company/...` URL. There is no lookup by domain. Use **enrich-cli** (`--research-company`) to find company LinkedIn URLs first.
- **gpt signals** need every `{{.Field}}` their prompt uses.

### Writing good gpt prompts

- Ask one yes/no question about one record, with a time window when it matters: `Has {{.AccountName}} ({{.AccountWebsite}}) raised funding {{.Last90Days}}?`
- Give the company's website or domain as well as its name, so web search finds the right company.
- Date fields: `{{.CurrentDate}}`, and `{{.Last7Days}}`, `{{.Last14Days}}`, `{{.Last30Days}}`, `{{.Last60Days}}`, `{{.Last90Days}}` and `{{.Last180Days}}` (these render as `after:YYYY-MM-DD`).
- Use `--word-limit 60` (or `word_limit`) for short answers in a CSV.

---

## Workflow 1: Manage keys

```bash
tiga keys list                 # what is configured (masked)
tiga keys set openai -         # paste the key on stdin; keeps it out of shell history
tiga keys set linkdapi -       # only for LinkedIn signal types
tiga keys rm linkdapi
```

`OPENAI_API_KEY` and `LINKDAPI_API_KEY` (or `LINKDAPI_KEY`) override the file. `tiga signals` and `tiga enrich` share the same key file, managed by `tiga keys`.

---

## Workflow 2: Research one record (`tiga signals run`)

```bash
tiga signals run --account-name Stripe --account-domain stripe.com \
  --prompt "Has {{.AccountName}} raised funding {{.Last90Days}}?"
```

Output is JSON: `{"results": [{"key", "label", "type", "value", "status", "status_name", "sources", "error", "cached"}]}`. Add `--format table` for a readable view.

**Record flags:**
- **Person:** `--first-name`, `--last-name`, `--name`, `--email`, `--linkedin-url`, `--title`
- **Account:** `--account-name`, `--account-domain`, `--account-website`, `--account-linkedin`, `--account-industry`
- **Any other field:** `--field Key=Value`

Several signals in one run (files first, then the inline one):

```bash
tiga signals run -s funding.json -s hiring.json --account-name Acme --account-domain acme.com \
  --account-linkedin https://www.linkedin.com/company/acme --buy-linkedin
```

A person LinkedIn check:

```bash
tiga signals run --type started_new_role --started-within THREE_MONTHS \
  --first-name Jane --last-name Doe --linkedin-url https://www.linkedin.com/in/jane-doe --buy-linkedin
```

---

## Workflow 3: Filter a list (`tiga signals csv`)

**Use when:** the user wants only the accounts or people where one signal hit.

1. Run `head -n 5 accounts.csv` and confirm the columns the prompt needs.
2. Dry-run, and show the user the numbers:

```bash
tiga signals csv accounts.csv --prompt "Is {{.AccountName}} ({{.AccountWebsite}}) hiring salespeople {{.Last30Days}}?" \
  --label "Hiring sales" --dry-run
```

3. After approval, run it:

```bash
tiga signals csv accounts.csv --prompt "Is {{.AccountName}} ({{.AccountWebsite}}) hiring salespeople {{.Last30Days}}?" \
  --label "Hiring sales" -o scored.csv
```

4. Keep the hits. Each signal adds four columns: `<key>_value`, `<key>_status`, `<key>_sources` (space-separated URLs) and `<key>_error`. The key is the label in lowercase with `_` (`Hiring sales` gives `hiring_sales`). Filter on status with a CSV-aware tool, never `grep`, because values can contain commas and newlines:

```bash
python3 -c "import csv,sys; r=csv.DictReader(open('scored.csv')); w=csv.DictWriter(sys.stdout, r.fieldnames); w.writeheader(); [w.writerow(x) for x in r if x['hiring_sales_status']=='1']" > hits.csv
```

Progress lines go to stderr (`[3/50] row 3: hiring_sales=success`). `--quiet` turns them off. `--concurrency` (default 5) sets how many rows run at once.

---

## Workflow 4: Stacked signals for scoring

**Use when:** several signals firing together mean a hot account, such as funding plus hiring plus a new executive.

1. Pick 2–5 signals. Write each as a definition file (see the examples below), or mix files with one inline signal.
2. Dry-run them all at once, and show the per-signal numbers:

```bash
tiga signals csv accounts.csv -s funding.json -s hiring.json -s exec.json --buy-linkedin --dry-run
```

3. After approval, run without `--dry-run` and with `-o scored.csv`.
4. Score: 1 point per signal with `_status == 1`. Sort by score, and show the top accounts with each hit's value and source.

**Chaining:** a later signal can use an earlier one's answer as `{{.<key>}}`, for example a tier signal whose prompt says `Given {{.funding}} and {{.hiring}}, …`. It only runs when the earlier signals hit. When one missed, the later signal is status 3 at no cost.

---

## Workflow 5: Tiering

Run 3–5 signals across dimensions, such as ICP fit, buying intent, timing and competitive. Then tier the accounts:
- **Tier 1:** 3 or more hits. Immediate outreach.
- **Tier 2:** 2 hits. Nurture.
- **Tier 3:** 0–1 hits. Monitor.

Write one CSV per tier, and report the counts and a few example accounts per tier with their evidence.

---

## Signal definition files

A definition file uses the Tiga API's create-signal body: `{"label": ..., "computed_config": {...}}`. Unknown fields are ignored, and `required_dependencies` is worked out from the prompt.

**Funding** (gpt, account):

```json
{
  "label": "Funding",
  "computed_config": {
    "type": "gpt",
    "is_account_insight": true,
    "prompt": "Has {{.AccountName}} ({{.AccountWebsite}}) announced a funding round {{.Last90Days}}? Say the amount and date.",
    "word_limit": 60
  }
}
```

**ICP fit without web search** (gpt, reasoning over the row's own columns; hits have no sources):

```json
{
  "label": "ICP fit",
  "computed_config": {
    "type": "gpt",
    "is_account_insight": true,
    "can_use_web_search": false,
    "prompt": "Given industry {{.AccountIndustry}} and size {{.employee_count}}, is {{.AccountName}} a fit for B2B payroll software?"
  }
}
```

**New role** (person, LinkedIn):

```json
{
  "label": "New role",
  "computed_config": {
    "type": "started_new_role",
    "started_new_role_config": {"started_new_job_within": "THREE_MONTHS", "should_exclude_advisor_roles": true}
  }
}
```

**Posts about hiring** (person, LinkedIn):

```json
{
  "label": "Posting about hiring",
  "computed_config": {
    "type": "linkedin_posts",
    "date_range": "ONE_MONTH",
    "prompt": "Is this person posting about growing their sales team?"
  }
}
```

**Company posts about a launch** (account, LinkedIn):

```json
{
  "label": "Launch posts",
  "computed_config": {
    "type": "linkedin_posts",
    "is_account_insight": true,
    "date_range": "THREE_MONTHS",
    "prompt": "Did the company announce a new product or major launch?"
  }
}
```

**Executive hire** (account, LinkedIn; the prompt lists the titles):

```json
{
  "label": "Exec hire",
  "computed_config": {
    "type": "hired_executive",
    "is_account_insight": true,
    "date_range": "SIX_MONTHS",
    "prompt": "CFO, CRO, VP of Sales"
  }
}
```

**Hiring for a role** (account, LinkedIn; up to 10 titles, and the first title with jobs wins):

```json
{
  "label": "Hiring AEs",
  "computed_config": {
    "type": "hiring_for_role",
    "is_account_insight": true,
    "exact_match_search": true,
    "hiring_for_role": {"job_titles": ["Account Executive", "Sales Development Representative"], "location": "CA"}
  }
}
```

`location` is optional. Leave it out to search the whole US, and set it only when the user names a state.

**Windows** (`date_range` and `started_new_job_within`): `TWO_WEEKS`, `ONE_MONTH`, `THREE_MONTHS`, `SIX_MONTHS`, `TWELVE_MONTHS`, `THIS_QUARTER` and `THIS_YEAR`.

**Other useful fields:**
- `model`: the research model
- `max_tokens`
- `should_classify_output_as_bool`
- `advanced_model_verify`: a second call that checks the answer against its sources
- `expiration_in_days`: how long a stored result is kept (default 30)
- `has_precondition` with `precondition: {property, property_type, operator, value}`: skip rows that don't match

---

## Per-run flags (`tiga signals run` and `tiga signals csv`)

| Flag | Default | Effect |
|---|---|---|
| `--web-search` / `--no-web-search` | on | gpt researches on the web; off means no sources |
| `--bool` / `--no-bool` | on | One small extra call per hit; a "no" answer becomes status 2 |
| `--verify` | off | One extra call per hit; an answer its sources don't support becomes status 2 |
| `--model <name>` | definition's, then `gpt-5.6-terra` | Research model for gpt signals and for post summaries (classifiers always use `gpt-5.4-mini`) |
| `--buy-linkedin` | off | Allows paid linkdapi calls |
| `--refresh-linkedin` | off | Refetches LinkedIn data, and skips stored results for LinkedIn signals |
| `--no-cache` | off | Recomputes every signal (results are stored again) |
| `--config <path>` | `~/.config/tiga/config.json` | Key file |

**Inline signal flags** (all need `--prompt` or `--type`):
- `--label`, `--prompt`, `--type`, `--account`, `--word-limit`, `--date-range`
- `--started-within`, `--exclude-advisor-roles`
- `--job-title` (repeatable), `--location`, `--exact-match`

**`tiga signals csv` only:**
- `-i`, or the first argument, or `-` for stdin
- `-o` (default: stdout)
- `--col Field=Header`
- `--concurrency`, `--quiet`, `--dry-run`

---

## Troubleshooting by status

| Status | Meaning | What to do |
|---|---|---|
| 1 `success` | Hit. Read `value` and `sources`. | Report it with its evidence. |
| 2 `not_found` | Ran, but no hit (including a sourced "no" with bool on, or a LinkedIn profile or company that wasn't found). | Normal. A large share of 2s may mean the prompt is too strict. |
| 3 `missing_dependencies` | A needed field was empty, or not a valid LinkedIn URL. `_error` says which one (`missing: AccountWebsite`). | Map the column with `--col` (a bare `linkedin` column is the *person* URL; see principle 2), fill it with **enrich-cli**, or change the prompt. Costs nothing. |
| 4 `failed` | A vendor error. `_error` has the message, with keys redacted. | `openai: 401` or `429`: check the key and quota. `linkdapi: 4xx`: check the key and credits. Rerun: failures are never stored, so only those rows are retried. |
| 5 `precondition_failed` | The definition's precondition skipped the row. | Expected. |

**Exit codes:** `signals` exits 1 only for a startup or file error, or Ctrl-C. Rows that miss or fail still exit 0, and an interrupted CSV keeps the rows already finished.
