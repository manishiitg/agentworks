package virtualtools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// TestParseManagedMigrationStatementsStripsLeadingComments reproduces a
// review finding on PLAT-221: a naturally-commented migration statement
// (a common human/agent authoring style) was rejected outright, because
// neither this parser nor the workspace service's own validator treats "--"
// as insignificant before the anchored "^\s*CREATE..." shape check.
func TestParseManagedMigrationStatementsStripsLeadingComments(t *testing.T) {
	script := `BEGIN IMMEDIATE;

-- Add outcome table
CREATE TABLE IF NOT EXISTS outcomes (
  id TEXT PRIMARY KEY
);

/* index for lookups */
CREATE INDEX IF NOT EXISTS idx_outcomes_id ON outcomes(id);

COMMIT;
`
	statements, err := parseManagedMigrationStatements(script)
	if err != nil {
		t.Fatalf("commented migration rejected: %v", err)
	}
	if len(statements) != 2 {
		t.Fatalf("statements = %d, want 2: %#v", len(statements), statements)
	}
	for _, statement := range statements {
		if strings.HasPrefix(statement, "--") || strings.HasPrefix(statement, "/*") {
			t.Fatalf("statement still starts with its comment, not the DDL: %q", statement)
		}
	}
	if !strings.HasPrefix(statements[0], "CREATE TABLE IF NOT EXISTS outcomes") {
		t.Fatalf("first statement = %q", statements[0])
	}
	if !strings.HasPrefix(statements[1], "CREATE INDEX IF NOT EXISTS idx_outcomes_id") {
		t.Fatalf("second statement = %q", statements[1])
	}
}

// TestParseManagedMigrationStatementsStillRejectsDisallowedShapes proves the
// comment-stripping fix did not loosen the actual allow-list: a genuinely
// disallowed statement, commented or not, is still rejected.
func TestParseManagedMigrationStatementsStillRejectsDisallowedShapes(t *testing.T) {
	for _, script := range []string{
		"-- drop the whole table\nDROP TABLE outcomes",
		"PRAGMA journal_mode=DELETE",
		"-- pragma\nPRAGMA journal_mode=DELETE",
	} {
		if _, err := parseManagedMigrationStatements(script); err == nil {
			t.Fatalf("script %q was accepted, want rejected", script)
		}
	}
}

func TestWorkflowDBToolDefinitionsExplainSafeShellPayloadEncoding(t *testing.T) {
	for _, tool := range []any{workflowDBQueryToolDefinition(), workflowDBMutateToolDefinition()} {
		raw, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"jq -n --arg", "single-quoted JSON literal"} {
			if !strings.Contains(string(raw), want) {
				t.Fatalf("tool definition missing safe shell-payload guidance %q: %s", want, raw)
			}
		}
	}
}

func TestWorkflowDBToolRegistryExposesQueryAndMutation(t *testing.T) {
	registry := CreateWorkflowDBToolRegistry("http://127.0.0.1:1", "", "session")
	if len(registry.Tools) != 4 {
		t.Fatalf("tools=%d, want 4", len(registry.Tools))
	}
	for _, name := range []string{"query_workflow_db", "mutate_workflow_db", "apply_workflow_db_migration", "create_workflow_database_snapshot"} {
		if registry.Executors[name] == nil {
			t.Fatalf("missing executor %q", name)
		}
		if registry.Categories[name] != WorkflowDBToolCategory {
			t.Fatalf("category[%q]=%q", name, registry.Categories[name])
		}
	}
}

func TestWorkflowDBWorkspacePathRecognizesWorkProject(t *testing.T) {
	for _, candidate := range []string{
		"Chats/Work/projects/demo",
		"_users/alice/Chats/Work/projects/demo/db/db.sqlite",
		"/srv/workspace/_users/alice/Chats/Work/projects/demo/frontend",
	} {
		if got := workflowDBWorkspacePathFromCandidate(candidate); got != "Chats/Work/projects/demo" {
			t.Fatalf("workflowDBWorkspacePathFromCandidate(%q) = %q", candidate, got)
		}
	}
}

