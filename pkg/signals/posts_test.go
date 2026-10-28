package signals

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// postsJSON is a synthetic /posts/all body; each post is (text, age in days before liNow).
func postsJSON(posts ...any) string {
	var items []string
	for i := 0; i+1 < len(posts); i += 2 {
		text, _ := json.Marshal(posts[i].(string))
		ts := liNow.AddDate(0, 0, -posts[i+1].(int)).UnixMilli()
		items = append(items, fmt.Sprintf(`{"urn":"urn:li:activity:%d","url":"https://www.linkedin.com/feed/update/urn:li:activity:%d","text":%s,"author":{"name":"Jane Doe"},"postedAt":{"timestamp":%d},"resharedPostContent":null}`, i, i, text, ts))
	}
	return `{"success":true,"statusCode":200,"message":"ok","data":{"cursor":"","posts":[` + strings.Join(items, ",") + `]}}`
}

func postsSignal(t *testing.T, extra string) Signal {
	t.Helper()
	return parseSignal(t, `{"label":"Posts","computed_config":{"type":"linkedin_posts","prompt":"Is Jane talking about hiring?"`+extra+`}}`)
}

func postsOpts() GPTOptions {
	return GPTOptions{APIKey: "sk-test", Now: func() time.Time { return liNow }}
}

func postsStub(t *testing.T, posts string, classify func(n int) *http.Response) *stubOpenAI {
	t.Helper()
	withLiCache(t)
	return newStubOpenAI(t, map[string]func(int) *http.Response{
		"/profile/full": always(200, profileJSON()),
		"/posts/all":    always(200, posts),
		"/responses":    classify,
	})
}

func jsonFound(found bool, output string) *http.Response {
	b, _ := json.Marshal(map[string]any{"output": output, "found": found})
	return jsonResponse(200, outputText(string(b)))
}

func TestPostsFirstMatchWins(t *testing.T) {
	// Calls: classify post 0 (no), classify post 1 (yes), generate for post 1.
	stub := postsStub(t, postsJSON("Lunch pics", 1, "We are hiring AEs!", 2, "Also hiring", 3), func(n int) *http.Response {
		switch n {
		case 1:
			return jsonFound(false, "")
		case 2:
			return jsonFound(true, "classified")
		default:
			return jsonFound(true, "Jane is hiring AEs.")
		}
	})
	res := RunPersonPosts(context.Background(), postsSignal(t, ""), jane, liClient(), postsOpts())
	if res.Status != StatusSuccess || res.Value != "Jane is hiring AEs." || len(res.Sources) != 1 ||
		res.Sources[0].URL != "https://www.linkedin.com/feed/update/urn:li:activity:2" || res.Sources[0].Title != "LinkedIn Post" {
		t.Fatalf("res = %+v", res)
	}
	if stub.count("/responses") != 3 {
		t.Fatalf("openai calls = %d", stub.count("/responses"))
	}
	classify := stub.reqs[len(stub.reqs)-2].body // the second classify call
	gen := stub.last("/responses").body
	if classify["model"] != ClassifierModel || gen["model"] != DefaultGPTModel {
		t.Fatalf("models = %v / %v", classify["model"], gen["model"])
	}
	if _, ok := gen["tools"]; ok {
		t.Fatal("posts must not web search")
	}
}

func TestPostsNoneMatch(t *testing.T) {
	postsStub(t, postsJSON("Lunch pics", 1, "Weekend", 2), func(int) *http.Response { return jsonFound(false, "") })
	if res := RunPersonPosts(context.Background(), postsSignal(t, ""), jane, liClient(), postsOpts()); res.Status != StatusNotFound {
		t.Fatalf("res = %+v", res)
	}
}

func TestPostsDateRangeFilter(t *testing.T) {
	// ONE_MONTH from 2026-10-28 starts 2026-09-28; a post exactly then is in range, 40 days is not.
	stub := postsStub(t, postsJSON("old hiring post", 40, "boundary hiring post", 30), func(int) *http.Response { return jsonFound(false, "") })
	RunPersonPosts(context.Background(), postsSignal(t, `,"date_range":"ONE_MONTH"`), jane, liClient(), postsOpts())
	if stub.count("/responses") != 1 || !strings.Contains(stub.last("/responses").body["input"].(string), "boundary hiring post") {
		t.Fatalf("calls = %d", stub.count("/responses"))
	}
}

func TestPostsCap(t *testing.T) {
	var posts []any
	for i := range 40 {
		posts = append(posts, fmt.Sprintf("post %d", i), i)
	}
	stub := postsStub(t, postsJSON(posts...), func(int) *http.Response { return jsonFound(false, "") })
	RunPersonPosts(context.Background(), postsSignal(t, ""), jane, liClient(), postsOpts())
	if stub.count("/responses") != maxPostsClassified || maxPostsClassified != 25 {
		t.Fatalf("calls = %d", stub.count("/responses"))
	}
}

