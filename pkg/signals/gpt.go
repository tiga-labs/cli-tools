package signals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DefaultGPTModel is the research model when neither the definition nor the run sets one
// (gandalf's GPT_DEFAULT_AGENTIC_MODEL).
const DefaultGPTModel = "gpt-5.6-terra"

// defaultMaxOutputTokens is used when the definition's max_tokens is 0 (as in gandalf).
const defaultMaxOutputTokens = 4048

const researchInstructions = "Produce a source-backed business signal using only the supplied inputs and web-search evidence. Do not rely on prior knowledge. Every factual conclusion must have at least one URL citation. If the evidence does not directly support the conclusion or the company identity is uncertain, do not claim that it does."

// noSearchInstructions is gandalf's computedColumnSystemPrompt, copied verbatim.
const noSearchInstructions = "You're job is to generate json output based on inputs.\n\nJSON Output: {\n  output: <text>,\n  found: <boolean> /* True You Successfully found the Output, False You Did Not Find The Output */\n}\nOutput should be formatted html must be wrapped in <span>, may include <b>, <i>, <em>"

// jsonInputSuffix ends every json_object request's input: OpenAI's Responses API requires the
// word "json" in the input messages, and the instructions field does not count.
const jsonInputSuffix = "\n\nRespond in JSON."

const verifyInstructions = `Check whether the answer is directly supported by the question's facts and the listed sources. Reply JSON {"supported": <boolean>, "reason": <string>}.`

// verifyNoSourcesInstructions is used when the answer has no sources (web search off, D12).
const verifyNoSourcesInstructions = `Check whether the answer is directly supported by the question's facts. Reply JSON {"supported": <boolean>, "reason": <string>}.`

// GPTOptions are the per-run settings for a gpt signal.
type GPTOptions struct {
	APIKey string
	// Model overrides the definition's model; empty uses the definition's, then DefaultGPTModel.
	Model string
	// WebSearch is the effective setting: the definition allows it and the run did not turn it off.
	WebSearch bool
	// Bool and Verify are ORed with the definition's should_classify_output_as_bool and
	// advanced_model_verify.
	Bool   bool
	Verify bool
	// Now returns the time used for date merge fields; nil means time.Now.
	Now func() time.Time
}

// RunGPT runs one gpt signal against one record.
func RunGPT(ctx context.Context, s Signal, r Record, opt GPTOptions) Result {
	if s.Config.Type != TypeGPT {
		return failed(fmt.Errorf("RunGPT: signal type %q is not gpt", s.Config.Type))
	}
	now := time.Now
	if opt.Now != nil {
		now = opt.Now
	}
	fields := r.MergeFields(now())
	if s.Config.HasPrecondition && !AssessPrecondition(fields, s.Config.Precondition) {
		return Result{Status: StatusPreconditionFailed}
	}
	if missing := MissingDependencies(s, r); len(missing) > 0 {
		return Result{Status: StatusMissingDependencies, Error: "missing: " + strings.Join(missing, ", ")}
	}

	prompt := Render(s.Config.Prompt, fields)
	if s.Config.WordLimit > 0 {
		prompt += "\n\nOutput Word Limit: " + strconv.Itoa(s.Config.WordLimit)
	}
	model := opt.Model
	if model == "" {
		model = s.Config.Model
	}
	if model == "" {
		model = DefaultGPTModel
	}
	maxTokens := s.Config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxOutputTokens
	}

	var res Result
	if opt.WebSearch {
		res = research(ctx, opt.APIKey, model, prompt, maxTokens)
	} else {
		res = reason(ctx, opt.APIKey, model, prompt, maxTokens)
	}

	if res.Status == StatusSuccess && (opt.Bool || s.Config.ShouldClassifyOutputAsBool) {
		found, err := classifyChat(ctx, opt.APIKey, ClassifierModel, prompt, res.Value)
		if err != nil {
			return failed(err)
		}
		if !found {
			res.Status = StatusNotFound // keep Value and Sources as evidence (K1)
		}
	}
	if res.Status == StatusSuccess && (opt.Verify || s.Config.AdvancedModelVerify) {
		supported, err := verify(ctx, opt.APIKey, model, prompt, res)
		if err != nil {
			return failed(err)
		}
		if !supported {
			res.Status = StatusNotFound
		}
	}
	return res
}

