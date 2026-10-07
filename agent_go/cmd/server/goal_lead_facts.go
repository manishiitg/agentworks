package server

import (
	"context"
	"fmt"
	"math"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// More code-collected facts for the Pulse's goal check (PLAT-697, "ask_builder
// and more goal facts"): plan changes and owner answers since the last check,
// spend, login and connection hints, and spikes. Like the silence alarm these
// are computed, not judged; the goal-check skill says what to do with each.

const (
	goalLeadFactsMaxItems     = 12
	goalLeadFactsTextRunes    = 240
	goalLeadSpikeWindow       = 14 * 24 * time.Hour
	goalLeadSpikeMinRuns      = 5
	goalLeadSpikeCostFactor   = 2.0
	goalLeadSpikeMinCostDelta = 0.05
)

func goalLeadShortText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > goalLeadFactsTextRunes {
		return string(runes[:goalLeadFactsTextRunes]) + "…"
	}
	return text
}

// goalLeadPlanChange is one plan edit since the last check, from
// planning/changelog.
type goalLeadPlanChange struct {
	At      string   `json:"at"`
	Tool    string   `json:"tool"`
	Reason  string   `json:"reason,omitempty"`
	StepIDs []string `json:"step_ids,omitempty"`
	By      string   `json:"by,omitempty"`
	Session string   `json:"session_id,omitempty"`
}

func goalLeadPlanChanges(ctx context.Context, workspacePath string, since time.Time) ([]goalLeadPlanChange, int) {
	entries, err := readPlanChangelogEntries(ctx, workspacePath)
	if err != nil {
		return []goalLeadPlanChange{}, 0
	}
	out := []goalLeadPlanChange{}
	total := 0
	for _, entry := range entries {
		at, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil || !at.After(since) {
			continue
		}
		total++
		if len(out) >= goalLeadFactsMaxItems {
			continue
		}
		steps := append([]string(nil), entry.StepIDs...)
		for _, change := range entry.Changes {
			if change.StepID != "" && !slices.Contains(steps, change.StepID) {
				steps = append(steps, change.StepID)
			}
		}
		by := firstNonEmptyTrimmed(entry.Origin.Username, entry.Origin.AgentName, entry.Origin.Type, entry.Actor)
		if entry.Origin.Type != "" && by != entry.Origin.Type {
			by += " (" + entry.Origin.Type + ")"
		}
		out = append(out, goalLeadPlanChange{At: formatStoredTime(at.UTC()), Tool: entry.Tool, Reason: goalLeadShortText(entry.Reason),
			StepIDs: steps, By: by, Session: entry.Origin.SessionID})
	}
	return out, total
}

// goalLeadOwnerAnswers are decision requests the owner answered since the last
// check. Owner messages inside Builder chats are not read here: their
// transcripts can be tens of megabytes; ask_builder asks the chat instead.
func goalLeadOwnerAnswers(ctx context.Context, workspacePath string, since time.Time) []map[string]string {
	out := []map[string]string{}
	inputs, err := listReportHumanInputs(ctx, workspacePath, "", "")
	if err != nil {
		return out
	}
	for _, input := range inputs {
		answered := parseStoredTime(input.AnsweredAt)
		if input.Source == "user_suggestion" || answered.IsZero() || !answered.After(since) || len(out) >= goalLeadFactsMaxItems {
			continue
		}
		answer := firstNonEmptyTrimmed(reportHumanInputOptionTitle(input.Options, input.SelectedOptionID), input.SelectedOptionID)
		if note := strings.TrimSpace(input.Note); note != "" {
			answer = strings.TrimSpace(answer + " — " + goalLeadShortText(note))
		}
		item := map[string]string{"input_id": input.ID, "question": goalLeadShortText(input.Question), "answer": answer,
			"answered_at": formatStoredTime(answered.UTC()), "status": input.Status}
		if input.OutcomeSummary != "" {
			item["applied"] = goalLeadShortText(input.OutcomeSummary)
		}
		out = append(out, item)
	}
	return out
}

