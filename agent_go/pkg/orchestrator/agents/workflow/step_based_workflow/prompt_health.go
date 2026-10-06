package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"unicode/utf8"
)

// currentPlanPromptHealth measures a fresh authored snapshot, never the run snapshot.
func (hcpo *StepBasedWorkflowOrchestrator) currentPlanPromptHealth(ctx context.Context) (PromptHealthReport, error) {
	plan, err := hcpo.ReadCurrentPlan(ctx)
	if err != nil {
		return PromptHealthReport{}, err
	}
	report := BuildPromptHealthReport(plan.Steps)
	// PLAT-561: open reference breaks travel with the prompt-health report.
	if refs, refErr := CollectReferenceMap(hcpo.GetWorkspacePath()); refErr == nil && refs.Note != "" {
		report.ReferenceMap = &refs
	}
	return report, nil
}

// PromptHealthStep is the compact, deterministic description-size record for
// one authored plan step. It deliberately measures only authored description
// text: runtime context, tool definitions, and referenced files are different
// payloads and must not be conflated with prompt-contract bloat.
type PromptHealthStep struct {
	ID               string   `json:"id"`
	Title            string   `json:"title,omitempty"`
	Type             StepType `json:"type"`
	DescriptionChars int      `json:"description_chars"`
	Threshold        string   `json:"threshold"`
	// Budget is the PLAT-556 trigger set: size vs the plan's median, dated
	// text, cross-step duplication and the description layout.
	Budget *PromptBudgetStep `json:"budget,omitempty"`
}

// PromptHealthDuplicateCluster identifies one long paragraph shared verbatim
// by multiple distinct steps. Short common phrases are intentionally ignored:
// this is a review signal for extractable contracts, not a prose-style lint.
type PromptHealthDuplicateCluster struct {
	Fingerprint    string   `json:"fingerprint"`
	StepIDs        []string `json:"step_ids"`
	ParagraphChars int      `json:"paragraph_chars"`
	RepeatedChars  int      `json:"repeated_chars"`
}

// PromptHealthReport is the small objective input a Technical Review needs to
// decide whether prompt-contract consolidation deserves a finding. It does not
// decide that a large prompt is defective: the reviewer still judges whether
// the contract is necessary and whether a safe extraction exists.
type PromptHealthReport struct {
	StepsWithDescriptions    int                            `json:"steps_with_descriptions"`
	TotalDescriptionChars    int                            `json:"total_description_chars"`
	Over5K                   int                            `json:"over_5k"`
	Over10K                  int                            `json:"over_10k"`
	Over20K                  int                            `json:"over_20k"`
	RepeatedDescriptionChars int                            `json:"repeated_description_chars"`
	RequiresTechnicalReview  bool                           `json:"requires_technical_review"`
	TechnicalReviewTrigger   string                         `json:"technical_review_trigger,omitempty"`
	Steps                    []PromptHealthStep             `json:"steps"`
	DuplicateClusters        []PromptHealthDuplicateCluster `json:"duplicate_clusters,omitempty"`
	// PLAT-556 budget triggers. Any step listed in ConsolidationDueSteps makes
	// Architecture due with focus prompt_design; never a gate on runs or edits.
	PlanMedianChars          int      `json:"plan_median_chars"`
	LargestDescriptionChars  int      `json:"largest_description_chars"`
	LargestDescriptionStepID string   `json:"largest_description_step_id,omitempty"`
	StepsOverBudget          int      `json:"steps_over_budget"`
	DatedTextCount           int      `json:"dated_text_count"`
	DuplicatedBudgetChars    int      `json:"duplicated_300_chars"`
	StepsWithoutLayout       int      `json:"steps_without_layout"`
	ConsolidationDueSteps    []string `json:"consolidation_due_steps,omitempty"`
	// ReferenceMap (PLAT-561) lists references that no longer resolve: steps,
	// files, evals, notes and guides naming what was removed or moved.
	ReferenceMap *ReferenceMapReport `json:"reference_map,omitempty"`
}

const (
	promptHealthWarningChars  = 5_000
	promptHealthReviewChars   = 10_000
	promptHealthCriticalChars = 20_000
	promptHealthMinDuplicate  = 240
)

