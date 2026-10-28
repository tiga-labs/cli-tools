package salesnav

import (
	"errors"
	"testing"
)

func TestValidateMax(t *testing.T) {
	for _, n := range []int{1, 1000, 7000} {
		if err := ValidateMax(n); err != nil {
			t.Errorf("ValidateMax(%d) = %v", n, err)
		}
	}
	for _, n := range []int{0, -5, 7001} {
		if err := ValidateMax(n); err == nil {
			t.Errorf("ValidateMax(%d) = nil, want error", n)
		}
	}
	want := "--max must be between 1 and 7000 (got 7001). Imports are capped at 7,000 leads to protect your LinkedIn account."
	if err := ValidateMax(7001); err == nil || err.Error() != want {
		t.Errorf("ValidateMax(7001) = %v, want %q", err, want)
	}
}

func leads(n int) []Lead { return make([]Lead, n) }

func TestNextStart(t *testing.T) {
	tests := []struct {
		name        string
		requested   int
		p           Page
		rows, limit int
		next        int
		done        DoneReason
	}{
		{"more pages", 0, Page{Start: 0, Count: 25, Total: 98, Leads: leads(25)}, 25, 1000, 25, NotDone},
		{"last page", 75, Page{Start: 75, Count: 25, Total: 98, Leads: leads(23)}, 98, 1000, 0, DoneEnd},
		{"max reached", 975, Page{Start: 975, Count: 25, Total: 987, Leads: leads(12)}, 1000, 1000, 0, DoneMax},
		{"max passed", 0, Page{Start: 0, Count: 25, Total: 5000, Leads: leads(25)}, 1010, 1000, 0, DoneMax},
		{"ended early (search cap)", 2500, Page{Start: 2500, Count: 25, Total: 3000}, 2500, 7000, 0, DoneEndedEarly},
		{"empty after page 1 with total 0", 1000, Page{Start: 1000, Count: 25, Total: 0}, 1000, 7000, 0, DoneEndedEarly},
		{"empty list", 0, Page{Start: 0, Count: 25, Total: 0}, 0, 1000, 0, DoneEnd},
		{"count 0, total 0", 0, Page{Start: 0, Count: 0, Total: 0, Leads: leads(25)}, 25, 1000, 0, DoneEnd},
		{"count 0 falls back to lead count", 50, Page{Start: 50, Count: 0, Total: 500, Leads: leads(25)}, 75, 1000, 75, NotDone},
	}
	for _, tt := range tests {
		next, done, err := NextStart(tt.requested, tt.p, tt.rows, tt.limit)
		if err != nil {
			t.Errorf("%s: err = %v", tt.name, err)
			continue
		}
		if done != tt.done || (done == NotDone && next != tt.next) {
			t.Errorf("%s: NextStart = (%d, %d), want (%d, %d)", tt.name, next, done, tt.next, tt.done)
		}
		if done == NotDone && next <= tt.requested {
			t.Errorf("%s: next %d does not advance past requested %d", tt.name, next, tt.requested)
		}
	}
}

// A response for a different offset than requested must stop the run, never re-request.
func TestNextStartPageMismatchStops(t *testing.T) {
	_, _, err := NextStart(25, Page{Start: 0, Count: 25, Total: 500, Leads: leads(25)}, 50, 1000)
	var stop *Stop
	if !errors.As(err, &stop) || stop.Kind != StopFailed {
		t.Fatalf("err = %v, want a StopFailed", err)
	}
	if want := "LinkedIn returned a different page than requested (asked for offset 25, got 0)"; stop.Reason != want {
		t.Fatalf("reason = %q, want %q", stop.Reason, want)
	}
}
