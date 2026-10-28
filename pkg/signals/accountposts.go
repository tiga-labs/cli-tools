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

// maxCompanyPostPages caps company-post paging (10 posts a page, 1 credit each).
const maxCompanyPostPages = 3

// Gandalf's hired_executive post categories; only executiveHired is a hit.
const (
	executiveHired          = "executive_hired"
	executiveAttendedEvent  = "executive_attended_event"
	executiveSpokeAtEvent   = "executive_spoke_at_event"
	executiveReceivedAward  = "executive_received_award"
	executiveOther          = "executive_other"
	notAboutExecutive       = "not_about_executive"
	executiveClassifierName = "hired_executive_post_classification"
)

// RunAccountPosts runs an account linkedin_posts or hired_executive signal on the company's
// recent LinkedIn posts (a port of gandalf's computeCustomColumnUsingLinkedInPostsForAccount).
func RunAccountPosts(ctx context.Context, s Signal, r Record, li *linkdapi.Client, opt GPTOptions) Result {
	isExec := s.Config.Type == TypeHiredExecutive
	if !isExec && (s.Config.Type != TypeLinkedInPosts || !s.Config.IsAccountInsight) {
		return failed(fmt.Errorf("RunAccountPosts: signal %q is not an account post signal", s.Label))
	}
	now := time.Now()
	if opt.Now != nil {
		now = opt.Now()
	}
	id, company, res, ok := fetchCompany(ctx, s, r, li, now)
	if !ok {
		return res
	}
	windowStart, err := dayWindowStart(s.Config.DateRange, now)
	if err != nil {
		return failed(err)
	}
	posts, err := companyPosts(ctx, li, id, windowStart)
	if err != nil {
		return failed(err)
	}

	model, maxTokens := postModels(s, opt)
	prompt := func(p linkdapi.Post) string {
		if isExec {
			return hiredExecutivePrompt(company, s.Config.Prompt, p.Text)
		}
		return postPrompt(`the company "`+company+`"`, s.Config.Prompt, s.Config.FewShotExamples, p.Text)
	}
	classify := func(ctx context.Context, post linkdapi.Post, prompt string) (bool, error) {
		if isExec {
			return classifyExecutivePost(ctx, opt.APIKey, post.Text)
		}
		found, _, err := reasonJSON(ctx, opt.APIKey, ClassifierModel, noSearchInstructions, prompt, maxTokens)
		return found, err
	}
	generate := func(ctx context.Context, prompt string) (bool, string, error) {
		return reasonJSON(ctx, opt.APIKey, model, noSearchInstructions, prompt, maxTokens)
	}
	return firstMatchingPost(ctx, inWindow(posts, windowStart), prompt, classify, generate)
}

// companyPosts reads up to maxCompanyPostPages pages, stopping at an empty page or once a page
// reaches back past windowStart.
func companyPosts(ctx context.Context, li *linkdapi.Client, id int, windowStart time.Time) ([]linkdapi.Post, error) {
	var all []linkdapi.Post
	for page := range maxCompanyPostPages {
		posts, err := li.CompanyPosts(ctx, id, page)
		if errors.Is(err, linkdapi.ErrNotFound) {
			break
		}
		if err != nil {
			return nil, err
		}
		all = append(all, posts...)
		if len(posts) == 0 || (!windowStart.IsZero() && posts[len(posts)-1].Date.Before(windowStart)) {
			break
		}
	}
	return all, nil
}

// fetchCompany runs the shared checks for account LinkedIn types (precondition, dependencies,
// a company URL) and resolves the company id. name is AccountName, else the URL slug.
func fetchCompany(ctx context.Context, s Signal, r Record, li *linkdapi.Client, now time.Time) (int, string, Result, bool) {
	if s.Config.HasPrecondition && !AssessPrecondition(r.MergeFields(now), s.Config.Precondition) {
		return 0, "", Result{Status: StatusPreconditionFailed}, false
	}
	if missing := MissingDependencies(s, r); len(missing) > 0 {
		return 0, "", Result{Status: StatusMissingDependencies, Error: "missing: " + strings.Join(missing, ", ")}, false
	}
	slug, ok := linkdapi.CompanySlugFromURL(r.Get("AccountLinkedInUrl"))
	if !ok {
		return 0, "", Result{Status: StatusMissingDependencies, Error: "missing: AccountLinkedInUrl (not a LinkedIn company URL)"}, false
	}
	id, err := li.CompanyID(ctx, slug)
	if errors.Is(err, linkdapi.ErrCompanyNotFound) {
		return 0, "", Result{Status: StatusNotFound, Error: err.Error(), Sources: []Source{}}, false
	}
	if err != nil {
		return 0, "", failed(err), false
	}
	name := strings.TrimSpace(r.Get("AccountName"))
	if name == "" {
		name = slug
	}
	return id, name, Result{}, true
}

// hiredExecutivePrompt is gandalf's hired_executive branch of getPromptForLinkedInAccountPostsSignal.
func hiredExecutivePrompt(companyName, titles, postText string) string {
	return fmt.Sprintf(`
		You are looking at a LinkedIn post from the company "%s".
		I want to know if this post is about whether "%s" has hired any of these executive titles:
		"%s"

		The post would contain language similar to this:
		"We are pleased to announce that John Smith will be our new Chief Financial Officer."
		"Please join me in welcoming our new CEO, John Smith, who is a seasoned leader with a stellar track record."
		"We are delighted to welcome Jane Doe to our company as our new Chief Marketing Officer!"

		If the company has hired an executive, summarize its post and tell me the name of the individual hired.

		LinkedIn Post Text:
		"%s"
	`, companyName, companyName, titles, postText)
}

var executiveClassifierFormat = map[string]any{
	"type": "json_schema",
	"json_schema": map[string]any{
		"name":   executiveClassifierName,
		"strict": true,
		"schema": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"category": map[string]any{
					"type":        "string",
					"description": "The category of the post",
					"enum":        []string{executiveHired, executiveAttendedEvent, executiveSpokeAtEvent, executiveReceivedAward, executiveOther, notAboutExecutive},
				},
				"reason": map[string]any{"type": "string", "description": "The reason for the classification"},
			},
			"required": []string{"category", "reason"},
		},
	},
}

// classifyExecutivePost is gandalf's HIRED_EXECUTIVE classifyPost branch: true only when the
// post is about hiring an executive.
func classifyExecutivePost(ctx context.Context, apiKey, postText string) (bool, error) {
	content, err := chatJSON(ctx, apiKey, ClassifierModel, []map[string]string{
		{"role": "system", "content": `
		You're acting as a research assistant who reads LinkedIn posts. You generate json output based on inputs.
		Your job is to classify the post into 1 of 6 categories.
	`},
		{"role": "user", "content": fmt.Sprintf(`
		Which category does this post belong to?

		LinkedIn Post:
		%s
	`, postText)},
	}, executiveClassifierFormat)
	if err != nil {
		return false, err
	}
	var verdict struct {
		Category string `json:"category"`
	}
	if err := json.Unmarshal([]byte(content), &verdict); err != nil {
		return false, errors.New("openai: invalid classifier output")
	}
	return verdict.Category == executiveHired, nil
}
