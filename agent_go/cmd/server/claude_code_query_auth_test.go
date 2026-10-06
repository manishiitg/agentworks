package server

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"os"
	"path/filepath"
	"testing"
)

// Regression for Vaibhav's Code on Excellence: drive real account registry and
// principal admission before the exact preflight used by handleQuery.
func TestClaudeCodeQueryAuthenticationUsesOnlyAdmittedAccount(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	t.Setenv("AGENT_PRODUCTS", "code,work")
	profile := &resolvedAgentProfile{Definition: agentprofiles.Profile{ID: "code"}}
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Private Claude", "auth_method": "cli_login"})
	scope := providerAccountScope{Principal: "alice", Product: "code"}
	record, err := env.api.admitProviderAccount(context.Background(), scope, "claude-code", account.ID)
	if err != nil {
		t.Fatal(err)
	}
	home, err := providerConnectionHome(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	privateFile := filepath.Join(home, ".claude", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(privateFile), 0700); err != nil {
		t.Fatal(err)
	}
	ambientFile := filepath.Join(env.home, ".claude", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(ambientFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ambientFile, []byte(`{"claudeAiOauth":{"accessToken":"server-dummy"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := claudeCodeQueryAuthenticationError(profile, "claude-code", record); err == nil {
		t.Fatal("empty private account used the ambient login")
	}
	if err := os.WriteFile(privateFile, []byte(`{"claudeAiOauth":{"accessToken":"private-dummy"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := claudeCodeQueryAuthenticationError(profile, "claude-code", record); err != nil {
		t.Fatalf("admitted private CLI login rejected: %v", err)
	}
	if err := claudeCodeQueryAuthenticationError(profile, "claude-code", nil); err == nil {
		t.Fatal("service login bypassed the Code token gate")
	}
	scope.Principal = "bob"
	if _, err := env.api.admitProviderAccount(context.Background(), scope, "claude-code", account.ID); err == nil {
		t.Fatal("another user admitted Alice's private account")
	}
	if err := os.Remove(privateFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ambientFile, privateFile); err != nil {
		t.Fatal(err)
	}
	if err := claudeCodeQueryAuthenticationError(profile, "claude-code", record); err == nil {
		t.Fatal("legacy link to the server login passed preflight")
	}
	tokenAccount := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Token", "auth_method": "oauth_token", "credential": "dummy-private-token"})
	scope.Principal = "alice"
	tokenRecord, err := env.api.admitProviderAccount(context.Background(), scope, "claude-code", tokenAccount.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := claudeCodeQueryAuthenticationError(profile, "claude-code", tokenRecord); err != nil {
		t.Fatalf("admitted private token rejected: %v", err)
	}
}
