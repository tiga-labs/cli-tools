package signals

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/tiga-labs/cli-tools/pkg/httpx"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type recorded struct {
	path   string
	header http.Header
	body   map[string]any
}

// stubOpenAI serves canned bodies by URL path suffix and records every request.
type stubOpenAI struct {
	mu       sync.Mutex
	reqs     []recorded
	handlers map[string]func(n int) *http.Response
	counts   map[string]int
}

func newStubOpenAI(t *testing.T, handlers map[string]func(n int) *http.Response) *stubOpenAI {
	t.Helper()
	s := &stubOpenAI{handlers: handlers, counts: map[string]int{}}
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		if req.Body != nil {
			raw, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(raw, &body)
		}
		s.mu.Lock()
		s.reqs = append(s.reqs, recorded{path: req.URL.Path, header: req.Header.Clone(), body: body})
		var h func(int) *http.Response
		var n int
		for suffix, fn := range s.handlers {
			if strings.HasSuffix(req.URL.Path, suffix) {
				s.counts[suffix]++
				h, n = fn, s.counts[suffix]
			}
		}
		s.mu.Unlock()
		if h == nil {
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
		return h(n), nil
	})
	t.Cleanup(func() { httpx.Transport = nil })
	return s
}

func (s *stubOpenAI) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

func (s *stubOpenAI) count(suffix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[suffix]
}

func (s *stubOpenAI) last(suffix string) recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.reqs) - 1; i >= 0; i-- {
		if strings.HasSuffix(s.reqs[i].path, suffix) {
			return s.reqs[i]
		}
	}
	return recorded{}
}

func always(status int, body string) func(int) *http.Response {
	return func(int) *http.Response { return jsonResponse(status, body) }
}

func TestCreateResponseSendsAuthAndBody(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, `{"status":"completed","output":[]}`)})
	if _, err := createResponse(context.Background(), "sk-test", map[string]any{"model": "gpt-x"}); err != nil {
		t.Fatal(err)
	}
	req := stub.last("/responses")
	if req.path != "/v1/responses" {
		t.Fatalf("path = %q", req.path)
	}
	if got := req.header.Get("Authorization"); got != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", got)
	}
	if req.body["model"] != "gpt-x" {
		t.Fatalf("body = %v", req.body)
	}
}

func TestCreateResponseErrorMessage(t *testing.T) {
	newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(401, `{"error":{"message":"Incorrect API key provided: sk-te****"}}`)})
	_, err := createResponse(context.Background(), "sk-test", map[string]any{})
	if err == nil || !strings.HasPrefix(err.Error(), "openai: 401") || !strings.Contains(err.Error(), "Incorrect API key") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseResponseJoinsPartsAndDedupesCitations(t *testing.T) {
	body := `{"status":"completed","output":[
		{"type":"web_search_call","status":"completed"},
		{"type":"message","content":[
			{"type":"output_text","text":"Yes, ","annotations":[{"type":"url_citation","url":"https://a.com","title":"A"}]},
			{"type":"output_text","text":"Acme raised ","annotations":[{"type":"url_citation","url":"https://a.com","title":"A again"},{"type":"url_citation","url":"","title":"empty"}]}
		]},
		{"type":"message","content":[
			{"type":"output_text","text":"$10M.","annotations":[{"type":"url_citation","url":"https://b.com","title":""}]}
		]}
	]}`
	var r openAIResponse
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if got := r.text(); got != "Yes, Acme raised $10M." {
		t.Fatalf("text = %q", got)
	}
	want := []Source{{URL: "https://a.com", Title: "A"}, {URL: "https://b.com", Title: "https://b.com"}}
	got := r.sources()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("sources = %v", got)
	}
}

func TestParseResponseIncomplete(t *testing.T) {
	var r openAIResponse
	if err := json.Unmarshal([]byte(`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}`), &r); err != nil {
		t.Fatal(err)
	}
	if ok, reason := r.incomplete(); !ok || reason != "max_output_tokens" {
		t.Fatalf("incomplete = %v %q", ok, reason)
	}
	r = openAIResponse{Status: "completed"}
	if ok, _ := r.incomplete(); ok {
		t.Fatal("completed reported incomplete")
	}
}

func TestClassifyChatParsesContainsResult(t *testing.T) {
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{"/chat/completions": always(200,
		`{"choices":[{"message":{"content":"{\"containsResult\":false,\"doesNotContainResult\":true,\"explanation\":\"x\"}"}}]}`)})
	got, err := classifyChat(context.Background(), "sk-test", ClassifierModel, "q?", "no")
	if err != nil || got {
		t.Fatalf("got=%v err=%v", got, err)
	}
	req := stub.last("/chat/completions")
	if req.body["model"] != "gpt-5.4-mini" {
		t.Fatalf("model = %v", req.body["model"])
	}
	rf, _ := req.body["response_format"].(map[string]any)
	if rf["type"] != "json_schema" {
		t.Fatalf("response_format = %v", rf)
	}

	newStubOpenAI(t, map[string]func(int) *http.Response{"/chat/completions": always(200,
		`{"choices":[{"message":{"content":"{\"containsResult\":true,\"doesNotContainResult\":false,\"explanation\":\"x\"}"}}]}`)})
	if got, err := classifyChat(context.Background(), "sk-test", ClassifierModel, "q?", "yes"); err != nil || !got {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func jsonString(s string) (string, error) {
	b, err := json.Marshal(s)
	return string(b), err
}
