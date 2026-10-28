package signals

import (
	"maps"
	"strconv"
	"strings"
	"time"
)

// DateFields returns the date merge fields for now, as in the Tiga app: CurrentDate plus
// LastNDays values in search-operator form ("after:YYYY-MM-DD").
func DateFields(now time.Time) map[string]string {
	fields := map[string]string{"CurrentDate": now.Format("2006-01-02")}
	for _, days := range []int{7, 14, 30, 60, 90, 180} {
		fields["Last"+strconv.Itoa(days)+"Days"] = "after:" + now.AddDate(0, 0, -days).Format("2006-01-02")
	}
	return fields
}

// MergeFields returns the record's fields with the date fields on top (dates cannot be overridden).
func (r Record) MergeFields(now time.Time) map[string]string {
	fields := maps.Clone(r.fields)
	if fields == nil {
		fields = map[string]string{}
	}
	maps.Copy(fields, DateFields(now))
	return fields
}

// Render replaces each {{.Name}} in tmpl with fields[Name], or "" when it is not set.
// Text that is not a merge field is left as is.
func Render(tmpl string, fields map[string]string) string {
	return mergeFieldPattern.ReplaceAllStringFunc(tmpl, func(token string) string {
		name := strings.TrimSpace(mergeFieldPattern.FindStringSubmatch(token)[1])
		return fields[name]
	})
}

// MissingDependencies returns the signal's required fields that are empty on the record, in
// order. Date fields are always available.
func MissingDependencies(s Signal, r Record) []string {
	dates := DateFields(time.Now())
	var missing []string
	for _, dep := range s.Config.RequiredDependencies {
		if _, isDate := dates[dep]; isDate {
			continue
		}
		if strings.TrimSpace(r.Get(dep)) == "" {
			missing = append(missing, dep)
		}
	}
	return missing
}
