package signals

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/linkdapi"
)

// maxPostsClassified caps the posts one row sends to the classifier (gandalf has no cap).
const maxPostsClassified = 25

// RunPersonPosts finds the first of the person's recent LinkedIn posts that answers the
// signal's prompt and summarizes it (a port of gandalf's person linkedin_posts signal).
func RunPersonPosts(ctx context.Context, s Signal, r Record, li *linkdapi.Client, opt GPTOptions) Result {
	if s.Config.Type != TypeLinkedInPosts || s.Config.IsAccountInsight {
		return failed(fmt.Errorf("RunPersonPosts: signal %q is not a person linkedin_posts signal", s.Label))
	}
	now := time.Now()
	if opt.Now != nil {
		now = opt.Now()
	}
	profile, res, ok := fetchProfile(ctx, s, r, li, now)
	if !ok {
		return res
	}
	posts, err := li.Posts(ctx, profile.URN)
	if errors.Is(err, linkdapi.ErrNotFound) {
		return Result{Status: StatusNotFound, Sources: []Source{}}
	}
	if err != nil {
		return failed(err)
	}
	windowStart, err := WindowStart(s.Config.DateRange, now)
	if err != nil {
		return failed(err)
	}

	var inRange []linkdapi.Post
	for _, p := range posts {
		if !p.Date.Before(windowStart) {
			inRange = append(inRange, p)
		}
	}
	slices.SortStableFunc(inRange, func(a, b linkdapi.Post) int { return b.Date.Compare(a.Date) }) // newest first
	if len(inRange) > maxPostsClassified {
		inRange = inRange[:maxPostsClassified]
	}

	model := firstNonEmpty(opt.Model, s.Config.Model, DefaultGPTModel)
	maxTokens := s.Config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxOutputTokens
	}
	name := strings.TrimSpace(profile.FirstName + " " + profile.LastName)

	var lastErr error
	for _, post := range inRange {
		prompt := personPostPrompt(name, s.Config.Prompt, s.Config.FewShotExamples, post.Text)
		found, _, err := reasonJSON(ctx, opt.APIKey, ClassifierModel, noSearchInstructions, prompt, maxTokens)
		if err != nil {
			lastErr = err
			continue // gandalf swallows a post it could not classify
		}
		if !found {
			continue
		}
		found, output, err := reasonJSON(ctx, opt.APIKey, model, noSearchInstructions, prompt, maxTokens)
		if err != nil {
			lastErr = err
			continue
		}
		if !found || output == "" {
			continue
		}
		return Result{Status: StatusSuccess, Value: output, Sources: []Source{{URL: post.URL, Title: "LinkedIn Post"}}}
	}
	if lastErr != nil {
		// A post that could not be classified may have been the hit: not a reliable miss.
		return failed(lastErr)
	}
	return Result{Status: StatusNotFound, Sources: []Source{}}
}

// personPostPrompt is gandalf's getPromptForLinkedInPersonPostsSignal text.
func personPostPrompt(personName, userPrompt, fewShot, postText string) string {
	prompt := fmt.Sprintf(`
		You are looking at a LinkedIn post from this person: "%s".
		You are doing research for a user.  Answer the user's prompt:
		"%s"

		If you find what the user is looking for, summarize the post.
		`, personName, userPrompt)
	if fewShot != "" {
		prompt += fmt.Sprintf(`
			The post would contain language similar to this:
			%s
			`, fewShot)
	}
	prompt += fmt.Sprintf(`
		LinkedIn Post Text:
		"%s"
		`, postText)
	return prompt
}