// research is gandalf's agentic path: web search is required, and a hit needs a URL citation.
func research(ctx context.Context, apiKey, model, prompt string, maxTokens int) Result {
	resp, err := createResponse(ctx, apiKey, map[string]any{
		"model":             model,
		"instructions":      researchInstructions,
		"input":             prompt,
		"max_output_tokens": maxTokens,
		"max_tool_calls":    10,
		"tools":             []map[string]any{{"type": "web_search_preview", "search_context_size": "high"}},
		"tool_choice":       "required",
	})
	if err != nil {
		return failed(err)
	}
	if err := resp.failure(); err != nil {
		return failed(err)
	}
	text, sources := resp.text(), resp.sources()
	if text == "" || len(sources) == 0 {
		return Result{Status: StatusNotFound, Value: text, Sources: sources}
	}
	return Result{Status: StatusSuccess, Value: text, Sources: sources}
}

// reason is the web-search-off path: gandalf's JSON {output, found}, with no sources (D12).
func reason(ctx context.Context, apiKey, model, prompt string, maxTokens int) Result {
	found, value, err := reasonJSON(ctx, apiKey, model, noSearchInstructions, prompt, maxTokens)
	if err != nil {
		return failed(err)
	}
	if !found || value == "" {
		return Result{Status: StatusNotFound, Value: value, Sources: []Source{}}
	}
	return Result{Status: StatusSuccess, Value: value, Sources: []Source{}}
}

// reasonJSON asks model for JSON {output, found} with no tools.
func reasonJSON(ctx context.Context, apiKey, model, instructions, prompt string, maxTokens int) (found bool, output string, err error) {
	resp, err := createResponse(ctx, apiKey, map[string]any{
		"model":             model,
		"instructions":      instructions,
		"input":             prompt + jsonInputSuffix,
		"max_output_tokens": maxTokens,
		"text":              map[string]any{"format": map[string]any{"type": "json_object"}},
	})
	if err != nil {
		return false, "", err
	}
	if err := resp.failure(); err != nil {
		return false, "", err
	}
	var out struct {
		Output string `json:"output"`
		Found  bool   `json:"found"`
	}
	if err := json.Unmarshal([]byte(resp.text()), &out); err != nil {
		return false, "", errors.New("openai: invalid JSON output")
	}
	return out.Found, stripValueHTML(out.Output), nil
}

// verify asks the research model whether the answer is supported by its sources (K2).
func verify(ctx context.Context, apiKey, model, prompt string, res Result) (bool, error) {
	var input strings.Builder
	input.WriteString("Question:\n" + prompt + "\n\nAnswer:\n" + res.Value)
	instructions := verifyNoSourcesInstructions
	if len(res.Sources) > 0 {
		instructions = verifyInstructions
		input.WriteString("\n\nSources:\n")
		for _, src := range res.Sources {
			input.WriteString(src.URL + "\n")
		}
	}
	resp, err := createResponse(ctx, apiKey, map[string]any{
		"model":        model,
		"instructions": instructions,
		"input":        input.String() + jsonInputSuffix,
		"text":         map[string]any{"format": map[string]any{"type": "json_object"}},
	})
	if err != nil {
		return false, err
	}
	if err := resp.failure(); err != nil {
		return false, err
	}
	var out struct {
		Supported bool `json:"supported"`
	}
	if err := json.Unmarshal([]byte(resp.text()), &out); err != nil {
		return false, errors.New("openai: invalid JSON output")
	}
	return out.Supported, nil
}

func failed(err error) Result {
	return Result{Status: StatusFailed, Error: err.Error()}
}
