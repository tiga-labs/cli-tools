package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/tiga-labs/cli-tools/pkg/enrich"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runCLI(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printRootUsage(stderr)
		return nil
	}

	switch args[0] {
	case "-h", "--help", "help":
		printRootUsage(stdout)
		return nil
	case "-v", "--version", "version":
		fmt.Fprintf(stdout, "enrich %s\n", version)
		return nil
	case "keys", "key":
		return runKeysCommand(args[1:], stdout, stderr)
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
	fmt.Fprintln(w, `enrich — Standalone Waterfall Contact Enrichment CLI

Usage:
  enrich <command> [flags]

Commands:
  person      Enrich a single contact via flags and output JSON, table, or CSV
  csv         Run parallel waterfall enrichment against a CSV of contacts
  keys        Manage vendor API keys (set, rm, list)

Supported Waterfall Vendors (in default execution order):
  findymail, prospeo, apollo, lusha, seamless, dropcontact, hunter, datagma, leadiq

Supported Feature Vendors:
  renidly     (LinkedIn URN / handle & company profile resolution)
  zerobounce  (Email deliverability verification)
  openai      (AI person & company name cleaning)

Run "enrich <command> --help" for more information on a command.`)
}

// ============================================================================
// Subcommand: enrich keys
// ============================================================================

func runKeysCommand(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("enrich keys", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "Path to config file (default: ~/.config/enrich/config.json)")
	jsonOut := fs.Bool("json", false, "Output key list as JSON")

	fs.Usage = func() {
		fmt.Fprintln(stderr, `Usage:
  enrich keys list [--json] [--config <path>]
  enrich keys set <vendor> <api-key> [--config <path>]
  enrich keys rm <vendor> [--config <path>]

Vendors:
  Waterfall: findymail, prospeo, apollo, lusha, seamless, dropcontact, hunter, datagma, leadiq
  Features:  renidly, zerobounce, openai`)
	}

	// Allow flags before or after positional arguments
	posArgs, flagArgs := splitPositionalAndFlags(args, map[string]bool{"--config": true, "-config": true})
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	action := "list"
	if len(posArgs) > 0 {
		action = strings.ToLower(posArgs[0])
	}

	cfg, err := enrich.LoadConfig(*configPath)
	if err != nil {
		return err
	}

	switch action {
	case "list", "ls":
		statuses := cfg.ListKeyStatuses()
		if *jsonOut {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(statuses)
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "VENDOR\tCATEGORY\tCONFIGURED\tSOURCE\tKEY")
		for _, s := range statuses {
			cfgStr := "no"
			if s.Configured {
				cfgStr = "yes"
			}
			mask := s.MaskedKey
			if mask == "" {
				mask = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.Vendor, s.Category, cfgStr, s.Source, mask)
		}
		return tw.Flush()

	case "set", "add":
		if len(posArgs) < 3 {
			fs.Usage()
			return errors.New("usage: enrich keys set <vendor> <api-key>")
		}
		vendor := posArgs[1]
		key := posArgs[2]
		if err := cfg.SetKey(vendor, key); err != nil {
			return err
		}
		if err := enrich.SaveConfig(*configPath, cfg); err != nil {
			return err
		}
		norm, _ := enrich.NormalizeVendorName(vendor)
		fmt.Fprintf(stdout, "Saved API key for %s (%s)\n", norm, enrich.MaskKey(key))
		return nil

	case "rm", "remove", "delete", "del":
		if len(posArgs) < 2 {
			fs.Usage()
			return errors.New("usage: enrich keys rm <vendor>")
		}
		vendor := posArgs[1]
		if err := cfg.RemoveKey(vendor); err != nil {
			return err
		}
		if err := enrich.SaveConfig(*configPath, cfg); err != nil {
			return err
		}
		norm, _ := enrich.NormalizeVendorName(vendor)
		fmt.Fprintf(stdout, "Removed API key for %s from config\n", norm)
		return nil

	default:
		fs.Usage()
		return fmt.Errorf("unknown keys action %q (expected: list, set, rm)", action)
	}
}

// ============================================================================
// Shared RunOptions Flags
// ============================================================================

type runFlagBindings struct {
	configPath      *string
	email           *bool
	noEmail         *bool
	linkedin        *bool
	noLinkedin      *bool
	phone           *bool
	noPhone         *bool
	vendors         *string
	excludeVendors  *string
	resolveURN      *bool
	noResolveURN    *bool
	cleanNames      *bool
	noCleanNames    *bool
	researchCompany *bool
	noResearchComp  *bool
	verifyEmail     *bool
	noVerifyEmail   *bool
	concurrency     *int
}

