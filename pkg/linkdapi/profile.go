package linkdapi

import (
	"context"
	"strings"
	"time"
)

// Profile is a person's LinkedIn profile.
type Profile struct {
	Username   string
	URN        string
	FirstName  string
	LastName   string
	Headline   string
	About      string
	Geo        string
	Experience []Experience // most recent first, as linkdapi returns them
	Skills     []string
	Languages  []string
	Education  []string // "School, Degree (Field)"
}

// Experience is one employer with its roles, most recent first.
type Experience struct {
	CompanyName string
	CompanyURL  string
	Roles       []Role
}

// Role is one title held at an employer.
type Role struct {
	Title          string
	EmploymentType string
	Start          time.Time
	End            time.Time // zero while the role is current
	Current        bool
}

type ymd struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

// time converts a linkdapi date; year 0 is "not set", and month/day 0 default to 1.
func (d ymd) time() time.Time {
	if d.Year == 0 {
		return time.Time{}
	}
	m, day := time.Month(d.Month), d.Day
	if m == 0 {
		m = time.January
	}
	if day == 0 {
		day = 1
	}
	return time.Date(d.Year, m, day, 0, 0, 0, 0, time.UTC)
}

type rawProfile struct {
	URN       string `json:"urn"`
	Username  string `json:"username"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Headline  string `json:"headline"`
	Summary   string `json:"summary"`
	Geo       struct {
		Full string `json:"full"`
	} `json:"geo"`
	Skills []struct {
		Name string `json:"name"`
	} `json:"skills"`
	Languages []struct {
		Name string `json:"name"`
	} `json:"languages"`
	Educations []struct {
		SchoolName   string `json:"schoolName"`
		Degree       string `json:"degree"`
		FieldOfStudy string `json:"fieldOfStudy"`
	} `json:"educations"`
	FullPositions []struct {
		CompanyID      int    `json:"companyId"`
		CompanyName    string `json:"companyName"`
		CompanyURL     string `json:"companyURL"`
		Title          string `json:"title"`
		EmploymentType string `json:"employmentType"`
		Start          ymd    `json:"start"`
		End            ymd    `json:"end"`
	} `json:"fullPositions"`
}

// Profile returns the profile for a LinkedIn username (the linkedin.com/in/<username> slug).
func (c *Client) Profile(ctx context.Context, username string) (Profile, error) {
	body, err := c.fetch(ctx, profileRequest(username))
	if err != nil {
		return Profile{}, err
	}
	var raw rawProfile
	if err := decodeData(body, &raw); err != nil {
		return Profile{}, err
	}
	return raw.toProfile(), nil
}

func (raw rawProfile) toProfile() Profile {
	p := Profile{
		Username:  strings.TrimSpace(raw.Username),
		URN:       strings.TrimSpace(raw.URN),
		FirstName: strings.TrimSpace(raw.FirstName),
		LastName:  strings.TrimSpace(raw.LastName),
		Headline:  strings.TrimSpace(raw.Headline),
		About:     strings.TrimSpace(raw.Summary),
		Geo:       strings.TrimSpace(raw.Geo.Full),
	}
	for _, s := range raw.Skills {
		if n := strings.TrimSpace(s.Name); n != "" {
			p.Skills = append(p.Skills, n)
		}
	}
	for _, l := range raw.Languages {
		if n := strings.TrimSpace(l.Name); n != "" {
			p.Languages = append(p.Languages, n)
		}
	}
	for _, e := range raw.Educations {
		line := strings.TrimSpace(e.SchoolName)
		if d := strings.TrimSpace(e.Degree); d != "" {
			line += ", " + d
		}
		if f := strings.TrimSpace(e.FieldOfStudy); f != "" {
			line += " (" + f + ")"
		}
		if line != "" {
			p.Education = append(p.Education, line)
		}
	}

	// Group roles by companyId (a promotion is several roles at one company), as gandalf's
	// buildExperienceData does. companyId 0 has no company page, so it is never grouped.
	seen := map[int]int{}
	for _, pos := range raw.FullPositions {
		role := Role{
			Title:          strings.TrimSpace(pos.Title),
			EmploymentType: strings.TrimSpace(pos.EmploymentType),
			Start:          pos.Start.time(),
			End:            pos.End.time(),
		}
		role.Current = role.End.IsZero()
		if idx, ok := seen[pos.CompanyID]; ok && pos.CompanyID != 0 {
			p.Experience[idx].Roles = append(p.Experience[idx].Roles, role)
			continue
		}
		if pos.CompanyID != 0 {
			seen[pos.CompanyID] = len(p.Experience)
		}
		p.Experience = append(p.Experience, Experience{
			CompanyName: strings.TrimSpace(pos.CompanyName),
			CompanyURL:  strings.TrimSpace(pos.CompanyURL),
			Roles:       []Role{role},
		})
	}
	return p
}

// CachedProfile returns a fresh cached profile without any HTTP call (never for a refreshing
// Client, which does not read the cache).
func (c *Client) CachedProfile(username string) (Profile, bool) {
	body, ok := c.cachedBody(profileRequest(username))
	if !ok {
		return Profile{}, false
	}
	var raw rawProfile
	if err := decodeData(body, &raw); err != nil {
		return Profile{}, false
	}
	return raw.toProfile(), true
}
