package step_based_workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutionOnlyPromptIncludesCodeExecutionInstructions(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}

	prompt := agent.executionOnlySystemPromptProcessor(map[string]string{
		"WorkspacePath":         "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution",
		"WorkflowRoot":          "/app/workspace-docs/Workflow/test",
		"StepExecutionPath":     "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution/step-sample",
		"StepContextOutput":     "output.json",
		"LearningHistory":       "",
		"StepNumber":            "step-sample",
		"PreviousStepsSummary":  "",
		"KnowledgebasePath":     "/app/workspace-docs/Workflow/test/knowledgebase",
		"UseKnowledgebase":      "true",
		"FolderGuardReadPaths":  "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution",
		"FolderGuardWritePaths": "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution/step-sample",
		"IsCodeExecutionMode":   "true",
		"IsScriptedMode":        "false",
	})

	requiredSnippets := []string{
		"## Code execution",
		"Derive output paths from `os.environ['STEP_OUTPUT_DIR']` in code.",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("expected prompt to contain %q\n\nPrompt:\n%s", snippet, prompt)
		}
	}
}

func TestExecutionOnlyPromptLeavesConsequenceTriageToPulseReview(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}
	prompt := agent.executionOnlySystemPromptProcessor(map[string]string{})

	for _, want := range []string{
		"consequential non-fatal evidence",
		"`CONCERNS: <what happened, the exact affected artifact or operation, and the evidence>`",
		"do not classify, deduplicate or file it anywhere else",
		"Pulse reads these lines from retained summaries",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("execution prompt missing concern handoff %q:\n%s", want, prompt)
		}
	}
}

func TestExecutionOnlyPromptUsesManagedWorkflowDBTools(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}

	prompt := agent.executionOnlySystemPromptProcessor(map[string]string{
		"WorkspacePath":         "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution",
		"WorkflowRoot":          "/app/workspace-docs/Workflow/test",
		"StepExecutionPath":     "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution/step-sample",
		"StepContextOutput":     "",
		"LearningHistory":       "",
		"StepNumber":            "step-sample",
		"KnowledgebasePath":     "/app/workspace-docs/Workflow/test/knowledgebase",
		"FolderGuardReadPaths":  "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution, /app/workspace-docs/Workflow/test/db",
		"FolderGuardWritePaths": "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution/step-sample, /app/workspace-docs/Workflow/test/db",
		"IsCodeExecutionMode":   "false",
		"IsScriptedMode":        "false",
		"DBAccess":              DBAccessReadWrite,
	})

	requiredSnippets := []string{
		"Use `query_workflow_db` for schema discovery and reads",
		"`mutate_workflow_db` for transactional INSERT/UPDATE/DELETE operations",
		`jq -n --arg sql "$sql"`,
		"never place SQL containing single quotes",
		"persist results with `mutate_workflow_db`",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("expected prompt to contain %q\n\nPrompt:\n%s", snippet, prompt)
		}
	}
	for _, forbidden := range []string{"$DB_PATH", "sqlite3"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("managed execution prompt still teaches raw DB access %q\n\nPrompt:\n%s", forbidden, prompt)
		}
	}
}

func TestReadOnlyExecutionPromptCannotRecommendMutationOrRawSQLite(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}
	prompt := agent.executionOnlySystemPromptProcessor(map[string]string{
		"WorkspacePath":         "/app/workspace-docs/Workflow/test/runs/run/execution",
		"WorkflowRoot":          "/app/workspace-docs/Workflow/test",
		"StepExecutionPath":     "/app/workspace-docs/Workflow/test/runs/run/execution/reader",
		"StepContextOutput":     "result.json",
		"KnowledgebasePath":     "/app/workspace-docs/Workflow/test/knowledgebase",
		"FolderGuardReadPaths":  "/app/workspace-docs/Workflow/test/db",
		"FolderGuardWritePaths": "/app/workspace-docs/Workflow/test/runs/run/execution/reader",
		"IsScriptedMode":        "false",
		"DBAccess":              DBAccessRead,
	})
	if !strings.Contains(prompt, "READ-ONLY workflow evidence") || !strings.Contains(prompt, "query_workflow_db") {
		t.Fatalf("read-only DB guidance missing:\n%s", prompt)
	}
	// Naming the forbidden tool is correct, explicit guidance -- "do not call
	// mutate_workflow_db" cannot avoid the substring "mutate_workflow_db" and
	// should not have to. What must never appear is an instruction that
	// recommends calling it.
	if !strings.Contains(prompt, "do not call `mutate_workflow_db`") {
		t.Fatalf("read-only prompt does not explicitly prohibit mutate_workflow_db:\n%s", prompt)
	}
	for _, forbidden := range []string{"Use `mutate_workflow_db`", "$DB_PATH", "sqlite3", "INSERT ... ON CONFLICT"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("read-only prompt contains %q:\n%s", forbidden, prompt)
		}
	}
}