func bindRunFlags(fs *flag.FlagSet) runFlagBindings {
	defaults := enrich.DefaultRunOptions()
	return runFlagBindings{
		configPath:      fs.String("config", "", "Path to config file (default: ~/.config/enrich/config.json)"),
		email:           fs.Bool("email", defaults.NeedEmail, "Enrich work email address"),
		noEmail:         fs.Bool("no-email", false, "Disable email enrichment"),
		linkedin:        fs.Bool("linkedin", defaults.NeedLinkedIn, "Enrich person LinkedIn URL"),
		noLinkedin:      fs.Bool("no-linkedin", false, "Disable LinkedIn URL enrichment"),
		phone:           fs.Bool("phone", defaults.NeedPhone, "Enrich phone number"),
		noPhone:         fs.Bool("no-phone", false, "Disable phone number enrichment"),
		vendors:         fs.String("vendors", "", "Comma-separated waterfall vendor order (default: all configured keys in priority order)"),
		excludeVendors:  fs.String("exclude-vendors", "", "Comma-separated waterfall vendors to skip for this run"),
		resolveURN:      fs.Bool("resolve-urn", defaults.ResolveURN, "Resolve LinkedIn URN / handle via Renidly API"),
		noResolveURN:    fs.Bool("no-resolve-urn", false, "Disable Renidly LinkedIn URN resolution"),
		cleanNames:      fs.Bool("clean-names", defaults.CleanNames, "Clean person and company names via OpenAI"),
		noCleanNames:    fs.Bool("no-clean-names", false, "Disable OpenAI name cleaning"),
		researchCompany: fs.Bool("research-company", defaults.ResearchCompany, "Resolve company LinkedIn/website via Renidly, Apollo, and website scraping"),
		noResearchComp:  fs.Bool("no-research-company", false, "Disable company research"),
		verifyEmail:     fs.Bool("verify-email", defaults.VerifyEmail, "Verify discovered emails via ZeroBounce"),
		noVerifyEmail:   fs.Bool("no-verify-email", false, "Disable ZeroBounce email verification"),
		concurrency:     fs.Int("concurrency", defaults.Concurrency, "Number of concurrent workers for CSV processing"),
	}
}

func (b runFlagBindings) toRunOptions() enrich.RunOptions {
	opts := enrich.DefaultRunOptions()
	opts.NeedEmail = *b.email && !*b.noEmail
	opts.NeedLinkedIn = *b.linkedin && !*b.noLinkedin
	opts.NeedPhone = *b.phone && !*b.noPhone

	opts.ResolveURN = *b.resolveURN && !*b.noResolveURN
	opts.CleanNames = *b.cleanNames && !*b.noCleanNames
	opts.ResearchCompany = *b.researchCompany && !*b.noResearchComp
	opts.VerifyEmail = *b.verifyEmail && !*b.noVerifyEmail
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
// Subcommand: enrich person
// ============================================================================

func runPersonCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("enrich person", flag.ContinueOnError)
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
// Subcommand: enrich csv
// ============================================================================

func runCSVCommand(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("enrich csv", flag.ContinueOnError)
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

	posArgs, flagArgs := splitPositionalAndFlags(args, valueFlags)
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

	writeToFile := outFile != "" && outFile != "-"
	if writeToFile && inFile != "" && inFile != "-" {
		same, err := isSameFile(inFile, outFile)
		if err != nil {
			return err
		}
		if same {
			return fmt.Errorf("output CSV %q is the same file as the input; choose a different -o path", outFile)
		}
	}

	var writer io.Writer = stdout
	var tmpOut *os.File
	if writeToFile {
		// Write to a temp file beside the target and rename on completion, so an
		// existing output file is never truncated by a run that fails early.
		tmpOut, err = os.CreateTemp(filepath.Dir(outFile), ".enrich-*.csv")
		if err != nil {
			return fmt.Errorf("failed to create output CSV %q: %w", outFile, err)
		}
		defer os.Remove(tmpOut.Name()) // no-op after a successful rename
		defer tmpOut.Close()
		if err := tmpOut.Chmod(0644); err != nil {
			return fmt.Errorf("failed to create output CSV %q: %w", outFile, err)
		}
		writer = tmpOut
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

	runErr := enrich.ProcessCSV(ctx, reader, writer, cfg, opts, overrides, progressFn)
	if tmpOut == nil {
		return runErr
	}
	// Keep partial results on cancellation; discard the temp file on any other failure.
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}
	if err := tmpOut.Close(); err != nil {
		return fmt.Errorf("failed to write output CSV %q: %w", outFile, err)
	}
	if err := os.Rename(tmpOut.Name(), outFile); err != nil {
		return fmt.Errorf("failed to write output CSV %q: %w", outFile, err)
	}
	return runErr
}

// isSameFile reports whether a and b refer to the same file on disk.
func isSameFile(a, b string) (bool, error) {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false, err
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false, err
	}
	if absA == absB {
		return true, nil
	}
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return false, nil
	}
	return os.SameFile(infoA, infoB), nil
}

func validateFeatureKeys(cfg *enrich.Config, opts enrich.RunOptions) error {
	if opts.CleanNames && cfg.GetKey(enrich.VendorOpenAI) == "" {
		return errors.New("--clean-names requires an OpenAI API key (set via `enrich keys set openai <key>` or OPENAI_API_KEY)")
	}
	if opts.VerifyEmail && cfg.GetKey(enrich.VendorZeroBounce) == "" {
		return errors.New("--verify-email requires a ZeroBounce API key (set via `enrich keys set zerobounce <key>` or ZEROBOUNCE_API_KEY)")
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

// splitPositionalAndFlags separates positional arguments from flags so Go's standard
// flag package doesn't stop parsing flags when a positional argument appears first.
func splitPositionalAndFlags(args []string, valueFlags map[string]bool) (positional []string, flags []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			if !strings.Contains(arg, "=") && valueFlags[arg] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			positional = append(positional, arg)
		}
	}
	return positional, flags
}
