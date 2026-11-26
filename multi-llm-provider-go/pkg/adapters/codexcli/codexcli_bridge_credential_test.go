package codexcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-941: per-build/hourly host bridge auth must not change a kept
// app-server's actual env. A real account change still replaces that process.
func TestCodexProcessBridgeCredentialReusesAndReleasesExactGeneration(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CODEX_HOME", t.TempDir())
	const owner = "credential-regression"
	priorStart, priorFactory := startCodexAppServer, AppBridgeCredentialForOwner
	t.Cleanup(func() {
		closeCodexAppSession(owner)
		startCodexAppServer = priorStart
		AppBridgeCredentialForOwner = priorFactory
	})
	generation, closed, launches := 0, 0, 0
	var current *AppBridgeCredential
	AppBridgeCredentialForOwner = func(o, scope string) *AppBridgeCredential {
		if o != owner || scope != owner+":vt:host-main" {
			return nil
		}
		if current == nil {
			generation++
			entry := &AppBridgeCredential{Token: fmt.Sprintf("fixture-generation-%d", generation)}
			active := false
			entry.Started = func() { active = true }
			entry.Release = func() {
				if current == entry {
					current = nil
					closed++
				}
			}
			entry.ReleasePending = func() {
				if !active {
					entry.Release()
				}
			}
			current = entry
		}
		return current
	}
	var loads []map[string]any
	var originalRelease func()
	startCodexAppServer = func(_ *CodexCLIAdapter, _ context.Context, options *llmtypes.CallOptions, _ string) (*codexRPC, error) {
		launches++
		lease := appBridgeCredential(owner, options)
		if lease == nil {
			t.Fatal("host lease not passed to process")
		}
		env := llmtypes.CodingAgentSecretEnvironmentFromOptions(options)
		if env["MCP_API_TOKEN"] != lease.Token || env["MCP_AUTH"] != "Authorization: Bearer "+lease.Token {
			t.Fatal("process got stale shell bridge credential")
		}
		r := &codexRPC{pending: map[string]chan codexRPCMessage{}, done: make(chan struct{}), exited: make(chan struct{}), stop: func() {}, releaseBridgeCredential: lease.Release}
		close(r.exited)
		r.stdin = &warmThreadWriter{t: t, rpc: r, loads: &loads}
		lease.Started()
		if originalRelease == nil {
			originalRelease = lease.Release
		}
		return r, nil
	}
	options := func(hour string) *llmtypes.CallOptions {
		o := &llmtypes.CallOptions{}
		raw, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"api-bridge": map[string]any{"command": "mcpbridge", "env": map[string]string{"MCP_SESSION_ID": owner, "MCP_VIRTUAL_SCOPE_ID": owner + ":vt:host-main", "MCP_API_TOKEN": hour}}}})
		for _, opt := range []llmtypes.CallOption{WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true), WithProjectDirID(dir), llmtypes.WithCodingProviderLaunchOnly(), WithMCPServers(string(raw)), llmtypes.WithCodingAgentSecretEnvironment(map[string]string{"MCP_SESSION_ID": owner, "MCP_API_TOKEN": hour, "MCP_AUTH": "Authorization: Bearer " + hour})} {
			opt(o)
		}
		return o
	}
	adapter := NewCodexCLIAdapter("account-one", "test", &MockLogger{})
	run := func(o *llmtypes.CallOptions) {
		t.Helper()
		if _, err := adapter.generateContentAppServer(context.Background(), nil, o); err != nil {
			t.Fatal(err)
		}
	}
	first := options("hour-one")
	run(first)
	run(options("hour-two"))
	if launches != 1 || generation != 1 || len(loads) != 1 {
		t.Fatal("volatile bridge env replaced process or thread")
	}
	// Rewriting is private to this invocation, not a mutation of host options.
	if llmtypes.CodingAgentSecretEnvironmentFromOptions(first)["MCP_API_TOKEN"] != "hour-one" {
		t.Fatal("host options mutated")
	}
	if appBridgeCredential(owner, func() *llmtypes.CallOptions {
		o := options("hour-three")
		WithMCPServers(fmt.Sprintf(`{"api-bridge":{"command":"mcpbridge","env":{"MCP_SESSION_ID":%q,"MCP_VIRTUAL_SCOPE_ID":%q}}}`, owner, owner+":vt:child"))(o)
		return o
	}()) != nil {
		t.Fatal("child acquired host process lease")
	}
	savedPath := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir())
	if _, err := adapter.generateContentAppServer(context.Background(), nil, options("preflight-warm")); err == nil || current == nil || closed != 0 {
		t.Fatal("preflight failure revoked a live process")
	}
	t.Setenv("PATH", savedPath)
	adapter.apiKey = "account-two"
	run(options("hour-three"))
	if launches != 2 || generation != 2 || closed != 1 {
		t.Fatal("real process replacement did not rotate exact generation")
	}
	originalRelease()
	if current == nil {
		t.Fatal("old cleanup revoked replacement")
	}
	closeCodexAppSession(owner)
	if current != nil || closed != 2 {
		t.Fatal("process close retained its credential")
	}
	// PLAT-746-941: an unexpected process exit rotates bridge auth, not
	// conversation identity. No explicit resume ID is supplied by the caller.
	t.Setenv("PATH", savedPath)
	run(options("after-close"))
	codexAppSessions.Lock()
	session := codexAppSessions.sessions[owner]
	codexAppSessions.Unlock()
	session.mu.Lock()
	deadRPC := session.rpc
	session.mu.Unlock()
	deadRPC.finish(fmt.Errorf("fixture process exit"))
	run(options("after-exit"))
	if launches != 4 || len(loads) != 4 || loads[3]["method"] != "thread/resume" {
		t.Fatal("process credential rotation discarded cached thread")
	}
	closeCodexAppSession(owner)
	if current != nil || closed != 4 {
		t.Fatal("replacement retained credential")
	}
	// Constructor failure releases even before cmd.Start (no process to own it).
	t.Setenv("PATH", t.TempDir())
	_, preflightErr := adapter.generateContentAppServer(context.Background(), nil, options("hour-four"))
	if preflightErr == nil || current != nil || closed != 5 {
		t.Fatal("preflight retained an unowned credential")
	}
	_, err := adapter.launchAppServer(context.Background(), options("hour-five"), dir)
	if err == nil || current != nil || closed != 6 {
		t.Fatal("failed constructor retained credential")
	}
}