// goalLeadSpend is spend over the last 7 days against the 7 before, from the
// workflow's own cost ledger (costs/costs.sqlite, PLAT-184), and runs whose
// cost is well above the 14-day median run cost. There is no budget field on a
// workflow, so the trend is reported without one.
func goalLeadSpend(workspacePath string, now time.Time) map[string]interface{} {
	out := map[string]interface{}{"budget": "none configured: workflows have no budget setting; report the trend only"}
	ledgerPath := costledger.WorkspaceLedgerPath(workspacePath)
	if ledgerPath == "" {
		out["available"] = false
		return out
	}
	if _, err := os.Stat(ledgerPath); err != nil {
		out["available"] = false
		out["note"] = "no cost ledger for this workflow yet"
		return out
	}
	ledger, err := costledger.WorkspaceLedger(workspacePath)
	if err != nil || ledger == nil {
		out["available"] = false
		return out
	}
	runs, err := ledger.RunCostsSince(now.Add(-goalLeadSpikeWindow))
	if err != nil {
		out["available"] = false
		out["note"] = err.Error()
		return out
	}
	out["available"] = true
	weekAgo := now.Add(-7 * 24 * time.Hour)
	last7, previous7 := 0.0, 0.0
	perRun := []float64{}
	for _, run := range runs {
		if run.FirstAt.After(weekAgo) {
			last7 += run.CostUSD
		} else {
			previous7 += run.CostUSD
		}
		if run.RunID != "" {
			perRun = append(perRun, run.CostUSD)
		}
	}
	out["last_7_days_usd"] = goalLeadCents(last7)
	out["previous_7_days_usd"] = goalLeadCents(previous7)
	if previous7 > 0 {
		out["change_pct"] = math.Round((last7 - previous7) / previous7 * 100)
	}
	if len(perRun) >= goalLeadSpikeMinRuns {
		median := goalLeadMedian(perRun)
		out["median_run_usd_14d"] = goalLeadCents(median)
		spikes := []map[string]interface{}{}
		for _, run := range runs {
			if run.RunID == "" || !run.FirstAt.After(weekAgo) {
				continue
			}
			if run.CostUSD >= median*goalLeadSpikeCostFactor && run.CostUSD-median >= goalLeadSpikeMinCostDelta {
				spikes = append(spikes, map[string]interface{}{"run_id": run.RunID, "cost_usd": goalLeadCents(run.CostUSD), "started_at": formatStoredTime(run.FirstAt.UTC())})
			}
		}
		out["cost_spikes"] = spikes
	}
	return out
}

func goalLeadCents(value float64) float64 { return math.Round(value*100) / 100 }

