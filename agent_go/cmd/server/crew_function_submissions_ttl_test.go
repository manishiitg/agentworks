package server

import (
	"testing"
	"time"
)

// A submission_id keeps returning its original call for a week; after that it
// starts a new call, so a recurring ID ("daily-status") is not stuck on an old
// result. A record saved before expiry existed keeps binding.
func TestCrewFunctionSubmissionExpiry(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	at := func(age time.Duration) crewFunctionSubmissionIndex {
		return crewFunctionSubmissionIndex{CallID: "fn-1", SavedAt: now.Add(-age).Format(time.RFC3339)}
	}
	for name, tc := range map[string]struct {
		index crewFunctionSubmissionIndex
		want  bool
	}{
		"fresh":              {at(time.Hour), false},
		"just inside":        {at(crewFunctionSubmissionTTL - time.Minute), false},
		"just past":          {at(crewFunctionSubmissionTTL + time.Minute), true},
		"long ago":           {at(90 * 24 * time.Hour), true},
		"saved before TTLs":  {crewFunctionSubmissionIndex{CallID: "fn-1"}, false},
		"unreadable savedAt": {crewFunctionSubmissionIndex{CallID: "fn-1", SavedAt: "yesterday"}, false},
	} {
		if got := crewFunctionSubmissionExpired(tc.index, now); got != tc.want {
			t.Fatalf("%s: expired = %v, want %v", name, got, tc.want)
		}
	}
}
