package signals

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/httpx"
)

const fundingPrompt = "Has {{.AccountName}} raised funding?"

func fundingSignal(t *testing.T, extra string) Signal {
	t.Helper()
	s, err := Parse([]byte(`{"label":"Funding","computed_config":{"type":"gpt","is_account_insight":true,"prompt":"` + fundingPrompt + `"` + extra + `}}`))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var acme = NewRecord(map[string]string{"AccountName": "Acme"})

func gptOpts() GPTOptions {
	return GPTOptions{APIKey: "sk-test", WebSearch: true, Now: func() time.Time { return time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC) }}
}

const researchHit = `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Yes, Acme raised $10M.","annotations":[{"type":"url_citation","url":"https://a.com","title":"A"}]}]}]}`

func outputText(text string) string {
	b, _ := jsonString(text)
	return `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":` + b + `,"annotations":[]}]}]}`
}

func classifier(contains bool) func(int) *http.Response {
	v := "false"
	if contains {
		v = "true"
	}
	return always(200, `{"choices":[{"message":{"content":"{\"containsResult\":`+v+`,\"doesNotContainResult\":false,\"explanation\":\"x\"}"}}]}`)
}

func TestRunGPTWebSearchHit(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, researchHit)})
	res := RunGPT(context.Background(), fundingSignal(t, ""), acme, gptOpts())
	if res.Status != StatusSuccess || res.Value != "Yes, Acme raised $10M." || len(res.Sources) != 1 || res.Sources[0].URL != "https://a.com" {
		t.Fatalf("res = %+v", res)
	}
	body := stub.last("/responses").body
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["type"] != "web_search_preview" || tools[0].(map[string]any)["search_context_size"] != "high" {
		t.Fatalf("tools = %v", body["tools"])
	}
	if body["tool_choice"] != "required" || body["max_tool_calls"] != float64(10) || body["model"] != "gpt-5.6-terra" || body["max_output_tokens"] != float64(4048) {
		t.Fatalf("body = %v", body)
	}
	if _, ok := body["temperature"]; ok {
		t.Fatal("temperature must not be sent")
	}
	if !strings.Contains(body["input"].(string), "Has Acme raised funding?") || !strings.HasPrefix(body["instructions"].(string), "Produce a source-backed business signal") {
		t.Fatalf("input=%v instructions=%v", body["input"], body["instructions"])
	}
}

func TestRunGPTModelOverride(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, researchHit)})
	s := fundingSignal(t, `,"model":"gpt-x","max_tokens":900`)
	RunGPT(context.Background(), s, acme, gptOpts())
	if b := stub.last("/responses").body; b["model"] != "gpt-x" || b["max_output_tokens"] != float64(900) {
		t.Fatalf("body = %v", b)
	}
	opt := gptOpts()
	opt.Model = "gpt-y"
	RunGPT(context.Background(), s, acme, opt)
	if got := stub.last("/responses").body["model"]; got != "gpt-y" {
		t.Fatalf("model = %v", got)
	}
}

func TestRunGPTNoSourcesIsNotFound(t *testing.T) {
	newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, outputText("Yes, they did."))})
	if res := RunGPT(context.Background(), fundingSignal(t, ""), acme, gptOpts()); res.Status != StatusNotFound {
		t.Fatalf("res = %+v", res)
	}
}

func TestRunGPTEmptyTextIsNotFound(t *testing.T) {
	newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, outputText("   "))})
	if res := RunGPT(context.Background(), fundingSignal(t, ""), acme, gptOpts()); res.Status != StatusNotFound {
		t.Fatalf("res = %+v", res)
	}
}

func TestRunGPTIncompleteIsFailed(t *testing.T) {
	newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200,
		`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"Yes, Acme","annotations":[{"type":"url_citation","url":"https://a.com"}]}]}]}`)})
	res := RunGPT(context.Background(), fundingSignal(t, ""), acme, gptOpts())
	if res.Status != StatusFailed || res.Error != "openai: response incomplete (max_output_tokens)" {
		t.Fatalf("res = %+v", res)
	}
}

