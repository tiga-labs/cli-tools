package signals

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/linkdapi"
)

// maxJobSources caps the job listings returned as sources.
const maxJobSources = 10

// RunHiringForRole reports whether the company lists LinkedIn jobs for any of the signal's job
// titles (a port of gandalf's GetJobListingsFromLinkedIn on linkdapi's job search). The first
// title with matching jobs wins.
func RunHiringForRole(ctx context.Context, s Signal, r Record, li *linkdapi.Client, now time.Time) Result {
	if s.Config.Type != TypeHiringForRole {
		return failed(fmt.Errorf("RunHiringForRole: signal type %q is not hiring_for_role", s.Config.Type))
	}
	id, company, res, ok := fetchCompany(ctx, s, r, li, now)
	if !ok {
		return res
	}
	state := strings.ToUpper(strings.TrimSpace(s.Config.HiringForRole.Location))

	for _, title := range jobTitles(s) {
		search, err := li.Jobs(ctx, id, title)
		if err != nil {
			return failed(err)
		}
		var matches []linkdapi.Job
		for _, job := range search.Jobs {
			if !jobInLocation(job.Location, state) {
				continue
			}
			if s.Config.ExactMatchSearch && !strings.Contains(strings.ToLower(job.Title), strings.ToLower(title)) {
				continue
			}
			matches = append(matches, job)
		}
		if len(matches) == 0 {
			continue
		}

		count := fmt.Sprintf("%d", len(matches))
		if len(search.Jobs) >= linkdapi.JobsPageSize {
			count = "at least " + count // one page only; there may be more
		}
		noun := "jobs"
		if len(matches) == 1 {
			noun = "job"
		}
		value := fmt.Sprintf(`As of %s %s has %s %s with the term "%s" `, now.Format("Monday, January 2, 2006"), company, count, noun, title)
		if state != "" {
			value += "located in " + usStates[state] + " "
		}
		value += "listed on LinkedIn."

		sources := []Source{}
		for i, job := range matches {
			if i == maxJobSources {
				break
			}
			sources = append(sources, Source{URL: job.URL, Title: fmt.Sprintf("Job Listing (%d)", i+1)})
		}
		return Result{Status: StatusSuccess, Value: value, Sources: sources}
	}
	return Result{Status: StatusNotFound, Sources: []Source{}}
}

// jobTitles returns the signal's non-empty job titles, trimmed.
func jobTitles(s Signal) []string {
	if s.Config.Type != TypeHiringForRole {
		return nil
	}
	var titles []string
	for _, t := range s.Config.HiringForRole.JobTitles {
		if t = strings.TrimSpace(t); t != "" {
			titles = append(titles, t)
		}
	}
	return titles
}

// metroStates maps LinkedIn metro-area locations (lowercase prefixes) to their state.
var metroStates = map[string]string{
	"greater seattle area":               "WA",
	"seattle metropolitan area":          "WA",
	"san francisco bay area":             "CA",
	"greater los angeles area":           "CA",
	"los angeles metropolitan area":      "CA",
	"greater san diego area":             "CA",
	"new york city metropolitan area":    "NY",
	"greater new york city area":         "NY",
	"washington dc-baltimore area":       "DC",
	"dallas-fort worth metroplex":        "TX",
	"greater houston":                    "TX",
	"austin, texas metropolitan area":    "TX",
	"greater boston":                     "MA",
	"greater chicago area":               "IL",
	"atlanta metropolitan area":          "GA",
	"denver metropolitan area":           "CO",
	"greater philadelphia":               "PA",
	"pittsburgh metropolitan area":       "PA",
	"miami-fort lauderdale area":         "FL",
	"greater orlando":                    "FL",
	"greater tampa bay area":             "FL",
	"greater phoenix area":               "AZ",
	"greater minneapolis-st. paul area":  "MN",
	"detroit metropolitan area":          "MI",
	"portland, oregon metropolitan area": "OR",
	"raleigh-durham-chapel hill area":    "NC",
	"charlotte metro":                    "NC",
	"salt lake city metropolitan area":   "UT",
	"nashville metropolitan area":        "TN",
	"greater st. louis":                  "MO",
}

// jobInLocation matches a LinkedIn job location ("Redmond, WA (Hybrid)", "Seattle, Washington,
// United States", "United States (Remote)", "Greater Seattle Area") against a state code, or
// the US when state is "". The state is the last part, or the part before "United States";
// a location of three or more parts that does not end in "United States" is outside the US
// ("Washington, England, United Kingdom"). "Washington, DC" is DC, not WA.
func jobInLocation(location, state string) bool {
	loc, _, _ := strings.Cut(location, "(")
	lower := strings.ToLower(strings.TrimSpace(loc))
	for metro, code := range metroStates {
		if strings.HasPrefix(lower, metro) {
			return state == "" || state == code
		}
	}
	var parts []string
	for _, p := range strings.Split(loc, ",") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return false
	}
	last := parts[len(parts)-1]
	inUS := strings.EqualFold(last, "United States")
	if len(parts) >= 3 && !inUS {
		return false
	}
	candidate := last
	if inUS {
		candidate = ""
		if len(parts) >= 2 {
			candidate = parts[len(parts)-2]
		}
	}
	code := stateCode(candidate)
	if code == "" {
		return inUS && state == "" // "United States (Remote)" is in the US, but in no one state
	}
	return state == "" || state == code
}

// stateCode returns the US state code for a code ("WA", "D.C.") or a state name, else "".
func stateCode(part string) string {
	if c := strings.ToUpper(strings.ReplaceAll(part, ".", "")); usStates[c] != "" {
		return c
	}
	for c, name := range usStates {
		if strings.EqualFold(part, name) {
			return c
		}
	}
	return ""
}
