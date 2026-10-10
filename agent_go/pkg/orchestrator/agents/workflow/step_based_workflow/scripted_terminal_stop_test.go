package step_based_workflow

import (
	"strings"
	"testing"
)

func TestScriptedTerminalStopReportsOnlyExplicitRefusal(t *testing.T) {
	record := `AGENTWORKS_REFUSAL: {"reason":"History could not be verified","blocked_action":"Overwrite balance rows","resolution":"Restore database access"}`
	for _, tc := range []struct {
		name, output string
		explicit     bool
	}{
		{"project-a diagnostic output", "[preflight] Python bundle syntax, top-level imports and configuration OK\n{\"WORKFLOW_CODE_DEPS\":\"/data/deps\"}", false},
		{"command line error", "usage: main.py --env ENV\nmain.py: error: --env is required", false},
		{"empty", "", false},
		{"legacy prose", "ABORT: refusing to overwrite history", false},
		{"explicit", "preflight OK\n" + record + "\n[cwd] workspace", true},
		{"malformed", "AGENTWORKS_REFUSAL: {bad json}", false},
		{"incomplete", `AGENTWORKS_REFUSAL: {"reason":"History unavailable"}`, false},
		{"blank field", `AGENTWORKS_REFUSAL: {"reason":" ","blocked_action":"write","resolution":"retry"}`, false},
		{"wrong type", `AGENTWORKS_REFUSAL: {"reason":2,"blocked_action":"write","resolution":"retry"}`, false},
		{"duplicate", record + "\n" + record, false},
		{"conflicting", record + "\nAGENTWORKS_REFUSAL: {bad json}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := scriptedTerminalStopError("collector", tc.output).Error()
			for _, want := range []string{"stopped with exit code 2", "Automatic repair was withheld", "do not bypass a guard", "Captured output:"} {
				if !strings.Contains(got, want) {
					t.Fatalf("missing %q: %s", want, got)
				}
			}
			if !strings.Contains(got, tc.output) {
				t.Fatal("original output must remain available for diagnosis")
			}
			if tc.explicit {
				for _, want := range []string{"The script reported a refusal", "Reason: History could not be verified", "Blocked action: Overwrite balance rows", "Resolution: Restore database access"} {
					if !strings.Contains(got, want) {
						t.Fatalf("missing %q: %s", want, got)
					}
				}
			} else if !strings.Contains(got, "No valid explicit refusal record was supplied") {
				t.Fatalf("must not infer intent from output: %s", got)
			}
			for _, claim := range []string{"working correctly", "not a bug", "protective logic detected"} {
				if strings.Contains(got, claim) {
					t.Fatalf("unverified claim %q: %s", claim, got)
				}
			}
			decision := decideScriptedFastPath(&ScriptedFastPathResult{
				RanScript: true, ExitCode: 2, TerminalRefusal: true,
				TerminalRefusalReason: tc.output, ExistingScript: "main.py",
			})
			if !decision.TerminalRefusal || decision.PriorScript != "" || decision.PriorError != "" {
				t.Fatalf("all exit-2 cases must remain terminal without relearn context: %+v", decision)
			}
		})
	}
}
