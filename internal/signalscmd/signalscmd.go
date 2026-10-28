package signalscmd

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/tiga-labs/cli-tools/pkg/cliflags"
	"github.com/tiga-labs/cli-tools/pkg/csvrun"
	"github.com/tiga-labs/cli-tools/pkg/keystore"
	"github.com/tiga-labs/cli-tools/pkg/linkdapi"
	"github.com/tiga-labs/cli-tools/pkg/resultcache"
	"github.com/tiga-labs/cli-tools/pkg/signals"
)

// Run implements `tiga signals <command>`.
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
	case "run":
		return runRunCommand(ctx, args[1:], stdout, stderr)
	case "csv":
		return runCSVCommand(ctx, args[1:], stdin, stdout, stderr)
	case "cache":
		return runCacheCommand(args[1:], stdout, stderr)
	default:
		printRootUsage(stderr)
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func printRootUsage(w io.Writer) {
	fmt.Fprintln(w, `tiga signals — run AI signals on a contact, an account, or a CSV of them

Usage:
  tiga signals <command> [flags]

Commands:
  run         Run signals against one record (flags) and print JSON or a table
  csv         Run signals against every row of a CSV and append result columns
  cache       Clear stored results (cache clear [--linkedin])

Signals come from definition files (-s file.json, repeatable; the Tiga create-signal body)
or inline flags (--prompt "..." [--label ...], or --type <type>). Types: gpt, started_new_role,
linkedin_posts, hired_executive, hiring_for_role. Every type but gpt needs LinkedIn data:
--buy-linkedin and a linkdapi key.

Manage vendor API keys with "tiga keys".
Run "tiga signals <command> --help" for more information on a command.`)
}

// ============================================================================
// Shared signal and feature flags
// ============================================================================

// multiFlag is a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

type signalFlags struct {
	fs         *flag.FlagSet
	configPath *string
	files      multiFlag
	typ        *string
	label      *string
	prompt     *string
	account    *bool
	wordLimit  *int
	dateRange  *string
	model      *string
	started    *string
	excludeAdv *bool
	jobTitles  multiFlag
	location   *string
	exactMatch *bool
	webSearch  cliflags.Toggle
	buyLI      cliflags.Toggle
	refreshLI  *bool
	noCache    *bool
	boolean    cliflags.Toggle
	verify     cliflags.Toggle
}

func bindSignalFlags(fs *flag.FlagSet) *signalFlags {
	f := &signalFlags{fs: fs}
	f.configPath = fs.String("config", "", "Path to config file (default: ~/.config/tiga/config.json)")
	fs.Var(&f.files, "s", "Signal definition JSON file (repeatable; runs in the order given)")
	fs.Var(&f.files, "signal", "Same as -s")
	f.typ = fs.String("type", "", "Inline signal type: gpt (default when --prompt is set), started_new_role, linkedin_posts (person; with --account, the company), hired_executive, or hiring_for_role")
	f.label = fs.String("label", "", "Inline signal label (its column key is the label slug)")
	f.prompt = fs.String("prompt", "", "Inline signal prompt, with {{.Field}} merge fields")
	f.account = fs.Bool("account", false, "Inline signal is an account signal")
	f.wordLimit = fs.Int("word-limit", 0, "Inline signal output word limit")
	f.dateRange = fs.String("date-range", "", "Inline linkedin_posts/hired_executive date window (e.g. THREE_MONTHS; one of "+strings.Join(signals.Windows, ", ")+")")
	f.started = fs.String("started-within", "", "Inline started_new_role window (e.g. THREE_MONTHS; default THIS_YEAR)")
	f.excludeAdv = fs.Bool("exclude-advisor-roles", false, "Inline started_new_role: skip advisor/board/mentor roles (OpenAI call per role)")
	fs.Var(&f.jobTitles, "job-title", "Inline hiring_for_role job title to search (repeatable, up to 10; first title with jobs wins)")
	f.location = fs.String("location", "", "Inline hiring_for_role US state code (e.g. WA; default: anywhere in the US)")
	f.exactMatch = fs.Bool("exact-match", false, "Inline hiring_for_role: keep only jobs whose title contains the search term")
	f.buyLI = cliflags.BindToggle(fs, "buy-linkedin", false, "Allow paid linkdapi calls for LinkedIn data (LinkedIn signal types and {{.PersonLi_*}} fields; cached on disk)", "Never call linkdapi")
	f.noCache = fs.Bool("no-cache", false, "Ignore stored results and recompute (the new results are stored)")
	f.refreshLI = fs.Bool("refresh-linkedin", false, "Ignore the LinkedIn data cache and refetch (still paid per profile)")
	f.model = fs.String("model", "", "Research model for every gpt signal (default: the definition's, then "+signals.DefaultGPTModel+")")
	f.webSearch = cliflags.BindToggle(fs, "web-search", true, "Let gpt signals search the web (hits need sources)", "Disable web search; hits may have no sources")
	f.boolean = cliflags.BindToggle(fs, "bool", true, "Classify each hit as found/not found with "+signals.ClassifierModel+"; not found becomes status 2 (one small extra call per hit)", "Disable classification: any sourced answer, even \"no\", stays status 1")
	f.verify = cliflags.BindToggle(fs, "verify", false, "Check each hit against its sources; unsupported becomes status 2 (extra OpenAI call)", "Disable verification")
	return f
}

