package httpx

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func response(status int, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader("{}"))}
}

// withTransport installs a counting transport and short retry waits for one test.
func withTransport(t *testing.T, fn func(n int) (*http.Response, error)) *int {
	t.Helper()
	calls := 0
	Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return fn(calls)
	})
	prevWait := RetryMaxWait
	RetryMaxWait = 10 * time.Millisecond
	t.Cleanup(func() { Transport = nil; RetryMaxWait = prevWait })
	return &calls
}

func TestClientSharedInProduction(t *testing.T) {
	if Transport != nil {
		t.Skip("Transport is set")
	}
	if a, b := Client(), Client(); a != b {
		t.Fatalf("expected a shared client when Transport is nil")
	}
	if Client().GetClient().Jar != nil {
		t.Fatalf("shared client must not keep a cookie jar")
	}
}

func TestRetriesOn429ThenSucceeds(t *testing.T) {
	calls := withTransport(t, func(n int) (*http.Response, error) {
		if n == 1 {
			return response(http.StatusTooManyRequests, http.Header{"Retry-After": []string{"0"}}), nil
		}
		return response(http.StatusOK, nil), nil
	})
	resp, err := Client().R().Get("https://vendor.test/x")
	if err != nil || resp.StatusCode() != http.StatusOK || *calls != 2 {
		t.Fatalf("status=%v err=%v calls=%d", resp.StatusCode(), err, *calls)
	}
}

func TestNoRetryOnTransportError(t *testing.T) {
	calls := withTransport(t, func(int) (*http.Response, error) { return nil, errors.New("boom") })
	if _, err := Client().R().Post("https://vendor.test/submit"); err == nil {
		t.Fatalf("expected transport error")
	}
	if *calls != 1 {
		t.Fatalf("transport errors must not be retried, got %d calls", *calls)
	}
}

func TestNoRetryOn500(t *testing.T) {
	calls := withTransport(t, func(int) (*http.Response, error) { return response(http.StatusInternalServerError, nil), nil })
	if _, err := Client().R().Get("https://vendor.test/x"); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("500 must not be retried, got %d calls", *calls)
	}
}
