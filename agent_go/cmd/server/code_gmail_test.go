package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// Managing a Code's Google accounts is its owner's alone, and the agent's
// Gmail tools in a Code act on that Code's accounts as its owner.
func TestCodeGmailScopeIsOwnerOnly(t *testing.T) {
	request := func(userID string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		return req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: userID, Username: userID}))
	}
	scope, err := gmailRequestScope(request("alice"), "_users/alice/Chats/Code/projects/app-1")
	if err != nil || scope.CodeWorkspace != "_users/alice/Chats/Code/projects/app-1" || scope.UserID != "alice" {
		t.Fatalf("owner scope = %+v %v", scope, err)
	}
	if scope, err := gmailRequestScope(request("alice"), "Chats/Code/projects/app-1/code"); err != nil || scope.CodeWorkspace != "_users/alice/Chats/Code/projects/app-1" {
		t.Fatalf("logical path scope = %+v %v", scope, err)
	}
	if _, err := gmailRequestScope(request("bob"), "_users/alice/Chats/Code/projects/app-1"); err == nil {
		t.Fatal("another user managed a Code's Google accounts")
	}
	if scope, err := gmailRequestScope(request("alice"), ""); err != nil || scope.CodeWorkspace != "" {
		t.Fatalf("shared scope = %+v %v", scope, err)
	}

	if got := gmailToolScope("_users/alice/Chats/Code/projects/app-1"); got.CodeWorkspace != "_users/alice/Chats/Code/projects/app-1" || got.UserID != "alice" {
		t.Fatalf("Code tool scope = %+v", got)
	}
	if got := gmailToolScope("_users/alice/Chats/Work/projects/crew-1"); got.CodeWorkspace != "" {
		t.Fatalf("a Crew got a Code scope: %+v", got)
	}
	if root := common.CodeProjectRoot("alice", "Workflow/x"); root != "" {
		t.Fatalf("a workflow classified as a Code: %q", root)
	}
}
