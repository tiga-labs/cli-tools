package signals

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/linkdapi"
)

// RunOptions are the per-run settings shared by every signal in a run.
type RunOptions struct {
	OpenAIKey   string
	LinkdAPIKey string
	// Model overrides the model of every gpt signal.
	Model       string
	NoWebSearch bool
	Bool        bool
	Verify      bool
	// BuyLinkedIn allows paid linkdapi calls (off by default, D13); RefreshLinkedIn skips
	// reading the linkdapi disk cache.
	BuyLinkedIn     bool
	RefreshLinkedIn bool
	// DryRun skips the API key checks: a Plan makes no vendor calls.
	DryRun bool
	// Now returns the time used for date merge fields; nil means time.Now.
	Now func() time.Time
	// Redact scrubs secrets from every result error; nil leaves errors as they are.
	Redact func(string) string
}

// Runner runs an ordered list of signals against records. It is safe for concurrent use:
// rows that give a signal identical inputs share one execution, and each LinkedIn profile
// is fetched at most once per run.
type Runner struct {
	signals []Signal
	opt     RunOptions
	li      *linkdapi.Client

	mu     sync.Mutex
	shared map[string]*sharedCall
}

type sharedCall struct {
	once sync.Once
	res  Result
}

// NewRunner validates the run up front, so no row starts before a fixable error is reported.
func NewRunner(signals []Signal, opt RunOptions) (*Runner, error) {
	if len(signals) == 0 {
		return nil, errors.New("at least one signal is required (-s <file> or --prompt)")
	}
	labels := map[string]string{}
	needsOpenAI, needsLinkdAPI := false, false
	for _, s := range signals {
		key := s.Key()
		if prev, dup := labels[key]; dup {
			return nil, fmt.Errorf("signals %q and %q both use column key %q; give one a different label", prev, s.Label, key)
		}
		labels[key] = s.Label
		switch s.Config.Type {
		case TypeGPT:
			needsOpenAI = true
			needsLinkdAPI = needsLinkdAPI || (opt.BuyLinkedIn && len(personLiDeps(s)) > 0)
		case TypeStartedNewRole, TypeLinkedInPosts:
			if s.Config.Type == TypeLinkedInPosts && s.Config.IsAccountInsight {
				return nil, fmt.Errorf("signal %q: type %s (account) is not available in this version yet", s.Label, s.Config.Type)
			}
			if !opt.BuyLinkedIn {
				return nil, fmt.Errorf("signal %q: %s needs LinkedIn data; add --buy-linkedin (spends linkdapi credits)", s.Label, s.Config.Type)
			}
			needsLinkdAPI = true
			needsOpenAI = needsOpenAI || s.Config.Type == TypeLinkedInPosts || s.Config.StartedNewRoleConfig.ShouldExcludeAdvisorRoles
		default:
			return nil, fmt.Errorf("signal %q: type %s is not available in this version yet", s.Label, s.Config.Type)
		}
	}
	if needsOpenAI && !opt.DryRun && strings.TrimSpace(opt.OpenAIKey) == "" {
		return nil, errors.New("these signals require an OpenAI API key (set via `signals keys set openai -` or OPENAI_API_KEY)")
	}
	if needsLinkdAPI && !opt.DryRun && strings.TrimSpace(opt.LinkdAPIKey) == "" {
		return nil, errors.New("LinkedIn data requires a linkdapi API key (set via `signals keys set linkdapi -` or LINKDAPI_API_KEY)")
	}
	return &Runner{
		signals: signals,
		opt:     opt,
		li:      linkdapi.New(opt.LinkdAPIKey, opt.RefreshLinkedIn),
		shared:  map[string]*sharedCall{},
	}, nil
}

// Signals returns the run's signals in order.
func (r *Runner) Signals() []Signal {
	return r.signals
}

func (r *Runner) now() time.Time {
	if r.opt.Now != nil {
		return r.opt.Now()
	}
	return time.Now()
}

// Run returns one result per signal, in order. A hit's value is available to later signals
// as {{.<key>}}; a miss leaves it empty, so a later signal that needs it is status 3.
func (r *Runner) Run(ctx context.Context, rec Record) []Result {
	results := make([]Result, len(r.signals))
	for i, s := range r.signals {
		var res Result
		filled, failure := r.withPersonLi(ctx, s, rec)
		if failure != nil {
			res = *failure
		} else {
			rec = filled
			res = r.runShared(ctx, s, rec)
		}
		if r.opt.Redact != nil && res.Error != "" {
			res.Error = r.opt.Redact(res.Error)
		}
		results[i] = res
		// A miss clears the key, so an input column with the same name never stands in for it.
		value := ""
		if res.Status == StatusSuccess {
			value = res.Value
		}
		rec = rec.With(s.Key(), value)
	}
	return results
}