func (f *signalFlags) loadSignals() ([]signals.Signal, error) {
	var out []signals.Signal
	for _, path := range f.files {
		s, err := signals.LoadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	inline := strings.TrimSpace(*f.prompt) != "" || strings.TrimSpace(*f.typ) != ""
	if !inline {
		var orphans []string
		f.fs.Visit(func(fl *flag.Flag) {
			switch fl.Name {
			case "label", "account", "word-limit", "date-range", "started-within", "exclude-advisor-roles", "job-title", "location", "exact-match":
				orphans = append(orphans, "--"+fl.Name)
			}
		})
		if len(orphans) > 0 {
			return nil, fmt.Errorf("%s describe an inline signal and need --prompt (they do not change -s files)", strings.Join(orphans, ", "))
		}
	}
	if inline {
		typ := strings.TrimSpace(*f.typ)
		if typ == "" {
			typ = signals.TypeGPT
		}
		s, err := signals.FromInline(signals.Inline{
			Type:                typ,
			Label:               *f.label,
			Prompt:              *f.prompt,
			Account:             *f.account,
			WordLimit:           *f.wordLimit,
			DateRange:           *f.dateRange,
			StartedWithin:       strings.TrimSpace(*f.started),
			ExcludeAdvisorRoles: *f.excludeAdv,
			JobTitles:           f.jobTitles,
			Location:            strings.TrimSpace(*f.location),
			ExactMatch:          *f.exactMatch,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// newRunner loads keys and signals and validates the run before any work starts. A dry run
// needs no API keys.
func (f *signalFlags) newRunner(dryRun bool) (*signals.Runner, error) {
	cfg, err := keystore.Load(*f.configPath, signals.Vendors)
	if err != nil {
		return nil, err
	}
	sigs, err := f.loadSignals()
	if err != nil {
		return nil, err
	}
	return signals.NewRunner(sigs, signals.RunOptions{
		OpenAIKey:       cfg.GetKey(signals.VendorOpenAI),
		LinkdAPIKey:     cfg.GetKey(signals.VendorLinkdAPI),
		Model:           strings.TrimSpace(*f.model),
		NoWebSearch:     !f.webSearch.Value(),
		Bool:            f.boolean.Value(),
		Verify:          f.verify.Value(),
		BuyLinkedIn:     f.buyLI.Value(),
		RefreshLinkedIn: *f.refreshLI,
		DryRun:          dryRun,
		ResultCache:     true,
		NoCache:         *f.noCache,
		Redact:          cfg.RedactSecrets,
	})
}

// setUsage prints a usage line, the standard merge fields, and the status codes before the
// flag list.
func setUsage(fs *flag.FlagSet, usage string, stderr io.Writer) {
	fs.Usage = func() {
		fmt.Fprintf(stderr, `Usage:
  %s

Standard fields (for {{.Field}} in prompts and --col Field=Header; any CSV column also works as {{.column}}):
  Person:  %s
  Account: %s
  With --buy-linkedin: PersonLi_* (Headline, About, Experience, Posts, RecentPosts, ...) and CompanyLi_Posts, CompanyLi_RecentPosts

Status codes (the _status column / "status"):
  1 success  2 not_found  3 missing_dependencies  4 failed  5 precondition_failed

Flags:
`, usage, strings.Join(signals.PersonFields, ", "), strings.Join(signals.AccountFields, ", "))
		fs.PrintDefaults()
	}
}

// valueFlags lists every flag on fs that takes a value, for SplitPositionalAndFlags.
func valueFlags(fs *flag.FlagSet) map[string]bool {
	m := map[string]bool{}
	fs.VisitAll(func(fl *flag.Flag) {
		if b, ok := fl.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			return
		}
		m["-"+fl.Name], m["--"+fl.Name] = true, true
	})
	return m
}

func parseFlags(fs *flag.FlagSet, args []string) (positional []string, err error) {
	positional, flagArgs := cliflags.SplitPositionalAndFlags(args, valueFlags(fs))
	return positional, fs.Parse(flagArgs)
}

// ============================================================================
// Subcommand: tiga signals run
// ============================================================================

func runRunCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tiga signals run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setUsage(fs, "tiga signals run (-s <file.json>... | --prompt <text> | --type <type>) <record flags> [flags]", stderr)
	sf := bindSignalFlags(fs)

	recordFlags := []struct{ flag, field, usage string }{
		{"first-name", "FirstName", "Person's first name"},
		{"last-name", "LastName", "Person's last name"},
		{"name", "full_name", "Person's full name (split into first/last when those are empty)"},
		{"email", "EmailAddress", "Person's email address"},
		{"linkedin-url", "LinkedInUrl", "Person's LinkedIn profile URL"},
		{"title", "Title", "Person's job title"},
		{"account-name", "AccountName", "Company name"},
		{"account-domain", "AccountDomain", "Company domain (e.g. acme.com)"},
		{"account-website", "AccountWebsite", "Company website URL"},
		{"account-linkedin", "AccountLinkedInUrl", "Company LinkedIn URL"},
		{"account-industry", "AccountIndustry", "Company industry"},
	}
	values := make([]*string, len(recordFlags))
	for i, rf := range recordFlags {
		values[i] = fs.String(rf.flag, "", rf.usage)
	}
	var extra multiFlag
	fs.Var(&extra, "field", "Any merge field as Key=Value (repeatable), e.g. --field funding_stage=\"Series B\"")
	format := fs.String("format", "json", "Output format: json | table")

	posArgs, err := parseFlags(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if len(posArgs) > 0 {
		return fmt.Errorf("unexpected argument %q", posArgs[0])
	}
	switch strings.ToLower(strings.TrimSpace(*format)) {
	case "json", "table":
	default:
		return fmt.Errorf("unsupported --format %q (expected: json, table)", *format)
	}

	fields := map[string]string{}
	for i, rf := range recordFlags {
		if v := strings.TrimSpace(*values[i]); v != "" {
			fields[rf.field] = v
		}
	}
	for _, kv := range extra {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return fmt.Errorf("invalid --field %q (expected Key=Value)", kv)
		}
		fields[strings.TrimSpace(k)] = v
	}
	if len(fields) == 0 {
		return errors.New("no record given; pass record flags such as --account-name or --field Key=Value")
	}

	runner, err := sf.newRunner(false)
	if err != nil {
		return err
	}
	results := runner.Run(ctx, signals.NewRecord(fields))
	if err := printResults(stdout, *format, runner.Signals(), results); err != nil {
		return err
	}
	return ctx.Err() // an interrupted run exits non-zero (L9)
}

type resultJSON struct {
	Key        string           `json:"key"`
	Label      string           `json:"label"`
	Type       string           `json:"type"`
	Value      string           `json:"value"`
	Status     signals.Status   `json:"status"`
	StatusName string           `json:"status_name"`
	Sources    []signals.Source `json:"sources"`
	Error      string           `json:"error,omitempty"`
	Cached     bool             `json:"cached"`
}

func printResults(w io.Writer, format string, sigs []signals.Signal, results []signals.Result) error {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		out := struct {
			Results []resultJSON `json:"results"`
		}{Results: []resultJSON{}}
		for i, s := range sigs {
			res := results[i]
			sources := res.Sources
			if sources == nil {
				sources = []signals.Source{}
			}
			out.Results = append(out.Results, resultJSON{
				Key: s.Key(), Label: s.Label, Type: s.Config.Type, Value: res.Value,
				Status: res.Status, StatusName: res.Status.String(), Sources: sources, Error: res.Error,
				Cached: res.Cached,
			})
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)

	case "table":
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for i, s := range sigs {
			res := results[i]
			if i > 0 {
				fmt.Fprintln(tw)
			}
			fmt.Fprintf(tw, "Signal:\t%s (%s)\n", s.Label, s.Key())
			fmt.Fprintf(tw, "Status:\t%d %s\n", res.Status, res.Status)
			fmt.Fprintf(tw, "Value:\t%s\n", strings.ReplaceAll(res.Value, "\n", " "))
			for _, src := range res.Sources {
				fmt.Fprintf(tw, "Source:\t%s\n", src.URL)
			}
			if res.Error != "" {
				fmt.Fprintf(tw, "Error:\t%s\n", res.Error)
			}
		}
		return tw.Flush()

	default:
		return fmt.Errorf("unsupported --format %q (expected: json, table)", format)
	}
}

// ============================================================================
// Subcommand: tiga signals csv
// ============================================================================

func runCSVCommand(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tiga signals csv", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setUsage(fs, "tiga signals csv <input.csv|-> (-s <file.json>... | --prompt <text> | --type <type>) [-o out.csv] [--dry-run] [flags]", stderr)
	sf := bindSignalFlags(fs)

	inputPath := fs.String("i", "", "Input CSV file path (or '-' for stdin; also accepts first positional arg)")
	inputLong := fs.String("input", "", "Input CSV file path")
	outputPath := fs.String("o", "", "Output CSV file path (default: stdout)")
	outputLong := fs.String("output", "", "Output CSV file path")
	var cols multiFlag
	fs.Var(&cols, "col", "Map a standard field to a CSV header as Field=Header (repeatable), e.g. --col AccountName=\"Company Legal Name\"")
	concurrency := fs.Int("concurrency", 5, "Number of rows processed in parallel")
	quiet := fs.Bool("quiet", false, "Suppress progress output on stderr")
	dryRun := fs.Bool("dry-run", false, "Print the rows and calls the run would make, as JSON, without calling any vendor")

	posArgs, err := parseFlags(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	inFile := firstNonEmpty(*inputPath, *inputLong)
	if inFile == "" && len(posArgs) > 0 {
		inFile, posArgs = posArgs[0], posArgs[1:]
	}
	if len(posArgs) > 0 {
		return fmt.Errorf("unexpected argument %q", posArgs[0])
	}
	if inFile == "" {
		return errors.New("no input CSV given; pass a file path, or - to read stdin")
	}
	outFile := firstNonEmpty(*outputPath, *outputLong)

	overrides := map[string]string{}
	for _, kv := range cols {
		field, header, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(field) == "" || strings.TrimSpace(header) == "" {
			return fmt.Errorf("invalid --col %q (expected Field=Header)", kv)
		}
		overrides[strings.TrimSpace(field)] = header
	}

	runner, err := sf.newRunner(*dryRun)
	if err != nil {
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

	if *dryRun {
		return printPlan(reader, stdout, runner, overrides)
	}

	appended := appendedHeaders(runner.Signals())
	var (
		mu       sync.Mutex
		finished = map[int][]signals.Result{}
	)
	opts := csvrun.Options{Concurrency: *concurrency, AppendedHeaders: appended}
	if !*quiet {
		opts.OnRowDone = func(completed, total, index int) {
			mu.Lock()
			results := finished[index]
			delete(finished, index)
			mu.Unlock()
			parts := make([]string, len(results))
			for i, res := range results {
				parts[i] = runner.Signals()[i].Key() + "=" + res.Status.String()
				if res.Cached {
					parts[i] += "(cached)"
				}
			}
			fmt.Fprintf(stderr, "[%d/%d] row %d: %s\n", completed, total, index+1, strings.Join(parts, " "))
		}
	}

	prepare := func(headers []string) (csvrun.RowFunc, error) {
		if err := checkColumnClash(headers, appended); err != nil {
			return nil, err
		}
		cm, err := signals.MapColumns(headers, overrides)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, index int, row []string) []string {
			results := runner.Run(ctx, cm.Record(row))
			if !*quiet {
				mu.Lock()
				finished[index] = results
				mu.Unlock()
			}
			return resultCells(results)
		}, nil
	}

	return csvrun.WriteOutput(inFile, outFile, stdout, ".signals-*.csv", func(w io.Writer) error {
		return csvrun.Process(ctx, reader, w, opts, prepare)
	})
}

// appendedHeaders are the result columns, four per signal, in signal order.
func appendedHeaders(sigs []signals.Signal) []string {
	var headers []string
	for _, s := range sigs {
		k := s.Key()
		headers = append(headers, k+"_value", k+"_status", k+"_sources", k+"_error")
	}
	return headers
}

func resultCells(results []signals.Result) []string {
	cells := make([]string, 0, 4*len(results))
	for _, res := range results {
		urls := make([]string, len(res.Sources))
		for i, src := range res.Sources {
			urls[i] = src.URL
		}
		cells = append(cells, res.Value, strconv.Itoa(int(res.Status)), strings.Join(urls, " "), res.Error)
	}
	return cells
}

// checkColumnClash refuses inputs that already have a result column (e.g. a previous output).
func checkColumnClash(headers, appended []string) error {
	existing := map[string]bool{}
	for _, h := range headers {
		existing[csvrun.NormalizeHeader(h)] = true
	}
	for _, a := range appended {
		if existing[a] {
			return fmt.Errorf("input CSV already has a %q column; give the signal a different --label (or label in its file) or drop the old result columns", a)
		}
	}
	return nil
}

func printPlan(r io.Reader, w io.Writer, runner *signals.Runner, overrides map[string]string) error {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	rows, err := cr.ReadAll()
	if err != nil {
		return fmt.Errorf("failed to read CSV: %w", err)
	}
	if len(rows) == 0 {
		return errors.New("CSV input is empty")
	}
	if err := checkColumnClash(rows[0], appendedHeaders(runner.Signals())); err != nil {
		return err
	}
	cm, err := signals.MapColumns(rows[0], overrides)
	if err != nil {
		return err
	}
	recs := make([]signals.Record, 0, len(rows)-1)
	for _, row := range rows[1:] {
		recs = append(recs, cm.Record(row))
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Rows    int               `json:"rows"`
		Signals []signals.PlanRow `json:"signals"`
	}{Rows: len(recs), Signals: runner.Plan(recs)})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// ============================================================================
// Subcommand: tiga signals cache
// ============================================================================

func runCacheCommand(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tiga signals cache", flag.ContinueOnError)
	fs.SetOutput(stderr)
	alsoLinkedIn := fs.Bool("linkedin", false, "Also clear cached LinkedIn data (the next run pays linkdapi again)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, `Usage:
  tiga signals cache clear [--linkedin]

Flags:`)
		fs.PrintDefaults()
	}
	posArgs, err := parseFlags(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if len(posArgs) != 1 || posArgs[0] != "clear" {
		fs.Usage()
		return errors.New("usage: tiga signals cache clear [--linkedin]")
	}
	n, err := resultcache.Clear()
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Removed %d cached result(s)\n", n)
	if *alsoLinkedIn {
		n, err := linkdapi.ClearCache()
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Removed %d cached LinkedIn response(s)\n", n)
	}
	return nil
}
