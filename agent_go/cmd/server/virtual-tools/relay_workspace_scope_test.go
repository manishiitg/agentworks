package virtualtools

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"testing"
)

func TestPublishedRelayDBAndCostsKeepVersionScope(t *testing.T) {
	path := "Workflow/.relay_releases/abcdef/v1"
	cfg := &common.SessionShellConfig{Env: map[string]string{"DB_PATH": path + "/db/db.sqlite"}}
	got, err := resolveWorkflowWorkspaceFolder("review-session", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("unexpected scope %q", got)
	}
	t.Logf("release=%s resolved_root=%s db=%s costs=%s", path, got, got+"/db/db.sqlite", workflowCostsPathFromCandidate(path))
}