func (r *Runner) runShared(ctx context.Context, s Signal, rec Record) Result {
	key := shareKey(s, rec)
	r.mu.Lock()
	call, ok := r.shared[key]
	if !ok {
		call = &sharedCall{}
		r.shared[key] = call
	}
	r.mu.Unlock()
	call.once.Do(func() { call.res = r.execute(ctx, s, rec) })
	res := call.res
	res.Sources = slices.Clone(res.Sources) // rows never share a slice
	return res
}

func (r *Runner) execute(ctx context.Context, s Signal, rec Record) Result {
	switch s.Config.Type {
	case TypeStartedNewRole:
		return RunStartedNewRole(ctx, s, rec, r.li, r.opt.OpenAIKey, r.now())
	case TypeLinkedInPosts:
		return RunPersonPosts(ctx, s, rec, r.li, r.gptOptions(s))
	default:
		return RunGPT(ctx, s, rec, r.gptOptions(s))
	}
}

// personLiDeps returns the PersonLi_* fields a signal's prompt uses.
func personLiDeps(s Signal) []string {
	var deps []string
	for _, d := range s.Config.RequiredDependencies {
		if strings.HasPrefix(d, personLiPrefix) {
			deps = append(deps, d)
		}
	}
	return deps
}

// withPersonLi fills a gpt signal's empty PersonLi_* fields from the person's LinkedIn
// profile when --buy-linkedin is on. A missing or unknown profile leaves them empty (so the
// signal is status 3); any other fetch failure is returned as the signal's result.
func (r *Runner) withPersonLi(ctx context.Context, s Signal, rec Record) (Record, *Result) {
	if !r.opt.BuyLinkedIn || s.Config.Type != TypeGPT {
		return rec, nil
	}
	var needed []string
	for _, d := range personLiDeps(s) {
		if strings.TrimSpace(rec.Get(d)) == "" {
			needed = append(needed, d)
		}
	}
	if len(needed) == 0 {
		return rec, nil
	}
	username, ok := linkdapi.UsernameFromURL(rec.Get("LinkedInUrl"))
	if !ok {
		return rec, nil
	}
	profile, err := r.li.Profile(ctx, username)
	if errors.Is(err, linkdapi.ErrNotFound) {
		return rec, nil
	}
	if err != nil {
		res := failed(err)
		return rec, &res
	}
	var posts []linkdapi.Post
	if slices.ContainsFunc(needed, func(d string) bool { return slices.Contains(personLiPostFields, d) }) {
		if posts, err = r.li.Posts(ctx, profile.URN); err != nil && !errors.Is(err, linkdapi.ErrNotFound) {
			res := failed(err)
			return rec, &res
		}
		if posts == nil {
			posts = []linkdapi.Post{}
		}
	}
	fields := PersonLiFields(profile, posts, r.now())
	for _, d := range needed {
		rec = rec.With(d, fields[d])
	}
	return rec, nil
}

func (r *Runner) gptOptions(s Signal) GPTOptions {
	return GPTOptions{
		APIKey:    r.opt.OpenAIKey,
		Model:     r.opt.Model,
		WebSearch: s.WebSearch() && !r.opt.NoWebSearch,
		Bool:      r.opt.Bool,
		Verify:    r.opt.Verify,
		Now:       r.opt.Now,
	}
}

// shareKey identifies a signal's inputs on a record: its key, its required field values, and
// its precondition field's value. Rows with equal keys get the same result.
func shareKey(s Signal, rec Record) string {
	parts := []string{s.Key()}
	for _, dep := range s.Config.RequiredDependencies {
		parts = append(parts, rec.Get(dep))
	}
	if s.Config.HasPrecondition {
		parts = append(parts, rec.Get(preconditionField(s.Config.Precondition)))
	}
	return strings.Join(parts, "\x00")
}

func preconditionField(p Precondition) string {
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(p.Property), "{{."), "}}")
}

// PlanRow is a dry-run estimate for one signal.
type PlanRow struct {
	Key                    string `json:"key"`
	Type                   string `json:"type"`
	Model                  string `json:"model,omitempty"`
	WebSearch              bool   `json:"web_search"`
	Bool                   bool   `json:"bool"`
	Verify                 bool   `json:"verify"`
	RowsReady              int    `json:"rows_ready"`
	RowsMissingDeps        int    `json:"rows_missing_deps"`
	RowsPreconditionFailed int    `json:"rows_precondition_failed"`
	ResearchCalls          int    `json:"research_calls"`
	MaxOpenAICalls         int    `json:"max_openai_calls"`
	LinkdAPICalls          int    `json:"linkdapi_calls"`
}

