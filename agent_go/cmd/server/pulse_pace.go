package server

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Pulse pace (owner, 2026-10-08): how hard Pulse pushes on the goal, separate
// from autonomy (what it may do). It bounds the time Pulse may choose for its
// next goal check, sets the default when it chooses none, decides whether a
// failed run wakes it, and how much Goal Work it takes on per turn.
const (
	pulsePaceCalm       = "calm"
	pulsePaceSteady     = "steady"
	pulsePaceAggressive = "aggressive"
)

type pulsePaceRules struct {
	Name          string
	MinGap        time.Duration
	MaxGap        time.Duration
	DefaultGap    time.Duration
	WakeOnFailure bool
	Items         string
	Summary       string
}

var pulsePaces = map[string]pulsePaceRules{
	pulsePaceCalm: {Name: pulsePaceCalm, MinGap: 24 * time.Hour, MaxGap: 7 * 24 * time.Hour, DefaultGap: 72 * time.Hour,
		WakeOnFailure: false, Items: "at most 1 Goal Work item per turn",
		Summary: "Calm: check every 1-7 days (3 when you do not choose); a failed run waits for your next check; at most 1 Goal Work item per turn."},
	pulsePaceSteady: {Name: pulsePaceSteady, MinGap: 6 * time.Hour, MaxGap: 3 * 24 * time.Hour, DefaultGap: 24 * time.Hour,
		WakeOnFailure: true, Items: "1-3 Goal Work items per turn",
		Summary: "Steady: check every 6 hours to 3 days (1 day when you do not choose); a failed run wakes you once; 1-3 Goal Work items per turn."},
	pulsePaceAggressive: {Name: pulsePaceAggressive, MinGap: time.Hour, MaxGap: 24 * time.Hour, DefaultGap: 6 * time.Hour,
		WakeOnFailure: true, Items: "up to 3 Goal Work items per turn, and follow-ups chained in the same turn",
		Summary: "Aggressive: check every 1-24 hours (6 when you do not choose), soon after the next run that should show a fix; a failed run wakes you at once; up to 3 Goal Work items per turn, chaining follow-ups."},
}

func normalizePulsePace(value string) (string, error) {
	switch v := strings.ToLower(strings.TrimSpace(value)); v {
	case "":
		return pulsePaceSteady, nil
	case pulsePaceCalm, pulsePaceSteady, pulsePaceAggressive:
		return v, nil
	}
	return "", fmt.Errorf("pulse.pace must be calm, steady or aggressive")
}

// workflowPulsePace is the workflow's pace rules (steady when unset or unreadable).
func workflowPulsePace(ctx context.Context, workspacePath string) pulsePaceRules {
	pace := pulsePaceSteady
	if manifest, found, err := ReadWorkflowManifest(ctx, workspacePath); err == nil && found && manifest != nil && manifest.Pulse != nil {
		if normalized, err := normalizePulsePace(manifest.Pulse.Pace); err == nil {
			pace = normalized
		}
	}
	return pulsePaces[pace]
}

// pulsePaceText is the pace line for Pulse's and the Builder's prompts.
func pulsePaceText(rules pulsePaceRules) string {
	return "Pace (workflow.json pulse.pace): " + rules.Summary
}
