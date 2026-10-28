package signals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/tiga-labs/cli-tools/pkg/httpx"
)

// OpenAIBaseURL is the OpenAI API root (a var so tests and proxies can point elsewhere).
var OpenAIBaseURL = "https://api.openai.com/v1"

// ClassifierModel is the cheap model used to classify a research answer as found / not found.
const ClassifierModel = "gpt-5.4-mini"

// maxErrorRunes caps the OpenAI error message copied into an error, so a long body never
// floods a CSV cell.
const maxErrorRunes = 200

// openAIResponse is the subset of a Responses API body the signals use.
type openAIResponse struct {
	Status string `json:"status"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
	IncompleteDetails struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Annotations []struct {
				Type  string `json:"type"`
				URL   string `json:"url"`
				Title string `json:"title"`
			} `json:"annotations"`
		} `json:"content"`
	} `json:"output"`
}

// text joins every output_text part of every message item, in order.
func (r openAIResponse) text() string {
	var b strings.Builder
	for _, item := range r.Output {
		if item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			if part.Type == "output_text" {
				b.WriteString(part.Text)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// sources returns the url_citation annotations, deduplicated by URL, in order.
func (r openAIResponse) sources() []Source {
	sources := []Source{}
	seen := map[string]bool{}
	for _, item := range r.Output {
		if item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			for _, a := range part.Annotations {
				url := strings.TrimSpace(a.URL)
				if a.Type != "url_citation" || url == "" || seen[url] {
					continue
				}
				seen[url] = true
				title := strings.TrimSpace(a.Title)
				if title == "" {
					title = url
				}
				sources = append(sources, Source{URL: url, Title: title})
			}
		}
	}
	return sources
}

// incomplete reports whether OpenAI stopped before finishing, and why (e.g. max_output_tokens).
func (r openAIResponse) incomplete() (bool, string) {
	return r.Status == "incomplete", r.IncompleteDetails.Reason
}

// failure returns an error when a 200 body reports that the response did not complete:
// "incomplete", "failed", "cancelled", or any other non-completed status.
func (r openAIResponse) failure() error {
	if ok, why := r.incomplete(); ok {
		if why == "" {
			why = "unknown"
		}
		return fmt.Errorf("openai: response incomplete (%s)", why)
	}
	if r.Status == "" || r.Status == "completed" {
		return nil
	}
	msg := "openai: response " + r.Status
	if r.Error != nil && strings.TrimSpace(r.Error.Message) != "" {
		msg += ": " + truncateRunes(strings.TrimSpace(r.Error.Message), maxErrorRunes)
	}
	return errors.New(msg)
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// createResponse POSTs a Responses API request on the slow client.
func createResponse(ctx context.Context, apiKey string, body map[string]any) (openAIResponse, error) {
	var out openAIResponse
	resp, err := httpx.SlowClient().R().
		SetContext(ctx).
		SetAuthToken(apiKey).
		SetHeader("Content-Type", "application/json").
		SetBody(body).
		Post(OpenAIBaseURL + "/responses")
	if err != nil {
		return out, requestError(ctx, err)
	}
	if err := statusError(resp.StatusCode(), resp.Body()); err != nil {
		return out, err
	}
	if err := json.Unmarshal(resp.Body(), &out); err != nil {
		return out, errors.New("openai: invalid response body")
	}
	return out, nil
}

// classifierSchema is gandalf's signal_result_classifier structured output.
var classifierSchema = map[string]any{
	"type": "json_schema",
	"json_schema": map[string]any{
		"name":   "signal_result_classifier",
		"strict": true,
		"schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"containsResult":       map[string]any{"type": "boolean"},
				"doesNotContainResult": map[string]any{"type": "boolean"},
				"explanation":          map[string]any{"type": "string"},
			},
			"required":             []string{"containsResult", "doesNotContainResult", "explanation"},
			"additionalProperties": false,
		},
	},
}

// classifyChat asks the classifier whether output answers question (gandalf's bool classification).
func classifyChat(ctx context.Context, apiKey, model, question, output string) (bool, error) {
	content, err := chatJSON(ctx, apiKey, model, []map[string]string{
		{"role": "system", "content": "Classify the output of this research question"},
		{"role": "user", "content": "Classify the result as found or Not found"},
		{"role": "user", "content": "Original Question:" + question},
		{"role": "user", "content": "Research Output:" + output},
	}, classifierSchema)
	if err != nil {
		return false, err
	}
	var verdict struct {
		ContainsResult bool `json:"containsResult"`
	}
	if err := json.Unmarshal([]byte(content), &verdict); err != nil {
		return false, errors.New("openai: invalid classifier output")
	}
	return verdict.ContainsResult, nil
}

// chatJSON POSTs a chat completion with a JSON response format and returns the first
// choice's content.
func chatJSON(ctx context.Context, apiKey, model string, messages []map[string]string, format map[string]any) (string, error) {
	var chat struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	resp, err := httpx.Client().R().
		SetContext(ctx).
		SetAuthToken(apiKey).
		SetHeader("Content-Type", "application/json").
		SetBody(map[string]any{"model": model, "messages": messages, "response_format": format}).
		Post(OpenAIBaseURL + "/chat/completions")
	if err != nil {
		return "", requestError(ctx, err)
	}
	if err := statusError(resp.StatusCode(), resp.Body()); err != nil {
		return "", err
	}
	if err := json.Unmarshal(resp.Body(), &chat); err != nil || len(chat.Choices) == 0 {
		return "", errors.New("openai: empty classifier response")
	}
	return chat.Choices[0].Message.Content, nil
}

// requestError reports a transport failure; a cancelled or expired context is reported as such.
func requestError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("openai: request failed: %w", err)
}

// statusError turns a non-2xx response into "openai: <code> <text>[: <message>]".
func statusError(code int, body []byte) error {
	if code >= 200 && code < 300 {
		return nil
	}
	msg := fmt.Sprintf("openai: %d %s", code, http.StatusText(code))
	var apiErr struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &apiErr) == nil && strings.TrimSpace(apiErr.Error.Message) != "" {
		msg += ": " + truncateRunes(strings.TrimSpace(apiErr.Error.Message), maxErrorRunes)
	}
	return errors.New(msg)
}