func goalLeadMedian(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// goalLeadErrorRateSpike compares the share of failed runs since the last
// check with the median daily share over the last 14 days.
func goalLeadErrorRateSpike(entries []ScheduleRunEntry, since, now time.Time) map[string]interface{} {
	type day struct{ total, failed int }
	days := map[string]*day{}
	recentTotal, recentFailed := 0, 0
	for _, entry := range entries {
		if isPulseScheduleID(entry.ScheduleID) || entry.StartedAt.Before(now.Add(-goalLeadSpikeWindow)) {
			continue
		}
		switch entry.Status {
		case "queued", "running":
			continue
		}
		failed := isPulseFixFailedRunStatus(entry.Status)
		key := entry.StartedAt.UTC().Format("2006-01-02")
		if days[key] == nil {
			days[key] = &day{}
		}
		days[key].total++
		if failed {
			days[key].failed++
		}
		if entry.StartedAt.After(since) {
			recentTotal++
			if failed {
				recentFailed++
			}
		}
	}
	rates := []float64{}
	for _, d := range days {
		rates = append(rates, float64(d.failed)/float64(d.total))
	}
	out := map[string]interface{}{"runs_since_last_check": recentTotal, "failed_since_last_check": recentFailed}
	if len(rates) < 3 || recentTotal == 0 {
		return out
	}
	median := goalLeadMedian(rates)
	recent := float64(recentFailed) / float64(recentTotal)
	out["failure_rate_since_last_check"] = math.Round(recent*100) / 100
	out["median_daily_failure_rate_14d"] = math.Round(median*100) / 100
	out["spike"] = recentFailed >= 2 && recent >= math.Max(2*median, median+0.3)
	return out
}

// goalLeadLoginHintPattern is a narrow text match for expired logins and
// failing connections in run errors and steps' CONCERNS: lines. Runs carry no
// structured error kind, so these are hints to check, never findings.
var goalLeadLoginHintPattern = regexp.MustCompile(`(?i)\b(?:401|403)\b|unauthori[sz]ed|invalid_grant|(?:token|session|login|credentials?) (?:has |have |is |are )?(?:expired|revoked|invalid)|expired (?:token|session|login|credentials?)|re-?authenticat|reconnect (?:the|your)|not logged in|login required|failed to connect to (?:the )?mcp|mcp server [^.]{0,40}(?:unavailable|not connected|disconnected)`)

func goalLeadLoginHints(entries []ScheduleRunEntry, concerns []StepConcern, since time.Time) []map[string]string {
	out := []map[string]string{}
	add := func(item map[string]string) {
		if len(out) < goalLeadFactsMaxItems {
			out = append(out, item)
		}
	}
	for _, run := range goalLeadFailedRuns(entries, since) {
		if match := goalLeadLoginHintPattern.FindString(run.Error); match != "" {
			add(map[string]string{"run_id": run.RunID, "at": run.FinishedAt, "matched": match, "error": goalLeadShortText(run.Error)})
		}
	}
	for _, concern := range concerns {
		if match := goalLeadLoginHintPattern.FindString(concern.Text); match != "" {
			add(map[string]string{"step_id": concern.StepID, "at": concern.LastSeenAt, "matched": match, "concern": goalLeadShortText(concern.Text)})
		}
	}
	return out
}

// goalLeadMoreFacts are the facts above, since the last check.
func goalLeadMoreFacts(ctx context.Context, workspacePath string, since, now time.Time) map[string]interface{} {
	changes, changeCount := goalLeadPlanChanges(ctx, workspacePath, since)
	entries, _ := ReadScheduleRuns(ctx, workspacePath)
	concerns := collectStepConcerns(workspacePath, since).Concerns
	return map[string]interface{}{
		"facts_since":        formatStoredTime(since.UTC()),
		"plan_changes":       changes,
		"plan_change_count":  changeCount,
		"plan_changes_note":  fmt.Sprintf("Plan edits since your last check (planning/changelog, newest first, at most %d shown): tool, reason, steps, who and from which session. For a change that touches a goal-driving step or how the goal metric is measured, ask_builder(kind=\"question\") what changed and why, then record the answer in goal memory (source builder_answer).", goalLeadFactsMaxItems),
		"owner_answers":      goalLeadOwnerAnswers(ctx, workspacePath, since),
		"owner_answers_note": "Decision requests the owner answered since your last check. Owner messages inside Builder chats are not collected here (their transcripts are too large to scan each check); ask_builder when you need what the owner decided there.",
		"spend":              goalLeadSpend(workspacePath, now),
		"spend_note":         "Spend from this workflow's cost ledger: the last 7 days against the 7 before, and runs costing at least twice the 14-day median run (cost_spikes). There is no budget setting: report the trend; a sharp rise or a spike goes to the owner with a recommendation, never a spending change of yours.",
		"error_rate":         goalLeadErrorRateSpike(entries, since, now),
		"login_hints":        goalLeadLoginHints(entries, concerns, since),
		"login_hints_note":   "Possible expired logins or failing connections: a narrow text match on run errors and CONCERNS: lines (runs carry no structured error kind), so a hint, not a finding. Check the run; you cannot log in for the owner: one clear ask naming the account or connection.",
	}
}
