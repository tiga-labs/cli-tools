// Package httpx provides the vendor HTTP client shared by every Tiga CLI tool.
package httpx

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
)

// Transport, when non-nil (in tests), intercepts HTTP calls made by vendor clients.
var Transport http.RoundTripper

// Vendor HTTP client settings. The shared client reads these once, when it is first used;
// clients built for a non-nil Transport (tests) read them on every call.
var (
	// Timeout bounds each vendor HTTP attempt so a stalled connection cannot hang a worker.
	Timeout = 60 * time.Second
	// RetryCount is how many times a 429/503 response is retried.
	RetryCount = 3
	// RetryMaxWait caps the wait between retries, including Retry-After values.
	RetryMaxWait = 30 * time.Second
)

var (
	sharedClientOnce sync.Once
	sharedClient     *resty.Client
)

// Client returns the vendor HTTP client. In production a single client is shared so
// connections are reused; when Transport is set, a fresh client is built per call.
func Client() *resty.Client {
	if Transport != nil {
		return build().SetTransport(Transport)
	}
	sharedClientOnce.Do(func() { sharedClient = build() })
	return sharedClient
}

func build() *resty.Client {
	minWait := time.Second
	if RetryMaxWait < minWait {
		minWait = RetryMaxWait
	}
	return resty.New().
		SetCookieJar(nil). // never share vendor cookies across requests
		SetTimeout(Timeout).
		SetRetryCount(RetryCount).
		SetRetryWaitTime(minWait).
		SetRetryMaxWaitTime(RetryMaxWait).
		// A retry condition replaces resty's default retry-on-error, so transport errors are
		// not retried and non-idempotent submits (e.g. Seamless, Dropcontact) are never sent twice.
		AddRetryCondition(func(r *resty.Response, _ error) bool {
			return r != nil && (r.StatusCode() == http.StatusTooManyRequests || r.StatusCode() == http.StatusServiceUnavailable)
		}).
		SetRetryAfter(retryAfterHeader)
}

// retryAfterHeader honors a Retry-After header given in seconds; 0 falls back to jittered backoff.
func retryAfterHeader(_ *resty.Client, r *resty.Response) (time.Duration, error) {
	if r == nil {
		return 0, nil
	}
	secs, err := strconv.Atoi(strings.TrimSpace(r.Header().Get("Retry-After")))
	if err != nil || secs <= 0 {
		return 0, nil
	}
	return time.Duration(secs) * time.Second, nil
}
