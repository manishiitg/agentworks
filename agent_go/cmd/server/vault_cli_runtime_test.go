package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
)

func TestVaultCLIRuntimeIsolatesProviderConfigAndKeepsProject(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	project := filepath.Join(docs, caplayerproduct.WorkspaceRoot)
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(project, "AGENTS.md")
	if err := os.WriteFile(marker, []byte("project guidance"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(root, "state"))
	for _, provider := range []string{"muse-cli", "claude-code", "codex-cli", "cursor-cli", "pi-cli"} {
		dir, err := linkedProjectCLIWorkingDir(caplayerproduct.WorkspaceRoot, "owner", "chat", provider, "vault")
		if err != nil {
			t.Fatal(err)
		}
		if rel, _ := filepath.Rel(docs, dir); !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatal("runtime leaked into workspace")
		}
		target, err := filepath.EvalSymlinks(filepath.Join(dir, "project"))
		canonical, _ := filepath.EvalSymlinks(project)
		if err != nil || target != canonical {
			t.Fatalf("wrong project: %s %v", target, err)
		}
		again, err := linkedProjectCLIWorkingDir(caplayerproduct.WorkspaceRoot, "owner", "chat", provider, "vault")
		if err != nil || again != dir {
			t.Fatal("runtime not stable after restart")
		}
		other, err := linkedProjectCLIWorkingDir(caplayerproduct.WorkspaceRoot, "other", "chat", provider, "vault")
		if err != nil || other == dir {
			t.Fatal("users share provider configuration")
		}
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "project guidance" {
		t.Fatal("project guidance was overwritten")
	}
	t.Setenv("AGENTWORKS_STATE_ROOT", docs)
	if _, err := linkedProjectCLIWorkingDir(caplayerproduct.WorkspaceRoot, "owner", "chat", "muse-cli", "vault"); err == nil {
		t.Fatal("allowed runtime inside workspace")
	}
}
