package enrichcmd

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/tiga-labs/cli-tools/pkg/cliflags"
	"github.com/tiga-labs/cli-tools/pkg/csvrun"
	"github.com/tiga-labs/cli-tools/pkg/enrich"
)

// Run implements `tiga enrich <command>`.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printRootUsage(stderr)
		return nil
	}

	switch args[0] {
	case "-h", "--help", "help":
		printRootUsage(stdout)
		return nil
	case "keys", "key":
		return errors.New("keys moved to the top level: use `tiga keys`")
	case "person", "contact":
		return runPersonCommand(ctx, args[1:], stdout, stderr)
	case "csv":
		return runCSVCommand(ctx, args[1:], stdin, stdout, stderr)
	default:
		printRootUsage(stderr)
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func printRootUsage(w io.Writer) {
	fmt.Fprintln(w, `tiga enrich — Standalone Waterfall Contact Enrichment

Usage:
  tiga enrich <command> [flags]

Commands:
  person      Enrich a single contact via flags and output JSON, table, or CSV
  csv         Run parallel waterfall enrichment against a CSV of contacts

Supported Waterfall Vendors (in default execution order):
  findymail, prospeo, apollo, lusha, seamless, dropcontact, hunter, datagma, leadiq

Supported Feature Vendors:
  renidly     (LinkedIn URN / handle & company profile resolution)
  zerobounce  (Email deliverability verification)
  openai      (AI person & company name cleaning)

Manage vendor API keys with "tiga keys".
Run "tiga enrich <command> --help" for more information on a command.`)
}

// ============================================================================
// Shared RunOptions Flags
// ============================================================================

type runFlagBindings struct {
	configPath      *string
	email           cliflags.Toggle
	linkedin        cliflags.Toggle
	phone           cliflags.Toggle
	vendors         *string
	excludeVendors  *string
	resolveURN      cliflags.Toggle
	cleanNames      cliflags.Toggle
	researchCompany cliflags.Toggle
	verifyEmail     cliflags.Toggle
	concurrency     *int
}

func bindRunFlags(fs *flag.FlagSet) runFlagBindings {
	defaults := enrich.DefaultRunOptions()
	return runFlagBindings{
		configPath:      fs.String("config", "", "Path to config file (default: ~/.config/tiga/config.json)"),
		email:           cliflags.BindToggle(fs, "email", defaults.NeedEmail, "Enrich work email address", "Disable email enrichment"),
		linkedin:        cliflags.BindToggle(fs, "linkedin", defaults.NeedLinkedIn, "Enrich person LinkedIn URL", "Disable LinkedIn URL enrichment"),
		phone:           cliflags.BindToggle(fs, "phone", defaults.NeedPhone, "Enrich phone number", "Disable phone number enrichment"),
		vendors:         fs.String("vendors", "", "Comma-separated waterfall vendor order (default: all configured keys in priority order)"),
		excludeVendors:  fs.String("exclude-vendors", "", "Comma-separated waterfall vendors to skip for this run"),
		resolveURN:      cliflags.BindToggle(fs, "resolve-urn", defaults.ResolveURN, "Resolve LinkedIn URN / handle via Renidly API", "Disable Renidly LinkedIn URN resolution"),
		cleanNames:      cliflags.BindToggle(fs, "clean-names", defaults.CleanNames, "Clean person and company names via OpenAI", "Disable OpenAI name cleaning"),
		researchCompany: cliflags.BindToggle(fs, "research-company", defaults.ResearchCompany, "Resolve company LinkedIn/website via Renidly, Apollo, and website scraping", "Disable company research"),
		verifyEmail:     cliflags.BindToggle(fs, "verify-email", defaults.VerifyEmail, "Verify discovered emails via ZeroBounce", "Disable ZeroBounce email verification"),
		concurrency:     fs.Int("concurrency", defaults.Concurrency, "Number of concurrent workers for CSV processing"),
	}
}

func (b runFlagBindings) toRunOptions() enrich.RunOptions {
	opts := enrich.DefaultRunOptions()
	opts.NeedEmail = b.email.Value()
	opts.NeedLinkedIn = b.linkedin.Value()
	opts.NeedPhone = b.phone.Value()

	opts.ResolveURN = b.resolveURN.Value()
	opts.CleanNames = b.cleanNames.Value()
	opts.ResearchCompany = b.researchCompany.Value()
	opts.VerifyEmail = b.verifyEmail.Value()
	opts.Concurrency = *b.concurrency

	if strings.TrimSpace(*b.vendors) != "" {
		opts.Vendors = splitCSVList(*b.vendors)
	}
	if strings.TrimSpace(*b.excludeVendors) != "" {
		opts.Exclude = splitCSVList(*b.excludeVendors)
	}
	return opts
}

func splitCSVList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// ============================================================================
// Subcommand: tiga enrich person
// ============================================================================

func runPersonCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tiga enrich person", flag.ContinueOnError)
	fs.SetOutput(stderr)

	rb := bindRunFlags(fs)

	firstName := fs.String("first-name", "", "Contact's first name")
	lastName := fs.String("last-name", "", "Contact's last name")
	fullName := fs.String("name", "", "Contact's full name (if first/last not split)")
	emailAddr := fs.String("email-address", "", "Contact's existing email address")
	phoneNum := fs.String("phone-number", "", "Contact's existing phone number")
	personLI := fs.String("linkedin-url", "", "Contact's LinkedIn profile URL")
	urn := fs.String("urn", "", "Contact's LinkedIn profile URN or Renidly entityId")
	title := fs.String("title", "", "Contact's job title")
	company := fs.String("company", "", "Company / account name")
	domain := fs.String("domain", "", "Company domain (e.g. acme.com)")
	website := fs.String("website", "", "Company website URL")
	companyLI := fs.String("company-linkedin", "", "Company LinkedIn URL")
	format := fs.String("format", "json", "Output format: json | table | csv")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	rec := enrich.ContactRecord{
		FirstName:       strings.TrimSpace(*firstName),
		LastName:        strings.TrimSpace(*lastName),
		Email:           strings.TrimSpace(*emailAddr),
		Phone:           strings.TrimSpace(*phoneNum),
		PersonLinkedIn:  strings.TrimSpace(*personLI),
		LinkedInURN:     strings.TrimSpace(*urn),
		Title:           strings.TrimSpace(*title),
		CompanyName:     strings.TrimSpace(*company),
		Domain:          strings.TrimSpace(*domain),
		Website:         strings.TrimSpace(*website),
		CompanyLinkedIn: strings.TrimSpace(*companyLI),
	}

	if rec.FirstName == "" && rec.LastName == "" && strings.TrimSpace(*fullName) != "" {
		parts := strings.Fields(strings.TrimSpace(*fullName))
		if len(parts) == 1 {
			rec.FirstName = parts[0]
		} else if len(parts) > 1 {
			rec.FirstName = parts[0]
			rec.LastName = strings.Join(parts[1:], " ")
		}
	}

	if rec.FirstName == "" && rec.LastName == "" && rec.Email == "" && rec.PersonLinkedIn == "" && rec.LinkedInURN == "" {
		return errors.New("at least one contact identifier (--first-name/--last-name, --name, --email-address, --linkedin-url, or --urn) is required")
	}

	cfg, err := enrich.LoadConfig(*rb.configPath)
	if err != nil {
		return err
	}

	opts := rb.toRunOptions()
	if err := validateFeatureKeys(cfg, opts); err != nil {
		return err
	}

	if err := enrich.EnrichContact(ctx, cfg, opts, &rec); err != nil {
		return err
	}

	switch strings.ToLower(strings.TrimSpace(*format)) {
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rec)

	case "table":
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "First Name:\t%s\n", rec.FirstName)
		fmt.Fprintf(tw, "Last Name:\t%s\n", rec.LastName)
		fmt.Fprintf(tw, "Title:\t%s\n", rec.Title)
		fmt.Fprintf(tw, "Email:\t%s\n", rec.Email)
		fmt.Fprintf(tw, "Email Source:\t%s\n", rec.EmailSource)
		fmt.Fprintf(tw, "Email Verified:\t%s\n", rec.EmailVerified)
		fmt.Fprintf(tw, "Phone:\t%s\n", rec.Phone)
		fmt.Fprintf(tw, "Phone Source:\t%s\n", rec.PhoneSource)
		fmt.Fprintf(tw, "Person LinkedIn:\t%s\n", rec.PersonLinkedIn)
		fmt.Fprintf(tw, "Person LI Source:\t%s\n", rec.PersonLinkedInSource)
		fmt.Fprintf(tw, "Company:\t%s\n", rec.CompanyName)
		fmt.Fprintf(tw, "Domain:\t%s\n", rec.Domain)
		fmt.Fprintf(tw, "Company LinkedIn:\t%s\n", rec.CompanyLinkedIn)
		fmt.Fprintf(tw, "Company LI Source:\t%s\n", rec.CompanyLinkedInSource)
		fmt.Fprintf(tw, "Status:\t%s\n", rec.Status)
		if rec.Error != "" {
			fmt.Fprintf(tw, "Error:\t%s\n", rec.Error)
		}
		return tw.Flush()

	case "csv":
		cw := csv.NewWriter(stdout)
		_ = cw.Write(enrich.AppendedCSVHeaders)
		_ = cw.Write([]string{
			rec.FirstName,
			rec.LastName,
			rec.Email,
			rec.EmailSource,
			rec.EmailVerified,
			rec.Phone,
			rec.PhoneSource,
			rec.PersonLinkedIn,
			rec.PersonLinkedInSource,
			rec.Title,
			rec.CompanyName,
			rec.Domain,
			rec.CompanyLinkedIn,
			rec.CompanyLinkedInSource,
			rec.Status,
			rec.Error,
		})
		cw.Flush()
		return cw.Error()

	default:
		return fmt.Errorf("unsupported --format %q (expected: json, table, csv)", *format)
	}
}

