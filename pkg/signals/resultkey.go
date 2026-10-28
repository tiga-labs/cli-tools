package signals

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/linkdapi"
	"github.com/tiga-labs/cli-tools/pkg/resultcache"
)

// cacheLogicVersion is part of every result key. Bump it when a runner's prompts, models, or
// rules change in a way that changes results, so an upgraded CLI does not serve old answers.
var cacheLogicVersion = "2" // 2: JSON-path answers are plain text, not HTML

// resultKey identifies a stored result: the normalized definition without its label, the run
// settings that change the result, and the row's inputs (the share key without the signal's
// column key). Date merge fields are left out; expiry handles staleness.
func (r *Runner) resultKey(s Signal, rec Record) string {
	cfg, _ := json.Marshal(s.Config)
	opt := r.gptOptions(s)
	model, web, boolean, verify := "", false, false, false
	switch s.Config.Type {
	case TypeGPT:
		model = firstNonEmpty(opt.Model, s.Config.Model, DefaultGPTModel)
		web = opt.WebSearch
		boolean = opt.Bool || s.Config.ShouldClassifyOutputAsBool
		verify = opt.Verify || s.Config.AdvancedModelVerify
	case TypeLinkedInPosts, TypeHiredExecutive:
		model, _ = postModels(s, opt)
	}
	return resultcache.Key("logic="+cacheLogicVersion, "classifier="+ClassifierModel, string(cfg), "model="+model,
		"web="+strconv.FormatBool(web), "bool="+strconv.FormatBool(boolean), "verify="+strconv.FormatBool(verify),
		"window="+r.windowDay(s), inputsKey(s, rec))
}

// windowDay is the first day of a LinkedIn type's date window, so a stored hit is not served
// once its role or post has fallen out of the window. gpt keys do not change by day (R1).
func (r *Runner) windowDay(s Signal) string {
	window := ""
	switch s.Config.Type {
	case TypeStartedNewRole:
		window = s.Config.StartedNewRoleConfig.StartedNewJobWithin
	case TypeLinkedInPosts, TypeHiredExecutive:
		window = s.Config.DateRange
	}
	if window == "" {
		return ""
	}
	start, err := dayWindowStart(window, r.now())
	if err != nil {
		return ""
	}
	return start.Format("2006-01-02")
}

// usesLinkedInData reports whether a signal's result depends on fetched LinkedIn data.
func (r *Runner) usesLinkedInData(s Signal) bool {
	return s.Config.Type != TypeGPT || (r.opt.BuyLinkedIn && len(linkedInDeps(s)) > 0)
}

// resultTTL is how long a result stays valid: the definition's expiration_in_days, capped at
// the lifetime of the LinkedIn data it was computed from.
func (r *Runner) resultTTL(s Signal) time.Duration {
	ttl := time.Duration(s.Config.ExpirationInDays) * 24 * time.Hour
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	var data time.Duration
	switch s.Config.Type {
	case TypeStartedNewRole:
		data = linkdapi.ProfileTTL
	case TypeLinkedInPosts, TypeHiredExecutive:
		data = linkdapi.PostsTTL
	case TypeHiringForRole:
		data = linkdapi.JobsTTL
	default:
		for _, d := range linkedInDeps(s) {
			t := linkdapi.ProfileTTL
			if usesPosts([]string{d}) {
				t = linkdapi.PostsTTL
			}
			if data == 0 || t < data {
				data = t
			}
		}
	}
	if data > 0 && data < ttl {
		return data
	}
	return ttl
}

// readsCache reports whether this run may use stored results for s.
func (r *Runner) readsCache(s Signal) bool {
	return r.opt.ResultCache && !r.opt.NoCache && !(r.opt.RefreshLinkedIn && r.usesLinkedInData(s))
}

// cachedResult returns a stored result for s on rec.
func (r *Runner) cachedResult(s Signal, rec Record) (Result, bool) {
	if !r.readsCache(s) {
		return Result{}, false
	}
	raw, ok := resultcache.Get(r.resultKey(s, rec), r.now())
	if !ok {
		return Result{}, false
	}
	var res Result
	if err := json.Unmarshal(raw, &res); err != nil || (res.Status != StatusSuccess && res.Status != StatusNotFound) {
		return Result{}, false
	}
	res.Cached = true
	return res, true
}

// storeResult saves found and not-found results; other statuses are recomputed next time.
func (r *Runner) storeResult(s Signal, rec Record, res Result) {
	if !r.opt.ResultCache || (res.Status != StatusSuccess && res.Status != StatusNotFound) {
		return
	}
	if r.opt.Redact != nil && res.Error != "" {
		res.Error = r.opt.Redact(res.Error)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return
	}
	_ = resultcache.Put(r.resultKey(s, rec), raw, r.now().Add(r.resultTTL(s))) // a failed write only costs a recompute
}
