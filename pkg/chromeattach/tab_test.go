package chromeattach

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenTabDefaultContext(t *testing.T) {
	f := newFakeCDP(t)
	b := connectFake(t, f)
	if _, err := b.OpenTab(context.Background(), "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	calls := f.calls()
	if len(calls) != 2 {
		t.Fatalf("recorded %d calls, want 2: %+v", len(calls), calls)
	}
	create := calls[0]
	if create.Method != "Target.createTarget" || create.Params["url"] != "https://example.com/" || create.Params["background"] != true {
		t.Fatalf("createTarget = %+v", create)
	}
	if _, ok := create.Params["browserContextId"]; ok {
		t.Fatal("createTarget sent browserContextId; tabs must open in the default context")
	}
	attach := calls[1]
	if attach.Method != "Target.attachToTarget" || attach.Params["targetId"] != "T1" || attach.Params["flatten"] != true {
		t.Fatalf("attachToTarget = %+v", attach)
	}
}

func TestEvalReturnsValue(t *testing.T) {
	f := newFakeCDP(t)
	f.evalFn = func(string) (any, string) { return map[string]any{"status": 200, "body": "{}"}, "" }
	b := connectFake(t, f)
	tab, err := b.OpenTab(context.Background(), "about:blank")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := tab.Eval(context.Background(), "fetchIt()", &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != 200 || out.Body != "{}" {
		t.Fatalf("out = %+v", out)
	}
	calls := f.calls()
	ev := calls[len(calls)-1]
	if ev.Method != "Runtime.evaluate" || ev.SessionID != "S1" || ev.Params["awaitPromise"] != true || ev.Params["returnByValue"] != true || ev.Params["expression"] != "fetchIt()" {
		t.Fatalf("Runtime.evaluate = %+v", ev)
	}
}

func TestEvalJSError(t *testing.T) {
	f := newFakeCDP(t)
	f.evalFn = func(string) (any, string) { return nil, "TypeError: x is undefined" }
	b := connectFake(t, f)
	tab, err := b.OpenTab(context.Background(), "about:blank")
	if err != nil {
		t.Fatal(err)
	}
	err = tab.Eval(context.Background(), "x.y", nil)
	var jsErr *JSError
	if !errors.As(err, &jsErr) || !strings.Contains(err.Error(), "TypeError: x is undefined") {
		t.Fatalf("err = %v, want JSError with the exception text", err)
	}
}

func TestWaitLoaded(t *testing.T) {
	f := newFakeCDP(t)
	var polls atomic.Int32
	f.evalFn = func(string) (any, string) {
		if polls.Add(1) < 3 {
			return loadState("loading", "about:blank"), ""
		}
		return loadState("complete", "about:blank"), ""
	}
	b := connectFake(t, f)
	tab, err := b.OpenTab(context.Background(), "about:blank")
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.WaitLoaded(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := polls.Load(); n < 3 {
		t.Fatalf("polled %d times, want >= 3", n)
	}
}

func TestWaitLoadedTimeout(t *testing.T) {
	f := newFakeCDP(t)
	f.evalFn = func(string) (any, string) { return loadState("loading", "about:blank"), "" }
	b := connectFake(t, f)
	tab, err := b.OpenTab(context.Background(), "about:blank")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := tab.WaitLoaded(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context deadline", err)
	}
}

func TestTabCloseAfterCancel(t *testing.T) {
	f := newFakeCDP(t)
	b := connectFake(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	tab, err := b.OpenTab(ctx, "about:blank")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := tab.Close(); err != nil {
		t.Fatalf("Close after cancel: %v", err)
	}
	if err := tab.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	var closes int
	for _, c := range f.calls() {
		if c.Method == "Target.closeTarget" {
			closes++
			if c.Params["targetId"] != "T1" {
				t.Fatalf("closeTarget targetId = %v, want T1", c.Params["targetId"])
			}
		}
	}
	if closes != 1 {
		t.Fatalf("sent %d closeTarget, want 1", closes)
	}
}

// TestOnlyAllowedMethods pins C4: the package only ever creates, uses and closes its own tab.
func TestOnlyAllowedMethods(t *testing.T) {
	f := newFakeCDP(t)
	f.evalFn = func(expr string) (any, string) {
		if strings.Contains(expr, "readyState") {
			return loadState("complete", "about:blank"), ""
		}
		return 2, ""
	}
	dir := t.TempDir()
	f.writePortFile(dir)
	ctx := context.Background()
	b, err := Connect(ctx, Options{UserDataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	tab, err := b.OpenTab(ctx, "about:blank")
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.WaitLoaded(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tab.Eval(ctx, "1+1", &n); err != nil {
		t.Fatal(err)
	}
	if err := tab.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}

	allowed := map[string]bool{"Target.createTarget": true, "Target.attachToTarget": true, "Target.closeTarget": true, "Runtime.evaluate": true}
	for _, c := range f.calls() {
		if !allowed[c.Method] {
			t.Errorf("sent disallowed method %s", c.Method)
		}
		if c.Method == "Target.closeTarget" && c.Params["targetId"] != "T1" {
			t.Errorf("closed a target it did not create: %v", c.Params["targetId"])
		}
		if c.Method == "Runtime.evaluate" && c.SessionID != "S1" {
			t.Errorf("evaluated outside its own session: %q", c.SessionID)
		}
	}
}

func TestOpenTabCancelDuringCreateClosesTab(t *testing.T) {
	f := newFakeCDP(t)
	f.createDelay = 300 * time.Millisecond
	b := connectFake(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	if _, err := b.OpenTab(ctx, "https://example.com/"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	var closed bool
	for _, c := range f.calls() {
		if c.Method == "Target.closeTarget" && c.Params["targetId"] == "T1" {
			closed = true
		}
	}
	if !closed {
		t.Fatal("tab Chrome created during Ctrl-C was left open")
	}
}

// loadState answers WaitLoaded's probe with the given readyState and href.
func loadState(state, href string) map[string]any {
	return map[string]any{"state": state, "href": href}
}

func TestWaitLoadedSkipsInitialBlank(t *testing.T) {
	f := newFakeCDP(t)
	var polls atomic.Int32
	f.evalFn = func(string) (any, string) {
		if polls.Add(1) < 3 {
			return loadState("complete", "about:blank"), ""
		}
		return loadState("complete", "https://example.com/"), ""
	}
	b := connectFake(t, f)
	tab, err := b.OpenTab(context.Background(), "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.WaitLoaded(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := polls.Load(); n < 3 {
		t.Fatalf("returned after %d polls while still on about:blank", n)
	}
}

func TestWaitLoadedRetriesCDPError(t *testing.T) {
	f := newFakeCDP(t)
	f.evalCDPErrors.Store(2)
	f.evalFn = func(string) (any, string) { return loadState("complete", "https://example.com/"), "" }
	b := connectFake(t, f)
	tab, err := b.OpenTab(context.Background(), "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.WaitLoaded(context.Background()); err != nil {
		t.Fatalf("WaitLoaded gave up on a transient CDP error: %v", err)
	}
}
