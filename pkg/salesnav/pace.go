package salesnav

import "time"

// Rand is the random source for waits; *math/rand/v2.Rand satisfies it.
type Rand interface {
	Int64N(n int64) int64
}

// Pacer decides how long to wait before each page. It keeps Deku's pace for the first
// 1,000 leads and slows down after that, with a longer break every 500 leads.
type Pacer struct {
	R Rand
}

const (
	slowAfter  = 1000
	breakEvery = 500
)

// FirstWait is the wait before the first page.
func (p Pacer) FirstWait() time.Duration {
	return p.between(5*time.Second, 11*time.Second)
}

// NextWait is the wait after a page took the rows written from prevRows to rows.
func (p Pacer) NextWait(prevRows, rows int) time.Duration {
	if rows <= slowAfter {
		return p.between(2*time.Second, 5*time.Second)
	}
	d := p.between(6*time.Second, 15*time.Second)
	// One break per page that crossed a multiple of 500 above 1,000, however many it crossed.
	if rows/breakEvery > max(prevRows, slowAfter)/breakEvery {
		d += p.between(60*time.Second, 180*time.Second)
	}
	return d
}

// between is uniform in [lo, hi] at millisecond resolution.
func (p Pacer) between(lo, hi time.Duration) time.Duration {
	span := int64((hi-lo)/time.Millisecond) + 1
	return lo + time.Duration(p.R.Int64N(span))*time.Millisecond
}
