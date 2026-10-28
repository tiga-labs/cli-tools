package signals

import (
	"testing"
	"time"
)

func TestWindowStart(t *testing.T) {
	now := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	cases := map[string]time.Time{
		"TWO_WEEKS":     time.Date(2026, 10, 13, 15, 0, 0, 0, time.UTC),
		"ONE_MONTH":     time.Date(2026, 9, 27, 15, 0, 0, 0, time.UTC),
		"THREE_MONTHS":  time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC),
		"SIX_MONTHS":    time.Date(2026, 4, 27, 15, 0, 0, 0, time.UTC),
		"TWELVE_MONTHS": time.Date(2025, 10, 27, 15, 0, 0, 0, time.UTC),
		"THIS_QUARTER":  time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		"THIS_YEAR":     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	for w, want := range cases {
		got, err := WindowStart(w, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("WindowStart(%s) = %v, %v; want %v", w, got, err, want)
		}
	}
	if got, err := WindowStart("", now); err != nil || !got.IsZero() {
		t.Fatalf("empty window = %v, %v", got, err)
	}
	if _, err := WindowStart("LAST_WEEK", now); err == nil {
		t.Fatal("expected error")
	}
}
