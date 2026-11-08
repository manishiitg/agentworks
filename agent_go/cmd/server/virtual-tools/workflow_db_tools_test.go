package virtualtools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// TestParseManagedMigrationStatementsStripsLeadingComments reproduces a
// review finding on PLAT-221: a naturally-commented migration statement
// (a common human/agent authoring style) was rejected outright, because
// neither this parser nor the workspace service's own validator treats "--"
// as insignificant before the anchored "^\s*CREATE..." shape check.

// TestParseManagedMigrationStatementsStillRejectsDisallowedShapes proves the
// comment-stripping fix did not loosen the actual allow-list: a genuinely
// disallowed statement, commented or not, is still rejected.

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
	for _, name := range []string{"query_workflow_db", "mutate_workflow_db", "create_workflow_database_snapshot"} {
		if registry.Executors[name] == nil {
			t.Fatalf("missing executor %q", name)
		}
		if registry.Categories[name] != WorkflowDBToolCategory {
			t.Fatalf("category[%q]=%q", name, registry.Categories[name])
		}
	}
}

func TestWorkflowDBWorkspacePathRecognizesWorkProject(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", "/srv/workspace")
	for _, candidate := range []string{"Crew/demo", "Crew/demo/db/db.sqlite", "/srv/workspace/Crew/demo/db/migrations/example.sql"} {
		if got := workflowDBWorkspacePathFromCandidate(candidate); got != "Crew/demo" {
			t.Fatalf("shared Crew database context=%q for %q", got, candidate)
		}
	}
	for candidate, want := range map[string]string{
		"Workflow/demo/Crew/other/db/db.sqlite":                "Workflow/demo",
		"/srv/workspace/Workflow/demo/Crew/other/db/db.sqlite": "Workflow/demo",
		"reports/Crew/other/db/db.sqlite":                      "",
		"/untrusted/Crew/other/db/db.sqlite":                   "",
		"Crew/demo/code/Crew/other":                            "Crew/demo",
	} {
		if got := workflowDBWorkspacePathFromCandidate(candidate); got != want {
			t.Fatalf("database workspace=%q want=%q for %q", got, want, candidate)
		}
	}
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
	if err == nil || !strings.Contains(err.Error(), "not workflow steps") {
		t.Fatalf("step snapshot error=%v", err)
	}
}

func TestWorkflowDBMutationExecutorDeniesReadOnlySession(t *testing.T) {
	sessionID := "workflow-db-read-only"
	defer common.ClearSessionShellConfig(sessionID)
	// Folder access alone must not bypass this step/session's DB read setting.
	common.SetSessionFolderGuard(sessionID, []string{"Workflow/demo/db"}, []string{"Workflow/demo/db"})
	common.SetSessionShellEnv(sessionID, map[string]string{workflowDBAccessEnv: "read"})

	registry := CreateWorkflowDBToolRegistry("http://127.0.0.1:1", "", sessionID)
	definitions := []mcpagent.ToolDefinition{}
	for _, tool := range registry.Tools {
		raw, err := json.Marshal(tool.Function.Parameters)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		definitions = append(definitions, mcpagent.ToolDefinition{Name: tool.Function.Name, InputSchema: schema, Execute: registry.Executors[tool.Function.Name], DisplayGroup: WorkflowDBToolCategory})
	}
	projected, err := platformtools.Project(definitions)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range projected {
		if tool.Name != "database" {
			continue
		}
		for _, action := range []string{"mutate"} {
			_, err := tool.Execute(context.Background(), map[string]any{"action": action, "sql": "DELETE FROM facts"})
			if err == nil || !strings.Contains(err.Error(), "denied") {
				t.Fatalf("grouped %s bypassed DB read access: %v", action, err)
			}
		}
		return
	}
	t.Fatal("database job missing from the admitted registry")
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