func TestExecutionOnlyUserPromptIncludesWorkshopHumanInput(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}

	prompt := agent.executionOnlyUserMessageProcessor(map[string]string{
		"StepTitle":               "Write back to Notion",
		"StepDescription":         "Publish the RCA.",
		"StepContextDependencies": "",
		"StepContextOutput":       "notion_writeback.json",
		"WorkspacePath":           "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution",
		"StepExecutionPath":       "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution/step-writeback",
		"IsCodeExecutionMode":     "true",
		"IsScriptedMode":          "false",
		"WorkshopHumanInput":      "post RCA q-20260509T124321-rslat as wiki page",
	})

	requiredSnippets := []string{
		"## Human Input (Highest Priority)",
		"execute_step(..., human_input=...)",
		"post RCA q-20260509T124321-rslat as wiki page",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("expected prompt to contain %q\n\nPrompt:\n%s", snippet, prompt)
		}
	}
}

func TestExecutionOnlyPromptsTreatSkillAsAdvisory(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}

	systemPrompt := agent.executionOnlySystemPromptProcessor(map[string]string{
		"WorkspacePath":         "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution",
		"WorkflowRoot":          "/app/workspace-docs/Workflow/test",
		"StepExecutionPath":     "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution/step-sample",
		"StepContextOutput":     "output.json",
		"LearningHistory":       "Use the legacy selector.",
		"StepNumber":            "step-sample",
		"KnowledgebasePath":     "/app/workspace-docs/Workflow/test/knowledgebase",
		"FolderGuardReadPaths":  "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution, /app/workspace-docs/Workflow/test/learnings/_global",
		"FolderGuardWritePaths": "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution/step-sample",
		"IsCodeExecutionMode":   "false",
		"IsScriptedMode":        "false",
	})
	systemSnippets := []string{
		"Treat learnings/skill content as advisory guidance from previous runs",
		"the system-level Step Charter and current user message (including any parent-agent or human input) are the source of truth",
	}
	for _, snippet := range systemSnippets {
		if !strings.Contains(systemPrompt, snippet) {
			t.Fatalf("expected system prompt to contain %q\n\nPrompt:\n%s", snippet, systemPrompt)
		}
	}
	if strings.Contains(systemPrompt, "Use the legacy selector.") {
		t.Fatal("attached-skill mode must not recursively inline legacy learning history")
	}

	userPrompt := agent.executionOnlyUserMessageProcessor(map[string]string{
		"StepTitle":           "Fetch report",
		"StepDescription":     "Use the current report endpoint.",
		"StepContextOutput":   "output.json",
		"StepExecutionPath":   "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution/step-fetch",
		"IsCodeExecutionMode": "false",
		"IsScriptedMode":      "false",
		"LearningHistory":     "Use the legacy selector.",
	})
	if !strings.Contains(userPrompt, "Read **Skill files** as guidance only") ||
		!strings.Contains(userPrompt, "The current step description is the main source of truth") {
		t.Fatalf("expected user prompt to treat skill as advisory\n\nPrompt:\n%s", userPrompt)
	}
}

