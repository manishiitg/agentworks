// Package pulsemodules labels saved review evidence; it does not schedule reviewers.
package pulsemodules

import "strings"

type Module struct {
	ID        string
	Label     string
	StepLabel string
	Aliases   []string
}

const (
	TechnicalReviewID       = "technical_review"
	ArchitectureReviewID    = "architecture_review"
	StrategicReviewID       = "strategic_review"
	PlanDriftReviewID       = "plan_drift_review"
	LegacyWorkflowReviewID  = "workflow_review"
	LegacyLLMOpsReviewID    = "llm_ops_review"
	LegacyStrategyAuditorID = "strategy_auditor"
	LegacyGoalAdvisorID     = "goal_advisor"
)
const (
	PseudoRunSummaryID = "run_summary"
)

var All = []Module{
	{
		ID:        TechnicalReviewID,
		Label:     "Technical review",
		StepLabel: "technical-review",
		Aliases: []string{
			"technical", "engineering", "engineering_review", "correctness",
			"correctness_review", "ops", "operations",
			LegacyWorkflowReviewID, LegacyLLMOpsReviewID,
		},
	},
	{
		ID:        ArchitectureReviewID,
		Label:     "Architecture review",
		StepLabel: "architecture-review",
		Aliases:   []string{"architecture", "workflow_improvement"},
	},
	{
		ID:        StrategicReviewID,
		Label:     "Strategic review",
		StepLabel: "strategic-review",
		Aliases: []string{
			"strategy", "strategy_review", "plan_effectiveness", "advisor",
			LegacyStrategyAuditorID, LegacyGoalAdvisorID,
		},
	},
	{
		ID:        PlanDriftReviewID,
		Label:     "Plan drift review",
		StepLabel: "plan-drift-review",
		Aliases:   []string{"drift_review", "plan_drift"},
	},
}

func IDs() []string {
	out := make([]string, 0, len(All))
	for _, m := range All {
		out = append(out, m.ID)
	}
	return out
}
func IsValid(id string) bool {
	for _, m := range All {
		if m.ID == id {
			return true
		}
	}
	return false
}
func Normalize(module string) string {
	module = strings.ToLower(strings.TrimSpace(module))
	module = strings.ReplaceAll(module, "-", "_")
	for _, m := range All {
		if m.ID == module {
			return m.ID
		}
		for _, a := range m.Aliases {
			if a == module {
				return m.ID
			}
		}
	}
	return module
}
func AcceptedForReviewReceipts() []string {
	return IDs()
}
