package signals

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/linkdapi"
)

// personLiPrefix marks merge fields filled from the person's LinkedIn profile.
const personLiPrefix = "PersonLi_"

// companyLiPrefix marks merge fields filled from the company's LinkedIn page.
const companyLiPrefix = "CompanyLi_"

// personLiPostFields need posts, not just the profile (CompanyLi_* fields are all posts).
var personLiPostFields = []string{"PersonLi_Posts", "PersonLi_RecentPosts", "CompanyLi_Posts", "CompanyLi_RecentPosts"}

// recentPostDays and recentRoleChangeDays match gandalf's RecentPosts and RecentRoleChange.
const (
	recentPostDays       = 14
	recentRoleChangeDays = 60
)

// PersonLiFields maps a profile (and posts, when fetched) to gandalf's PersonLi_* merge fields.
// Fields linkdapi does not provide (Causes, Projects, ...) are left out.
func PersonLiFields(p linkdapi.Profile, posts []linkdapi.Post, now time.Time) map[string]string {
	f := map[string]string{
		"PersonLi_FirstName": p.FirstName,
		"PersonLi_LastName":  p.LastName,
		"PersonLi_Headline":  p.Headline,
		"PersonLi_About":     p.About,
		"PersonLi_Summary":   p.About,
		"PersonLi_Skills":    strings.Join(p.Skills, ", "),
		"PersonLi_Languages": strings.Join(p.Languages, ", "),
		"PersonLi_Education": strings.Join(p.Education, "\n"),
	}

	var lines []string
	for _, exp := range p.Experience {
		lines = append(lines, exp.CompanyName)
		for _, role := range exp.Roles {
			lines = append(lines, role.Title)
			from := fmt.Sprintf("from: %v/%v", role.Start.Month(), role.Start.Year())
			if role.Current {
				lines = append(lines, from+" to: Present")
			} else {
				lines = append(lines, fmt.Sprintf("%s - to: %v/%v", from, role.End.Month(), role.End.Year()))
			}
		}
	}
	f["PersonLi_Experience"] = strings.Join(lines, "\n")

	// The current employer is the first experience; its oldest role is when they joined, and
	// the newest role's start is the latest promotion (gandalf used the newest role's end
	// date, which is always empty for a current role).
	recentChange := false
	if len(p.Experience) > 0 && len(p.Experience[0].Roles) > 0 {
		roles := p.Experience[0].Roles
		joined := roles[len(roles)-1].Start
		f["PersonLi_LatestNewJobDate"] = dateOrEmpty(joined)
		if len(roles) > 1 {
			f["PersonLi_LatestPromotionDate"] = dateOrEmpty(roles[0].Start)
		}
		recentChange = !joined.IsZero() && joined.After(now.AddDate(0, 0, -recentRoleChangeDays))
	}
	f["PersonLi_RecentRoleChange"] = strconv.FormatBool(recentChange)

	if posts != nil {
		f["PersonLi_Posts"], f["PersonLi_RecentPosts"] = postLines(posts, now)
	}
	return f
}

// CompanyLiFields maps a company's posts to the CompanyLi_* merge fields.
func CompanyLiFields(posts []linkdapi.Post, now time.Time) map[string]string {
	all, recent := postLines(posts, now)
	return map[string]string{"CompanyLi_Posts": all, "CompanyLi_RecentPosts": recent}
}

// postLines formats posts one per line as "YYYY-MM-DD: text": all of them, and the last 14 days.
func postLines(posts []linkdapi.Post, now time.Time) (all, recent string) {
	var a, r []string
	for _, post := range posts {
		line := post.Date.Format("2006-01-02") + ": " + post.Text
		a = append(a, line)
		if post.Date.After(now.AddDate(0, 0, -recentPostDays)) {
			r = append(r, line)
		}
	}
	return strings.Join(a, "\n"), strings.Join(r, "\n")
}

func dateOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}
