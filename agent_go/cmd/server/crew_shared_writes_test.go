package server

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	llmtypes "github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// 856-59: a shared caller's turn writes the Crew's working files except the files that define the Crew.
func TestCrewSharedProtectedPathsCoverTheDefiningFiles(t *testing.T) {
	got := crewSharedProtectedPaths("_users/owner/Chats/Work/projects/alpha/")
	for _, want := range []string{
		"_users/owner/Chats/Work/projects/alpha/MEMORY.md",
		"_users/owner/Chats/Work/projects/alpha/CLAUDE.md",
		"_users/owner/Chats/Work/projects/alpha/AGENTS.md",
		"_users/owner/Chats/Work/projects/alpha/functions.json",
		"_users/owner/Chats/Work/projects/alpha/workflow.json",
		"_users/owner/Chats/Work/projects/alpha/product.json",
		"_users/owner/Chats/Work/projects/alpha/skills/",
		"_users/owner/Chats/Work/projects/alpha/.claude/",
		"_users/owner/Chats/Work/projects/alpha/builder/",
	} {
		if !slices.Contains(got, want) {
			t.Fatalf("protected paths are missing %q: %v", want, got)
		}
	}
	for _, path := range got {
		if strings.Contains(path, "outputs") || strings.Contains(path, "code") || strings.Contains(path, "db") {
			t.Fatalf("working files must stay writable, got %q", path)
		}
	}
}

func TestCrewSharedWritesKeepSetupToolsDenied(t *testing.T) {
	reader := crewReaderDeniedToolsFor(false)
	shared := crewReaderDeniedToolsFor(true)
	for _, generic := range []string{"diff_patch_workspace_file", "image_gen", "image_edit"} {
		if !slices.Contains(reader, generic) {
			t.Fatalf("an ordinary reader must still be denied %q", generic)
		}
		if slices.Contains(shared, generic) {
			t.Fatalf("a shared caller's turn must be allowed %q (the folder guard protects the paths)", generic)
		}
	}
	for _, setup := range []string{"create_project_schedule", "update_project_skill_selection", "update_project_mcp_server_selection", "update_project_global_secret_selection", "set_work_identity", "set_workflow_secret", "install_mcp_server", "attach_workflow_reference"} {
		if !slices.Contains(shared, setup) {
			t.Fatalf("a shared caller's turn must stay denied %q", setup)
		}
	}
}

func TestReaderDeniedToolRefusalAllowsGenericFileToolsForSharedWrites(t *testing.T) {
	const sid = "shared-writes-refusal-session"
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sid)
	t.Cleanup(func() { common.ClearSessionShellConfig(sid) })
	common.SetSessionWorkflowReadOnly(sid, true)
	common.SetSessionCrewReader(sid, true)
	if msg := readerDeniedToolRefusal(ctx, "diff_patch_workspace_file"); msg == "" {
		t.Fatal("a reader chat must still be refused the file patch tool")
	}
	common.SetSessionCrewSharedWrites(sid, true)
	if msg := readerDeniedToolRefusal(ctx, "diff_patch_workspace_file"); msg != "" {
		t.Fatalf("a shared caller's turn was refused the file patch tool: %q", msg)
	}
	if msg := readerDeniedToolRefusal(ctx, "update_project_skill_selection"); msg == "" {
		t.Fatal("a shared caller's turn must stay refused a setup tool")
	}
}

func TestSharedCallerNoticeTellsWhatIsWritable(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"aman","can_create":true}]}`)
	notice := crewSessionNoticeBlock("_users/owner/Chats/Work/projects/alpha", "_users/owner/Chats/Work/projects/alpha/outputs", false, true)
	for _, want := range []string{"shared user", "working files", "MEMORY.md", "skills", "read-only to you", "outputs"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("shared caller notice missing %q:\n%s", want, notice)
		}
	}
	if strings.Contains(notice, "only place you can write") {
		t.Fatalf("a shared caller may write the working files:\n%s", notice)
	}
	reader := crewSessionNoticeBlock("_users/owner/Chats/Work/projects/alpha", "_users/owner/Chats/Work/projects/alpha/outputs", false, false)
	if !strings.Contains(reader, "only place you can write") {
		t.Fatalf("an ordinary reader still writes only the output folder:\n%s", reader)
	}
}

// The kernel policy of a shared caller's turn keeps the project writable and carries the protected paths as blocked writes.
func TestSharedCallerLandlockPolicyWritesTheProjectExceptProtectedPaths(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	session := "crew-policy-shared-writes"
	project := codingAgentWorkspaceWorkingDir("Crew/support")
	runtime := filepath.Join(t.TempDir(), "runtime")
	common.SetSessionFolderGuard(session, []string{"Crew/support"}, []string{"Crew/support"})
	common.SetSessionFolderGuardBlockedWritePaths(session, crewSharedProtectedPaths("Crew/support"))
	common.SetSessionCrewReader(session, true)
	common.SetSessionWorkflowReadOnly(session, true)
	common.SetSessionCrewSharedWrites(session, true)
	defer common.ClearSessionShellConfig(session)
	base := &llmtypes.CLISecurityPolicy{WorkspaceWritePaths: []string{project}}
	policy := cliLandlockPolicyForSession(session, "codex-cli", runtime, base)
	if !slices.Contains(policy.WorkspaceWritePaths, project) {
		t.Fatalf("a shared caller's turn lost the project write: %+v", policy)
	}
	for _, name := range []string{"MEMORY.md", "functions.json", "workflow.json", "skills"} {
		want := cliPolicyPath("Crew/support/" + name)
		if want == "" || !slices.Contains(policy.BlockedWritePaths, want) {
			t.Fatalf("%s is not a blocked write path: want %q in %v", name, want, policy.BlockedWritePaths)
		}
	}
	// An ordinary reader (same flags without the shared-writes mark) still cannot write the project.
	common.SetSessionCrewSharedWrites(session, false)
	reader := cliLandlockPolicyForSession(session, "codex-cli", runtime, base)
	if slices.Contains(reader.WorkspaceWritePaths, project) {
		t.Fatalf("an ordinary reader can write the linked project: %+v", reader)
	}
}
