package linkdapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/httpx"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type stub struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (s *stub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

// setup points the cache at a temp dir and serves status/body for every request.
func setup(t *testing.T, status int, body string) (*stub, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "linkdapi")
	prevDir := CacheDir
	CacheDir = func() (string, error) { return dir, nil }
	s := &stub{}
	httpx.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.mu.Lock()
		s.reqs = append(s.reqs, req)
		s.mu.Unlock()
		time.Sleep(5 * time.Millisecond) // let concurrent callers overlap
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() { httpx.Transport = nil; CacheDir = prevDir })
	return s, dir
}

const testKey = "ld-secret-key-123"

// profileFixture is synthetic, shaped like the 2026-10-28 probe.
const profileFixture = `{"success":true,"statusCode":200,"message":"ok","errors":null,"data":{
	"urn":"ACoAAAtest","username":"jane-doe","firstName":"Jane","lastName":"Doe","headline":"VP Sales at Acme",
	"summary":"I sell things.","geo":{"full":"Austin, Texas, United States","country":"United States"},
	"skills":[{"name":"Sales"},{"name":"SaaS"}],"languages":[{"name":"English"}],
	"educations":[{"schoolName":"UT Austin","degree":"BBA","fieldOfStudy":"Marketing"}],
	"fullPositions":[
		{"companyId":42,"companyName":"Acme ","companyURL":"https://www.linkedin.com/company/acme/","title":"VP Sales","employmentType":"Full-time","start":{"year":2026,"month":9,"day":1},"end":{"year":0,"month":0,"day":0}},
		{"companyId":42,"companyName":"Acme","companyURL":"https://www.linkedin.com/company/acme/","title":"Director of Sales","employmentType":"","start":{"year":2024,"month":0,"day":0},"end":{"year":2026,"month":8,"day":31}},
		{"companyId":0,"companyName":"Freelance","companyURL":"","title":"Advisor","employmentType":"","start":{"year":2020,"month":3,"day":0},"end":{"year":0,"month":0,"day":0}},
		{"companyId":0,"companyName":"Side Gig","companyURL":"","title":"Mentor","employmentType":"","start":{"year":2019,"month":0,"day":0},"end":{"year":0,"month":0,"day":0}}
	]}}`

func TestProfileRequest(t *testing.T) {
	s, _ := setup(t, 200, profileFixture)
	p, err := New(testKey, false).Profile(context.Background(), "jane-doe")
	if err != nil {
		t.Fatal(err)
	}
	req := s.reqs[0]
	if req.URL.Path != "/api/v1/profile/full" || req.URL.Query().Get("username") != "jane-doe" || req.Header.Get("X-linkdapi-apikey") != testKey {
		t.Fatalf("request = %s %v", req.URL, req.Header)
	}
	if p.URN != "ACoAAAtest" || p.Username != "jane-doe" || p.FirstName != "Jane" || p.Headline != "VP Sales at Acme" || p.About != "I sell things." || p.Geo != "Austin, Texas, United States" {
		t.Fatalf("profile = %+v", p)
	}
	if strings.Join(p.Skills, ",") != "Sales,SaaS" || strings.Join(p.Languages, ",") != "English" || len(p.Education) != 1 || p.Education[0] != "UT Austin, BBA (Marketing)" {
		t.Fatalf("lists = %+v / %+v / %+v", p.Skills, p.Languages, p.Education)
	}
}

func TestExperienceGrouping(t *testing.T) {
	setup(t, 200, profileFixture)
	p, err := New(testKey, false).Profile(context.Background(), "jane-doe")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Experience) != 3 {
		t.Fatalf("experience = %+v", p.Experience)
	}
	acme := p.Experience[0]
	if acme.CompanyName != "Acme" || len(acme.Roles) != 2 || acme.Roles[0].Title != "VP Sales" || !acme.Roles[0].Current || acme.Roles[1].Current {
		t.Fatalf("acme = %+v", acme)
	}
	if !acme.Roles[0].Start.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) || !acme.Roles[1].Start.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("dates = %v %v", acme.Roles[0].Start, acme.Roles[1].Start)
	}
	if !acme.Roles[1].End.Equal(time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)) || !acme.Roles[0].End.IsZero() {
		t.Fatalf("ends = %v %v", acme.Roles[0].End, acme.Roles[1].End)
	}
	if p.Experience[1].CompanyName != "Freelance" || p.Experience[2].CompanyName != "Side Gig" {
		t.Fatalf("companyId 0 must not group: %+v", p.Experience)
	}
	if !p.Experience[1].Roles[0].Start.Equal(time.Date(2020, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("month-only date = %v", p.Experience[1].Roles[0].Start)
	}
}

