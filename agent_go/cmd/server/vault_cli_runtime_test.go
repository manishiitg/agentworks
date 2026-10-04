package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
)

func TestVaultChatCreatesMissingUserWorkspaceBeforeCLILaunch(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(root, "state"))
	t.Setenv("MULTI_USER_MODE", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			FolderPath string `json:"folder_path"`
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/folders" || json.NewDecoder(r.Body).Decode(&body) != nil || body.FolderPath != caplayerproduct.WorkspaceRoot {
			t.Error("unexpected Vault workspace creation request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		user := r.Header.Get("X-User-ID")
		if user != "alice" && user != "bob" {
			t.Error("workspace creation lost the authenticated user")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		project := codingAgentWorkspaceWorkingDir(agentProfileRuntimeWorkspace(user, body.FolderPath))
		if _, err := os.Stat(project); err == nil {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if err := os.MkdirAll(project, 0755); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	profile := caplayerproduct.BuiltinAgentProfile()
	for _, user := range []string{"alice", "bob"} {
		folder := agentProfileRuntimeWorkspace(user, caplayerproduct.WorkspaceRoot)
		if _, err := linkedProjectCLIWorkingDir(folder, user, "chat", "agy-cli", "vault"); err == nil {
			t.Fatal("test must start with the missing-workspace failure")
		}
		if err := initializeProductConversationWorkspace(context.Background(), user, profile, productConversationBinding{WorkspacePath: caplayerproduct.WorkspaceRoot}); err != nil {
			t.Fatal(err)
		}
		// An existing tab also ensures the workspace on its next turn. A 409
		// must preserve the existing folder and its durable data.
		marker := filepath.Join(codingAgentWorkspaceWorkingDir(folder), "note.txt")
		if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := ensureVaultChatWorkspace(context.Background(), user); err != nil {
			t.Fatal(err)
		}
		dir, err := linkedProjectCLIWorkingDir(folder, user, "chat", "agy-cli", "vault")
		if err != nil {
			t.Fatal(err)
		}
		target, err := filepath.EvalSymlinks(filepath.Join(dir, "project"))
		want, _ := filepath.EvalSymlinks(codingAgentWorkspaceWorkingDir(folder))
		if err != nil || target != want {
			t.Fatalf("wrong user's workspace: %s %v", target, err)
		}
		if data, err := os.ReadFile(marker); err != nil || string(data) != "keep" {
			t.Fatal("workspace initialization changed existing data")
		}
	}
}

func TestVaultChatWorkspaceCreationFailureIsReturned(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	if err := ensureVaultChatWorkspace(context.Background(), "alice"); err == nil || !strings.Contains(err.Error(), "initialize Vault chat workspace") {
		t.Fatalf("workspace failure was ignored: %v", err)
	}
}

func TestVaultChatHasDefaultModelAndNativeBuilderTools(t *testing.T) {
	profile := caplayerproduct.BuiltinAgentProfile()
	provider, model := resolveProfileRuntimeModel(profile.Runtime, "", "")
	if provider == "" || model == "" || profile.Runtime.AgentTools.Mode != "full" {
		t.Fatalf("Vault is missing its default builder configuration: %s / %s", provider, model)
	}
}

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
