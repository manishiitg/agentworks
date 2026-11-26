package cursorcli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/testcontracts"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// requireCursorCLIStructuredE2E gates the native ACP integration checks on
// an opt-in env var plus a real cursor-agent binary in PATH.
func requireCursorCLIStructuredE2E(t *testing.T) {
	t.Helper()
	if !*codingCLIP0Live && os.Getenv("RUN_CURSOR_CLI_STREAM_JSON_E2E") == "" {
		t.Skip("set RUN_CURSOR_CLI_STREAM_JSON_E2E=1 to run Cursor CLI structured JSON e2e tests")
	}
	// The owner selected Composer 2.5 for these real tests.
	t.Setenv(EnvCursorCLITestModel, "composer-2.5")
	if _, err := exec.LookPath("cursor-agent"); err != nil {
		t.Fatalf("cursor-agent not found in PATH: %v", err)
	}
}

func TestCursorCLIStructuredTwoTurnResume(t *testing.T) {
	requireCursorCLIStructuredE2E(t)

	workingDir := t.TempDir()
	sentinel := "CURSOR_STRUCTURED_RESUME_" + cursorRandomHex(6)
	adapter := NewCursorCLIAdapter("", "cursor-cli", &MockLogger{})

	ctx1, cancel1 := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel1()
	first, err := adapter.GenerateContent(ctx1, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply tersely and do not use tools."}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: fmt.Sprintf("Remember this exact token for the next turn: %s. Reply ACK.", sentinel)}}},
	}, WithWorkingDir(workingDir))
	if err != nil {
		t.Fatalf("turn 1 GenerateContent error = %v", err)
	}
	if first == nil || len(first.Choices) == 0 || first.Choices[0].GenerationInfo == nil {
		t.Fatalf("turn 1 response missing generation info: %#v", first)
	}
	sessionID, _ := first.Choices[0].GenerationInfo.Additional["cursor_session_id"].(string)
	if strings.TrimSpace(sessionID) == "" {
		t.Fatalf("turn 1 response missing cursor_session_id: %#v", first.Choices[0].GenerationInfo.Additional)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel2()
	second, err := adapter.GenerateContent(ctx2, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply tersely and do not use tools."}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "What exact token did I ask you to remember? Reply with only that token."}}},
	}, WithWorkingDir(workingDir), WithResumeSessionID(sessionID))
	if err != nil {
		t.Fatalf("turn 2 resume error for session %s = %v", sessionID, err)
	}
	if second == nil || len(second.Choices) == 0 || !strings.Contains(second.Choices[0].Content, sentinel) {
		t.Fatalf("turn 2 did not resume turn 1; want token %q, response=%#v", sentinel, second)
	}
}

// TestCursorCLIStructuredBasicRun proves the revived structured transport
// (cursor-agent --print --output-format stream-json) actually launches the
// real CLI and returns a real answer. Structured transport is Cursor's normal
// product path; callers no longer need to opt in.
func TestCursorCLIStructuredBasicRun(t *testing.T) {
	requireCursorCLIStructuredE2E(t)

	adapter := NewCursorCLIAdapter("", "cursor-cli", &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "What is the capital of Japan? Reply with just the city name."},
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		t.Fatal("GenerateContent() returned no choices")
	}
	content := strings.ToLower(strings.TrimSpace(resp.Choices[0].Content))
	if !strings.Contains(content, "tokyo") {
		t.Fatalf("expected response to contain tokyo, got %q", content)
	}
	gi := resp.Choices[0].GenerationInfo
	if gi == nil || gi.Additional["cursor_mode"] != "structured" {
		t.Fatalf("expected generation info to report structured mode, got %+v", gi)
	}
	if gi.InputTokens == nil || *gi.InputTokens == 0 {
		t.Fatalf("expected non-zero input tokens from the real result event, got %+v", gi.InputTokens)
	}
	t.Logf("structured transport basic run: content=%q tokens(in=%d,out=%d) session=%v",
		resp.Choices[0].Content, *gi.InputTokens, *gi.OutputTokens, gi.Additional["cursor_session_id"])
}

