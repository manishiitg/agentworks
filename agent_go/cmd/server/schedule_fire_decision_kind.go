package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

// Scheduled occurrences that did not start fall into three groups, and Pulse
// must treat them differently. A single "skipped" list framed as "the
// scheduler working as designed" let a review note "four days lost to skipped
// schedules" and still conclude nothing needed fixing (social-media,
// 2026-09-27).
const (
	fireDecisionLost       = "lost"       // real work that never ran and will not
	fireDecisionDeferred   = "deferred"   // will still run (queued, waiting)
	fireDecisionDeliberate = "deliberate" // not run on purpose (paused, disabled)
)

// fireDecisionKind classifies a durable scheduler fire decision; "" means it
// started (or is not an outcome at all).
func fireDecisionKind(decision string) string {
	switch strings.TrimSpace(decision) {
	case "started", "attempted", "":
		return ""
	case "skipped_paused", "skipped_disabled":
		return fireDecisionDeliberate
	case "queued_busy", "queued_retry", "coalesced_busy", "waiting_for_dependency", "skipped_waiting_for_capacity":
		return fireDecisionDeferred
	default:
		// skipped_busy, expired_busy, expired_dependency_deadline,
		// blocked_dependency, failed_to_start, missed_scheduler_gap, and any
		// decision added later: the occurrence did not run.
		return fireDecisionLost
	}
}

// formatScheduleNonRunOccurrences renders the non-started occurrences of one
// schedule for get_schedule_runs, grouped by what they mean for the owner.
func formatScheduleNonRunOccurrences(decisions []schedulerstate.FireDecision, collisionPolicy string) string {
	groups := map[string][]schedulerstate.FireDecision{}
	for _, d := range decisions {
		if kind := fireDecisionKind(d.Decision); kind != "" {
			groups[kind] = append(groups[kind], d)
		}
	}
	if len(groups) == 0 {
		return ""
	}
	policy := strings.TrimSpace(collisionPolicy)
	if policy == "" {
		policy = "skip"
	}
	var sb strings.Builder
	write := func(title, meaning string, list []schedulerstate.FireDecision) {
		if len(list) == 0 {
			return
		}
		sb.WriteString(fmt.Sprintf("\n## %s (%d)\n\n%s\n\n", title, len(list), meaning))
		for _, d := range list {
			sb.WriteString(fmt.Sprintf("- scheduled_for=%s decision=%q reason=%q\n",
				d.ScheduledFor.Format("2006-01-02 15:04:05"), d.Decision, d.Reason))
		}
	}
	write("Lost runs", fmt.Sprintf("These occurrences did not run and will not: the work they were scheduled to do never happened. This schedule's collision_policy is %q. If it does real work (sends, posts, bids, writes, grows) and a run was lost because the workflow was busy, that is a Technical Review repair: set collision_policy=queue_latest with a max_start_delay_minutes that fits its purpose (see references/schedules.md).", policy), groups[fireDecisionLost])
	write("Deferred runs", "These occurrences were queued or are waiting and will still run; not a defect unless they later expire.", groups[fireDecisionDeferred])
	write("Deliberately not run", "Not run on purpose: all schedules were paused, or this one was disabled. Not a defect; do not change the schedule's policy for these.", groups[fireDecisionDeliberate])
	return sb.String()
}

// collisionPolicyForSchedule is the schedule's saved collision_policy, or ""
// when it cannot be read.
func collisionPolicyForSchedule(ctx context.Context, workspacePath, scheduleID string) string {
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found || manifest == nil {
		return ""
	}
	for _, sched := range manifest.Schedules {
		if sched.ID == scheduleID {
			return sched.CollisionPolicy
		}
	}
	return ""
}

// missedReasonFromDecisions is the scheduler's recorded decision for the
// occurrence scheduled at missedAt (within the matching tolerance), or "".
func missedReasonFromDecisions(decisions []schedulerstate.FireDecision, missedAt time.Time) string {
	best, bestGap := "", workflowScheduleMatchTolerance+time.Nanosecond
	for _, d := range decisions {
		if fireDecisionKind(d.Decision) == "" {
			continue
		}
		gap := d.ScheduledFor.Sub(missedAt)
		if gap < 0 {
			gap = -gap
		}
		if gap < bestGap {
			best, bestGap = d.Decision, gap
		}
	}
	return best
}
