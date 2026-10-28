// Package linkdapi is a small client for the linkdapi LinkedIn data API with an on-disk
// response cache, so reruns and prompt tweaks cost no vendor calls.
package linkdapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/httpx"
)

// BaseURL is the linkdapi API root (a var so tests can point elsewhere).
var BaseURL = "https://linkdapi.com/api/v1"

// Cache lifetimes: a role change must show within a TWO_WEEKS window, and posts change daily.
var (
	ProfileTTL = 7 * 24 * time.Hour
	PostsTTL   = 24 * time.Hour
)

// ErrNotFound is returned (wrapped) when linkdapi has no such profile.
var ErrNotFound = errors.New("linkdapi: profile not found")

// Cache kinds, for Cached.
const (
	KindProfile = "profile"
	KindPosts   = "posts"
)

const maxMessageRunes = 200

// Client fetches linkdapi data. It is safe for concurrent use; each request runs at most
// once per Client, and successful responses are cached on disk.
type Client struct {
	apiKey  string
	refresh bool

	mu    sync.Mutex
	calls map[string]*call
}

type call struct {
	once sync.Once
	body []byte
	err  error
}

// New returns a Client. With refresh set, the disk cache is not read (but is still written).
func New(apiKey string, refresh bool) *Client {
	return &Client{apiKey: apiKey, refresh: refresh, calls: map[string]*call{}}
}

type endpoint struct {
	path  string
	param string
	ttl   time.Duration
}

func endpointFor(kind string) endpoint {
	if kind == KindPosts {
		return endpoint{path: "/posts/all", param: "urn", ttl: PostsTTL}
	}
	return endpoint{path: "/profile/full", param: "username", ttl: ProfileTTL}
}

// normalizeID lowercases profile usernames (LinkedIn treats them case-insensitively).
func normalizeID(kind, id string) string {
	id = strings.TrimSpace(id)
	if kind == KindProfile {
		id = strings.ToLower(id)
	}
	return id
}

// Cached reports whether a fresh response for kind/id is on disk, without any HTTP call.
func (c *Client) Cached(kind, id string) bool {
	ep := endpointFor(kind)
	_, ok := readCache(cacheKey(ep.path, ep.param, normalizeID(kind, id)), ep.ttl)
	return ok
}

// fetch returns the response body for kind/id: from this Client's earlier call, the disk
// cache, or linkdapi.
func (c *Client) fetch(ctx context.Context, kind, id string) ([]byte, error) {
	ep := endpointFor(kind)
	id = normalizeID(kind, id)
	key := cacheKey(ep.path, ep.param, id)

	c.mu.Lock()
	cl, ok := c.calls[key]
	if !ok {
		cl = &call{}
		c.calls[key] = cl
	}
	c.mu.Unlock()

	cl.once.Do(func() {
		if !c.refresh {
			if body, ok := readCache(key, ep.ttl); ok {
				cl.body = body
				return
			}
		}
		cl.body, cl.err = c.get(ctx, ep, id)
		if cl.err == nil {
			writeCache(key, cl.body) // a cache write failure only costs a refetch later
		}
	})
	return cl.body, cl.err
}

type envelope struct {
	Success    bool            `json:"success"`
	StatusCode int             `json:"statusCode"`
	Message    string          `json:"message"`
	Data       json.RawMessage `json:"data"`
}

func (c *Client) get(ctx context.Context, ep endpoint, id string) ([]byte, error) {
	resp, err := httpx.Client().R().
		SetContext(ctx).
		SetHeader("X-linkdapi-apikey", c.apiKey).
		SetQueryParams(map[string]string{ep.param: id}).
		Get(BaseURL + ep.path)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("linkdapi: request failed: %w", err)
	}
	var env envelope
	jsonErr := json.Unmarshal(resp.Body(), &env)
	code := resp.StatusCode()
	if code == http.StatusNotFound || (jsonErr == nil && !env.Success && env.StatusCode == http.StatusNotFound) {
		return nil, ErrNotFound
	}
	if code < 200 || code >= 300 {
		return nil, statusError(code, env.Message)
	}
	if jsonErr != nil {
		return nil, errors.New("linkdapi: invalid response body")
	}
	if !env.Success {
		return nil, statusError(env.StatusCode, env.Message)
	}
	return resp.Body(), nil
}

func statusError(code int, message string) error {
	msg := fmt.Sprintf("linkdapi: %d %s", code, http.StatusText(code))
	if m := []rune(strings.TrimSpace(message)); len(m) > 0 {
		if len(m) > maxMessageRunes {
			m = append(m[:maxMessageRunes], '…')
		}
		msg += ": " + string(m)
	}
	return errors.New(msg)
}

// decodeData unmarshals an envelope's data into v.
func decodeData(body []byte, v any) error {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return errors.New("linkdapi: invalid response body")
	}
	if err := json.Unmarshal(env.Data, v); err != nil {
		return errors.New("linkdapi: unexpected response shape")
	}
	return nil
}

// UsernameFromURL returns the lowercased <username> of a linkedin.com/in/<username> URL.
func UsernameFromURL(u string) (string, bool) {
	u = strings.TrimSpace(u)
	if u == "" {
		return "", false
	}
	if !strings.Contains(u, "://") {
		u = "https://" + u
	}
	parsed, err := url.Parse(u)
	if err != nil || !strings.HasSuffix(strings.ToLower(parsed.Hostname()), "linkedin.com") {
		return "", false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "in" || parts[1] == "" {
		return "", false
	}
	return strings.ToLower(parts[1]), true
}
