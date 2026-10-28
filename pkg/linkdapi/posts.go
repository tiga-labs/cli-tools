package linkdapi

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Post is one LinkedIn post, newest first as linkdapi returns them.
type Post struct {
	URN    string
	URL    string
	Text   string
	Author string
	Date   time.Time
	Repost bool // a reshare with the poster's own commentary in Text
}

type rawPost struct {
	URN      string          `json:"urn"`
	URL      string          `json:"url"`
	Text     string          `json:"text"`
	PostedAt json.RawMessage `json:"postedAt"`
	Author   struct {
		Name string `json:"name"`
	} `json:"author"`
	Reshared json.RawMessage `json:"resharedPostContent"`
}

// now is the clock for relative post ages (a var so tests can pin it).
var now = time.Now

// Posts returns the first page (up to 100) of a person's posts by profile URN. Reshares with
// no commentary and posts with no parseable date are dropped.
func (c *Client) Posts(ctx context.Context, urn string) ([]Post, error) {
	body, err := c.fetch(ctx, KindPosts, urn)
	if err != nil {
		return nil, err
	}
	var data struct {
		Posts []rawPost `json:"posts"`
	}
	if err := decodeData(body, &data); err != nil {
		return nil, err
	}
	ref := now()
	posts := []Post{}
	for _, rp := range data.Posts {
		text := strings.TrimSpace(rp.Text)
		repost := len(rp.Reshared) > 0 && string(rp.Reshared) != "null"
		if repost && text == "" {
			continue
		}
		date, ok := parsePostedAt(rp.PostedAt, ref)
		if !ok {
			continue
		}
		posts = append(posts, Post{
			URN: rp.URN, URL: rp.URL, Text: text, Author: strings.TrimSpace(rp.Author.Name),
			Date: date, Repost: repost,
		})
	}
	return posts, nil
}

var relativeAge = regexp.MustCompile(`^(\d+)\s*(h|d|w|mo|mos|yr|yrs)$`)

// parsePostedAt reads linkdapi's postedAt: an object {timestamp (unix ms), fullDate,
// relativeDay}, tried in that order, or a bare string date / relative age.
func parsePostedAt(raw json.RawMessage, ref time.Time) (time.Time, bool) {
	var obj struct {
		Timestamp   int64  `json:"timestamp"`
		FullDate    string `json:"fullDate"`
		RelativeDay string `json:"relativeDay"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if obj.Timestamp > 0 {
			return time.UnixMilli(obj.Timestamp).UTC(), true
		}
		if t, err := time.Parse("2006-01-02 15:04:05.00 -0700 MST", strings.TrimSpace(obj.FullDate)); err == nil {
			return t.UTC(), true
		}
		return parseRelative(obj.RelativeDay, ref)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return time.Time{}, false
	}
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return parseRelative(s, ref)
}

// parseRelative converts LinkedIn ages ("5h", "3d", "2w", "4mo", "1yr") to a time before ref.
func parseRelative(s string, ref time.Time) (time.Time, bool) {
	m := relativeAge.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return time.Time{}, false
	}
	n, _ := strconv.Atoi(m[1])
	switch m[2] {
	case "h":
		return ref.Add(-time.Duration(n) * time.Hour), true
	case "d":
		return ref.AddDate(0, 0, -n), true
	case "w":
		return ref.AddDate(0, 0, -7*n), true
	case "mo", "mos":
		return ref.AddDate(0, -n, 0), true
	default:
		return ref.AddDate(-n, 0, 0), true
	}
}
