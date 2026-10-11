package step_based_workflow

import (
	"strings"
	"testing"
)

func humanInputStep(mutate func(*HumanInputPlanStep)) *HumanInputPlanStep {
	step := &HumanInputPlanStep{
		Type: StepTypeHumanInput,
		CommonStepFields: CommonStepFields{
			ID:    "ask-user",
			Title: "Ask the user",
		},
		Question:   "Proceed?",
		NextStepID: "end",
	}
	if mutate != nil {
		mutate(step)
	}
	return step
}

func planWith(steps ...PlanStepInterface) *PlanningResponse {
	return &PlanningResponse{Steps: steps}
}

func regularStep(id string) *ScriptedPlanStep {
	return &ScriptedPlanStep{
		Type: StepTypeScripted,
		CommonStepFields: CommonStepFields{
			ID:    id,
			Title: id,
		},
	}
}

func TestHumanInputDanglingNextStepIDRefsRejected(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*HumanInputPlanStep)
		want   string
	}{
		{
			name:   "dangling next_step_id",
			mutate: func(s *HumanInputPlanStep) { s.NextStepID = "missing-step" },
			want:   "next_step_id",
		},
		{
			name: "dangling if_yes_next_step_id",
			mutate: func(s *HumanInputPlanStep) {
				s.ResponseType = "yesno"
				s.IfYesNextStepID = "missing-step"
			},
			want: "if_yes_next_step_id",
		},
		{
			name: "dangling if_no_next_step_id",
			mutate: func(s *HumanInputPlanStep) {
				s.ResponseType = "yesno"
				s.IfNoNextStepID = "missing-step"
			},
			want: "if_no_next_step_id",
		},
		{
			name: "dangling option_routes target",
			mutate: func(s *HumanInputPlanStep) {
				s.ResponseType = "multiple_choice"
				s.Options = []string{"a", "b"}
				s.OptionRoutes = map[string]string{"0": "missing-step", "1": "end"}
			},
			want: "option_routes",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := planWith(humanInputStep(tc.mutate), regularStep("step-2"))
			err := validateLoadedPlanStructure(plan)
			if err == nil {
				t.Fatalf("expected dangling-reference error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "missing-step") {
				t.Fatalf("error should name %s and the dangling target, got: %v", tc.want, err)
			}
		})
	}
}

func TestHumanInputValidRefsAccepted(t *testing.T) {
	plan := planWith(
		humanInputStep(func(s *HumanInputPlanStep) {
			s.ResponseType = "yesno"
			s.IfYesNextStepID = "step-2"
			s.IfNoNextStepID = "end"
		}),
		regularStep("step-2"),
	)
	if err := validateLoadedPlanStructure(plan); err != nil {
		t.Fatalf("expected valid plan, got: %v", err)
	}
}

func TestHumanInputFieldConsistency(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*HumanInputPlanStep)
		want   string
	}{
		{
			name:   "missing question",
			mutate: func(s *HumanInputPlanStep) { s.Question = " " },
			want:   "question",
		},
		{
			name:   "unsupported response_type",
			mutate: func(s *HumanInputPlanStep) { s.ResponseType = "freeform" },
			want:   "unsupported response_type",
		},
		{
			name: "yesno fields on text step",
			mutate: func(s *HumanInputPlanStep) {
				s.ResponseType = "text"
				s.IfYesNextStepID = "end"
			},
			want: "only apply to response_type \"yesno\"",
		},
		{
			name: "option_routes on yesno step",
			mutate: func(s *HumanInputPlanStep) {
				s.ResponseType = "yesno"
				s.OptionRoutes = map[string]string{"0": "end"}
			},
			want: "only applies to response_type \"multiple_choice\"",
		},
		{
			name: "multiple_choice without options",
			mutate: func(s *HumanInputPlanStep) {
				s.ResponseType = "multiple_choice"
			},
			want: "no options",
		},
		{
			name: "option_routes index out of range",
			mutate: func(s *HumanInputPlanStep) {
				s.ResponseType = "multiple_choice"
				s.Options = []string{"a", "b"}
				s.OptionRoutes = map[string]string{"2": "end"}
			},
			want: "out of range",
		},
		{
			name: "option_routes key matches nothing",
			mutate: func(s *HumanInputPlanStep) {
				s.ResponseType = "multiple_choice"
				s.Options = []string{"a", "b"}
				s.OptionRoutes = map[string]string{"c": "end"}
			},
			want: "matches neither",
		},
		{
			name: "unmapped option with no fallback",
			mutate: func(s *HumanInputPlanStep) {
				s.ResponseType = "multiple_choice"
				s.Options = []string{"a", "b"}
				s.OptionRoutes = map[string]string{"0": "end"}
				s.NextStepID = ""
			},
			want: "no option_routes entry",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateHumanInputStepFieldsTyped(humanInputStep(tc.mutate))
			if err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error should contain %q, got: %v", tc.want, err)
			}
		})
	}
}

func TestHumanInputPartialOptionRoutesWithFallbackAccepted(t *testing.T) {
	// A partial option_routes map is the legitimate "else branch" pattern when
	// next_step_id provides the fallback — mapping by index or by option value.
	step := humanInputStep(func(s *HumanInputPlanStep) {
		s.ResponseType = "multiple_choice"
		s.Options = []string{"redo", "continue", "abort"}
		s.OptionRoutes = map[string]string{"0": "step-2", "abort": "end"}
		s.NextStepID = "step-2"
	})
	if err := validateHumanInputStepFieldsTyped(step); err != nil {
		t.Fatalf("expected partial map with fallback to validate, got: %v", err)
	}
	plan := planWith(step, regularStep("step-2"))
	if err := validateLoadedPlanStructure(plan); err != nil {
		t.Fatalf("expected plan to validate, got: %v", err)
	}
}

func TestHumanInputFullOptionRoutesWithoutFallbackAccepted(t *testing.T) {
	// Every option mapped (mix of index and value keys) — no fallback needed.
	step := humanInputStep(func(s *HumanInputPlanStep) {
		s.ResponseType = "multiple_choice"
		s.Options = []string{"a", "b"}
		s.OptionRoutes = map[string]string{"0": "end", "b": "end"}
		s.NextStepID = ""
	})
	if err := validateHumanInputStepFieldsTyped(step); err != nil {
		t.Fatalf("expected fully-mapped options to validate, got: %v", err)
	}
}