func TestPostsPromptContainsUserPromptAndFewShot(t *testing.T) {
	stub := postsStub(t, postsJSON("We are hiring AEs!", 1), func(int) *http.Response { return jsonFound(false, "") })
	RunPersonPosts(context.Background(), postsSignal(t, `,"few_shot_examples":"Join our team"`), jane, liClient(), postsOpts())
	input := stub.last("/responses").body["input"].(string)
	for _, want := range []string{`You are looking at a LinkedIn post from this person: "Jane Doe".`, `"Is Jane talking about hiring?"`, "Join our team", `"We are hiring AEs!"`} {
		if !strings.Contains(input, want) {
			t.Errorf("input missing %q:\n%s", want, input)
		}
	}
	if !strings.HasPrefix(stub.last("/responses").body["instructions"].(string), "You're job is to generate json output") {
		t.Fatal("posts must use gandalf's JSON system prompt")
	}
}

func TestPostsClassifierErrorSkipsPost(t *testing.T) {
	// Post 0 errors, post 1 matches: the error is swallowed.
	postsStub(t, postsJSON("a", 1, "b", 2), func(n int) *http.Response {
		if n == 1 {
			return jsonResponse(500, `{}`)
		}
		return jsonFound(true, "found b")
	})
	if res := RunPersonPosts(context.Background(), postsSignal(t, ""), jane, liClient(), postsOpts()); res.Status != StatusSuccess || res.Value != "found b" {
		t.Fatalf("res = %+v", res)
	}
	postsStub(t, postsJSON("a", 1, "b", 2), func(int) *http.Response { return jsonResponse(500, `{}`) })
	if res := RunPersonPosts(context.Background(), postsSignal(t, ""), jane, liClient(), postsOpts()); res.Status != StatusFailed {
		t.Fatalf("all errors must fail, res = %+v", res)
	}
}

func TestPostsNoPosts(t *testing.T) {
	stub := postsStub(t, postsJSON(), func(int) *http.Response { return jsonFound(true, "x") })
	if res := RunPersonPosts(context.Background(), postsSignal(t, ""), jane, liClient(), postsOpts()); res.Status != StatusNotFound || stub.count("/responses") != 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestRunnerPersonPostsPlanAndKeys(t *testing.T) {
	withLiCache(t)
	newStubOpenAI(t, map[string]func(int) *http.Response{})
	noOpenAI := liRunOpts()
	noOpenAI.OpenAIKey = ""
	if _, err := NewRunner([]Signal{postsSignal(t, "")}, noOpenAI); err == nil || !strings.Contains(err.Error(), "OpenAI") {
		t.Fatalf("err = %v", err)
	}
	if _, err := NewRunner([]Signal{postsSignal(t, "")}, runOpts()); err == nil || !strings.Contains(err.Error(), "--buy-linkedin") {
		t.Fatalf("err = %v", err)
	}
	p := mustRunner(t, []Signal{postsSignal(t, "")}, liRunOpts()).Plan([]Record{jane})[0]
	if p.RowsReady != 1 || p.MaxOpenAICalls != 2*maxPostsClassified || p.LinkdAPICalls != 2 {
		t.Fatalf("plan = %+v", p)
	}
}

func TestPostsDateRangeBoundaryDay(t *testing.T) {
	// ONE_MONTH from 2026-10-28 12:00 starts on 2026-09-28; a post at 08:00 that day is in range.
	withLiCache(t)
	ts := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC).UnixMilli()
	body := fmt.Sprintf(`{"success":true,"statusCode":200,"data":{"posts":[{"urn":"u","url":"https://x/1","text":"boundary morning post","postedAt":{"timestamp":%d}}]}}`, ts)
	stub := newStubOpenAI(t, map[string]func(int) *http.Response{
		"/profile/full": always(200, profileJSON()),
		"/posts/all":    always(200, body),
		"/responses":    func(int) *http.Response { return jsonFound(false, "") },
	})
	RunPersonPosts(context.Background(), postsSignal(t, `,"date_range":"ONE_MONTH"`), jane, liClient(), postsOpts())
	if stub.count("/responses") != 1 {
		t.Fatalf("boundary-day post not classified: calls = %d", stub.count("/responses"))
	}
}

func TestPlanPostsCeilingCountsGenerationMisses(t *testing.T) {
	withLiCache(t)
	newStubOpenAI(t, map[string]func(int) *http.Response{})
	p := mustRunner(t, []Signal{postsSignal(t, "")}, liRunOpts()).Plan([]Record{jane})[0]
	if p.MaxOpenAICalls != 2*maxPostsClassified {
		t.Fatalf("max_openai_calls = %d, want %d (classify + generate per post)", p.MaxOpenAICalls, 2*maxPostsClassified)
	}
}
