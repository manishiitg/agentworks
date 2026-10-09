package virtualtools

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// 2026-09-23: attaching salesoutreach's knowledgebase to the websiteaeo builder
// made every query_workflow_db call in that session fail as "ambiguous".
func TestResolveWorkflowWorkspaceFolderIgnoresAttachedKnowledgebaseSources(t *testing.T) {
	cfg := &common.SessionShellConfig{
		ReadPaths: []string{
			"Workflow/websiteaeo", "Chats", "skills",
			"/srv/workspace-docs/Workflow/salesoutreach/knowledgebase",
		},
		WritePaths: []string{"Workflow/websiteaeo", "/srv/workspace-docs/Workflow/salesoutreach/knowledgebase"},
	}
	got, err := resolveWorkflowWorkspaceFolder("s", cfg)
	if err != nil || got != "Workflow/websiteaeo" {
		t.Fatalf("resolveWorkflowWorkspaceFolder() = %q, %v; want Workflow/websiteaeo", got, err)
	}
}

// A Crew or workflow chat with another Crew project or workflow attached as
// context gets that whole root as a read grant; its own writable root decides.
func TestResolveWorkflowWorkspaceFolderPrefersOwnRootOverAttachedContext(t *testing.T) {
	got, err := resolveWorkflowWorkspaceFolder("s", &common.SessionShellConfig{
		ReadPaths:  []string{"_users/u1/Chats/Work/projects/crew-a", "_users/u1/Chats/Work/projects/crew-b", "Workflow/linked"},
		WritePaths: []string{"_users/u1/Chats/Work/projects/crew-a"},
	})
	if err != nil || got != "Chats/Work/projects/crew-a" {
		t.Fatalf("crew with attachments = %q, %v; want Chats/Work/projects/crew-a", got, err)
	}
}

func TestResolveWorkflowWorkspaceFolderKnowledgebaseOnlyAndRealConflicts(t *testing.T) {
	// A session granted only a knowledgebase still resolves to that workflow.
	got, err := resolveWorkflowWorkspaceFolder("s", &common.SessionShellConfig{
		ReadPaths: []string{"Workflow/alpha/knowledgebase"},
	})
	if err != nil || got != "Workflow/alpha" {
		t.Fatalf("kb-only = %q, %v; want Workflow/alpha", got, err)
	}
	// Two genuine workflow grants remain ambiguous.
	_, err = resolveWorkflowWorkspaceFolder("s", &common.SessionShellConfig{
		ReadPaths: []string{"Workflow/alpha", "Workflow/beta/db"},
	})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("two workflows err = %v, want ambiguous", err)
	}
}

// server A SDE crew: attached Crews are writable, so WritePaths name several Crew
// projects; the session's own working dir must still pick its database.
func TestResolveWorkflowWorkspaceFolderWorkingDirWinsOverWritableAttachedCrews(t *testing.T) {
	got, err := resolveWorkflowWorkspaceFolder("s", &common.SessionShellConfig{
		WorkingDir: "/data/docs/_users/u/Chats/Work/projects/gptlive1-cef0edb2",
		WritePaths: []string{"_users/u/Chats/Work/projects/gptlive1-cef0edb2/", "_users/u/Chats/Work/projects/rts-flow-tester-5090fe7e", "_users/u/Chats/Work/projects/new-project-2271585c"},
		ReadPaths:  []string{"Workflow/rtsprreviweer"},
	})
	if err != nil || got != "Chats/Work/projects/gptlive1-cef0edb2" {
		t.Fatalf("got %q, %v; want the session's own Crew project", got, err)
	}
}

func TestWorkflowDBWriteDenialTellsTheCallerItIsAPlatformProblem(t *testing.T) {
	msg := errWorkflowDBWriteGrantMissing("mutation", "session-group-x-1", "").Error()
	for _, want := range []string{
		"explicit db_access=read-write is required", // the original text stays for anything matching it
		"platform permission problem",
		"report it as a platform issue",
		`"session-group-x-1"`,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("denial message missing %q: %s", want, msg)
		}
	}
}