func TestExecutionOnlyCLIPromptUsesProjectedReferencesAndStaysUnderBudget(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}
	vars := map[string]string{
		"UseProjectedReferenceSkills": "true",
		"WorkspacePath":               "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution",
		"WorkflowRoot":                "/app/workspace-docs/Workflow/test",
		"StepExecutionPath":           "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution/step-browser",
		"StepContextOutput":           "result.json",
		"LearningHistory":             "legacy recursive inventory that must not be rendered",
		"StepNumber":                  "step-browser",
		"KnowledgebasePath":           "/app/workspace-docs/Workflow/test/knowledgebase",
		"DBPath":                      "/app/workspace-docs/Workflow/test/db/db.sqlite",
		"KbAccess":                    "read-write",
		"KbAccessLabel":               "READ/WRITE",
		"FolderGuardReadPaths":        "/app/workspace-docs/Workflow/test, /app/workspace-docs/Workflow/test/learnings/_global",
		"FolderGuardWritePaths":       "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution/step-browser, /app/workspace-docs/Workflow/test/db",
		"IsCodeExecutionMode":         "true",
		"IsScriptedMode":              "true",
		"HasBrowserAccess":            "true",
		"ScriptedInputArgs":           "/app/workspace-docs/Workflow/test/runs/iteration-0/default/execution/input.json",
		"ScriptedEnvVarNames":         "STEP_OUTPUT_DIR\nDB_PATH\nMCP_API_URL\nMCP_API_TOKEN\nSECRET_PASSWORD",
		"ScriptedVarMapping":          "{{ACCOUNT}} → os.environ['VAR_ACCOUNT']",
		"ValidationSchema":            `{"required_files":[{"path":"result.json"}]}`,
	}

	prompt := agent.executionOnlySystemPromptProcessor(vars)
	t.Logf("compact CLI execution prompt: %d bytes", len(prompt))
	for _, forbidden := range []string{
		"legacy recursive inventory",
		"## Python Best Practices",
		"## Browser automation rules",
		strings.TrimSpace(BuildMainPyAuthoringRules()),
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("CLI prompt still embeds projected reference material %q", forbidden)
		}
	}
	const maxCLISystemPromptBytes = 30_000
	if len(prompt) > maxCLISystemPromptBytes {
		t.Fatalf("CLI execution system prompt is %d bytes; budget is %d", len(prompt), maxCLISystemPromptBytes)
	}
}

func TestExecutionOnlyAPIPromptKeepsOneInlineAuthoringFallback(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}
	prompt := agent.executionOnlySystemPromptProcessor(map[string]string{
		"UseProjectedReferenceSkills": "false",
		"StepExecutionPath":           "/app/workspace-docs/Workflow/test/execution/step-scripted",
		"IsCodeExecutionMode":         "true",
		"IsScriptedMode":              "true",
		"HasBrowserAccess":            "true",
	})

	if !strings.Contains(prompt, strings.TrimSpace(BuildMainPyAuthoringRules())) {
		t.Fatal("API prompt lost its inline main.py authoring fallback")
	}
	if got := strings.Count(prompt, "## Python Best Practices"); got != 1 {
		t.Fatalf("API prompt must contain exactly one Python fallback section, got %d", got)
	}
	if !strings.Contains(prompt, "## Browser automation rules") {
		t.Fatal("API prompt lost its inline browser authoring fallback")
	}
}

// A synthetic learnings/KB closing turn (IsContributionTurn) must render as JUST the
// contribution instruction — the execute-the-task / Inputs / Output / "create the
// output file" scaffolding is contradictory and wasteful on a write-only turn. A
// normal turn keeps that scaffolding. Regression for the learnings/KB turn carrying
// execution scaffolding.
func TestContributionTurnUserMessageDropsExecutionScaffolding(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}
	contribMsg := "## Learnings Contribution (dedicated turn)\nCapture HOW to run this; write it to SKILL.md, then stop."

	contribution := agent.executionOnlyUserMessageProcessor(map[string]string{
		"StepDescription":    contribMsg,
		"IsContributionTurn": "true",
		"StepContextOutput":  "",
	})
	for _, forbidden := range []string{"Execution Checklist", "Create the output file", "MODE NOTE", "### Inputs", "### Output", "Verify the required outputs"} {
		if strings.Contains(contribution, forbidden) {
			t.Fatalf("contribution turn must not carry execution scaffolding %q:\n%s", forbidden, contribution)
		}
	}
	if !strings.Contains(contribution, "write it to SKILL.md, then stop.") {
		t.Fatalf("contribution message body was lost:\n%s", contribution)
	}

	normal := agent.executionOnlyUserMessageProcessor(map[string]string{
		"StepDescription":   "Fetch and persist the report.",
		"StepContextOutput": "report.json",
		"StepExecutionPath": "execution/step-1",
	})
	for _, want := range []string{"Execution Checklist", "Create the output file", "### Inputs", "### Output"} {
		if !strings.Contains(normal, want) {
			t.Fatalf("a normal execution turn must keep its scaffolding %q:\n%s", want, normal)
		}
	}
}

func TestExecutionOnlyUserMessagePreservesSequenceFollowUp(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}
	want := "Now verify the specialist result and repair any remaining gap."
	got := agent.executionOnlyUserMessageProcessor(map[string]string{
		"StepDescription": "opening task that must not be replayed",
		"FollowUpMessage": want,
	})
	if got != want {
		t.Fatalf("follow-up message = %q, want verbatim %q", got, want)
	}
}

