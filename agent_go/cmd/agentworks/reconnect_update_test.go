package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

func TestHookKeepsResponsiveHelperAcrossBuilds(t *testing.T) {
	root, err := os.MkdirTemp("", "aw-helper-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- agentworksclient.ServeLocalBridge(ctx, root, agentworksclient.LocalBridgeOptions{Version: "retained-build"})
	}()
	defer func() { cancel(); <-done }()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "ping"}); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not start")
		}
	}
	// A retained MCP executable must not stop every chat while checking a
	// version mismatch. Explicit update/stop commands still work below.
	refused, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "shutdown"})
	if err != nil || refused.Error == "" {
		t.Fatalf("unrequested helper replacement: %+v %v", refused, err)
	}
	if err := ensureLocalBridge(ctx, root, &options{}); err != nil {
		t.Fatal(err)
	}
	out, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "ping"})
	if err != nil || out.Version != "retained-build" {
		t.Fatalf("ordinary hook replaced helper: %+v %v", out, err)
	}
	if _, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "shutdown", ShutdownReason: "stop"}); err != nil {
		t.Fatal(err)
	}
}

func TestClaudePluginUpdateKeepsUnchangedHooks(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "marketplace")
	known, _ := json.Marshal(map[string]any{"agentworks": map[string]string{"installLocation": root}})
	files := map[string]string{
		filepath.Join(home, ".claude", "plugins", "known_marketplaces.json"):      string(known),
		filepath.Join(home, ".claude", "plugins", "installed_plugins.json"):       `{"plugins":{"agentworks@agentworks":[{"scope":"project","version":"1.0.13"},{"scope":"user","version":"1.0.13"}]}}`,
		filepath.Join(root, ".claude-plugin", "marketplace.json"):                 `{"plugins":[{"name":"agentworks","source":"./plugins/claude"}]}`,
		filepath.Join(root, "plugins", "claude", ".claude-plugin", "plugin.json"): `{"version":"1.0.13"}`,
	}
	read := func(p string) string { return files[p] }
	if !claudePluginCurrent(home, "agentworks@agentworks", read) {
		t.Fatal("unchanged package would reinstall")
	}
	files[filepath.Join(root, "plugins", "claude", ".claude-plugin", "plugin.json")] = `{"version":"1.0.14"}`
	if claudePluginCurrent(home, "agentworks@agentworks", read) {
		t.Fatal("changed package was skipped")
	}
}
