package chromeattach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Tab is a background tab this package opened. It is the only tab the package touches.
type Tab struct {
	browser   *Browser
	url       string
	targetID  string
	sessionID string
	closeOnce sync.Once
	closeErr  error
}

// JSError is an exception thrown by JavaScript run with Eval.
type JSError struct {
	Text string
}

func (e *JSError) Error() string { return "javascript error: " + e.Text }

// OpenTab opens url in a new background tab in Chrome's default profile.
func (b *Browser) OpenTab(ctx context.Context, url string) (*Tab, error) {
	var created struct {
		TargetID string `json:"targetId"`
	}
	// Wait for Chrome's answer even after Ctrl-C: once Chrome creates the tab, we need its
	// id to close it, or a stray tab stays in the user's browser.
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := b.call(cctx, "", "Target.createTarget", map[string]any{"url": url, "background": true}, &created); err != nil {
		return nil, err
	}
	t := &Tab{browser: b, url: url, targetID: created.TargetID}
	if err := ctx.Err(); err != nil {
		t.Close()
		return nil, err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := b.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": t.targetID, "flatten": true}, &attached); err != nil {
		t.Close()
		return nil, err
	}
	t.sessionID = attached.SessionID
	return t, nil
}

// Eval runs expr in the tab, awaits it if it is a promise, and decodes its JSON value
// into out, which may be nil.
func (t *Tab) Eval(ctx context.Context, expr string, out any) error {
	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	params := map[string]any{"expression": expr, "awaitPromise": true, "returnByValue": true}
	if err := t.browser.call(ctx, t.sessionID, "Runtime.evaluate", params, &res); err != nil {
		return err
	}
	if d := res.ExceptionDetails; d != nil {
		text := d.Exception.Description
		if text == "" {
			text = d.Text
		}
		return &JSError{Text: text}
	}
	if out == nil || len(res.Result.Value) == 0 {
		return nil
	}
	if err := json.Unmarshal(res.Result.Value, out); err != nil {
		return fmt.Errorf("decode javascript result: %w", err)
	}
	return nil
}

// WaitLoaded polls until the tab has left its initial about:blank document and finished
// loading, or ctx ends. CDP errors while the page is being replaced are retried.
func (t *Tab) WaitLoaded(ctx context.Context) error {
	for {
		var page struct {
			State string `json:"state"`
			Href  string `json:"href"`
		}
		err := t.Eval(ctx, "({state: document.readyState, href: location.href})", &page)
		var cdpErr *CDPError
		switch {
		case errors.As(err, &cdpErr):
			// e.g. "Cannot find default execution context" mid-navigation
		case err != nil:
			return err
		case page.State == "complete" && (page.Href != "about:blank" || t.url == "about:blank"):
			return nil
		}
		select {
		case <-time.After(250 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Close closes the tab. It uses its own timeout so it still works after the caller's
// context is cancelled (Ctrl-C). Calling it again is a no-op.
func (t *Tab) Close() error {
	t.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		t.closeErr = t.browser.call(ctx, "", "Target.closeTarget", map[string]any{"targetId": t.targetID}, nil)
	})
	return t.closeErr
}