func TestProfileNotFound(t *testing.T) {
	for _, c := range []struct {
		status   int
		body     string
		notFound bool
	}{
		{404, `{"success":false,"statusCode":404,"message":"Profile not found"}`, true},
		{200, `{"success":false,"statusCode":404,"message":"Profile not found","data":null}`, true},
		{500, `{"success":false,"statusCode":500,"message":"boom"}`, false},
		{200, `{"success":false,"statusCode":500,"message":"upstream error","data":null}`, false},
		{401, `{"success":false,"statusCode":401,"message":"Missing API Key"}`, false},
	} {
		setup(t, c.status, c.body)
		_, err := New(testKey, false).Profile(context.Background(), "nobody")
		if err == nil || errors.Is(err, ErrNotFound) != c.notFound || strings.Contains(err.Error(), testKey) {
			t.Errorf("%d %s: err = %v", c.status, c.body, err)
		}
	}
}

func TestCacheHitSkipsHTTP(t *testing.T) {
	s, _ := setup(t, 200, profileFixture)
	if _, err := New(testKey, false).Profile(context.Background(), "jane-doe"); err != nil {
		t.Fatal(err)
	}
	p, err := New(testKey, false).Profile(context.Background(), "Jane-Doe")
	if err != nil || p.Username != "jane-doe" || s.count() != 1 {
		t.Fatalf("err=%v calls=%d", err, s.count())
	}
	if !New("", false).Cached(KindProfile, "jane-doe") || New("", false).Cached(KindProfile, "someone-else") {
		t.Fatal("Cached is wrong")
	}
}

func TestFailuresAreNotCached(t *testing.T) {
	s, _ := setup(t, 500, `{"success":false,"statusCode":500,"message":"boom"}`)
	New(testKey, false).Profile(context.Background(), "jane-doe")
	New(testKey, false).Profile(context.Background(), "jane-doe")
	if s.count() != 2 {
		t.Fatalf("calls = %d", s.count())
	}
}

func TestRefreshBypassesRead(t *testing.T) {
	s, dir := setup(t, 200, profileFixture)
	New(testKey, false).Profile(context.Background(), "jane-doe")
	files, _ := os.ReadDir(dir)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(dir, files[0].Name()), old, old)
	if _, err := New(testKey, true).Profile(context.Background(), "jane-doe"); err != nil || s.count() != 2 {
		t.Fatalf("err=%v calls=%d", err, s.count())
	}
	info, _ := os.Stat(filepath.Join(dir, files[0].Name()))
	if !info.ModTime().After(old) {
		t.Fatal("refresh must rewrite the cache entry")
	}
}

func TestCacheExpired(t *testing.T) {
	s, dir := setup(t, 200, profileFixture)
	New(testKey, false).Profile(context.Background(), "jane-doe")
	files, _ := os.ReadDir(dir)
	stale := time.Now().Add(-ProfileTTL - time.Minute)
	os.Chtimes(filepath.Join(dir, files[0].Name()), stale, stale)
	if New("", false).Cached(KindProfile, "jane-doe") {
		t.Fatal("stale entry reported fresh")
	}
	New(testKey, false).Profile(context.Background(), "jane-doe")
	if s.count() != 2 {
		t.Fatalf("calls = %d", s.count())
	}
}

func TestCacheCorruptFileIsMiss(t *testing.T) {
	s, dir := setup(t, 200, profileFixture)
	New(testKey, false).Profile(context.Background(), "jane-doe")
	files, _ := os.ReadDir(dir)
	os.WriteFile(filepath.Join(dir, files[0].Name()), []byte(`{"success":true,"data":{"urn`), 0o600)
	p, err := New(testKey, false).Profile(context.Background(), "jane-doe")
	if err != nil || p.URN != "ACoAAAtest" || s.count() != 2 {
		t.Fatalf("p=%+v err=%v calls=%d", p, err, s.count())
	}
}

func TestCacheFilesPrivate(t *testing.T) {
	_, dir := setup(t, 200, profileFixture)
	New(testKey, false).Profile(context.Background(), "jane-doe")
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v %v", info.Mode().Perm(), err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	fi, _ := files[0].Info()
	data, _ := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if fi.Mode().Perm() != 0o600 || strings.Contains(string(data), testKey) || strings.Contains(files[0].Name(), "jane") {
		t.Fatalf("file mode=%v name=%s", fi.Mode().Perm(), files[0].Name())
	}
}

