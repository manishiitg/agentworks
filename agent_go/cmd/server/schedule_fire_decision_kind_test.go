package server

import (
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

// Pulse reads lost, deferred and deliberately-skipped occurrences as separate
// groups; a busy skip is lost work, a pause is not a defect.
func TestScheduleNonRunOccurrencesSeparateLostFromDeliberate(t *testing.T) {
	at := time.Date(2026, 9, 26, 12, 30, 0, 0, time.UTC)
	out := formatScheduleNonRunOccurrences([]schedulerstate.FireDecision{
		{Decision: "started", ScheduledFor: at},
		{Decision: "skipped_busy", Reason: "schedule run already active", ScheduledFor: at},
		{Decision: "skipped_paused", Reason: "global scheduler pause is active", ScheduledFor: at},
		{Decision: "queued_busy", Reason: "workflow busy", ScheduledFor: at},
	}, "")
	lost := strings.Index(out, "## Lost runs (1)")
	deferred := strings.Index(out, "## Deferred runs (1)")
	deliberate := strings.Index(out, "## Deliberately not run (1)")
	if lost < 0 || deferred < 0 || deliberate < 0 {
		t.Fatalf("groups missing:\n%s", out)
	}
	if !strings.Contains(out[lost:deferred], "skipped_busy") || !strings.Contains(out[deliberate:], "skipped_paused") {
		t.Fatalf("decisions in the wrong group:\n%s", out)
	}
	if !strings.Contains(out, `collision_policy is "skip"`) || !strings.Contains(out, "queue_latest") {
		t.Fatalf("lost runs must name the current policy and the repair:\n%s", out)
	}
	if formatScheduleNonRunOccurrences([]schedulerstate.FireDecision{{Decision: "started"}}, "skip") != "" {
		t.Fatal("started occurrences are not listed")
	}
}
