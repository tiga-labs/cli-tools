package main

import (
	"context"
	"fmt"
	"io"
)

// helpText is the one-screen-per-group reference printed by `tiga help`. help_test.go checks
// that every flag it names exists on a real command, so keep flag names exact.
const helpText = `tiga — contact enrichment and AI signals from the command line

Usage: tiga <command> [<subcommand>] [flags]
Flags may come before or after positional arguments. Every command takes --config <path>.

ENRICH — find work emails, LinkedIn URLs and phones across a waterfall of vendors

  tiga enrich person <who> [<company>] [<what to find>] [--format json|table|csv]
    who            --name "Jane Doe" | --first-name / --last-name, --email-address,
                   --linkedin-url, --urn, --title, --phone-number
    company        --company, --domain, --website, --company-linkedin
    what to find   --email (on), --linkedin (on), --phone (off); turn off with --no-email etc.
    paid extras    --verify-email (ZeroBounce), --clean-names (OpenAI),
                   --resolve-urn (Renidly), --research-company
    vendors        --vendors findymail,prospeo (order to try), --exclude-vendors apollo

  tiga enrich csv -i <in.csv|-> [-o out.csv] [flags]
    Same "what to find", paid extras and vendor flags as person, plus:
    columns        --col-name, --col-first-name, --col-last-name, --col-email, --col-linkedin,
                   --col-company, --col-domain, --col-website (headers are auto-detected)
    run            --concurrency 5, --quiet

  Examples:
    tiga enrich person --name "Jane Doe" --domain acme.com --format table
    tiga enrich csv -i contacts.csv -o enriched.csv --phone --verify-email

SIGNALS — ask AI research questions about contacts and accounts

  tiga signals run <signal> <record> [flags]
  tiga signals csv <in.csv|-> <signal> [-o out.csv] [--dry-run] [flags]
    signal         -s funding.json (repeatable; a signal definition file)
                   --prompt "Has {{.AccountName}} raised funding?" [--label funding] [--account]
                   --type started_new_role|linkedin_posts|hired_executive|hiring_for_role
                     with --started-within, --date-range, --job-title, --location,
                     --exact-match, --exclude-advisor-roles
    record (run)   --first-name, --last-name, --email, --linkedin-url, --title,
                   --account-name, --account-domain, --account-website, --account-linkedin,
                   --field Key=Value
    columns (csv)  --col AccountName="Company" (repeatable; headers are auto-detected)
    research       --web-search (on), --bool (on; classify hits), --verify (off),
                   --model, --word-limit
    LinkedIn       --buy-linkedin (paid linkdapi calls), --refresh-linkedin
    output         run: --format json|table; csv: -o, --dry-run (plan and cost, no calls),
                   --concurrency, --quiet, --no-cache

  tiga signals cache clear [--linkedin]
    Delete stored results (with --linkedin, also the cached LinkedIn data).

  Examples:
    tiga signals run --account-name Stripe --account-domain stripe.com --prompt "Has {{.AccountName}} raised funding {{.Last90Days}}?"
    tiga signals csv accounts.csv -s funding.json --dry-run

KEYS — vendor API keys for every command, stored in ~/.config/tiga/config.json

  tiga keys list [--json]           which vendors are set (keys masked)
  tiga keys set <vendor> -          read the key from stdin (keeps it out of shell history)
  tiga keys set <vendor> <key>
  tiga keys rm <vendor>
    enrich vendors   findymail, prospeo, apollo, lusha, seamless, dropcontact, hunter,
                     datagma, leadiq, renidly, zerobounce, openai
    signals vendors  openai, linkdapi
    Environment variables such as OPENAI_API_KEY override the file.

SKILLS — install the tiga skills for Claude Code and Codex

  tiga skills install [--claude] [--codex] [--project]
                    ~/.claude/skills (Claude Code) and ~/.agents/skills (Codex);
                    --project installs under the current directory; asks if no agent is given
  tiga skills list [--project]      what is installed, and whether it matches this tiga
  tiga skills uninstall [--claude] [--codex] [--project]

OTHER

  tiga version                      print the version
  tiga help <command> [<sub>]       full flag list, e.g. tiga help signals csv
`

func printHelp(w io.Writer) {
	fmt.Fprint(w, helpText)
}

// runHelp prints the overview, or the full --help of `tiga <args...>` on stdout.
func runHelp(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		printHelp(stdout)
		return nil
	}
	return runCLI(ctx, append(append([]string{}, args...), "--help"), stdin, stdout, stdout)
}
