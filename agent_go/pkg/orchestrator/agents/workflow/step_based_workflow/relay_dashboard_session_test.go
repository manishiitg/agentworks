package step_based_workflow

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestRelayDashboardBuilderKeepsExecutionStoresPrivate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	const workspace, builder, execution = "Workflow/relay", "relay-dashboard-builder", "relay-dashboard-execution"
	t.Cleanup(func() { common.ClearSessionShellConfig(builder); common.ClearSessionShellConfig(execution) })
	common.SetSessionFolderGuard(builder, []string{workspace}, []string{workspace})
	if err := ConfigureRelayDashboardBuilderSession(builder, workspace, true); err != nil {
		t.Fatal(err)
	}
	cfg := common.GetSessionShellConfig(builder)
	if cfg.Env[workflowDBAccessEnv] != DBAccessReadWrite || cfg.Env["WORKFLOW_KB_ACCESS"] != KBAccessNone {
		t.Fatalf("authoring capabilities: %+v", cfg)
	}
	if slices.Contains(cfg.BlockedPaths, workspace+"/db") {
		t.Fatal("dashboard HTML blocked with execution stores")
	}
	for _, private := range []string{"db/db.sqlite", "db/db.sqlite-wal", "db/db.sqlite-shm", "knowledgebase", "learnings"} {
		if !slices.Contains(cfg.BlockedPaths, filepath.Join(workspace, private)) {
			t.Fatalf("private path unprotected: %s", private)
		}
	}
	for _, dir := range []string{"code", "docs", "db/reports", "db/assets", "db/migrations"} {
		if info, err := os.Stat(filepath.Join(root, workspace, dir)); err != nil || !info.IsDir() {
			t.Fatalf("missing authored root %s: %v", dir, err)
		}
	}
	configureWorkflowDBSession(execution, workspace, DBAccessNone, false)
	if cfg := common.GetSessionShellConfig(execution); cfg.Env[workflowDBAccessEnv] != DBAccessNone || !slices.Contains(cfg.BlockedPaths, workspace+"/db") {
		t.Fatalf("execution gained authoring access: %+v", cfg)
	}
}
