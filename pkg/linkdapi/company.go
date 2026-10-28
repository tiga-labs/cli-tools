package linkdapi

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// CompanyPostsPageSize is how many posts one company-posts page holds (probe, 2026-10-28).
const CompanyPostsPageSize = 10

func companyIDRequest(slug string) request {
	return request{"/companies/company/universal-name-to-id", map[string]string{"universalName": strings.ToLower(strings.TrimSpace(slug))}, CompanyIDTTL}
}

func companyPostsRequest(id, page int) request {
	return request{"/companies/company/posts", map[string]string{"id": strconv.Itoa(id), "start": strconv.Itoa(page * CompanyPostsPageSize)}, PostsTTL}
}

// jobsRequest never sends a location: linkdapi's locations filter returns no jobs and geoId is
// ignored (probe, 2026-10-28), so callers filter on Job.Location.
func jobsRequest(companyID int, keyword string) request {
	return request{"/search/jobs", map[string]string{"companies": strconv.Itoa(companyID), "keyword": strings.ToLower(strings.TrimSpace(keyword))}, JobsTTL}
}

// CompanyID resolves a company's LinkedIn slug (linkedin.com/company/<slug>) to its id.
func (c *Client) CompanyID(ctx context.Context, slug string) (int, error) {
	body, err := c.fetch(ctx, companyIDRequest(slug))
	if errors.Is(err, ErrNotFound) {
		return 0, ErrCompanyNotFound
	}
	if err != nil {
		return 0, err
	}
	return decodeCompanyID(body)
}

func decodeCompanyID(body []byte) (int, error) {
	var data struct {
		ID string `json:"id"`
	}
	if err := decodeData(body, &data); err != nil {
		return 0, err
	}
	id, err := strconv.Atoi(strings.TrimSpace(data.ID))
	if err != nil || id <= 0 {
		return 0, ErrCompanyNotFound
	}
	return id, nil
}

// CachedCompanyID returns a fresh cached company id without any HTTP call.
func (c *Client) CachedCompanyID(slug string) (int, bool) {
	body, ok := c.cachedBody(companyIDRequest(slug))
	if !ok {
		return 0, false
	}
	id, err := decodeCompanyID(body)
	return id, err == nil
}

// CompanyPosts returns one page (CompanyPostsPageSize posts, newest first; page 0 is the
// newest) of a company's posts. Empty-text reposts are dropped.
func (c *Client) CompanyPosts(ctx context.Context, id, page int) ([]Post, error) {
	body, err := c.fetch(ctx, companyPostsRequest(id, page))
	if err != nil {
		return nil, err
	}
	return decodePosts(body)
}

// CachedCompanyPosts reports whether a page of company posts is fresh in the cache.
func (c *Client) CachedCompanyPosts(id, page int) bool {
	return c.cached(companyPostsRequest(id, page))
}

// Job is one LinkedIn job listing.
type Job struct {
	Title    string
	URL      string
	Location string // e.g. "Redmond, WA (Hybrid)", "United States (Remote)"
}

// JobSearch is one page of job search results. Total is linkdapi's count, which is the page
// size rather than the full match count.
type JobSearch struct {
	Total int
	Jobs  []Job
}

// JobsPageSize is how many listings one search page holds (probe, 2026-10-28).
const JobsPageSize = 20

// Jobs searches a company's open LinkedIn jobs for keyword (one page, 2 credits).
func (c *Client) Jobs(ctx context.Context, companyID int, keyword string) (JobSearch, error) {
	body, err := c.fetch(ctx, jobsRequest(companyID, keyword))
	if errors.Is(err, ErrNotFound) {
		return JobSearch{Jobs: []Job{}}, nil
	}
	if err != nil {
		return JobSearch{}, err
	}
	var data struct {
		Total int `json:"total"`
		Jobs  []struct {
			Title    string `json:"title"`
			URL      string `json:"url"`
			Location string `json:"location"`
		} `json:"jobs"`
	}
	if err := decodeData(body, &data); err != nil {
		return JobSearch{}, err
	}
	res := JobSearch{Total: data.Total, Jobs: []Job{}}
	for _, j := range data.Jobs {
		res.Jobs = append(res.Jobs, Job{
			Title:    strings.TrimSpace(strings.ReplaceAll(j.Title, " ", " ")),
			URL:      strings.TrimSpace(j.URL),
			Location: strings.TrimSpace(j.Location),
		})
	}
	return res, nil
}

// CachedJobs reports whether a job search is fresh in the cache.
func (c *Client) CachedJobs(companyID int, keyword string) bool {
	return c.cached(jobsRequest(companyID, keyword))
}
