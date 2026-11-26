package musecli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestMuseExecLaneMCPMountReachesCLIAndRestores proves the settings-merge
// mount reaches the CLI and leaves no trace, using the CLI's own failure as
// the witness: with an explicitly required unreachable dummy bridge URL, the run must fail with
// "Required MCP server `api-bridge` failed during startup" (observed live
// 2026-09-10) — proving muse read our merged settings — and the temp config
// home must have no settings.json afterwards (restore runs on failure too).
// A success-path run needs a real reachable bridge and belongs to the live
// mcp_bridge P0, not here.
func TestMuseExecLaneMCPMountReachesCLIAndRestores(t *testing.T) {
	requireMuseBinary(t)
	museEchoTestEnv(t)
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		t.Fatal("museEchoTestEnv must set XDG_CONFIG_HOME")
	}
	adapter := NewMuseCLIAdapter("", "muse-cli", museTestLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "say the word pineapple"}}},
	}, WithMCPConfig(`{"mcpServers": {"api-bridge": {"url": "http://127.0.0.1:9/bridge", "required": true}}}`), WithMuseStructuredTransport(true))
	if err == nil {
		t.Fatal("expected failure: dummy bridge URL cannot initialize")
	}
	if !strings.Contains(err.Error(), "api-bridge") {
		t.Fatalf("error = %q, want it to name the mounted server (proof the CLI read our merge)", err)
	}
	if _, statErr := os.Stat(filepath.Join(configHome, "muse", "settings.json")); !os.IsNotExist(statErr) {
		t.Fatal("settings.json left behind after failed mounted run (restore did not run)")
	}
}

// TestMuseExecLaneToolPolicyKeepsMCPVisible runs the installed Muse binary
// with the echo provider and a local MCP server. The PreToolUse policy is an
// execution gate, not a model-surface filter, so native tools remain listed;
// critically, the mounted MCP tool remains listed too. Direct policy behavior
// is pinned by TestMuseToolPolicyHookAllowsOnlyWebAndMCP and the Meta bridge
// coexistence proof lives in TestMuseCLIRealMCPBridge.
func TestMuseExecLaneToolPolicyKeepsMCPVisible(t *testing.T) {
	requireMuseBinary(t)
	museEchoTestEnv(t)
	var called atomic.Int32
	stub := museProbeMCPStub(&called)
	defer stub.Close()

	adapter := NewMuseCLIAdapter("", "muse-cli", museTestLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "say ok"}}},
	},
		WithMCPConfig(`{"mcpServers":{"probe-stub":{"url":"`+stub.URL+`/mcp"}}}`),
		WithToolAllowlist([]string{"web_search"}),
		WithMuseStructuredTransport(true),
	)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	sid := strings.TrimSpace(resp.Choices[0].GenerationInfo.CodingProviderSessionHandle.NativeSessionID)
	raw, err := os.ReadFile(museSessionLogPath(sid))
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	var active []string
	for _, line := range strings.Split(string(raw), "\n") {
		var event struct {
			PayloadType string `json:"payload_type"`
			Payload     struct {
				Event struct {
					Kind    string `json:"kind"`
					Toolset struct {
						ActiveTools []string `json:"active_tools"`
					} `json:"toolset"`
				} `json:"event"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &event) == nil &&
			event.PayloadType == "runtime.session" && event.Payload.Event.Kind == "model_request_configured" {
			active = event.Payload.Event.Toolset.ActiveTools
			if slices.Contains(active, "mcp__probe_stub__probe_echo") {
				break
			}
		}
	}
	if len(active) == 0 {
		t.Fatalf("session has no model_request_configured toolset: %s", raw)
	}
	t.Logf("Muse model-visible tools with restriction policy: %v", active)
	for _, want := range []string{"web_search", "mcp__probe_stub__probe_echo"} {
		if !slices.Contains(active, want) {
			t.Fatalf("required tool %q is not model-visible: %v", want, active)
		}
	}
}