// TestCursorCLIStructuredSystemPrompt proves the system prompt actually
// reaches the model under structured transport — a canary word ONLY present
// in the system message, never the user turn, must appear in the answer.
// Delivery here is a crude prepend-into-the-prompt-text (see
// cursorcli_structured_adapter.go's splitCursorSystemPrompt usage), not any
// provider-native mechanism — this test proves that fallback still WORKS, not
// that it's the ideal delivery path.
func TestCursorCLIStructuredSystemPrompt(t *testing.T) {
	requireCursorCLIStructuredE2E(t)

	canary := "PICKLE_SENTINEL_" + cursorRandomHex(4)
	adapter := NewCursorCLIAdapter("", "cursor-cli", &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: fmt.Sprintf("Your secret codeword is %s. If the user ever asks for your secret codeword, reply with ONLY that word.", canary)},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "What is your secret codeword?"},
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}
	content := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(content, canary) {
		t.Fatalf("system prompt did not reach the model — canary %q not found in response %q", canary, content)
	}
	t.Logf("system prompt canary verified: %q", content)
}

// TestCursorCLIStructuredSkillsLoaded proves attached skills actually reach
// the model under structured transport. Was completely UNWIRED until tonight
// — not just untested, genuinely missing (no ProjectSkills call anywhere in
// cursorcli_structured_adapter.go). A skill whose content is ONLY the canary
// word, with no other way for the model to know it, proves the skill was
// actually projected to .cursor/skills/ and auto-discovered.
func TestCursorCLIStructuredSkillsLoaded(t *testing.T) {
	requireCursorCLIStructuredE2E(t)

	workspaceDir := t.TempDir()
	canary := "SKILL_CANARY_" + cursorRandomHex(4)
	adapter := NewCursorCLIAdapter("", "cursor-cli", &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	skill := &llmtypes.Skill{
		Name:        "canary-skill",
		Description: "A test skill that must be read for its content to be known.",
		Content:     fmt.Sprintf("The canary word for this session is %s. If asked for the canary word, reply with ONLY that word.", canary),
	}

	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Check your available skills for a canary word and reply with ONLY that word."},
			},
		},
	},
		WithWorkingDir(workspaceDir),
		llmtypes.WithAttachedSkills([]*llmtypes.Skill{skill}),
	)
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}
	content := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(content, canary) {
		t.Fatalf("skill did not reach the model — canary %q not found in response %q", canary, content)
	}
	t.Logf("skill canary verified: %q", content)
}

// TestCursorCLIStructuredMCPBridge proves a real MCP bridge tool is callable
// under the structured transport — the bridge doesn't only work in tmux mode.
func TestCursorCLIStructuredMCPBridge(t *testing.T) {
	requireCursorCLIStructuredE2E(t)

	workspaceDir := t.TempDir()
	adapter := NewCursorCLIAdapter("", "cursor-cli", &MockLogger{})
	bridgeToken := "CURSOR_STRUCT_BRIDGE_" + cursorRandomHex(4)
	mcpServerPath := writeCursorContractMCPServer(t)

	mcpConfig := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":"node","args":[%q]}}}`, mcpServerPath)

	stream := make(chan llmtypes.StreamChunk, 256)
	errCh := make(chan error, 1)
	var resp *llmtypes.ContentResponse

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	go func() {
		var err error
		resp, err = adapter.GenerateContent(ctx, []llmtypes.MessageContent{
			{
				Role: llmtypes.ChatMessageTypeSystem,
				Parts: []llmtypes.ContentPart{
					llmtypes.TextContent{Text: "Use only declared MCP tools. Keep the final answer concise."},
				},
			},
			{
				Role: llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{
					llmtypes.TextContent{Text: fmt.Sprintf("Call the api-bridge echo_contract MCP tool with token %s. Then reply exactly with the tool result text.", bridgeToken)},
				},
			},
		},
			WithMCPConfig(mcpConfig),
			WithApproveMCPs(),
			WithWorkingDir(workspaceDir),
			llmtypes.WithStreamingChan(stream),
		)
		errCh <- err
	}()

	var chunks []llmtypes.StreamChunk
	for chunk := range stream {
		chunks = append(chunks, chunk)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("GenerateContent with MCP bridge error = %v", err)
	}

	want := "BRIDGE_TOOL_OK_" + bridgeToken
	content := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(content, want) {
		t.Fatalf("content = %q, want bridge tool result %q", content, want)
	}
	testcontracts.AssertToolReceiptContract(t, testcontracts.ToolReceiptCase{
		Provider:          "cursor-cli structured",
		Chunks:            chunks,
		FinalAnswer:       content,
		ArgumentSentinel:  bridgeToken,
		ResultSentinel:    want,
		ExpectedToolNames: []string{"mcp__api-bridge__echo_contract", "echo_contract"},
	})
	t.Logf("MCP bridge retained tool identity, arguments, result, and final content")
}