func TestWorkflowDBBackupSnapshotExecutorRejectsWorkflowStep(t *testing.T) {
	sessionID := "workflow-db-step-backup"
	defer common.ClearSessionShellConfig(sessionID)
	common.SetSessionFolderGuard(sessionID, []string{"Workflow/demo/db"}, nil)
	common.SetSessionShellEnv(sessionID, map[string]string{
		workflowDBAccessEnv: "read",
		"STEP_OUTPUT_DIR":   "Workflow/demo/runs/iteration-0/default/execution/step",
	})

	registry := CreateWorkflowDBToolRegistry("http://127.0.0.1:1", "", sessionID)
	_, err := registry.Executors["create_workflow_database_snapshot"](context.Background(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "restricted to the parent Builder/Pulse session") {
		t.Fatalf("step snapshot error=%v", err)
	}
}

func TestWorkflowDBMutationExecutorDeniesReadOnlySession(t *testing.T) {
	sessionID := "workflow-db-read-only"
	defer common.ClearSessionShellConfig(sessionID)
	common.SetSessionFolderGuard(sessionID, []string{"Workflow/demo/db"}, nil)
	common.SetSessionShellEnv(sessionID, map[string]string{workflowDBAccessEnv: "read"})

	registry := CreateWorkflowDBToolRegistry("http://127.0.0.1:1", "", sessionID)
	_, err := registry.Executors["mutate_workflow_db"](context.Background(), map[string]any{
		"statements": []any{map[string]any{"sql": "DELETE FROM facts"}},
	})
	if err == nil || !strings.Contains(err.Error(), "mutation denied") {
		t.Fatalf("read-only mutation error=%v", err)
	}
}

func TestWorkflowDBMutationExecutorDeniesSessionWithoutExplicitAccess(t *testing.T) {
	sessionID := "workflow-db-missing-access"
	defer common.ClearSessionShellConfig(sessionID)
	common.SetSessionFolderGuard(sessionID, []string{"Workflow/demo/db"}, []string{"Workflow/demo/db"})

	registry := CreateWorkflowDBToolRegistry("http://127.0.0.1:1", "", sessionID)
	_, err := registry.Executors["mutate_workflow_db"](context.Background(), map[string]any{
		"statements": []any{map[string]any{"sql": "DELETE FROM facts"}},
	})
	if err == nil || !strings.Contains(err.Error(), "explicit db_access=read-write is required") {
		t.Fatalf("missing-access mutation error=%v", err)
	}
}

func TestResolveCurrentWorkflowDBPathNeverAcceptsArbitraryDBPath(t *testing.T) {
	sessionID := "workflow-db-path"
	defer common.ClearSessionShellConfig(sessionID)
	common.SetSessionFolderGuard(sessionID, []string{"/tmp/untrusted.sqlite", "Workflow/demo/db"}, nil)

	path, err := resolveCurrentWorkflowDBPath(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if path != "Workflow/demo/db/db.sqlite" {
		t.Fatalf("resolved path=%q", path)
	}
}

func TestWorkflowDBToolsDenyDisabledRelaySession(t *testing.T) {
	const session = "relay-platform-stores-none"
	common.SetSessionFolderGuard(session, []string{"Workflow/relay"}, []string{"Workflow/relay"})
	common.SetSessionShellEnv(session, map[string]string{workflowDBAccessEnv: "none", "DB_PATH": "Workflow/relay/db/db.sqlite"})
	t.Cleanup(func() { common.ClearSessionShellConfig(session) })
	registry := CreateWorkflowDBToolRegistry("http://127.0.0.1:1", "", session)
	for name, executor := range registry.Executors {
		_, err := executor(context.Background(), map[string]any{"sql": "SELECT 1"})
		if err == nil || !(strings.Contains(err.Error(), "disabled") || strings.Contains(err.Error(), "denied")) {
			t.Fatalf("%s did not refuse before HTTP: %v", name, err)
		}
	}
}
