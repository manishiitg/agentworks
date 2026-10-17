package step_based_workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Plan Drift and the due flags use one objective rule for the 1.0.49 Resources section: all four lines present, no
// "none" for a reference the description names. Store settings do not deny workflow-folder reads.
func TestResourceIntentProblemsAreObjective(t *testing.T) {
	full := "## Goal\nDo it.\n\n## Resources\n- Knowledge base: none\n- Database: read the orders\n- Brain: none\n- Learnings: none"
	plan := func(description string, authored bool) []byte {
		raw, _ := json.Marshal(map[string]any{"steps": []any{map[string]any{"type": "agent", "id": "a", "description": description, "authored_prompt": authored,
			"prompt_contract": AgentDescriptionFirstPromptContract}}})
		return raw
	}
	for name, tc := range map[string]struct {
		description string
		authored    bool
		want        string // substring of the reason; empty means no problem
	}{
		"all four lines":                   {description: full},
		"no Resources section":             {description: "## Goal\nDo it.", want: "no ## Resources"},
		"a line missing":                   {description: strings.Replace(full, "\n- Brain: none", "", 1), want: "Brain"},
		"none for a named reference":       {description: full + "\n\n## Guides\nknowledgebase/tone.md", want: "not used"},
		"authored prompts are not checked": {description: "no sections", authored: true},
	} {
		problems, evaluated := ResourceIntentProblems(plan(tc.description, tc.authored))
		got := problems["a"]
		if tc.authored && evaluated["a"] {
			t.Errorf("%s: an authored prompt was evaluated", name)
		}
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: problem = %q, want containing %q", name, got, tc.want)
		}
	}
}

// A step that tells its agent to read a file that does not exist makes Plan Drift due once even when the break is old,
// so the owner's workflow gets it repaired or a plain question (a note break at the first look still does not).
func TestOldMissingFileNamedByAStepFlagsPlanDriftOnce(t *testing.T) {
	plan := `{"steps":[{"type":"agent","id":"step-a","description":"## Goal\nDo A with the company notes in knowledgebase/context/context.md.","items":[{"id":"x","type":"user_message","message":"Do A."}]}]}`
	planDriftCandidateWorkspace(t, "Workflow/old-missing-file", plan, reviewedStepConfig)
	items, err := CollectPlanDriftDueItems("Workflow/old-missing-file")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.StepID == "step-a" && strings.Contains(item.Reason, "knowledgebase/context/context.md") {
			found = true
		}
	}
	if !found {
		t.Fatalf("an old missing file named by a step must make Plan Drift due for it, got %#v", items)
	}
}

// The real regression: a named guide remains readable with automatic Learnings off.
// Both stamp surfaces and drift must agree without changing access or allowing contributions.
func TestResourceIntentNamedGuidesKeepConfiguredAccess(t *testing.T) {
	controller := newAgentClosingTestOrchestrator(t)
	controller.selectedRunFolder = "iteration-0/default"
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	workspace := controller.GetWorkspacePath()
	root := filepath.Join(docs, workspace)
	for _, path := range []string{"planning", "learnings/_global/references", "knowledgebase/notes"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"learnings/_global/references/method.md", "knowledgebase/notes/policy.md"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("Follow this guide."), 0644); err != nil {
			t.Fatal(err)
		}
	}
	description := "## Goal\nFollow the approved method.\n\n## Resources\n- Knowledge base: reads the named policy; no writes\n- Database: none\n- Brain: none\n- Learnings: reads the named method; no contributions\n\n## Guides\nlearnings/_global/references/method.md\nknowledgebase/notes/policy.md"
	raw, err := json.Marshal(map[string]any{"steps": []any{map[string]any{"type": "agent", "id": "work", "title": "Read named guides", "description": description, "prompt_contract": AgentDescriptionFirstPromptContract}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{LearningsAccessNone, LearningsAccessRead} {
		t.Run(mode, func(t *testing.T) {
			config := `{"steps":[{"id":"work","agent_configs":{"knowledgebase_access":"none","learnings_access":"` + mode + `"}}]}`
			configPath := filepath.Join(root, "planning/step_config.json")
			if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
				t.Fatal(err)
			}
			// MCP stamps share this preflight; the Builder invokes the same Resources validator.
			if err := CheckContractStamp(ResourceIntentContractVersion, root, []byte(`{}`), func() (string, error) { return string(raw), nil }); err != nil {
				t.Fatal(err)
			}
			if problems, evaluated := ResourceIntentProblems(raw); len(problems) != 0 || !evaluated["work"] {
				t.Fatalf("unexpected drift: %v / %v", problems, evaluated)
			}
			cfg := &AgentConfigs{KnowledgebaseAccess: KBAccessNone, LearningsAccess: mode}
			reads, writes := controller.setupExecutionFolderGuard("work", "work", KBAccessNone, mode, DBAccessNone, cfg)
			covers := func(paths []string, target string) bool {
				for _, path := range paths {
					if path == target || strings.HasPrefix(target, strings.TrimRight(path, "/")+"/") {
						return true
					}
				}
				return false
			}
			for _, path := range []string{"learnings/_global/references/method.md", "knowledgebase/notes/policy.md"} {
				target := filepath.Join(workspace, path)
				if !covers(reads, target) || covers(writes, target) {
					t.Fatalf("guide access changed: reads=%v writes=%v", reads, writes)
				}
			}
			if canWriteLearnings(cfg, nil) || kbAccessAllowsWrite(KBAccessNone) {
				t.Fatal("readable guides enabled writes")
			}
			after, err := os.ReadFile(configPath)
			if err != nil || string(after) != config {
				t.Fatalf("stamp changed access: %s / %v", after, err)
			}
			// Saying no use while the same description names a guide remains a contradiction.
			noUse := strings.Replace(string(raw), "Learnings: reads the named method; no contributions", "Learnings: none", 1)
			if err := ValidateResourceIntentContractStamp(noUse); err == nil {
				t.Fatal("stamp accepted none for a named guide")
			}
			if problems, _ := ResourceIntentProblems([]byte(noUse)); !strings.Contains(problems["work"], "not used") {
				t.Fatalf("drift lost the named-guide contradiction: %v", problems)
			}
		})
	}
}