func TestRunGPTHTTPErrorIsFailed(t *testing.T) {
	newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(500, `{"error":{"message":"server exploded"}}`)})
	res := RunGPT(context.Background(), fundingSignal(t, ""), acme, gptOpts())
	if res.Status != StatusFailed || !strings.HasPrefix(res.Error, "openai: 500") || strings.Contains(res.Error, "sk-test") {
		t.Fatalf("res = %+v", res)
	}
}

func noSearchOpts() GPTOptions {
	o := gptOpts()
	o.WebSearch = false
	return o
}

func TestRunGPTNoSearchFound(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, outputText(`{"output":"<span>Yes</span>","found":true}`))})
	res := RunGPT(context.Background(), fundingSignal(t, ""), acme, noSearchOpts())
	if res.Status != StatusSuccess || res.Value != "Yes" || res.Sources == nil || len(res.Sources) != 0 {
		t.Fatalf("res = %+v", res)
	}
	body := stub.last("/responses").body
	if _, ok := body["tools"]; ok {
		t.Fatalf("tools must not be sent: %v", body)
	}
	format, _ := body["text"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_object" || !strings.HasPrefix(body["instructions"].(string), "You're job is to generate json output") {
		t.Fatalf("body = %v", body)
	}
}

func TestRunGPTNoSearchNotFound(t *testing.T) {
	newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, outputText(`{"output":"no","found":false}`))})
	if res := RunGPT(context.Background(), fundingSignal(t, ""), acme, noSearchOpts()); res.Status != StatusNotFound {
		t.Fatalf("res = %+v", res)
	}
}

func TestRunGPTNoSearchBadJSONIsFailed(t *testing.T) {
	newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, outputText("sure!"))})
	res := RunGPT(context.Background(), fundingSignal(t, ""), acme, noSearchOpts())
	if res.Status != StatusFailed || res.Error != "openai: invalid JSON output" {
		t.Fatalf("res = %+v", res)
	}
}

func TestRunGPTBoolMiss(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, researchHit), "/chat/completions": classifier(false)})
	opt := gptOpts()
	opt.Bool = true
	res := RunGPT(context.Background(), fundingSignal(t, ""), acme, opt)
	if res.Status != StatusNotFound || res.Value != "Yes, Acme raised $10M." || len(res.Sources) != 1 {
		t.Fatalf("res = %+v", res)
	}
	if stub.count("/responses") != 1 || stub.count("/chat/completions") != 1 {
		t.Fatalf("calls: responses=%d chat=%d", stub.count("/responses"), stub.count("/chat/completions"))
	}
	if body := stub.last("/chat/completions").body; body["model"] != ClassifierModel {
		t.Fatalf("classifier model = %v", body["model"])
	}
}

func TestRunGPTBoolHit(t *testing.T) {
	newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, researchHit), "/chat/completions": classifier(true)})
	opt := gptOpts()
	opt.Bool = true
	if res := RunGPT(context.Background(), fundingSignal(t, ""), acme, opt); res.Status != StatusSuccess {
		t.Fatalf("res = %+v", res)
	}
}

func TestRunGPTBoolFromDefinition(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, researchHit), "/chat/completions": classifier(false)})
	res := RunGPT(context.Background(), fundingSignal(t, `,"should_classify_output_as_bool":true`), acme, gptOpts())
	if res.Status != StatusNotFound || stub.count("/chat/completions") != 1 {
		t.Fatalf("res = %+v chat=%d", res, stub.count("/chat/completions"))
	}
}

func TestRunGPTVerifyUnsupported(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": func(n int) *http.Response {
		if n == 1 {
			return jsonResponse(200, researchHit)
		}
		return jsonResponse(200, outputText(`{"supported":false,"reason":"no evidence"}`))
	}})
	opt := gptOpts()
	opt.Verify = true
	res := RunGPT(context.Background(), fundingSignal(t, ""), acme, opt)
	if res.Status != StatusNotFound || stub.count("/responses") != 2 {
		t.Fatalf("res = %+v calls=%d", res, stub.count("/responses"))
	}
	body := stub.last("/responses").body
	if _, ok := body["tools"]; ok || !strings.Contains(body["input"].(string), "https://a.com") || !strings.Contains(body["input"].(string), "Yes, Acme raised $10M.") {
		t.Fatalf("verify body = %v", body)
	}
}

