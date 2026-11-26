package productschedule

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

type DependencyResult int

const (
	DependencyReady DependencyResult = iota
	DependencyWaiting
	DependencyBlocked
)

// EvaluateDependencies is the shared timer policy for when.after. It reads
// occurrence evidence, never starts work or changes a schedule's enabled state.
// PLAT-871-33: Crew run timers must use the same outcome/delay/deadline rules.
func EvaluateDependencies(ctx context.Context, store *schedulerstate.Store, scopeType, scopeID string, when projectactions.When, occurrence, now time.Time) (DependencyResult, string) {
	dependencyIDs := []string(nil)
	if when.After != nil {
		dependencyIDs = when.After.Schedules
	}
	if len(dependencyIDs) == 0 {
		return DependencyReady, ""
	}
	location, err := time.LoadLocation(strings.TrimSpace(when.Timezone))
	if err != nil {
		location = time.UTC
	}
	if occurrence.IsZero() {
		occurrence = time.Now()
	}
	localOccurrence := occurrence.In(location)
	year, month, day := localOccurrence.Date()
	dayStart := time.Date(year, month, day, 0, 0, 0, 0, location).UTC()
	dayEnd := dayStart.In(location).AddDate(0, 0, 1).UTC()
	// A later occurrence of the prerequisite must never release an older
	// dependent occurrence after restart. Limit the lookup to prerequisite
	// occurrences scheduled no later than this dependent's own durable time.
	if dependentEnd := occurrence.UTC().Add(time.Nanosecond); dependentEnd.Before(dayEnd) {
		dayEnd = dependentEnd
	}
	var dependencyDeadline time.Time
	if deadline := strings.TrimSpace(when.After.Deadline); deadline != "" {
		parsed, _ := time.Parse("15:04", deadline)
		dependencyDeadline = time.Date(year, month, day, parsed.Hour(), parsed.Minute(), 0, 0, location)
	}
	if store == nil {
		return DependencyWaiting, "dependency state is unavailable"
	}
	terminalPolicy := strings.TrimSpace(when.After.Outcome)
	if terminalPolicy == "" {
		terminalPolicy = "completed"
	}
	waitingReasons := make([]string, 0, len(dependencyIDs))
	for _, dependencyID := range dependencyIDs {
		run, err := store.RunForScheduleOccurrence(ctx, scopeType, scopeID, dependencyID, dayStart, dayEnd)
		if err != nil {
			if errors.Is(err, schedulerstate.ErrRunNotFound) {
				if decision, decisionErr := store.FireDecisionForScheduleOccurrence(ctx, scopeType, scopeID, dependencyID, dayStart, dayEnd); decisionErr == nil && terminalScheduleFireDecision(decision.Decision) {
					return DependencyBlocked, fmt.Sprintf("prerequisite schedule %s did not run: %s", dependencyID, decision.Reason)
				}
				waitingReasons = append(waitingReasons, fmt.Sprintf("waiting for prerequisite schedule %s occurrence on %04d-%02d-%02d", dependencyID, year, month, day))
				continue
			}
			waitingReasons = append(waitingReasons, fmt.Sprintf("could not read prerequisite schedule %s occurrence: %v", dependencyID, err))
			continue
		}
		if !schedulerstate.IsTerminal(run.State) {
			waitingReasons = append(waitingReasons, fmt.Sprintf("prerequisite schedule %s occurrence %s is still %s", dependencyID, run.RunID, run.State))
			continue
		}
		if terminalPolicy == "completed" && run.State != schedulerstate.StateCompleted {
			return DependencyBlocked, fmt.Sprintf("prerequisite schedule %s occurrence %s ended %s; completed is required", dependencyID, run.RunID, run.State)
		}
		if !dependencyDeadline.IsZero() {
			if run.CompletedAt == nil {
				waitingReasons = append(waitingReasons, fmt.Sprintf("prerequisite schedule %s is terminal but its completion timestamp is unavailable", dependencyID))
				continue
			}
			releaseAt := run.CompletedAt.Add(time.Duration(when.After.DelayMinutes) * time.Minute)
			if releaseAt.After(dependencyDeadline) {
				return DependencyBlocked, fmt.Sprintf("prerequisite schedule %s released at %s after dependency deadline %s", dependencyID, releaseAt.Format(time.RFC3339), dependencyDeadline.Format(time.RFC3339))
			}
		}
		if delay := when.After.DelayMinutes; delay > 0 {
			if run.CompletedAt == nil {
				waitingReasons = append(waitingReasons, fmt.Sprintf("prerequisite schedule %s is terminal but its completion timestamp is unavailable", dependencyID))
				continue
			}
			releaseAt := run.CompletedAt.Add(time.Duration(delay) * time.Minute)
			if now.Before(releaseAt) {
				waitingReasons = append(waitingReasons, fmt.Sprintf("prerequisite schedule %s completed; waiting until %s for configured delay", dependencyID, releaseAt.Format(time.RFC3339)))
			}
		}
	}
	if len(waitingReasons) > 0 {
		if !dependencyDeadline.IsZero() && !now.Before(dependencyDeadline) {
			return DependencyBlocked, fmt.Sprintf("dependency deadline %s %s passed before prerequisites %s released: %s", when.After.Deadline, location, strings.Join(dependencyIDs, ", "), strings.Join(waitingReasons, "; "))
		}
		return DependencyWaiting, strings.Join(waitingReasons, "; ")
	}
	return DependencyReady, ""
}

func terminalScheduleFireDecision(decision string) bool {
	return strings.HasPrefix(decision, "skipped_") || strings.HasPrefix(decision, "blocked_") ||
		strings.HasPrefix(decision, "expired_") || decision == "failed_to_start" ||
		decision == "missed_scheduler_gap" || decision == "launch_outcome_unknown"
}
