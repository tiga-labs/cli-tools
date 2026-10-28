package linkdapi

import (
	"context"
	"errors"
	"testing"
)

func TestCompanySlugFromURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.linkedin.com/company/acme":           "acme",
		"linkedin.com/company/acme/about/":                "acme",
		"https://www.linkedin.com/company/Acme-Inc?trk=x": "acme-inc",
		"https://www.linkedin.com/showcase/acme-cloud/":   "acme-cloud",
		"https://www.linkedin.com/school/stanford-univ":   "stanford-univ",
		"https://www.linkedin.com/in/jane-doe":            "",
		"https://acme.com/company/acme":                   "",
		"":                                                "",
	} {
		got, ok := CompanySlugFromURL(in)
		if got != want || ok != (want != "") {
			t.Errorf("%q: got %q %v", in, got, ok)
		}
	}
}

func TestCompanyIDRequestAndCache(t *testing.T) {
	s, _ := setup(t, 200, `{"success":true,"statusCode":200,"data":{"id":"1035","universalName":"microsoft"}}`)
	id, err := New(testKey, false).CompanyID(context.Background(), "Microsoft")
	if err != nil || id != 1035 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	req := s.reqs[0]
	if req.URL.Path != "/api/v1/companies/company/universal-name-to-id" || req.URL.Query().Get("universalName") != "microsoft" {
		t.Fatalf("request = %s", req.URL)
	}
	if id, err := New(testKey, false).CompanyID(context.Background(), "microsoft"); err != nil || id != 1035 || s.count() != 1 {
		t.Fatalf("cache miss: id=%d err=%v calls=%d", id, err, s.count())
	}
	if id, ok := New("", false).CachedCompanyID("microsoft"); !ok || id != 1035 {
		t.Fatalf("CachedCompanyID = %d %v", id, ok)
	}
	if _, ok := New("", true).CachedCompanyID("microsoft"); ok {
		t.Fatal("a refreshing client must not report cached")
	}
}

func TestCompanyNotFound(t *testing.T) {
	setup(t, 404, `{"success":false,"statusCode":404,"message":"not found"}`)
	if _, err := New(testKey, false).CompanyID(context.Background(), "nope"); !errors.Is(err, ErrCompanyNotFound) || err.Error() != "linkdapi: company not found" {
		t.Fatalf("err = %v", err)
	}
	setup(t, 200, `{"success":true,"statusCode":200,"data":{"id":"","universalName":"nope"}}`)
	if _, err := New(testKey, false).CompanyID(context.Background(), "nope"); !errors.Is(err, ErrCompanyNotFound) {
		t.Fatalf("empty id: err = %v", err)
	}
}

const companyPostsFixture = `{"success":true,"statusCode":200,"data":{"posts":[
	{"urn":"urn:li:activity:1","url":"https://www.linkedin.com/feed/update/urn:li:activity:1","text":"","header":"Acme reposted this","postedAt":{"timestamp":1793134710973,"relativeDay":""},"author":{"name":"","type":"PERSON"},"resharedPostContent":null},
	{"urn":"urn:li:activity:2","url":"https://www.linkedin.com/feed/update/urn:li:activity:2","text":"Welcome our new CFO","postedAt":{"timestamp":1793117077819,"relativeDay":"23h"},"author":{"name":""},"resharedPostContent":null}
]}}`

func TestCompanyPostsDecodeAndPaging(t *testing.T) {
	s, _ := setup(t, 200, companyPostsFixture)
	posts, err := New(testKey, false).CompanyPosts(context.Background(), 1035, 2)
	if err != nil {
		t.Fatal(err)
	}
	q := s.reqs[0].URL.Query()
	if s.reqs[0].URL.Path != "/api/v1/companies/company/posts" || q.Get("id") != "1035" || q.Get("start") != "20" {
		t.Fatalf("request = %s", s.reqs[0].URL)
	}
	if len(posts) != 1 || posts[0].Text != "Welcome our new CFO" {
		t.Fatalf("empty-text repost must be dropped: %+v", posts)
	}
	New(testKey, false).CompanyPosts(context.Background(), 1035, 0)
	if s.count() != 2 || New("", false).CachedCompanyPosts(1035, 1) || !New("", false).CachedCompanyPosts(1035, 2) {
		t.Fatalf("pages must cache separately: calls=%d", s.count())
	}
}

func TestPostsDropEmptyText(t *testing.T) {
	setup(t, 200, `{"success":true,"statusCode":200,"data":{"posts":[{"urn":"a","url":"u","text":"  ","postedAt":{"timestamp":1793117077819},"resharedPostContent":null}]}}`)
	posts, err := New(testKey, false).Posts(context.Background(), "ACoAAAtest")
	if err != nil || len(posts) != 0 {
		t.Fatalf("posts=%+v err=%v", posts, err)
	}
}

const jobsFixture = `{"success":true,"statusCode":200,"data":{"total":3,"jobs":[
	{"id":"1","url":"https://www.linkedin.com/jobs/view/1","title":"Account Executive  ","companyName":"Acme","location":"Redmond, WA (Hybrid)","listedAt":1792068193000},
	{"id":"2","url":"https://www.linkedin.com/jobs/view/2","title":"Customer Success Account Manager","companyName":"Acme","location":"Tokyo, Tokyo, Japan (Hybrid)","listedAt":1792068193000},
	{"id":"3","url":"https://www.linkedin.com/jobs/view/3","title":"Senior Account Executive","companyName":"Acme","location":"United States (Remote)","listedAt":1792068193000}
]}}`

func TestJobsRequest(t *testing.T) {
	s, _ := setup(t, 200, jobsFixture)
	res, err := New(testKey, false).Jobs(context.Background(), 1035, "Account Executive")
	if err != nil {
		t.Fatal(err)
	}
	q := s.reqs[0].URL.Query()
	if s.reqs[0].URL.Path != "/api/v1/search/jobs" || q.Get("companies") != "1035" || q.Get("keyword") != "account executive" || q.Has("locations") || q.Has("geoId") {
		t.Fatalf("request = %s", s.reqs[0].URL)
	}
	if res.Total != 3 || len(res.Jobs) != 3 || res.Jobs[0].Title != "Account Executive" || res.Jobs[0].URL != "https://www.linkedin.com/jobs/view/1" || res.Jobs[0].Location != "Redmond, WA (Hybrid)" {
		t.Fatalf("res = %+v", res)
	}
}

func TestJobsCacheKeyIncludesKeyword(t *testing.T) {
	s, _ := setup(t, 200, jobsFixture)
	c := New(testKey, false)
	c.Jobs(context.Background(), 1035, "Account Executive")
	c.Jobs(context.Background(), 1035, "account executive") // same search, different case
	c.Jobs(context.Background(), 1035, "CFO")
	c.Jobs(context.Background(), 42, "CFO")
	if s.count() != 3 {
		t.Fatalf("calls = %d", s.count())
	}
	if !New("", false).CachedJobs(1035, "ACCOUNT EXECUTIVE") || New("", false).CachedJobs(1035, "SDR") {
		t.Fatal("CachedJobs is wrong")
	}
}