func TestRunGPTVerifySupported(t *testing.T) {
	newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": func(n int) *http.Response {
		if n == 1 {
			return jsonResponse(200, researchHit)
		}
		return jsonResponse(200, outputText(`{"supported":true,"reason":"ok"}`))
	}})
	opt := gptOpts()
	opt.Verify = true
	if res := RunGPT(context.Background(), fundingSignal(t, ""), acme, opt); res.Status != StatusSuccess {
		t.Fatalf("res = %+v", res)
	}
}

func TestRunGPTMissingDepsNoHTTP(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, researchHit)})
	res := RunGPT(context.Background(), fundingSignal(t, ""), NewRecord(nil), gptOpts())
	if res.Status != StatusMissingDependencies || res.Error != "missing: AccountName" || stub.calls() != 0 {
		t.Fatalf("res = %+v calls=%d", res, stub.calls())
	}
}

func TestRunGPTPreconditionNoHTTP(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, researchHit)})
	s := fundingSignal(t, `,"has_precondition":true,"precondition":{"property":"AccountIndustry","property_type":"text","operator":"ILIKE","value":"software"}`)
	res := RunGPT(context.Background(), s, acme, gptOpts())
	if res.Status != StatusPreconditionFailed || stub.calls() != 0 {
		t.Fatalf("res = %+v calls=%d", res, stub.calls())
	}
}

func TestRunGPTWordLimitAppended(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, researchHit)})
	RunGPT(context.Background(), fundingSignal(t, `,"word_limit":50`), acme, gptOpts())
	if input := stub.last("/responses").body["input"].(string); !strings.HasSuffix(input, "\n\nOutput Word Limit: 50") {
		t.Fatalf("input = %q", input)
	}
}

func TestRunGPTWrongType(t *testing.T) {
	s := fundingSignal(t, "")
	s.Config.Type = TypeLinkedInPosts
	if res := RunGPT(context.Background(), s, acme, gptOpts()); res.Status != StatusFailed || !strings.Contains(res.Error, "not gpt") {
		t.Fatalf("res = %+v", res)
	}
}

func TestRunGPTContextCancel(t *testing.T) {
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	t.Cleanup(func() { httpx.Transport = nil })
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	done := make(chan Result, 1)
	go func() { done <- RunGPT(ctx, fundingSignal(t, ""), acme, gptOpts()) }()
	select {
	case res := <-done:
		if res.Status != StatusFailed || res.Error != "context canceled" {
			t.Fatalf("res = %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunGPT did not return after cancel")
	}
}

func TestRunGPTVerifyWithoutSourcesJudgesFacts(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": func(n int) *http.Response {
		if n == 1 {
			return jsonResponse(200, outputText(`{"output":"<span>Yes</span>","found":true}`))
		}
		return jsonResponse(200, outputText(`{"supported":true,"reason":"ok"}`))
	}})
	opt := noSearchOpts()
	opt.Verify = true
	if res := RunGPT(context.Background(), fundingSignal(t, ""), acme, opt); res.Status != StatusSuccess {
		t.Fatalf("res = %+v", res)
	}
	body := stub.last("/responses").body
	instr, input := body["instructions"].(string), body["input"].(string)
	if strings.Contains(instr, "sources") || strings.Contains(input, "Sources:") || !strings.Contains(instr, "JSON") {
		t.Fatalf("verify without sources must judge the question's facts only: instructions=%q input=%q", instr, input)
	}
	format, _ := body["text"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_object" {
		t.Fatalf("verify format = %v", body["text"])
	}
}

func TestRunGPTFailedResponseStatusIsFailed(t *testing.T) {
	for _, status := range []string{"failed", "cancelled"} {
		newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200,
			`{"status":"`+status+`","error":{"message":"model error"},"output":[]}`)})
		res := RunGPT(context.Background(), fundingSignal(t, ""), acme, gptOpts())
		if res.Status != StatusFailed || !strings.Contains(res.Error, status) {
			t.Fatalf("%s: res = %+v", status, res)
		}
	}
}
