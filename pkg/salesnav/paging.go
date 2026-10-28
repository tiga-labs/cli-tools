package salesnav

import "fmt"

// Lead limits per import (D2).
const (
	DefaultMax = 1000
	MaxLimit   = 7000
)

// ValidateMax rejects a --max outside 1..MaxLimit. It never clamps.
func ValidateMax(n int) error {
	if n < 1 || n > MaxLimit {
		return fmt.Errorf("--max must be between 1 and %d (got %d). Imports are capped at 7,000 leads to protect your LinkedIn account.", MaxLimit, n)
	}
	return nil
}

// DoneReason says why there is no next page.
type DoneReason int

const (
	NotDone DoneReason = iota
	// DoneEnd: every lead LinkedIn reported has been fetched.
	DoneEnd
	// DoneMax: the --max limit has been reached.
	DoneMax
	// DoneEndedEarly: LinkedIn stopped returning leads before its reported total
	// (saved searches are capped by Sales Nav).
	DoneEndedEarly
)

// NextStart returns the offset of the next page, or why there is none. requested is the
// offset that was asked for and rows the total written so far, including page p. A
// response for a different offset is a *Stop: re-requesting would repeat one request
// over and over.
func NextStart(requested int, p Page, rows, limit int) (int, DoneReason, error) {
	if p.Start != requested {
		return 0, NotDone, &Stop{Kind: StopFailed, Reason: fmt.Sprintf("LinkedIn returned a different page than requested (asked for offset %d, got %d)", requested, p.Start)}
	}
	if len(p.Leads) == 0 {
		// After page 1, an empty page means the results were cut short, whatever total says.
		if requested > 0 || p.Total > 0 {
			return 0, DoneEndedEarly, nil
		}
		return 0, DoneEnd, nil
	}
	if rows >= limit {
		return 0, DoneMax, nil
	}
	step := p.Count
	if step <= 0 {
		step = len(p.Leads)
	}
	if requested+step >= p.Total {
		return 0, DoneEnd, nil
	}
	return requested + step, NotDone, nil
}
