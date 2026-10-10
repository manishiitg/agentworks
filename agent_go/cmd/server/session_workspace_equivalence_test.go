package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// The browser classifier in pkg/common mirrors the server canonicalizer so
// pkg/browser (which cannot import cmd/server) classifies sessions exactly
// like the live-discovery fix does. This test pins the two together: any
// drift fails here instead of in production.
func TestCanonicalSessionWorkspaceMatchesServer(t *testing.T) {
	paths := []string{
		"",
		"Workflow/trading",
		"Workflow/trading/code",
		"Workflow",
		"Chats/Work/projects/customer-qa-480b6936",
		"Chats/Work/projects/demo/db/reports",
		"Chats/Work/projects/",
		"Chats/Work",
		"Chats",
		"_users/u1/Chats/Work/projects/demo",
		"_users/u2/Chats/Work/projects/demo",
		"_users/default/Chats/Work/projects/demo",
		"../Workflow/x",
		"/Workflow/trading/",
		"Downloads/x",
		"Workflow//double",
	}
	for _, userID := range []string{"", "u1", "2a0aea4e635d33c3b8dcec39f2fb84bc", "not a user!!"} {
		for _, path := range paths {
			want := canonicalChatHistoryWorkspacePath(userID, path)
			if got := common.CanonicalSessionWorkspace(userID, path); got != want {
				t.Fatalf("user %q path %q: common=%q server=%q", userID, path, got, want)
			}
			wantCrew := isActiveWorkProjectWorkspace(userID, path)
			kind, _ := common.ClassifySessionWorkspace(userID, path)
			if (kind == common.SessionWorkspaceCrewProject) != wantCrew {
				t.Fatalf("user %q path %q: classifier crew=%v server=%v", userID, path, kind == common.SessionWorkspaceCrewProject, wantCrew)
			}
		}
	}
}
