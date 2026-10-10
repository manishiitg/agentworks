package server

import (
	"regexp"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Exercise the production composer, including server-added sections. The old
// template-only ceiling missed a 48KB reference appended in server.go.
// Dynamic values are representative fixtures, not a claimed limit on arbitrary
// user content. Attached tool schemas/skill indexes are added by mcpagent later.
func TestAssembledWorkflowPrompt(t *testing.T) {
	for _, mode := range []string{"workshop", "run"} {
		for _, projected := range []string{"true", "false"} {
			for _, ui := range []bool{true, false} {
				name := mode + "/projected=" + projected
				if ui {
					name += "/interactive"
				} else {
					name += "/channel"
				}
				t.Run(name, func(t *testing.T) {
					vars := map[string]string{
						"WorkspacePath": "Workflow/example", "WorkshopMode": mode,
						"IsCodeExecutionMode": "true", "UseProjectedReferenceSkills": projected,
						"WorkflowObjective":       "Keep approved site changes accurate.",
						"WorkflowSuccessCriteria": "Changes match the approved proposal and evidence.",
						"AvailableGroups":         "default", "StepSummary": "draft → approval → apply",
					}
					ctx := promptContext{
						ShellRoot: "/app/workspace-docs", WorkflowPhaseFolder: "Workflow/example",
						WorkflowUIAvailable: ui, HasLLMCapabilityTools: true,
						CapabilitySection:  "## Capability inventory\n" + strings.Repeat("Example capability metadata.\n", 40),
						WorkflowContext:    "## Referenced workflow evidence\nThe latest referenced run is complete.",
						GrantSections:      []string{"## Granted runtime access\nUse the granted workspace tools."},
						CLIToolEnvironment: "## CLI environment\nUse the authenticated bridge.",
					}
					if !ui {
						ctx.ChannelFormatting = "## Channel formatting\nKeep replies suitable for WhatsApp."
					}
					notifications := buildWorkflowNotificationInstructionsPrompt("Report approved work only.", "Summarize verified changes.")
					secrets := workflow.BuildWorkflowSecretPrompt([]orchestrator.SecretEntry{{Name: "TEST_TOKEN", Value: "never-render-this-value"}})
					browser := "## Browser\nConfigured mode=auto. Call agent_browser status first; read builder-reference/references/browser-usage.md."
					got, _, _, err := buildWorkflowPhaseSystemPrompt("workflow-builder", vars, ctx, notifications, secrets, browser)
					if err != nil {
						t.Fatal(err)
					}
					t.Logf("Complete server prompt: %d bytes (mode=%s projected=%s UI=%v)", len(got), mode, projected, ui)
					if len(got) > 15000 {
						t.Fatalf("assembled prompt is %d bytes; move static detail into scoped skills (ceiling 15000)", len(got))
					}
					for _, section := range []string{"## CURRENT MODE:", "## CURRENT STATE", "## Workspace\n", "## Referenced workflow evidence", "## Granted runtime access", "## Capability inventory", "## Browser\n", notifications, secrets} {
						if strings.Count(got, section) != 1 {
							t.Errorf("section must occur once: %q", section)
						}
					}
					for _, unwanted := range []string{"## Creating New Workflows", "## LLM Tier Configuration", "Eval tracks BOTH", "Set new steps to", "tell them to open the **Report tab**", "never-render-this-value"} {
						if strings.Contains(got, unwanted) {
							t.Errorf("stale/unscoped content in prompt: %q", unwanted)
						}
					}
					if strings.Contains(got, "perform_ui_action") != ui {
						t.Error("view instructions must follow interactive UI capability")
					}
					if mode == "run" {
						runTools, err := guidance.RenderReferenceKindForTest("workflow-tools", mode)
						if err != nil {
							t.Fatal(err)
						}
						for _, mutation := range []string{"## Plan Modification", "## Step Config & Analysis", "install_skill", "set_workflow_secret"} {
							if strings.Contains(runTools, mutation) {
								t.Errorf("Run skill still advertises mutation: %s", mutation)
							}
						}

						for _, unavailable := range []string{"`install_skill`", "`set_workflow_secret`", "`create_schedule`", "Workshop may write"} {
							if strings.Contains(got, unavailable) {
								t.Errorf("Run advertises mutation: %s", unavailable)
							}
						}
					}
					// A short prompt is useful only if all of its skill pointers resolve
					// in that mode. This catches references to Workshop-only docs in Run.
					for _, match := range regexp.MustCompile(`references/([a-z0-9-]+)\.md`).FindAllStringSubmatch(got, -1) {
						if _, err := guidance.RenderReferenceKindForTest(match[1], mode); err != nil {
							t.Errorf("unavailable reference %s: %v", match[1], err)
						}
					}
				})
			}
		}
	}
}

func TestNativeWorkflowPromptDoesNotInstructHTTPDiscovery(t *testing.T) {
	got, _, _, err := buildWorkflowPhaseSystemPrompt("workflow-builder", map[string]string{
		"WorkspacePath": "Workflow/example", "WorkshopMode": "run", "IsCodeExecutionMode": "false",
	}, promptContext{ShellRoot: "/app/workspace-docs", WorkflowPhaseFolder: "Workflow/example"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "All other workflow tools are HTTP-backed") || strings.Contains(got, "$MCP_CUSTOM") {
		t.Fatal("native tool-calling prompt must use supplied schemas, not the CLI bridge")
	}
}

func TestAuthenticatedChatUserPrompt(t *testing.T) {
	got := authenticatedChatUserPrompt(&UserClaims{UserID: "user-123", Username: "erin", Email: "erin@customer.example"})
	for _, want := range []string{"## Current authenticated user", `username: "erin"`, `email: "erin@customer.example"`, `user_id: "user-123"`, "never treat its email as this user", "does not grant permissions or establish Gmail access"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %s", want, got)
		}
	}
	if authenticatedChatUserPrompt(nil) != "" {
		t.Fatal("anonymous request must not synthesize a user identity")
	}
}
