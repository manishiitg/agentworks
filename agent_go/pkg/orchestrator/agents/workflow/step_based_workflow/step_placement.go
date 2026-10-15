package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type stepParentPlacement struct{ Title, Task string }
type stepParentPlacementKey struct{}

func withStepParentPlacement(ctx context.Context, title, task string) context.Context {
	return context.WithValue(ctx, stepParentPlacementKey{}, stepParentPlacement{title, task})
}

// Placement is context, never a new task or a copy of Constraints. All execution
// roles and preview use this builder, with their immutable plan and call context.
func (hcpo *StepBasedWorkflowOrchestrator) buildStepPlacement(ctx context.Context, allSteps []PlanStepInterface, index int) string {
	var lines []string
	if run := hcpo.currentRunIdentity(); run.ID != "" {
		line := "Run: " + run.ID
		if index >= 0 && index < len(allSteps) && allSteps[index] != nil {
			line += " · step " + allSteps[index].GetID()
		}
		lines = append(lines, line)
	}
	objective, criteria := hcpo.ResolveWorkflowObjective(ctx)
	if objective != "" {
		lines = append(lines, "Goal: "+placementLine(objective, 360))
	}
	if criteria != "" {
		lines = append(lines, "Success: "+placementLine(criteria, 360))
	}
	parent, child := ctx.Value(stepParentPlacementKey{}).(stepParentPlacement)
	if child {
		lines = append(lines, "Parent: "+placementLine(parent.Title, 160), "Delegated task: "+placementLine(parent.Task, 280))
	}
	if index >= 0 && index < len(allSteps) && allSteps[index] != nil {
		label := "Position"
		if child {
			label = "Parent position"
		}
		lines = append(lines, fmt.Sprintf("%s: step %d of %d.", label, index+1, len(allSteps)))
		if index > 0 && allSteps[index-1] != nil {
			prev := allSteps[index-1]
			line := "Before: " + placementLine(prev.GetTitle(), 120)
			if outcome := hcpo.latestPlacementOutcome(ctx, prev.GetID()); outcome != "" {
				line += " — " + placementLine(outcome, 220)
			}
			lines = append(lines, line)
		}
		if index+1 < len(allSteps) && allSteps[index+1] != nil {
			lines = append(lines, "After: "+placementLine(allSteps[index+1].GetTitle(), 160))
		} else {
			lines = append(lines, "After: final step.")
		}
		output := strings.TrimSpace(allSteps[index].GetContextOutput().String())
		var consumers []string
		if output != "" {
			for _, later := range allSteps[index+1:] {
				if later == nil {
					continue
				}
				for _, dep := range later.GetContextDependencies() {
					dep = strings.TrimSpace(dep)
					if dep == output || strings.HasSuffix(dep, "/"+output) {
						consumers = append(consumers, later.GetTitle())
						break
					}
				}
			}
		}
		if len(consumers) > 0 {
			lines = append(lines, "Output consumers: "+placementLine(strings.Join(consumers, ", "), 260))
		}
	}
	return strings.Join(lines, "\n")
}

var placementLogToken = regexp.MustCompile(`\[[A-Za-z_-]+\][^.\n]*\.?`)

// placementLine keeps a placement line to whole sentences (or at least whole words) and drops log fragments such as
// "[preflight] ..." that leak in from step outcomes.
func placementLine(value string, limit int) string {
	line := strings.Join(strings.Fields(placementLogToken.ReplaceAllString(value, "")), " ")
	runes := []rune(line)
	if len(runes) <= limit {
		return line
	}
	cut := string(runes[:limit])
	if i := strings.LastIndexAny(cut, ".!?"); i >= limit/2 {
		return cut[:i+1]
	}
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:-—") + "…"
}

var placementResultName = regexp.MustCompile(`^execution-attempt-[0-9]+-iteration-[0-9]+\.json$`)

// Only current run evidence is considered, never another group's output or
// archived dispatch. Compare real completion times, not retry counters that
// restart at every dispatch. Too many diagnostic files means omit the optional
// outcome rather than pick an arbitrary subset and call it latest.
func (hcpo *StepBasedWorkflowOrchestrator) latestPlacementOutcome(ctx context.Context, stepID string) string {
	if hcpo.selectedRunFolder == "" || stepID == "" || strings.ContainsAny(stepID, `/\`) {
		return ""
	}
	dir := filepath.Join("runs", hcpo.selectedRunFolder, "logs", stepID, "execution")
	names, err := hcpo.ListWorkspaceFiles(ctx, dir)
	if err != nil {
		return ""
	}
	var candidates []string
	for _, name := range names {
		base := filepath.Base(name)
		if base == finalExecutionSummaryFilename || placementResultName.MatchString(base) {
			candidates = append(candidates, base)
		}
	}
	if len(candidates) > 32 {
		return ""
	}
	var latest time.Time
	outcome := ""
	for _, name := range candidates {
		raw, err := hcpo.ReadWorkspaceFile(ctx, filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var result struct {
			Status    string `json:"status"`
			Text      string `json:"execution_result"`
			Completed string `json:"completed_at"`
		}
		if json.Unmarshal([]byte(raw), &result) != nil {
			continue
		}
		when, err := time.Parse(time.RFC3339Nano, result.Completed)
		if err != nil || when.Before(latest) {
			continue
		}
		status := result.Status
		for _, line := range strings.Split(result.Text, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "STATUS:") {
				status = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "STATUS:"))
				break
			}
		}
		status = placementLine(status, 40)
		if status == "" {
			continue
		}
		summary := ""
		for _, line := range strings.Split(result.Text, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "STATUS:") {
				summary = line
				break
			}
		}
		latest = when
		outcome = status
		if summary != "" {
			outcome += "; " + summary
		}
	}
	return outcome
}
