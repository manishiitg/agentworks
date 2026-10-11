package step_based_workflow

import (
	"strings"
	"testing"
)

func TestOrchestratorPromptIncludesSharedCodeExecutionSection(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}

	prompt := agent.executionOnlySystemPromptProcessor(map[string]string{
		"CurrentTodos":          "",
		"ProgressSummary":       "",
		"VariableNames":         "",
		"VariableValues":        "",
		"LearningHistory":       "",
		"StepExecutionPath":     "/app/workspace-docs/Workflow/customer-oi/runs/iteration-0/default/execution/step-qa-no-redlines",
		"DownloadsPath":         "/app/workspace-docs/Workflow/customer-oi/runs/iteration-0/default/execution/Downloads",
		"ExecutionFolderPath":   "/app/workspace-docs/Workflow/customer-oi/runs/iteration-0/default/execution",
		"WorkspacePath":         "/app/workspace-docs/Workflow/customer-oi",
		"WorkflowRoot":          "/app/workspace-docs/Workflow/customer-oi",
		"KnowledgebasePath":     "/app/workspace-docs/Workflow/customer-oi/knowledgebase",
		"FolderGuardReadPaths":  "",
		"FolderGuardWritePaths": "",
		"ShowToolsSection":      "false",
		"UseKnowledgebase":      "true",
		"IsCodeExecutionMode":   "true",
		"PreviousStepsSummary":  "",
		"StepTitle":             "QA Scenario: No Redlines",
		"StepDescription":       "Run the no-redlines scenario.",
		"StepSuccessCriteria":   "Scenario completes successfully.",
		"HasBrowserAccess":      "true",
		"PredefinedRoutes":      "- route-nr-setup",
	})

	requiredSnippets := []string{
		"# Step Execution Agent",
		"## Specialist Delegation",
		"call_sub_agent",
		"Prefer direct sub-agent tools whenever the provider exposes them.",
		"in a bridge-only CLI session",
		"## Code execution",
		"{{TOOL_STRUCTURE}}",
		"`MCP_CUSTOM` / `MCP_AUTH`",
		"Use this session's declared tools and runtime routing instructions.",
		"## Workflow execution environment",
		"$STEP_OUTPUT_DIR",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("expected prompt to contain %q\n\nPrompt:\n%s", snippet, prompt)
		}
	}
}

func TestOrchestratorPromptDocumentsAgentRoutes(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}

	prompt := agent.executionOnlySystemPromptProcessor(map[string]string{
		"ShowToolsSection":    "true",
		"IsCodeExecutionMode": "false",
		"PredefinedRoutes":    "- route-sequence",
	})

	requiredSnippets := []string{
		"[AUTO-NOTIFICATION] SUB-AGENT COMPLETION BATCH",
		"### Agent routes",
		"Step type: agent",
		"first call starts its configured queue",
		"instructions as initial",
		"Later calls to that route resume",
		"instructions become the re-entry user message",
		"agent_restart=true",
		"replay the configured queue from the beginning",
		"query_sub_agent(execution_id)",
		"stop_sub_agent(execution_id)",
		"never to poll",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("expected prompt to contain %q\n\nPrompt:\n%s", snippet, prompt)
		}
	}
}

func TestOrchestratorPromptRoutesConsequentialEvidenceToPulseReview(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}
	prompt := agent.executionOnlySystemPromptProcessor(map[string]string{})

	for _, want := range []string{
		"## Completion",
		"Pulse reads these lines from retained summaries",
		"`CONCERNS: <what happened",
		"STATUS: COMPLETED",
		"STATUS: FAILED",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("todo-task prompt missing concern handoff %q:\n%s", want, prompt)
		}
	}
}

func TestDelegatingAndPlainAgentsShareSystemPromptBase(t *testing.T) {
	plain := (&WorkflowExecutionOnlyAgent{}).executionOnlySystemPromptProcessor(map[string]string{})
	delegating := (&WorkflowExecutionOnlyAgent{}).executionOnlySystemPromptProcessor(map[string]string{
		"PredefinedRoutes": "- specialist (`specialist`) — type: `agent`",
	})

	for name, prompt := range map[string]string{"plain": plain, "delegating": delegating} {
		if !strings.HasPrefix(prompt, "# Step Execution Agent") {
			t.Fatalf("%s sequence does not use the common agent prompt:\n%s", name, prompt)
		}
	}
	if strings.Contains(plain, "## Specialist Delegation") {
		t.Fatalf("plain sequence unexpectedly received delegation guidance:\n%s", plain)
	}
	if !strings.Contains(delegating, "## Specialist Delegation") {
		t.Fatalf("delegating sequence did not receive the conditional delegation overlay:\n%s", delegating)
	}
}