// Plan estimates a run over recs without any HTTP call (the linkdapi disk cache is read, never
// the network). Earlier signals are assumed to hit, so the counts are upper bounds.
func (r *Runner) Plan(recs []Record) []PlanRow {
	now := r.now()
	plan := make([]PlanRow, len(r.signals))
	unique := make([]map[string]bool, len(r.signals))
	profiles := make([]map[string]bool, len(r.signals)) // usernames this signal fetches
	for i, s := range r.signals {
		plan[i] = PlanRow{Key: s.Key(), Type: s.Config.Type}
		if s.Config.Type == TypeGPT {
			opt := r.gptOptions(s)
			plan[i].Model = firstNonEmpty(opt.Model, s.Config.Model, DefaultGPTModel)
			plan[i].WebSearch = opt.WebSearch
			plan[i].Bool = opt.Bool || s.Config.ShouldClassifyOutputAsBool
			plan[i].Verify = opt.Verify || s.Config.AdvancedModelVerify
		}
		unique[i] = map[string]bool{}
		profiles[i] = map[string]bool{}
	}

	for _, rec := range recs {
		earlier := map[string]bool{}
		for i, s := range r.signals {
			skip := func(counter *int) {
				*counter++
				earlier[s.Key()] = true
				rec = rec.With(s.Key(), "")
			}
			// A precondition on an earlier hit's value is assumed to pass (upper bound).
			field := preconditionField(s.Config.Precondition)
			assumed := earlier[field] && rec.Get(field) != ""
			if s.Config.HasPrecondition && !assumed && !AssessPrecondition(rec.MergeFields(now), s.Config.Precondition) {
				skip(&plan[i].RowsPreconditionFailed)
				continue
			}
			username, validURL := linkdapi.UsernameFromURL(rec.Get("LinkedInUrl"))
			usesLinkedIn := s.Config.Type == TypeStartedNewRole || s.Config.Type == TypeLinkedInPosts
			planned := rec
			if r.opt.BuyLinkedIn && s.Config.Type == TypeGPT && validURL {
				// Stand in for fetched PersonLi_* fields.
				for _, d := range personLiDeps(s) {
					if strings.TrimSpace(planned.Get(d)) == "" {
						planned = planned.With(d, "\x01"+username)
						usesLinkedIn = true
					}
				}
			}
			if len(MissingDependencies(s, planned)) > 0 || (usesLinkedIn && !validURL) {
				skip(&plan[i].RowsMissingDeps)
				continue
			}
			earlier[s.Key()] = true
			plan[i].RowsReady++
			key := shareKey(s, planned)
			unique[i][key] = true
			if usesLinkedIn {
				profiles[i][username] = true
			}
			// Stand in for the hit's value: equal inputs give equal values, so later
			// signals' sharing is counted exactly.
			rec = rec.With(s.Key(), "\x01"+key)
		}
	}
	for i, s := range r.signals {
		plan[i].ResearchCalls = len(unique[i])
		switch s.Config.Type {
		case TypeStartedNewRole:
			if s.Config.StartedNewRoleConfig.ShouldExcludeAdvisorRoles {
				plan[i].MaxOpenAICalls = plan[i].ResearchCalls * maxAdvisorChecks
			}
		case TypeLinkedInPosts:
			plan[i].MaxOpenAICalls = plan[i].ResearchCalls * (maxPostsClassified + 1)
		default:
			perCall := 1
			if plan[i].Bool {
				perCall++
			}
			if plan[i].Verify {
				perCall++
			}
			plan[i].MaxOpenAICalls = plan[i].ResearchCalls * perCall
		}
		needsPosts := s.Config.Type == TypeLinkedInPosts ||
			slices.ContainsFunc(personLiDeps(s), func(d string) bool { return slices.Contains(personLiPostFields, d) })
		for username := range profiles[i] {
			profile, cached := r.li.CachedProfile(username)
			if !cached {
				plan[i].LinkdAPICalls++
				if needsPosts {
					plan[i].LinkdAPICalls++
				}
				continue
			}
			if needsPosts && !r.li.Cached(linkdapi.KindPosts, profile.URN) {
				plan[i].LinkdAPICalls++
			}
		}
	}
	return plan
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