// ============================================================================
// Subcommand: tiga enrich csv
// ============================================================================

func runCSVCommand(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tiga enrich csv", flag.ContinueOnError)
	fs.SetOutput(stderr)

	rb := bindRunFlags(fs)

	inputPath := fs.String("i", "", "Input CSV file path (or '-' for stdin; also accepts first positional arg)")
	inputLong := fs.String("input", "", "Input CSV file path")
	outputPath := fs.String("o", "", "Output CSV file path (default: stdout)")
	outputLong := fs.String("output", "", "Output CSV file path")
	quiet := fs.Bool("quiet", false, "Suppress progress output on stderr")

	colFirst := fs.String("col-first-name", "", "CSV column header for First Name")
	colLast := fs.String("col-last-name", "", "CSV column header for Last Name")
	colFull := fs.String("col-name", "", "CSV column header for Full Name")
	colEmail := fs.String("col-email", "", "CSV column header for Email")
	colPhone := fs.String("col-phone", "", "CSV column header for Phone")
	colLI := fs.String("col-linkedin", "", "CSV column header for Person LinkedIn URL")
	colURN := fs.String("col-urn", "", "CSV column header for LinkedIn URN / Entity ID")
	colCompany := fs.String("col-company", "", "CSV column header for Company Name")
	colDomain := fs.String("col-domain", "", "CSV column header for Company Domain")
	colWebsite := fs.String("col-website", "", "CSV column header for Company Website")
	colCompanyLI := fs.String("col-company-linkedin", "", "CSV column header for Company LinkedIn URL")
	colTitle := fs.String("col-title", "", "CSV column header for Job Title")

	valueFlags := map[string]bool{
		"-i": true, "--i": true, "-input": true, "--input": true,
		"-o": true, "--o": true, "-output": true, "--output": true,
		"-config": true, "--config": true,
		"-vendors": true, "--vendors": true,
		"-exclude-vendors": true, "--exclude-vendors": true,
		"-concurrency": true, "--concurrency": true,
		"-col-first-name": true, "--col-first-name": true,
		"-col-last-name": true, "--col-last-name": true,
		"-col-name": true, "--col-name": true,
		"-col-email": true, "--col-email": true,
		"-col-phone": true, "--col-phone": true,
		"-col-linkedin": true, "--col-linkedin": true,
		"-col-urn": true, "--col-urn": true,
		"-col-company": true, "--col-company": true,
		"-col-domain": true, "--col-domain": true,
		"-col-website": true, "--col-website": true,
		"-col-company-linkedin": true, "--col-company-linkedin": true,
		"-col-title": true, "--col-title": true,
	}

	posArgs, flagArgs := cliflags.SplitPositionalAndFlags(args, valueFlags)
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	inFile := firstNonEmptyStr(*inputPath, *inputLong)
	if inFile == "" && len(posArgs) > 0 {
		inFile = posArgs[0]
	}
	outFile := firstNonEmptyStr(*outputPath, *outputLong)

	cfg, err := enrich.LoadConfig(*rb.configPath)
	if err != nil {
		return err
	}

	opts := rb.toRunOptions()
	if err := validateFeatureKeys(cfg, opts); err != nil {
		return err
	}

	var reader io.Reader = stdin
	if inFile != "" && inFile != "-" {
		f, err := os.Open(inFile)
		if err != nil {
			return fmt.Errorf("failed to open input CSV %q: %w", inFile, err)
		}
		defer f.Close()
		reader = f
	}

	overrides := enrich.ColumnOverrides{
		FirstName:       *colFirst,
		LastName:        *colLast,
		FullName:        *colFull,
		Email:           *colEmail,
		Phone:           *colPhone,
		PersonLinkedIn:  *colLI,
		LinkedInURN:     *colURN,
		CompanyName:     *colCompany,
		Domain:          *colDomain,
		Website:         *colWebsite,
		CompanyLinkedIn: *colCompanyLI,
		Title:           *colTitle,
	}

	var progressFn enrich.ProgressCallback
	if !*quiet {
		progressFn = func(completed, total int, rec enrich.ContactRecord) {
			fmt.Fprintf(stderr, "[%d/%d] %s %s -> status=%s email=%s phone=%s\n",
				completed, total, rec.FirstName, rec.LastName, rec.Status, rec.Email, rec.Phone)
		}
	}

	return csvrun.WriteOutput(inFile, outFile, stdout, ".enrich-*.csv", func(w io.Writer) error {
		return enrich.ProcessCSV(ctx, reader, w, cfg, opts, overrides, progressFn)
	})
}

func validateFeatureKeys(cfg *enrich.Config, opts enrich.RunOptions) error {
	if opts.CleanNames && cfg.GetKey(enrich.VendorOpenAI) == "" {
		return errors.New("--clean-names requires an OpenAI API key (set via `tiga keys set openai <key>` or OPENAI_API_KEY)")
	}
	if opts.VerifyEmail && cfg.GetKey(enrich.VendorZeroBounce) == "" {
		return errors.New("--verify-email requires a ZeroBounce API key (set via `tiga keys set zerobounce <key>` or ZEROBOUNCE_API_KEY)")
	}
	return nil
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
