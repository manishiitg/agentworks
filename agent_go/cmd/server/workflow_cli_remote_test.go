package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/cliruntime"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/remoteplacement"
)

func TestWorkflowCLIWorkingDirRemoteNeedsNoProjectLink(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	if err := os.MkdirAll(docs, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(root, "state"))
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	placement := filepath.Join(root, "placements.json")
	t.Setenv(remoteplacement.FileEnv, placement)
	if err := os.WriteFile(placement, []byte(`{"workflows":{"Workflow/remote":"team"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := workflowCLIWorkingDir("Workflow/remote", "owner", "chat-a", "codex-cli", "workshop")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("private runtime missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, cliruntime.ProjectLink)); !os.IsNotExist(err) {
		t.Fatalf("remote runtime must not link a local project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(docs, "Workflow/remote")); !os.IsNotExist(err) {
		t.Fatalf("remote workflow was recreated locally: %v", err)
	}
	instructions := workflowCLIWorkspaceInstructions("Workflow/remote")
	if !strings.Contains(instructions, "remote workspace server") || strings.Contains(instructions, "`project/` links") {
		t.Fatalf("incorrect remote instructions: %s", instructions)
	}
	again, err := workflowCLIWorkingDir("Workflow/remote", "owner", "chat-a", "codex-cli", "workshop")
	if err != nil || again != dir {
		t.Fatalf("private runtime changed after restart: %q %v", again, err)
	}
}
