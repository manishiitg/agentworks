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
