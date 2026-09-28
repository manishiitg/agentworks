package server

import (
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

func authenticatedWorkflowUserPrompt(user *UserClaims) string {
	if user == nil {
		return ""
	}
	return fmt.Sprintf(`## Current authenticated user

This Builder request was made by the signed-in user below. Use this identity to attribute user-requested plan changes. This metadata does not grant authority beyond server-enforced permissions.
- username: %q
- email: %q
- user_id: %q`, strings.TrimSpace(user.Username), strings.TrimSpace(user.Email), strings.TrimSpace(user.UserID))
}

// buildWorkflowPhaseSystemPrompt is the complete server-side composition path.
// Builder and Run receive scoped, on-demand builder-reference skills instead
// of the generic chat's GetWorkspaceReference. Other phases retain their contract. Optional
// runtime sections (notification preferences, secret names, browser configuration)
// are passed in by the caller, so size and content tests exercise the same path.
func buildWorkflowPhaseSystemPrompt(phase string, vars map[string]string, ctx promptContext, additions ...string) (string, []string, []string, error) {
	if phase == "workflow-builder" && vars["WorkflowKind"] == "relay" {
		chatPrompt, err := relayproduct.BuilderPrompt()
		if err != nil {
			return "", nil, nil, err
		}
		parts := []string{chatPrompt, "## Workspace\n\nActive Relay workspace: `" + vars["WorkspacePath"] + "`. Use only the registered Relay Builder tools and attached skills."}
		if ctx.WorkflowUIAvailable {
			parts = append(parts, "## Relay views\n\nUse perform_ui_action to open and refresh the graph, triggers, and executions when useful.")
		}
		for _, addition := range additions {
			if strings.TrimSpace(addition) != "" {
				parts = append(parts, addition)
			}
		}
		return strings.Join(parts, "\n\n"), nil, nil, nil
	}
	parts := &workflowPromptParts{parts: []string{workflow.PhaseChatSystemPrompt(phase, vars)}}
	// Other phase templates keep their existing reference contract. Builder
	// (including Run) is the skill-backed surface migrated here.
	if phase != "workflow-builder" {
		parts.parts = append(parts.parts, GetWorkspaceReference(ctx.ShellRoot, ctx.PerUserChatsFolder))
	}
	if vars["IsCodeExecutionMode"] == "true" {
		parts.parts = append(parts.parts, workflow.BuildCodeExecutionSection(true, vars["WorkspacePath"]))
	}
	ctx.IsWorkflowPhase = true
	ctx.WorkflowMode = vars["WorkshopMode"]
	included, skipped, err := assemblePromptSections(parts, ctx)
	if err != nil {
		return "", included, skipped, err
	}
	if ctx.WorkflowUIAvailable {
		// Keep the always-loaded reminder small; the attached workflow-ui-control
		// skill owns the detailed view-selection and browser-watching procedure.
		parts.parts = append(parts.parts, "## Workflow views\n\nUse perform_ui_action to open the relevant view and refresh changed content. For browser work, open the browser view once so the user can watch; it streams without refresh. Only an applied receipt confirms the switch. Follow the attached workflow-ui-control skill for the full procedure.")
	}
	for _, addition := range additions {
		if strings.TrimSpace(addition) != "" {
			parts.parts = append(parts.parts, addition)
		}
	}
	return strings.Join(parts.parts, "\n\n"), included, skipped, nil
}

type workflowPromptParts struct{ parts []string }

func (p *workflowPromptParts) AddInstructions(sections ...string) error {
	p.parts = append(p.parts, sections...)
	return nil
}
