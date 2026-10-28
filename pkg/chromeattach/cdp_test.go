package chromeattach

import (
	"context"
	"errors"
	"testing"
	"time"
)

// connectFake attaches to f and closes the connection when the test ends.
func connectFake(t *testing.T, f *fakeCDP) *Browser {
	t.Helper()
	dir := t.TempDir()
	f.writePortFile(dir)
	b, err := Connect(context.Background(), Options{UserDataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func TestCallRoundTrip(t *testing.T) {
	f := newFakeCDP(t)
	b := connectFake(t, f)
	var out struct {
		TargetID string `json:"targetId"`
	}
	if err := b.call(context.Background(), "", "Target.createTarget", map[string]any{"url": "about:blank"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.TargetID != "T1" {
		t.Fatalf("targetId = %q, want T1", out.TargetID)
	}
	calls := f.calls()
	if len(calls) != 1 || calls[0].Params["url"] != "about:blank" {
		t.Fatalf("recorded %+v", calls)
	}
}

func TestCallSkipsEvents(t *testing.T) {
	f := newFakeCDP(t)
	b := connectFake(t, f)
	var out struct {
		SessionID string `json:"sessionId"`
	}
	if err := b.call(context.Background(), "", "Target.attachToTarget", map[string]any{"targetId": "T1"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.SessionID != "S1" {
		t.Fatalf("sessionId = %q, want S1", out.SessionID)
	}
}

func TestCallCDPError(t *testing.T) {
	f := newFakeCDP(t)
	b := connectFake(t, f)
	err := b.call(context.Background(), "", "Foo.bar", nil, nil)
	var cerr *CDPError
	if !errors.As(err, &cerr) || cerr.Code != -32601 || cerr.Method != "Foo.bar" {
		t.Fatalf("err = %v, want CDPError -32601 for Foo.bar", err)
	}
}

func TestCallCtxCancel(t *testing.T) {
	f := newFakeCDP(t)
	f.evalFn = func(string) (any, string) { time.Sleep(2 * time.Second); return nil, "" }
	b := connectFake(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := b.call(ctx, "S1", "Runtime.evaluate", map[string]any{"expression": "1"}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context deadline", err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("took %s, want < 500ms", d)
	}
}

func TestCallConnectionClosed(t *testing.T) {
	f := newFakeCDP(t)
	f.evalFn = func(string) (any, string) { time.Sleep(2 * time.Second); return nil, "" }
	b := connectFake(t, f)
	errc := make(chan error, 1)
	go func() {
		errc <- b.call(context.Background(), "S1", "Runtime.evaluate", map[string]any{"expression": "1"}, nil)
	}()
	time.Sleep(50 * time.Millisecond)
	f.closeConns()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrConnectionClosed) {
			t.Fatalf("pending call err = %v, want ErrConnectionClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending call hung after Chrome closed the connection")
	}
	if err := b.call(context.Background(), "", "Target.createTarget", nil, nil); !errors.Is(err, ErrConnectionClosed) {
		t.Fatalf("later call err = %v, want ErrConnectionClosed", err)
	}
}

func TestCloseDoesNotSendBrowserClose(t *testing.T) {
	f := newFakeCDP(t)
	b := connectFake(t, f)
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	for _, c := range f.calls() {
		if c.Method == "Browser.close" {
			t.Fatal("Close sent Browser.close")
		}
	}
}