func TestClientDedupesConcurrentProfile(t *testing.T) {
	s, _ := setup(t, 200, profileFixture)
	c := New(testKey, true) // refresh: dedupe must not rely on the disk cache
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Profile(context.Background(), "jane-doe"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if s.count() != 1 {
		t.Fatalf("calls = %d", s.count())
	}
}

func TestPostDateParsing(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		raw  string
		want time.Time
		ok   bool
	}{
		{`{"timestamp":1793138254174,"fullDate":"x","relativeDay":"16h"}`, time.UnixMilli(1793138254174).UTC(), true},
		{`{"fullDate":"2026-10-27 21:57:34.00 +0000 UTC","relativeDay":"16h"}`, time.Date(2026, 10, 27, 21, 57, 34, 0, time.UTC), true},
		{`{"relativeDay":"3d"}`, now.AddDate(0, 0, -3), true},
		{`{"relativeDay":"2w"}`, now.AddDate(0, 0, -14), true},
		{`{"relativeDay":"4mo"}`, now.AddDate(0, -4, 0), true},
		{`{"relativeDay":"1yr"}`, now.AddDate(-1, 0, 0), true},
		{`{"relativeDay":"5h"}`, now.Add(-5 * time.Hour), true},
		{`{"relativeDay":"soon"}`, time.Time{}, false},
		{`{}`, time.Time{}, false},
		{`null`, time.Time{}, false},
		{`"2026-10-01"`, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), true},
		{`"3d"`, now.AddDate(0, 0, -3), true},
	}
	for _, c := range cases {
		got, ok := parsePostedAt([]byte(c.raw), now)
		if ok != c.ok || !got.Equal(c.want) {
			t.Errorf("%s: got %v %v, want %v %v", c.raw, got, ok, c.want, c.ok)
		}
	}
}

const postsFixture = `{"success":true,"statusCode":200,"message":"ok","errors":null,"data":{"cursor":"abc","posts":[
	{"urn":"urn:li:activity:1","url":"https://www.linkedin.com/feed/update/urn:li:activity:1","text":"We are hiring!","author":{"name":"Jane Doe"},"postedAt":{"timestamp":1793138254174},"resharedPostContent":null},
	{"urn":"urn:li:activity:2","url":"https://www.linkedin.com/feed/update/urn:li:activity:2","text":"","author":{"name":"Jane Doe"},"postedAt":{"timestamp":1793138254174},"resharedPostContent":{"text":"orig","author":{"name":"Other"}}},
	{"urn":"urn:li:activity:3","url":"https://www.linkedin.com/feed/update/urn:li:activity:3","text":"Great read","author":{"name":"Jane Doe"},"postedAt":{"timestamp":1793038254174},"resharedPostContent":{"text":"orig","author":{"name":"Other"}}},
	{"urn":"urn:li:activity:4","url":"https://www.linkedin.com/feed/update/urn:li:activity:4","text":"No date","author":{"name":"Jane Doe"},"postedAt":{},"resharedPostContent":null}
]}}`

func TestPostsDropsBareReshares(t *testing.T) {
	s, _ := setup(t, 200, postsFixture)
	posts, err := New(testKey, false).Posts(context.Background(), "ACoAAAtest")
	if err != nil {
		t.Fatal(err)
	}
	if s.reqs[0].URL.Path != "/api/v1/posts/all" || s.reqs[0].URL.Query().Get("urn") != "ACoAAAtest" {
		t.Fatalf("request = %s", s.reqs[0].URL)
	}
	if len(posts) != 2 || posts[0].URN != "urn:li:activity:1" || posts[0].Repost || !posts[1].Repost || posts[1].Text != "Great read" || posts[0].Author != "Jane Doe" {
		t.Fatalf("posts = %+v", posts)
	}
	if !posts[0].Date.Equal(time.UnixMilli(1793138254174).UTC()) {
		t.Fatalf("date = %v", posts[0].Date)
	}
}

func TestUsernameFromURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.linkedin.com/in/jane-doe":         "jane-doe",
		"https://www.linkedin.com/in/Jane-Doe/":        "jane-doe",
		"linkedin.com/in/jane-doe?trk=x":               "jane-doe",
		"https://www.linkedin.com/in/jane-doe/details": "jane-doe",
		"https://www.linkedin.com/company/acme":        "",
		"not a url":                                    "",
		"":                                             "",
	} {
		got, ok := UsernameFromURL(in)
		if got != want || ok != (want != "") {
			t.Errorf("%q: got %q %v", in, got, ok)
		}
	}
}
