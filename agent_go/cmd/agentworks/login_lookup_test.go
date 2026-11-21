package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// #3373: `agentworks agents` built its list from attached chats, one per login
// file. It printed a server twice when two login files reach it, dropped a
// server whose listing failed, and left out logins with no attached chat.
func TestAgentDirectoriesListEachSavedServerOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APPDATA", home)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	listed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"agents":[{"name":"helper","id":"agent:helper"}]}`))
	}))
	defer listed.Close()
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"sign in again"}}`))
	}))
	defer refused.Close()
	for name, server := range map[string]string{"default": listed.URL, "work": listed.URL, "other": refused.URL} {
		path, err := agentworksclient.LoginPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err = agentworksclient.SaveConfig(path, agentworksclient.Config{Server: server, Token: "fixture-" + name}); err != nil {
			t.Fatal(err)
		}
	}
	chatOnly := agentworksclient.LocalAgentDirectory{Server: "https://chat-only.example.com", Agents: []byte(`{"agents":[]}`)}
	dirs := agentDirectories(context.Background(), "", []agentworksclient.LocalAgentDirectory{chatOnly})
	seen := map[string]agentworksclient.LocalAgentDirectory{}
	for _, d := range dirs {
		if _, dup := seen[d.Server]; dup {
			t.Fatalf("server listed twice: %s", d.Server)
		}
		seen[d.Server] = d
	}
	want := func(raw string) string {
		server, err := agentworksclient.ValidateServer(raw)
		if err != nil {
			t.Fatal(err)
		}
		return server
	}
	if d, ok := seen[want(listed.URL)]; !ok || d.Error != "" || len(d.Agents) == 0 {
		t.Fatalf("saved server with no attached chat missing or empty: %+v", d)
	}
	if d, ok := seen[want(refused.URL)]; !ok || d.Error == "" {
		t.Fatalf("a server that refused the listing must keep its section with the reason: %+v", d)
	}
	if _, ok := seen[want(chatOnly.Server)]; !ok {
		t.Fatal("a server only an attached chat reaches was dropped")
	}
}
