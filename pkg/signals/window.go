package signals

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Windows are the date-window values used by date_range, started_new_job_within, and
// date preconditions.
var Windows = []string{"TWO_WEEKS", "ONE_MONTH", "THREE_MONTHS", "SIX_MONTHS", "TWELVE_MONTHS", "THIS_QUARTER", "THIS_YEAR"}

// IsWindow reports whether w is one of Windows.
func IsWindow(w string) bool {
	return slices.Contains(Windows, w)
}

// dayWindowStart is WindowStart rounded down to the start of its day, so a post or role dated
// any time on the boundary day is inside the window.
func dayWindowStart(window string, now time.Time) (time.Time, error) {
	start, err := WindowStart(window, now)
	if err != nil || start.IsZero() {
		return start, err
	}
	return time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location()), nil
}

// WindowStart returns the earliest time inside window, measured back from now. An empty
// window means no limit and returns the zero time.
func WindowStart(window string, now time.Time) (time.Time, error) {
	switch window {
	case "":
		return time.Time{}, nil
	case "TWO_WEEKS":
		return now.AddDate(0, 0, -14), nil
	case "ONE_MONTH":
		return now.AddDate(0, -1, 0), nil
	case "THREE_MONTHS":
		return now.AddDate(0, -3, 0), nil
	case "SIX_MONTHS":
		return now.AddDate(0, -6, 0), nil
	case "TWELVE_MONTHS":
		return now.AddDate(0, -12, 0), nil
	case "THIS_QUARTER":
		quarter := (now.Month() - 1) / 3
		return time.Date(now.Year(), quarter*3+1, 1, 0, 0, 0, 0, now.Location()), nil
	case "THIS_YEAR":
		return time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location()), nil
	}
	return time.Time{}, fmt.Errorf("invalid window %q (expected one of: %s)", window, strings.Join(Windows, ", "))
}
