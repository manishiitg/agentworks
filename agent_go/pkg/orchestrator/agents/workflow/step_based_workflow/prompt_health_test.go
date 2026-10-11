package step_based_workflow

import "testing"

func TestBuildPromptHealthReportCountsNestedStepsAndTriggersReview(t *testing.T) {
	shared := "This is a deliberately long shared database access contract that is repeated verbatim across several steps so the prompt-health scanner can identify an extractable shared reference without treating short common phrases as a duplicate. "
	shared += shared
	steps := []PlanStepInterface{
		&AgentPlanStep{CommonStepFields: CommonStepFields{ID: "large", Title: "Large", Description: makeDescription(20_001)}},
		&AgentPlanStep{
			CommonStepFields: CommonStepFields{ID: "parent", Description: "parent description"},
			PredefinedRoutes: []PlanOrchestrationRoute{{RouteID: "child", SubAgentStep: &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "child", Description: shared}}}},
		},
		&RegularPlanStep{CommonStepFields: CommonStepFields{ID: "other", Description: shared}},
	}

	report := BuildPromptHealthReport(steps)
	if report.StepsWithDescriptions != 4 {
		t.Fatalf("steps_with_descriptions = %d, want 4", report.StepsWithDescriptions)
	}
	if report.Over20K != 1 || report.Over10K != 1 || report.Over5K != 1 {
		t.Fatalf("threshold counts = >5k:%d >10k:%d >20k:%d, want 1 each", report.Over5K, report.Over10K, report.Over20K)
	}
	if !report.RequiresTechnicalReview || report.TechnicalReviewTrigger != "one_or_more_steps_over_20k" {
		t.Fatalf("technical review trigger = %t/%q, want true/one_or_more_steps_over_20k", report.RequiresTechnicalReview, report.TechnicalReviewTrigger)
	}
	if len(report.DuplicateClusters) != 1 {
		t.Fatalf("duplicate clusters = %d, want 1", len(report.DuplicateClusters))
	}
	cluster := report.DuplicateClusters[0]
	if len(cluster.StepIDs) != 2 || cluster.StepIDs[0] != "child" || cluster.StepIDs[1] != "other" {
		t.Fatalf("duplicate cluster step IDs = %#v, want child and other", cluster.StepIDs)
	}
}

func TestBuildPromptHealthReportTriggersForBroadWarningDistribution(t *testing.T) {
	steps := make([]PlanStepInterface, 0, 10)
	for i := 0; i < 3; i++ {
		steps = append(steps, &RegularPlanStep{CommonStepFields: CommonStepFields{ID: string(rune('a' + i)), Description: makeDescription(5_001)}})
	}
	for i := 0; i < 7; i++ {
		steps = append(steps, &RegularPlanStep{CommonStepFields: CommonStepFields{ID: string(rune('k' + i)), Description: "small"}})
	}

	report := BuildPromptHealthReport(steps)
	if !report.RequiresTechnicalReview || report.TechnicalReviewTrigger != "at_least_30_percent_of_steps_over_5k" {
		t.Fatalf("technical review trigger = %t/%q, want broad warning trigger", report.RequiresTechnicalReview, report.TechnicalReviewTrigger)
	}
}

func makeDescription(chars int) string {
	result := make([]byte, chars)
	for i := range result {
		result[i] = 'x'
	}
	return string(result)
}