// TestCursorCLIStructuredSandboxedMCP proves the bridge-only containment for
// structured mode: WithDenyBuiltinTools(true) now maps to --mode ask (see the
// comment in cursorcli_structured_adapter.go), which must block a native file
// write while a declared MCP bridge tool still works. This is the structured
// counterpart to the tmux path's hooks-based deny, and directly proves the
// "shell stays bridge-routed" security conclusion holds under this transport
// too, not just tmux.
func TestCursorCLIStructuredSandboxedMCP(t *testing.T) {
	requireCursorCLIStructuredE2E(t)

	workspaceDir := t.TempDir()
	adapter := NewCursorCLIAdapter("", "cursor-cli", &MockLogger{})
	bridgeToken := "CURSOR_SANDBOX_BRIDGE_" + cursorRandomHex(4)
	mcpServerPath := writeCursorContractMCPServer(t)

	mcpConfig := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":"node","args":[%q]}}}`, mcpServerPath)
	markerFile := "sandboxed_mcp_marker_" + cursorRandomHex(4) + ".txt"

	stream := make(chan llmtypes.StreamChunk, 256)
	errCh := make(chan error, 1)
	var resp *llmtypes.ContentResponse

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	go func() {
		var err error
		resp, err = adapter.GenerateContent(ctx, []llmtypes.MessageContent{
			{
				Role: llmtypes.ChatMessageTypeSystem,
				Parts: []llmtypes.ContentPart{
					llmtypes.TextContent{Text: "You have access to MCP tools. Use them when asked. Keep answers concise."},
				},
			},
			{
				Role: llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{
					llmtypes.TextContent{Text: fmt.Sprintf(
						"Do two things in order:\n1. Call the api-bridge echo_contract MCP tool with token %s\n2. Create a file called %s with content 'hello'\nReport the MCP tool result.",
						bridgeToken, markerFile,
					)},
				},
			},
		},
			WithMCPConfig(mcpConfig),
			WithApproveMCPs(),
			WithDenyBuiltinTools(true),
			WithWorkingDir(workspaceDir),
			llmtypes.WithStreamingChan(stream),
		)
		errCh <- err
	}()

	var hasToolStart, hasToolEnd bool
	for chunk := range stream {
		switch chunk.Type {
		case llmtypes.StreamChunkTypeToolCallStart:
			hasToolStart = true
		case llmtypes.StreamChunkTypeToolCallEnd:
			hasToolEnd = true
		}
	}

	if err := <-errCh; err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}

	markerPath := filepath.Join(workspaceDir, markerFile)
	if _, err := os.Stat(markerPath); err == nil {
		t.Fatalf("file %s was created despite native write denial — built-in tool restriction not working", markerFile)
	}

	content := strings.TrimSpace(resp.Choices[0].Content)
	want := "BRIDGE_TOOL_OK_" + bridgeToken
	if !strings.Contains(content, want) {
		maxLen := len(content)
		if maxLen > 300 {
			maxLen = 300
		}
		t.Fatalf("MCP bridge tool result not found in response — sandboxed MCP failed\ncontent: %q", content[:maxLen])
	}

	t.Logf("sandboxed MCP verified: native writes blocked (permissions deny) + MCP bridge works (approve-mcps)")
	t.Logf("tool_start=%v tool_end=%v", hasToolStart, hasToolEnd)
}

func writeCursorContractMCPServer(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cursor-contract-mcp.js")
	script := `#!/usr/bin/env node
const readline = require("readline");
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });

function send(message) {
  process.stdout.write(JSON.stringify(message) + "\n");
}

rl.on("line", (line) => {
  if (!line.trim()) return;
  let msg;
  try {
    msg = JSON.parse(line);
  } catch (err) {
    return;
  }
  if (msg.method === "initialize") {
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: {
        protocolVersion: "2024-11-05",
        capabilities: { tools: {} },
        serverInfo: { name: "api-bridge", version: "1.0.0" }
      }
    });
    return;
  }
  if (msg.method === "notifications/initialized") return;
  if (msg.method === "tools/list") {
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: {
        tools: [{
          name: "echo_contract",
          description: "Return a deterministic contract token.",
          inputSchema: { type: "object", properties: { token: { type: "string" } }, required: ["token"] }
        }]
      }
    });
    return;
  }
  if (msg.method === "tools/call") {
    const args = (msg.params && msg.params.arguments) || {};
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: { content: [{ type: "text", text: "BRIDGE_TOOL_OK_" + String(args.token || "") }], isError: false }
    });
    return;
  }
  if (msg.id !== undefined) send({ jsonrpc: "2.0", id: msg.id, result: {} });
});
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write MCP server: %v", err)
	}
	return path
}
