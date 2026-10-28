package salesnav

import (
	"testing"
	"time"
)

type minRand struct{}
type maxRand struct{}
type midRand struct{}

func (minRand) Int64N(int64) int64   { return 0 }
func (maxRand) Int64N(n int64) int64 { return n - 1 }
func (midRand) Int64N(n int64) int64 { return n / 2 }

func TestFirstWait(t *testing.T) {
	if got := (Pacer{R: minRand{}}).FirstWait(); got != 5*time.Second {
		t.Errorf("min FirstWait = %s, want 5s", got)
	}
	if got := (Pacer{R: maxRand{}}).FirstWait(); got != 11*time.Second {
		t.Errorf("max FirstWait = %s, want 11s", got)
	}
}

func TestNextWaitTiers(t *testing.T) {
	lo, hi := Pacer{R: minRand{}}, Pacer{R: maxRand{}}
	tests := []struct {
		prev, rows int
		min, max   time.Duration
	}{
		{975, 1000, 2 * time.Second, 5 * time.Second},
		{1000, 1025, 6 * time.Second, 15 * time.Second},
	}
	for _, tt := range tests {
		if got := lo.NextWait(tt.prev, tt.rows); got != tt.min {
			t.Errorf("min NextWait(%d,%d) = %s, want %s", tt.prev, tt.rows, got, tt.min)
		}
		if got := hi.NextWait(tt.prev, tt.rows); got != tt.max {
			t.Errorf("max NextWait(%d,%d) = %s, want %s", tt.prev, tt.rows, got, tt.max)
		}
	}
}

func TestNextWaitBreaks(t *testing.T) {
	lo, hi := Pacer{R: minRand{}}, Pacer{R: maxRand{}}
	tests := []struct {
		prev, rows int
		want       time.Duration // with minRand
	}{
		{1475, 1500, 66 * time.Second},
		{1500, 1525, 6 * time.Second},
		{975, 1000, 2 * time.Second},
		{475, 500, 2 * time.Second},
		{1490, 1510, 66 * time.Second},
		{1490, 2010, 66 * time.Second},
		{1990, 2005, 66 * time.Second},
	}
	for _, tt := range tests {
		if got := lo.NextWait(tt.prev, tt.rows); got != tt.want {
			t.Errorf("NextWait(%d,%d) = %s, want %s", tt.prev, tt.rows, got, tt.want)
		}
	}
	if got := hi.NextWait(1475, 1500); got != 195*time.Second {
		t.Errorf("max NextWait(1475,1500) = %s, want 195s", got)
	}
}

func TestRunDuration(t *testing.T) {
	total := func(r Rand) time.Duration {
		p := Pacer{R: r}
		d := p.FirstWait()
		for rows := 50; rows <= 7000; rows += 25 {
			d += p.NextWait(rows-25, rows)
		}
		return d
	}
	if d := total(midRand{}); d < 60*time.Minute || d > 75*time.Minute {
		t.Errorf("average 7,000-lead run = %s, want 60–75 min", d)
	}
	if d := total(minRand{}); d < 35*time.Minute {
		t.Errorf("fastest 7,000-lead run = %s, want >= 35 min", d)
	}
	if d := total(maxRand{}); d > 100*time.Minute {
		t.Errorf("slowest 7,000-lead run = %s, want <= 100 min", d)
	}
}
