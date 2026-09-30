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

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

func TestGmailAccountRemovalOwnership(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"alice","username":"alice","email":"alice@example.com"},
		{"id":"bob","username":"bob","email":"bob@example.com"},
		{"id":"boss","username":"boss","email":"boss@example.com","admin":true},
		{"id":"disabled","username":"disabled","email":"disabled@example.com","disabled":true}
	]}`)
	for _, tc := range []struct {
		name       string
		userID     string
		tokenEmail string
		conn       services.GmailConnection
		want       bool
	}{
		{"legacy own mailbox", "alice", "", services.GmailConnection{Email: "alice@example.com"}, true},
		{"legacy email normalized", "alice", "", services.GmailConnection{Email: " ALICE@example.COM "}, true},
		{"another mailbox", "alice", "", services.GmailConnection{Email: "bob@example.com"}, false},
		{"display name is not ownership", "alice", "", services.GmailConnection{DisplayName: "alice@example.com"}, false},
		{"missing mailbox email", "alice", "", services.GmailConnection{}, false},
		{"current directory beats stale claim", "alice", "bob@example.com", services.GmailConnection{Email: "bob@example.com"}, false},
		{"stale claim does not hide current owner", "alice", "old@example.com", services.GmailConnection{Email: "alice@example.com"}, true},
		{"unknown identity cannot use claim email", "missing", "alice@example.com", services.GmailConnection{Email: "alice@example.com"}, false},
		{"signed out", "", "", services.GmailConnection{Email: "alice@example.com"}, false},
		{"disabled account", "disabled", "", services.GmailConnection{Email: "disabled@example.com"}, false},
		{"recorded owner", "alice", "", services.GmailConnection{OwnerID: "alice", Email: "external@example.com"}, true},
		{"recorded owner overrides email", "alice", "", services.GmailConnection{OwnerID: "bob", Email: "alice@example.com"}, false},
		{"disabled recorded owner", "disabled", "", services.GmailConnection{OwnerID: "disabled"}, false},
		{"shared admin", "boss", "", services.GmailConnection{Email: "alice@example.com"}, true},
		{"shared admin unknown email", "boss", "", services.GmailConnection{}, true},
		{"private owner", "alice", "", services.GmailConnection{ScopeWorkspace: "code", OwnerID: "alice"}, true},
		{"private email is insufficient", "alice", "", services.GmailConnection{ScopeWorkspace: "code", OwnerID: "bob", Email: "alice@example.com"}, false},
		{"private admin is not owner", "boss", "", services.GmailConnection{ScopeWorkspace: "code", OwnerID: "alice"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, "/x", nil)
			if tc.userID != "" {
				req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: tc.userID, Username: tc.userID, Email: tc.tokenEmail}))
			}
			if got := canRemoveGmailConnection(req, tc.conn); got != tc.want {
				t.Fatalf("removal allowed = %v, want %v", got, tc.want)
			}
		})
	}
	req := httptest.NewRequest(http.MethodDelete, "/x", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "alice", Provider: "bot_route"}))
	if canRemoveGmailConnection(req, services.GmailConnection{Email: "alice@example.com", OwnerID: "alice"}) {
		t.Fatal("a bot identity disconnected a human account")
	}
}

func TestLocalOwnerCanRemoveSharedGmail(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	withMemoryUserDirectory(t, `{"users":[]}`)
	if !canRemoveGmailConnection(httptest.NewRequest(http.MethodDelete, "/x", nil), services.GmailConnection{}) {
		t.Fatal("local installation owner cannot remove a shared account")
	}
}

// Exercise the public routes with a pre-ownership registry, including gog
// credential removal. Other accounts and shared settings must survive intact.
func TestLegacyGmailOwnerCanDisconnectOnlyOwnAccount(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","email":"alice@example.com"},{"id":"boss","username":"boss","admin":true}]}`)
	dir := t.TempDir()
	t.Setenv("GMAIL_CONNECTIONS_DIR", filepath.Join(dir, "connections"))
	t.Setenv("GMAIL_OAUTH_TOKEN_DIR", filepath.Join(dir, "tokens"))
	t.Setenv("GOG_HOME", filepath.Join(dir, "gog"))
	binary := filepath.Join(dir, "gog")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config := services.GmailConfig{GogPath: binary, GwsPath: binary, DefaultConnectionID: "other", Connections: []services.GmailConnection{
		{ID: "own", Email: "alice@example.com", AuthBackend: "gog", ClientName: "app", Enabled: true},
		{ID: "other", Email: "bob@example.com", AuthBackend: "gog", ClientName: "app", Enabled: true},
		{ID: "private", ScopeWorkspace: "_users/bob/Chats/Code/projects/app-1", OwnerID: "bob", Email: "alice@example.com", Enabled: true},
	}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{"config/gmail-config.json": string(data)}}
	workspace := httptest.NewServer(mock)
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	previous := services.GetGmailService()
	t.Cleanup(func() { services.SetGmailService(previous) })
	svc, err := services.InitGmailService()
	if err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	GmailConnectionRoutes(router, nil)
	GmailOAuthClientRoutes(router, nil)
	call := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/human-feedback/gmail/"+path, strings.NewReader(`{"email":"alice@example.com"}`))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "alice", Username: "alice"}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	for id, want := range map[string]bool{"own": true, "other": false} {
		rec := call(http.MethodGet, "connections/"+id)
		var out GmailConnectionResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.CanRemove != want {
			t.Fatalf("GET %s = %d %s, want can_remove=%v", id, rec.Code, rec.Body.String(), want)
		}
	}
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{http.MethodPatch, "connections/own", http.StatusForbidden},
		{http.MethodPost, "connections/own/default", http.StatusForbidden},
		{http.MethodPost, "connections/own/test", http.StatusForbidden},
		{http.MethodDelete, "oauth-clients/app", http.StatusForbidden},
		{http.MethodDelete, "connections/other", http.StatusForbidden},
		{http.MethodDelete, "connections/private", http.StatusNotFound},
		{http.MethodDelete, "connections/own", http.StatusOK},
		{http.MethodDelete, "connections/own", http.StatusNotFound},
	} {
		if rec := call(tc.method, tc.path); rec.Code != tc.code {
			t.Fatalf("%s %s = %d %s, want %d", tc.method, tc.path, rec.Code, rec.Body.String(), tc.code)
		}
	}
	if _, exists := svc.GetConnection("own"); exists {
		t.Fatal("own account still exists")
	}
	if _, exists := svc.GetConnection("other"); !exists {
		t.Fatal("another user's account removed")
	}
	if _, exists := svc.GetConnection("private"); !exists {
		t.Fatal("another Code's account removed")
	}
	if svc.GetConfig().DefaultConnectionID != "other" {
		t.Fatal("another account's default changed")
	}
	mock.mu.Lock()
	persisted := mock.files["config/gmail-config.json"]
	mock.mu.Unlock()
	var saved services.GmailConfig
	if err := json.Unmarshal([]byte(persisted), &saved); err != nil || len(saved.Connections) != 2 {
		t.Fatalf("removal not persisted: %v %s", err, persisted)
	}
}
