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

// Shared Google accounts are the organisation's: a non-admin cannot change,
// test, re-authorize, delete or default them, or replace OAuth clients.
func TestSharedGmailWritesNeedAnAdmin(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"boss","username":"boss","admin":true},{"id":"member","username":"member","can_create":true}]}`)
	reached := false
	next := func(w http.ResponseWriter, r *http.Request) { reached = true }
	call := func(gate func(http.HandlerFunc) http.HandlerFunc, userID string) int {
		reached = false
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: userID, Username: userID}))
		rec := httptest.NewRecorder()
		gate(next)(rec, req)
		return rec.Code
	}
	for name, gate := range map[string]func(http.HandlerFunc) http.HandlerFunc{
		"connection manager": requireGmailConnectionManager,
		"settings write":     requireAdminWrite,
	} {
		if code := call(gate, "member"); code != http.StatusForbidden || reached {
			t.Fatalf("%s: a member got through (%d)", name, code)
		}
		if call(gate, "boss"); !reached {
			t.Fatalf("%s: an admin was refused", name)
		}
	}
}

// config_home and credentials_file are host paths: aimed at a shared
// account's gws dir or key file they read and send as that account. A Code's
// private account never takes them (not even from an admin); a shared one
// only from an admin.
func TestGmailCredentialPathsNeedAnAdminAndNeverAPrivateAccount(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"boss","username":"boss","admin":true},{"id":"alice","username":"alice","can_create":true}]}`)
	call := func(userID string, private bool, req GmailConnectionRequest) int {
		r := httptest.NewRequest(http.MethodPost, "/x", nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: userID, Username: userID}))
		rec := httptest.NewRecorder()
		if gmailCredentialPathsAllowed(rec, r, private, req) {
			return http.StatusOK
		}
		return rec.Code
	}
	for _, req := range []GmailConnectionRequest{{ConfigHome: "/srv/gmail/connections/gmail_001"}, {CredentialsFile: "/srv/keys/org.json"}} {
		if code := call("alice", true, req); code != http.StatusForbidden {
			t.Fatalf("a Code owner pinned %+v on a private account (%d)", req, code)
		}
		if code := call("boss", true, req); code != http.StatusForbidden {
			t.Fatalf("an admin pinned %+v on a private account (%d)", req, code)
		}
		if code := call("alice", false, req); code != http.StatusForbidden {
			t.Fatalf("a member pinned %+v on a shared account (%d)", req, code)
		}
		if code := call("boss", false, req); code != http.StatusOK {
			t.Fatalf("an admin was refused %+v on a shared account (%d)", req, code)
		}
	}
	if code := call("alice", true, GmailConnectionRequest{DisplayName: "mine"}); code != http.StatusOK {
		t.Fatalf("a plain private create was refused (%d)", code)
	}
}
