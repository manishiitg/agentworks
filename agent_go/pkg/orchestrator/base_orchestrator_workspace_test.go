package orchestrator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// Regression for the server A PR Reviewer webhook failure: context_dependencies
// resolved route_selection.json to an absolute run path. The deterministic
// branch then used the direct BaseOrchestrator workspace reader, bypassing the
// agent-tool normalization wrapper, and Folder Guard rejected the otherwise
// valid file because its grant was workspace-relative.
func TestResolveWorkspacePathNormalizesAbsoluteRunDependency(t *testing.T) {
	docsRoot := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)

	bo := &BaseOrchestrator{workspacePath: "Workflow/rtsprreviweer", logger: loggerv2.NewDefault()}
	absolute := filepath.Join(
		docsRoot,
		"Workflow/rtsprreviweer/runs/iteration-42-hook/default/execution/pr-eligibility-gate/route_selection.json",
	)

	want := "Workflow/rtsprreviweer/runs/iteration-42-hook/default/execution/pr-eligibility-gate/route_selection.json"
	if got := filepath.ToSlash(bo.resolveWorkspacePath(absolute)); got != want {
		t.Fatalf("resolveWorkspacePath(%q) = %q, want %q", absolute, got, want)
	}
}

func TestResolveWorkspacePathLeavesOutsideAbsolutePathFailClosed(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	bo := &BaseOrchestrator{workspacePath: "Workflow/rtsprreviweer", logger: loggerv2.NewDefault()}

	const outside = "/etc/passwd"
	if got := bo.resolveWorkspacePath(outside); got != outside {
		t.Fatalf("resolveWorkspacePath(%q) = %q, want unchanged outside path", outside, got)
	}
}

func TestReadWorkspaceFileAcceptsExistingEmptyFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"filepath":"empty.txt","content":"","size":0}}`))
	}))
	defer server.Close()

	bo := &BaseOrchestrator{WorkspaceClient: workspace.NewClient(server.URL)}
	content, err := bo.ReadWorkspaceFile(context.Background(), "empty.txt")
	if err != nil {
		t.Fatalf("empty file should be a successful read: %v", err)
	}
	if content != "" {
		t.Fatalf("expected empty content, got %q", content)
	}
}

func TestSetWorkspaceEnvRefBackfillsSecretsLoadedBeforeExecutor(t *testing.T) {
	bo := &BaseOrchestrator{logger: loggerv2.NewDefault()}
	bo.SetSecrets([]SecretEntry{
		{Name: "USERNAME", Value: "account@example.test"},
		{Name: "PASSWORD", Value: "private-value"},
	})

	env := map[string]string{"MCP_API_URL": "http://example.test/s/session-1"}
	bo.SetWorkspaceEnvRef(env)

	if got := env["SECRET_USERNAME"]; got != "account@example.test" {
		t.Fatalf("SECRET_USERNAME = %q, want attached workflow secret", got)
	}
	if got := env["SECRET_PASSWORD"]; got != "private-value" {
		t.Fatalf("SECRET_PASSWORD = %q, want attached workflow secret", got)
	}
}
