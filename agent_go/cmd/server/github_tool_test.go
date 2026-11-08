package server

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// PLAT-746: the github tool uses exactly the turn's shell selection. A turn whose
// shell gets no secrets (here a Run turn) gets no GitHub token either.
func TestGitHubToolUsesTheTurnsShellSecrets(t *testing.T) {
	api := &StreamingAPI{}
	const session = "github-run-turn"
	recordSessionSecretSource(session, sessionSecretSource{User: "reader", RunMode: true})
	t.Cleanup(func() { sessionSecretSources.Delete(session) })
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, session)
	if _, err := api.githubFromTool(ctx, map[string]interface{}{"action": "issues", "repo": "not a repo"}); err == nil || !strings.Contains(err.Error(), "owner/name") {
		t.Fatalf("malformed repo: %v", err)
	}
	if _, err := api.githubFromTool(ctx, map[string]interface{}{"action": "issues", "repo": "example/project"}); err == nil || !strings.Contains(err.Error(), "No GitHub account is connected") {
		t.Fatalf("a turn without shell secrets got GitHub: %v", err)
	}
}

// PLAT-746 review: a workflow step started by a Run person uses the workflow owner's
// GitHub login, as its shell's secrets do, not the Run person's.
func TestGitHubStepUsesTheWorkflowOwnersLoginNotTheRunPerson(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	registerWorkflowOwner("Workflow/shared-report", "owner")
	api := &StreamingAPI{}
	if got := api.githubStepOwner(context.Background(), "runner", "Workflow/shared-report"); got != "owner" {
		t.Fatalf("a Run person's run looked up %q's GitHub, want the workflow owner's", got)
	}
	if got := api.githubStepOwner(context.Background(), "runner", "Workflow/unregistered"); got != "runner" {
		t.Fatalf("a workflow with no recorded owner should keep the caller, got %q", got)
	}
}
