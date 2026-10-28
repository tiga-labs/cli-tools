---
name: salesnav-cli
description: "Import a LinkedIn Sales Navigator lead list or saved search into a CSV from the command line with `tiga salesnav-list-import`, using the user's own logged-in Chrome (no Tiga account, no extension, no LinkedIn API key). Use when the user wants to pull, export, download, or build a list from Sales Nav, for example 'export my Sales Navigator list to CSV', 'pull this saved search', 'import my Sales Nav leads and find their emails', or when they paste a linkedin.com/sales/lists/people/... or linkedin.com/sales/search/people?savedSearchId=... URL. Also use when an import stopped, was interrupted, or printed an error such as 'not logged in in your Chrome profile', 'LinkedIn answered 429', or 'nothing to resume'."
---

# Sales Nav CLI Skill

`tiga salesnav-list-import <url>` reads a Sales Navigator **lead list** or **saved search** through the Chrome the user is already logged into, and writes the leads to a CSV. It pages slowly on purpose and stops at LinkedIn's first warning, because the user's LinkedIn account is what is at risk. The CSV feeds straight into `tiga enrich csv` (the **enrich-cli** skill).

**The user's LinkedIn account matters more than speed or completeness.** Every rule below follows from that.

## Before every run

Ask the user to confirm these, rather than probing their browser:

1. Chrome 144 or newer is running, with remote debugging turned on at `chrome://inspect/#remote-debugging` (once per Chrome install).
2. Sales Nav is logged in in **Chrome's first profile** (the profile Chrome created first, often named "Person 1"). tiga can only use that profile; other profiles are invisible to it.
3. They will **click Allow** on Chrome's "Allow remote debugging?" prompt. It appears on **every** run, and you cannot click it for them. Clicking Cancel ends the run.

## Size and time

The default is 1,000 leads; `--max` goes up to 7,000.

| Leads | Time | Run it |
|---|---|---|
| up to 1,000 | about 2.5 min | in the foreground |
| 2,000 | about 14 min | in the background |
| 3,000 | about 25 min | in the background |
| 4,000 | about 35 min | in the background |
| 5,000 | about 47 min | in the background |
| 6,000 | about 57 min | in the background |
| 7,000 | about 68 min | in the background |

- **Ask before passing a `--max` above 1,000.** Tell the user the number and the time from this table, and wait for a yes. A bigger list is not permission for a bigger run.
- Above 1,000 leads the run outlasts a normal tool call: run it in the background and wait for it to exit, or ask the user to run it in their own terminal.
- One import at a time. Never start a second import while one is running, and never point two runs at the same `-o` file.

## Commands

```bash
# Lead list or saved search, copied from the address bar
tiga salesnav-list-import 'https://www.linkedin.com/sales/lists/people/123'
tiga salesnav-list-import 'https://www.linkedin.com/sales/search/people?savedSearchId=456&sessionId=abc%3D%3D' -o leads.csv

# After the user agreed to a bigger run (in the background)
tiga salesnav-list-import 'https://www.linkedin.com/sales/lists/people/123' --max 5000

# Continue a stopped or interrupted import: paste the command it printed
tiga salesnav-list-import 'https://www.linkedin.com/sales/lists/people/123' -o salesnav-list-123.csv --resume

# Start over, only when the user asks to discard the file
tiga salesnav-list-import 'https://www.linkedin.com/sales/lists/people/123' --force
```

- Always single-quote the URL; saved-search URLs contain `&`.
- On `--resume`, leave `--max` out: the import keeps the limit it started with.
- **A finished import is final.** `Done: …` (including `Stopped at --max N.`) deletes the checkpoint, so `--resume` cannot extend it. To get more leads than a finished run's `--max`, start a new run with a bigger `--max` (and a new `-o`, or `--force` if the user wants the old file replaced). It reads the first leads again from LinkedIn, so ask first and quote the time from the table.
- `--force` deletes the existing file. Use it only when the user says to start over.
- `--chrome-user-data-dir <dir>` points at a different Chrome **installation's** data directory (for example a separate Chrome Beta). It does not choose a profile inside a Chrome.