func TestExecutionOnlySystemPromptContainsStepCharter(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}
	prompt := agent.executionOnlySystemPromptProcessor(map[string]string{
		"StepTitle":               "Research the account",
		"StepDescription":         "Produce an evidence-backed account brief and fail closed when sources disagree.",
		"StepContextDependencies": "- account.json — resolved dependency",
	})

	for _, want := range []string{"## Step Charter", "**Title:** Research the account", "Produce an evidence-backed account brief", "## Declared Inputs", "account.json — resolved dependency"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing step charter content %q:\n%s", want, prompt)
		}
	}
}

// A step in a product without platform stores (a Relay) is told nothing about a
// workflow database, knowledgebase or learnings it cannot reach (PLAT-454).
func TestExecutionOnlyPromptNamesNoPlatformStoresWhenThereAreNone(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}
	for name, vars := range map[string]map[string]string{
		"declared output": {"DBAccess": DBAccessNone, "KbAccess": KBAccessNone, "DBGuidance": BuildManagedWorkflowDBGuidance(DBAccessNone), "StepContextOutput": "result.json", "DBPath": "/w/db"},
		"no output file":  {"DBAccess": DBAccessNone, "KbAccess": KBAccessNone, "DBGuidance": BuildManagedWorkflowDBGuidance(DBAccessNone), "DBPath": "/w/db"},
	} {
		prompt := agent.executionOnlySystemPromptProcessor(vars)
		for _, banned := range []string{"mutate_workflow_db", "query_workflow_db", "db/db.sqlite", "`/w/db/`", "$DB_PATH", "knowledgebase/context", "knowledgebase/notes", "Knowledgebase access for this step", "**learnings/**", "**knowledgebase/**"} {
			if strings.Contains(prompt, banned) {
				t.Errorf("%s: the prompt of a step with no platform stores still mentions %q", name, banned)
			}
		}
		// The prompt does not explain the absence either: it simply omits the section.
		for _, banned := range []string{"persistent stores", "no workflow database", "soul/soul.md"} {
			if strings.Contains(prompt, banned) {
				t.Errorf("%s: the prompt still has stores text %q", name, banned)
			}
		}
	}
	// A normal workflow step keeps its database and knowledge-base guidance.
	normal := agent.executionOnlySystemPromptProcessor(map[string]string{"DBAccess": DBAccessReadWrite, "KbAccess": KBAccessRead, "DBGuidance": BuildManagedWorkflowDBGuidance(DBAccessReadWrite)})
	for _, want := range []string{"mutate_workflow_db", "**knowledgebase/**", "**learnings/**"} {
		if !strings.Contains(normal, want) {
			t.Errorf("a normal step lost %q", want)
		}
	}
	if note := buildMessageSequenceAccessNote(MessageSequenceWriteAccess{DB: true, Knowledgebase: true, Learnings: true}, DBAccessNone); strings.Contains(note, "db/") || strings.Contains(note, "knowledgebase") || strings.Contains(note, "learnings") || strings.Contains(note, "workflow_db") {
		t.Errorf("the access note of a step with no stores names a store: %s", note)
	}
}

func TestSharedKnowledgebasePromptUsesMCPInsteadOfLocalRecipes(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspace := "Workflow/shared"
	if err := os.MkdirAll(filepath.Join(root, workspace), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, workspace, "workflow.json"), []byte(`{"id":"shared","knowledgebase_mode":"shared","shared_knowledgebase":[{"alias":"payments","folder_id":"folder_shared","access":"read"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"KbAccess": "read", "KnowledgebasePath": "archive", "KnowledgebaseContribution": ""}
	applySharedKBPrompt(workspace, vars)
	if vars["KbAccess"] != "shared" || vars["KnowledgebasePath"] != "" || !strings.Contains(vars["KBGuidanceBlock"], "read_knowledgebase") {
		t.Fatal(vars)
	}
	vars["DBAccess"] = "read"
	vars["WorkflowRoot"] = workspace
	prompt := (&WorkflowExecutionOnlyAgent{}).executionOnlySystemPromptProcessor(vars)
	if !strings.Contains(prompt, "read_knowledgebase") || strings.Contains(prompt, "jq '.topics") || strings.Contains(prompt, "ALWAYS `cat knowledgebase/") {
		t.Fatal("shared prompt retained local recipes", prompt)
	}

}
