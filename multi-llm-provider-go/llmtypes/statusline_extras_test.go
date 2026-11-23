package llmtypes

import (
	"testing"
	"time"
)

func TestFormatUsageExtraWithReset(t *testing.T) {
	loc := time.FixedZone("IST", 5*3600+30*60)
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, loc)

	cases := []struct {
		name       string
		pct        float64
		resetsAt   int64
		want       string
	}{
		{"under 24h -> clock", 24, time.Date(2026, 6, 6, 15, 30, 0, 0, loc).Unix(), "5h 24% →3:30pm"},
		{"over 24h -> weekday", 41, time.Date(2026, 6, 13, 9, 0, 0, 0, loc).Unix(), "5h 41% →Sat"},
		{"past reset -> no suffix", 50, time.Date(2026, 6, 6, 9, 0, 0, 0, loc).Unix(), "5h 50%"},
		{"zero -> no suffix", 50, 0, "5h 50%"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FormatUsageExtraWithReset("5h", c.pct, c.resetsAt, now)
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