// BuildPromptHealthReport measures the current authored plan without reading
// changelog or runtime data. Nested todo-task route steps are included because
// they have their own descriptions and are executed as independent agents.
func BuildPromptHealthReport(steps []PlanStepInterface) PromptHealthReport {
	report := PromptHealthReport{}
	paragraphOwners := make(map[string]map[string]struct{})
	paragraphLengths := make(map[string]int)

	seen := make(map[string]struct{})
	var budgetInputs []promptBudgetInput
	for _, info := range collectAllSteps(steps) {
		step := info.Step
		if step == nil {
			continue
		}
		id := strings.TrimSpace(step.GetID())
		if id == "" {
			continue
		}
		if _, duplicateID := seen[id]; duplicateID {
			continue
		}
		seen[id] = struct{}{}

		description := strings.TrimSpace(step.GetDescription())
		if description == "" {
			continue
		}
		chars := utf8.RuneCountInString(description)
		scriptOnly := false
		if regular, ok := step.(*RegularPlanStep); ok {
			scriptOnly = regular.ScriptOnly
		}
		budgetInputs = append(budgetInputs, promptBudgetInput{id: id, stepType: step.StepType(), scriptOnly: scriptOnly, description: description})
		report.StepsWithDescriptions++
		report.TotalDescriptionChars += chars
		threshold := promptHealthThreshold(chars)
		switch threshold {
		case "warning":
			report.Over5K++
		case "review":
			report.Over5K++
			report.Over10K++
		case "critical":
			report.Over5K++
			report.Over10K++
			report.Over20K++
		}
		report.Steps = append(report.Steps, PromptHealthStep{
			ID:               id,
			Title:            step.GetTitle(),
			Type:             step.StepType(),
			DescriptionChars: chars,
			Threshold:        threshold,
		})

		for _, paragraph := range promptHealthParagraphs(description) {
			fingerprint := promptHealthFingerprint(paragraph)
			if paragraphOwners[fingerprint] == nil {
				paragraphOwners[fingerprint] = make(map[string]struct{})
				paragraphLengths[fingerprint] = utf8.RuneCountInString(paragraph)
			}
			paragraphOwners[fingerprint][id] = struct{}{}
		}
	}

	for fingerprint, owners := range paragraphOwners {
		if len(owners) < 2 {
			continue
		}
		stepIDs := make([]string, 0, len(owners))
		for stepID := range owners {
			stepIDs = append(stepIDs, stepID)
		}
		sort.Strings(stepIDs)
		paragraphChars := paragraphLengths[fingerprint]
		repeatedChars := paragraphChars * (len(stepIDs) - 1)
		report.RepeatedDescriptionChars += repeatedChars
		report.DuplicateClusters = append(report.DuplicateClusters, PromptHealthDuplicateCluster{
			Fingerprint:    fingerprint,
			StepIDs:        stepIDs,
			ParagraphChars: paragraphChars,
			RepeatedChars:  repeatedChars,
		})
	}

	sort.Slice(report.Steps, func(i, j int) bool {
		if report.Steps[i].DescriptionChars == report.Steps[j].DescriptionChars {
			return report.Steps[i].ID < report.Steps[j].ID
		}
		return report.Steps[i].DescriptionChars > report.Steps[j].DescriptionChars
	})
	sort.Slice(report.DuplicateClusters, func(i, j int) bool {
		if report.DuplicateClusters[i].RepeatedChars == report.DuplicateClusters[j].RepeatedChars {
			return report.DuplicateClusters[i].Fingerprint < report.DuplicateClusters[j].Fingerprint
		}
		return report.DuplicateClusters[i].RepeatedChars > report.DuplicateClusters[j].RepeatedChars
	})

	applyPromptBudget(&report, budgetInputs)
	setPromptHealthReviewTrigger(&report)
	return report
}

func promptHealthThreshold(chars int) string {
	switch {
	case chars > promptHealthCriticalChars:
		return "critical"
	case chars > promptHealthReviewChars:
		return "review"
	case chars > promptHealthWarningChars:
		return "warning"
	default:
		return "normal"
	}
}

func setPromptHealthReviewTrigger(report *PromptHealthReport) {
	if report.Over20K > 0 {
		report.RequiresTechnicalReview = true
		report.TechnicalReviewTrigger = "one_or_more_steps_over_20k"
		return
	}
	if report.StepsWithDescriptions > 0 && report.Over5K*100 >= report.StepsWithDescriptions*30 {
		report.RequiresTechnicalReview = true
		report.TechnicalReviewTrigger = "at_least_30_percent_of_steps_over_5k"
		return
	}
	if report.RepeatedDescriptionChars >= promptHealthReviewChars {
		report.RequiresTechnicalReview = true
		report.TechnicalReviewTrigger = "at_least_10k_repeated_description_chars"
	}
}

func promptHealthParagraphs(description string) []string {
	description = strings.ReplaceAll(description, "\r\n", "\n")
	description = strings.ReplaceAll(description, "\r", "\n")
	sections := strings.Split(description, "\n\n")
	paragraphs := make([]string, 0)
	for _, section := range sections {
		normalized := strings.TrimSpace(section)
		normalized = strings.Join(strings.Fields(normalized), " ")
		if utf8.RuneCountInString(normalized) >= promptHealthMinDuplicate {
			paragraphs = append(paragraphs, normalized)
		}
	}
	return paragraphs
}

func promptHealthFingerprint(paragraph string) string {
	sum := sha256.Sum256([]byte(paragraph))
	return hex.EncodeToString(sum[:8])
}
