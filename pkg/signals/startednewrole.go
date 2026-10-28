package signals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/linkdapi"
)

// maxAdvisorChecks caps how many candidate positions one row sends to the advisor classifier.
const maxAdvisorChecks = 5

// advisorSystemPrompt is gandalf's excludeAdvisorRoles system prompt, copied verbatim.
const advisorSystemPrompt = `
				You are an agent to classify a titles.
				Your job is to determine if the given title should be excluded.
				
				We need to exclude titles like the following: "Advisor", "Board Member", "Mentor"

				# Examples:
				Role: "Mentor"
				Output: {"should_exclude_title": true}

				Role: "Consultant"
				Output: {"should_exclude_title": true}

				Role: "Software Developer"
				Output: {"should_exclude_title": false}

				Role: "Guest Lecturer"
				Output: {"should_exclude_title": true}

				Role: "Senior Account Executive"
				Output: {"should_exclude_title": false}

				Role: "VP of sales"
				Output: {"should_exclude_title": false}

				Role: "Investor"
				Output: {"should_exclude_title": true}

				Role: "Trustee"
				Output: {"should_exclude_title": true}
			`

// RunStartedNewRole reports whether the person started a current role inside the signal's
// started_new_job_within window (a port of gandalf's computePersonStartedNewRole).
func RunStartedNewRole(ctx context.Context, s Signal, r Record, li *linkdapi.Client, openAIKey string, now time.Time) Result {
	if s.Config.Type != TypeStartedNewRole {
		return failed(fmt.Errorf("RunStartedNewRole: signal type %q is not started_new_role", s.Config.Type))
	}
	profile, res, ok := fetchProfile(ctx, s, r, li, now)
	if !ok {
		return res
	}

	windowStart, err := WindowStart(s.Config.StartedNewRoleConfig.StartedNewJobWithin, now)
	if err != nil {
		return failed(err)
	}

	type candidate struct {
		company string
		role    linkdapi.Role
	}
	var candidates []candidate
	for _, exp := range profile.Experience {
		for _, role := range exp.Roles {
			if !role.Current || role.Start.IsZero() || role.Start.Before(windowStart) {
				continue
			}
			candidates = append(candidates, candidate{exp.CompanyName, role})
			break // one candidate per employer, as in gandalf
		}
	}

	var classifyErr error
	for i, c := range candidates {
		if s.Config.StartedNewRoleConfig.ShouldExcludeAdvisorRoles {
			if i >= maxAdvisorChecks {
				break
			}
			exclude, err := isAdvisorTitle(ctx, openAIKey, c.role.Title)
			if err != nil {
				classifyErr = err
				continue // gandalf skips a position it could not classify
			}
			if exclude {
				continue
			}
		}
		name := strings.TrimSpace(r.Get("FirstName") + " " + r.Get("LastName"))
		if name == "" {
			name = strings.TrimSpace(profile.FirstName + " " + profile.LastName)
		}
		return Result{
			Status:  StatusSuccess,
			Value:   fmt.Sprintf("%s - %s started role %q at %s", c.role.Start.Format("2006-01-02"), name, c.role.Title, c.company),
			Sources: []Source{{URL: profileURL(profile, r), Title: "LinkedIn Profile"}},
		}
	}
	if classifyErr != nil {
		// A position that could not be classified may have been the hit: not a reliable miss.
		return failed(classifyErr)
	}
	return Result{Status: StatusNotFound, Sources: []Source{}}
}

// fetchProfile runs the shared checks for person LinkedIn types (precondition, dependencies,
// a profile URL) and buys the profile. ok is false when res is the final result.
func fetchProfile(ctx context.Context, s Signal, r Record, li *linkdapi.Client, now time.Time) (linkdapi.Profile, Result, bool) {
	fields := r.MergeFields(now)
	if s.Config.HasPrecondition && !AssessPrecondition(fields, s.Config.Precondition) {
		return linkdapi.Profile{}, Result{Status: StatusPreconditionFailed}, false
	}
	if missing := MissingDependencies(s, r); len(missing) > 0 {
		return linkdapi.Profile{}, Result{Status: StatusMissingDependencies, Error: "missing: " + strings.Join(missing, ", ")}, false
	}
	username, ok := linkdapi.UsernameFromURL(r.Get("LinkedInUrl"))
	if !ok {
		return linkdapi.Profile{}, Result{Status: StatusMissingDependencies, Error: "missing: LinkedInUrl (not a LinkedIn profile URL)"}, false
	}
	profile, err := li.Profile(ctx, username)
	if errors.Is(err, linkdapi.ErrNotFound) {
		return linkdapi.Profile{}, Result{Status: StatusNotFound, Error: err.Error(), Sources: []Source{}}, false
	}
	if err != nil {
		return linkdapi.Profile{}, failed(err), false
	}
	return profile, Result{}, true
}

func profileURL(p linkdapi.Profile, r Record) string {
	username := p.Username
	if username == "" {
		username, _ = linkdapi.UsernameFromURL(r.Get("LinkedInUrl"))
	}
	return "https://www.linkedin.com/in/" + username
}

// isAdvisorTitle asks the classifier whether a title is an advisor/board/mentor-type role.
func isAdvisorTitle(ctx context.Context, apiKey, title string) (bool, error) {
	content, err := chatJSON(ctx, apiKey, ClassifierModel, []map[string]string{
		{"role": "system", "content": advisorSystemPrompt},
		{"role": "user", "content": fmt.Sprintf(`Role: "%s"`, title)},
	}, map[string]any{"type": "json_object"})
	if err != nil {
		return false, err
	}
	var verdict struct {
		ShouldExcludeTitle bool `json:"should_exclude_title"`
	}
	if err := json.Unmarshal([]byte(content), &verdict); err != nil {
		return false, errors.New("openai: invalid classifier output")
	}
	return verdict.ShouldExcludeTitle, nil
}