## When it ends

| Exit | Meaning | What you do |
|---|---|---|
| 0 | Done (`Done: N leads in <file>.`). The import is complete. | Report the count and file; offer the printed `Next:` enrich command (see below). |
| 1, with `Interrupted.` | The user (or a timeout) stopped it with Ctrl-C | Report the count and the printed resume command. Resume only if the user asks. If **your** tool call's timeout killed it, say so and offer to resume in the background. |
| 1, any other message | Setup or input problem | Fix the cause with the user (table below), then run the **same command again**. If the output does not say leads were saved, there is no file to resume. |
| 2 | LinkedIn pushed back (401, 403, 429, 999, a login/security redirect, or an unexpected response) | Stop. Tell the user what LinkedIn said, how many leads were saved, and the printed resume command (if leads were saved). Do **not** resume or rerun in this session unless the user explicitly tells you to after hearing that LinkedIn recommends waiting hours. |
| 2, with `No leads were saved.` on a first run | Often not real pushback: Chrome's first profile is logged into a LinkedIn account **without** Sales Nav | Before anything else, ask the user whether Chrome's first profile is signed into the account that has Sales Nav. Still do not rerun until the user says so. |

Never retry, loop, or "try once more" after an exit 2, and never try to work around a stop.

## Troubleshooting (exact messages)

| Message | Cause and fix |
|---|---|
| `Chrome remote debugging is off` | Turn it on at `chrome://inspect/#remote-debugging`, then run again. |
| `could not reach Chrome` | Chrome is closed, or remote debugging was turned off, or Chrome just restarted. Open Chrome, check the toggle, wait a few seconds, run again. |
| `prompt was declined` | The user clicked Cancel. Run again and have them click Allow. |
| `no answer to Chrome's "Allow remote debugging?" prompt in time` | Nobody clicked within 2 minutes. Run again while the user is at the screen. |
| `Sales Nav is not logged in in your Chrome profile "<name>"` | tiga only uses Chrome's first profile, named in the message. The user must log into Sales Nav in that profile. There is no option to pick another profile. |
| `already exists. Use --resume … or --force` | Ask the user which: continue that file (`--resume`) or start over (`--force`). |
| `nothing to resume` | If the output file exists, that import already finished: report it and do not rerun (see "A finished import is final"). If no file exists, the earlier run saved nothing: run without `--resume`. |
| `was changed outside salesnav-list-import` / `is shorter than its last checkpoint` | The CSV was edited after the import stopped. Ask the user: `--force` to start over, or a new `-o` file. |
| `this is an unsaved Sales Navigator search` | Have the user save the search in Sales Nav, open it from Saved searches, and copy the URL from the address bar. |
| `this is a Sales Navigator account list` | Only lead lists and saved lead searches are supported. |
| `LinkedIn stopped returning results before its total` | Sales Nav caps saved-search results. Suggest saving the leads to a lead list and importing the list. |

## The CSV

Columns: `first_name, last_name, full_name, title, company_name, company_linkedin, location, linkedin_urn, sales_nav_url, degree, date_added_to_list, source_url`.

- `linkedin_urn` is the Sales Nav lead id; `tiga enrich csv` turns it into a LinkedIn profile URL (Renidly).
- `degree` is the connection degree; `-1` means out of network.
- `date_added_to_list` is filled for lead lists only.

The file holds real people's names. Report counts, not contents; show a few rows only if the user asks.

## Then enrich

The import prints the next step, for example:

```bash
tiga enrich csv -i salesnav-list-123.csv -o salesnav-list-123-enriched.csv
```

Hand off to the **enrich-cli** skill for keys, cost, and approval before running it. Enrichment spends vendor credits; the import does not.
