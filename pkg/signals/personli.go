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

// personLiPostFields need the person's posts, not just the profile.
var personLiPostFields = []string{"PersonLi_Posts", "PersonLi_RecentPosts"}

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
		var all, recent []string
		for _, post := range posts {
			line := post.Date.Format("2006-01-02") + ": " + post.Text
			all = append(all, line)
			if post.Date.After(now.AddDate(0, 0, -recentPostDays)) {
				recent = append(recent, line)
			}
		}
		f["PersonLi_Posts"] = strings.Join(all, "\n")
		f["PersonLi_RecentPosts"] = strings.Join(recent, "\n")
	}
	return f
}

func dateOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}
