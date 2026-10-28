package signals

import (
	"strconv"
	"strings"
	"time"
)

// AssessPrecondition reports whether fields satisfy p (a port of gandalf's
// AssessInsightPrecondition). Text matching is case-sensitive, as in gandalf. Any value that
// fails to parse, or an unknown operator, returns false.
func AssessPrecondition(fields map[string]string, p Precondition) bool {
	key := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(p.Property), "{{."), "}}")
	data := fields[key]

	switch p.PropertyType {
	case "text":
		switch p.Operator {
		case "IS_NOT_EMPTY":
			return data != ""
		case "ILIKE":
			return strings.Contains(data, p.Value)
		case "NOT ILIKE":
			return !strings.Contains(data, p.Value)
		case "=":
			return data == p.Value
		case "!=":
			return data != p.Value
		}
	case "number":
		have, err := strconv.Atoi(strings.TrimSpace(data))
		if err != nil {
			return false
		}
		want, err := strconv.Atoi(strings.TrimSpace(p.Value))
		if err != nil {
			return false
		}
		switch p.Operator {
		case "=":
			return have == want
		case "!=":
			return have != want
		case ">":
			return have > want
		case "<":
			return have < want
		case ">=":
			return have >= want
		case "<=":
			return have <= want
		}
	case "boolean":
		v := strings.ToLower(strings.TrimSpace(data))
		return (v == "true" && p.Operator == "IS TRUE") || (v == "false" && p.Operator == "IS FALSE")
	case "date":
		value, err := time.Parse("2006-01-02", strings.TrimSpace(data))
		if err != nil {
			return false
		}
		start, err := WindowStart(p.Operator, time.Now())
		if err != nil || p.Operator == "" {
			return false
		}
		return value.After(start)
	}
	return false
}