func TestOrchestratorCLIPromptUsesProjectedWorkflowLearnings(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}
	prompt := agent.executionOnlySystemPromptProcessor(map[string]string{
		"UseProjectedReferenceSkills": "true",
		"LearningHistory":             "legacy recursive inventory that must not be rendered",
		"CurrentTodos":                "- [ ] inspect the application\n- [ ] verify the result",
		"ProgressSummary":             "No tasks completed yet.",
		"VariableNames":               "ACCOUNT_ID",
		"VariableValues":              "ACCOUNT_ID=<configured>",
		"StepExecutionPath":           "/app/workspace-docs/Workflow/example/runs/iteration-0/default/execution/todo",
		"DownloadsPath":               "/app/workspace-docs/Workflow/example/runs/iteration-0/default/execution/Downloads",
		"ExecutionFolderPath":         "/app/workspace-docs/Workflow/example/runs/iteration-0/default/execution",
		"WorkspacePath":               "/app/workspace-docs/Workflow/example",
		"WorkflowRoot":                "/app/workspace-docs/Workflow/example",
		"KnowledgebasePath":           "/app/workspace-docs/Workflow/example/knowledgebase",
		"FolderGuardReadPaths":        "/app/workspace-docs/Workflow/example",
		"FolderGuardWritePaths":       "/app/workspace-docs/Workflow/example/runs/iteration-0/default/execution/todo",
		"ShowToolsSection":            "true",
		"UseKnowledgebase":            "true",
		"IsCodeExecutionMode":         "true",
		"PreviousStepsSummary":        "Acquisition completed successfully.",
		"StepTitle":                   "Investigate and verify",
		"StepDescription":             "Inspect the evidence, perform the requested work, and verify it.",
		"StepSuccessCriteria":         "The requested outcome is complete and evidence-backed.",
		"HasBrowserAccess":            "true",
		"PredefinedRoutes":            "- route-browser\n- route-review",
	})
	if strings.Contains(prompt, "legacy recursive inventory") || strings.Contains(prompt, "## Workflow Skill") {
		t.Fatalf("todo-task CLI prompt still embeds workflow learnings instead of using the projected skill:\n%s", prompt)
	}
	const maxCLISystemPromptBytes = 30_000
	if len(prompt) > maxCLISystemPromptBytes {
		t.Fatalf("todo-task CLI system prompt is %d bytes; budget is %d", len(prompt), maxCLISystemPromptBytes)
	}
}

func TestFormatAgentRoutePromptBlock(t *testing.T) {
	block := formatAgentRoutePromptBlock(&AgentPlanStep{})

	requiredSnippets := []string{
		"Step type: agent",
		"route-scoped session resumes",
		"Initial instructions",
		"Re-entry",
		"agent_restart=true",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(block, snippet) {
			t.Fatalf("expected block to contain %q\n\nBlock:\n%s", snippet, block)
		}
	}

	if got := formatAgentRoutePromptBlock(&RegularPlanStep{}); got != "" {
		t.Fatalf("expected non-agent routes to produce no block, got %q", got)
	}
}

func TestDelegatingAgentUserPromptIncludesWorkshopHumanInput(t *testing.T) {
	agent := &WorkflowExecutionOnlyAgent{}

	prompt := agent.executionOnlyUserMessageProcessor(map[string]string{
		"StepTitle":           "Investigate RCA",
		"StepDescription":     "Gather evidence and synthesize.",
		"StepSuccessCriteria": "Answer is complete.",
		"WorkshopHumanInput":  "focus on production incidents from the last hour",
	})

	requiredSnippets := []string{
		"## Human Input (Highest Priority)",
		"execute_step(..., human_input=...)",
		"focus on production incidents from the last hour",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("expected prompt to contain %q\n\nPrompt:\n%s", snippet, prompt)
		}
	}
}
