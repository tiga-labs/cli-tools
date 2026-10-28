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
	windowStart, err := dayWindowStart(s.Config.DateRange, now)
	if err != nil {
		return failed(err)
	}

	model, maxTokens := postModels(s, opt)
	name := strings.TrimSpace(profile.FirstName + " " + profile.LastName)
	prompt := func(p linkdapi.Post) string {
		return postPrompt(`this person: "`+name+`"`, s.Config.Prompt, s.Config.FewShotExamples, p.Text)
	}
	return firstMatchingPost(ctx, inWindow(posts, windowStart), prompt,
		func(ctx context.Context, _ linkdapi.Post, prompt string) (bool, error) {
			found, _, err := reasonJSON(ctx, opt.APIKey, ClassifierModel, noSearchInstructions, prompt, maxTokens)
			return found, err
		},
		func(ctx context.Context, prompt string) (bool, string, error) {
			return reasonJSON(ctx, opt.APIKey, model, noSearchInstructions, prompt, maxTokens)
		})
}

// postModels returns the generation model and output-token limit for a post signal.
func postModels(s Signal, opt GPTOptions) (string, int) {
	maxTokens := s.Config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxOutputTokens
	}
	return firstNonEmpty(opt.Model, s.Config.Model, DefaultGPTModel), maxTokens
}

// inWindow keeps posts on or after start, newest first, capped at maxPostsClassified.
func inWindow(posts []linkdapi.Post, start time.Time) []linkdapi.Post {
	var in []linkdapi.Post
	for _, p := range posts {
		if !p.Date.Before(start) {
			in = append(in, p)
		}
	}
	slices.SortStableFunc(in, func(a, b linkdapi.Post) int { return b.Date.Compare(a.Date) })
	if len(in) > maxPostsClassified {
		in = in[:maxPostsClassified]
	}
	return in
}

// firstMatchingPost classifies posts in order and returns the first whose generation step
// also finds an answer (gandalf's AIGenForLinkedInPost loop). A post whose classification or
// generation errors is skipped; if any errored and none matched, the result is failed, since
// the skipped post may have been the hit.
func firstMatchingPost(
	ctx context.Context,
	posts []linkdapi.Post,
	prompt func(linkdapi.Post) string,
	classify func(ctx context.Context, post linkdapi.Post, prompt string) (bool, error),
	generate func(ctx context.Context, prompt string) (bool, string, error),
) Result {
	var lastErr error
	for _, post := range posts {
		p := prompt(post)
		found, err := classify(ctx, post, p)
		if err != nil {
			lastErr = err
			continue
		}
		if !found {
			continue
		}
		found, output, err := generate(ctx, p)
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
		return failed(lastErr)
	}
	return Result{Status: StatusNotFound, Sources: []Source{}}
}

// postPrompt is gandalf's linkedin_posts prompt (getPromptForLinkedIn{Person,Account}PostsSignal);
// subject is `this person: "<name>"` or `the company "<name>"`.
func postPrompt(subject, userPrompt, fewShot, postText string) string {
	prompt := fmt.Sprintf(`
		You are looking at a LinkedIn post from %s.
		You are doing research for a user.  Answer the user's prompt:
		"%s"

		If you find what the user is looking for, summarize the post.
		`, subject, userPrompt)
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
