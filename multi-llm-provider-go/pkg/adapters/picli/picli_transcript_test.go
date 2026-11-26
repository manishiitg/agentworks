package picli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestReadPiTranscriptSummaryExtractsMessagesUsageAndCost(t *testing.T) {
	sessionDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", sessionDir)

	sessionID := "mlp-pi-test-session"
	transcript := filepath.Join(sessionDir, "2026-06-25T10-26-57-426Z_"+sessionID+".jsonl")
	turnStart := time.Date(2026, 6, 25, 10, 26, 57, 0, time.UTC)
	beforeTurn := turnStart.Add(-time.Minute).Format(time.RFC3339Nano)
	inTurn := turnStart.Add(time.Second).Format(time.RFC3339Nano)
	lines := []string{
		`{"type":"message","timestamp":"` + beforeTurn + `","message":{"role":"assistant","content":[{"type":"text","text":"old"}],"usage":{"input":999,"output":999,"totalTokens":1998,"cost":{"total":9.99}}}}`,
		`{"type":"message","timestamp":"` + inTurn + `","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"message","timestamp":"` + inTurn + `","message":{"role":"assistant","content":[{"type":"text","text":"hi back"}],"api":"google-generative-ai","provider":"google","model":"gemini-3.5-flash","usage":{"input":446,"output":31,"totalTokens":477,"cacheRead":4,"cacheWrite":5,"cost":{"input":0.000669,"output":0.000279,"total":0.000948,"cacheRead":0.000001,"cacheWrite":0.000002}},"stopReason":"stop","responseId":"resp-1"}}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	summary := readPiTranscriptSummary(sessionID, turnStart)
	if summary == nil {
		t.Fatal("expected transcript summary")
	}
	if summary.Path != transcript {
		t.Fatalf("Path = %q, want %q", summary.Path, transcript)
	}
	if !summary.hasUsage() {
		t.Fatal("expected usage")
	}
	if summary.InputTokens != 446 || summary.OutputTokens != 31 || summary.TotalTokens != 477 {
		t.Fatalf("tokens = %d/%d/%d, want 446/31/477", summary.InputTokens, summary.OutputTokens, summary.TotalTokens)
	}
	if summary.CacheReadTokens != 4 || summary.CacheWriteTokens != 5 {
		t.Fatalf("cache tokens = %d/%d, want 4/5", summary.CacheReadTokens, summary.CacheWriteTokens)
	}
	if summary.TotalCostUSD != 0.000948 || summary.InputCostUSD != 0.000669 || summary.OutputCostUSD != 0.000279 {
		t.Fatalf("costs = total %v input %v output %v", summary.TotalCostUSD, summary.InputCostUSD, summary.OutputCostUSD)
	}
	if summary.Provider != "google" || summary.Model != "gemini-3.5-flash" || summary.API != "google-generative-ai" {
		t.Fatalf("route = %q/%q api=%q", summary.Provider, summary.Model, summary.API)
	}
	if len(summary.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(summary.Messages))
	}
	if summary.Messages[0].Role != llmtypes.ChatMessageTypeHuman || summary.Messages[1].Role != llmtypes.ChatMessageTypeAI {
		t.Fatalf("message roles = %#v", summary.Messages)
	}
}

func TestLatestPiTranscriptPathUsesConfiguredSessionDirAndNewestMatch(t *testing.T) {
	sessionDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", sessionDir)

	sessionID := "mlp-pi-newest"
	oldPath := filepath.Join(sessionDir, "2026-06-25T10-00-00-000Z_"+sessionID+".jsonl")
	projectDir := filepath.Join(sessionDir, "--project-workdir--")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	newPath := filepath.Join(projectDir, "2026-06-25T11-00-00-000Z_"+sessionID+".jsonl")
	otherPath := filepath.Join(sessionDir, "2026-06-25T12-00-00-000Z_other.jsonl")
	for _, path := range []string{oldPath, newPath, otherPath} {
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes old: %v", err)
	}
	if err := os.Chtimes(newPath, newTime, newTime); err != nil {
		t.Fatalf("chtimes new: %v", err)
	}

	if got := latestPiTranscriptPath(sessionID); got != newPath {
		t.Fatalf("latestPiTranscriptPath = %q, want %q", got, newPath)
	}
}

func TestLastPiAssistantTextUsesFinalAssistantMessage(t *testing.T) {
	messages := []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "tool preamble"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "tool result"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "FINAL ANSWER"),
	}
	if got := lastPiAssistantText(messages); got != "FINAL ANSWER" {
		t.Fatalf("lastPiAssistantText() = %q, want FINAL ANSWER", got)
	}
}

// PLAT-179. A message combining intermediate commentary with the tool call
// it introduces -- {"type":"text",...},{"type":"toolCall",...} in the SAME
// content array -- must preserve the tool call as a real llmtypes.ToolCall,
// not silently drop it down to text-only. That preserved ToolCall is what
// lets mcpagent's shared retainedturn.finalResponse recognize this message
// as not-yet-final; before this fix, pi's own reader threw the toolCall
// block away here, and by the time the message reached finalResponse there
// was nothing left distinguishing it from a genuinely finished reply.
func TestPiTranscriptPartsPreservesAToolCallAlongsideText(t *testing.T) {
	content := []piTranscriptContent{
		{Type: "text", Text: "progress update, not the final answer"},
		{Type: "toolCall", ID: "call_1", Name: "mcp"},
	}
	parts := piTranscriptParts(content)
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2 (text + toolCall): %#v", len(parts), parts)
	}
	text, ok := parts[0].(llmtypes.TextContent)
	if !ok || text.Text != "progress update, not the final answer" {
		t.Fatalf("parts[0] = %#v, want the text content first", parts[0])
	}
	call, ok := parts[1].(llmtypes.ToolCall)
	if !ok || call.ID != "call_1" {
		t.Fatalf("parts[1] = %#v, want a preserved ToolCall with ID call_1", parts[1])
	}
}

// Confirmed live against a real session transcript (Workflow/trading, 50 of
// 50 toolCall blocks named "mcp"): a bridge-routed tool call's transcript
// entry looks like {"type":"toolCall","name":"mcp","arguments":{"tool":
// "api_bridge_execute_shell_command","args":"..."}}. Before this fix, the
// reconstructed ToolCall's FunctionCall.Name stayed the generic "mcp" label
// pi reports, since Arguments was never even parsed into the Go struct.
func TestPiTranscriptPartsRecoversRealToolNameFromBridgeWrapper(t *testing.T) {
	content := []piTranscriptContent{
		{
			Type:      "toolCall",
			ID:        "call_1",
			Name:      "mcp",
			Arguments: []byte(`{"tool":"api_bridge_execute_shell_command","args":"{\"command\":\"ls\"}"}`),
		},
	}
	parts := piTranscriptParts(content)
	if len(parts) != 1 {
		t.Fatalf("parts = %d, want 1: %#v", len(parts), parts)
	}
	call, ok := parts[0].(llmtypes.ToolCall)
	if !ok || call.FunctionCall == nil {
		t.Fatalf("parts[0] = %#v, want a ToolCall with a FunctionCall", parts[0])
	}
	if call.FunctionCall.Name != "api_bridge_execute_shell_command" {
		t.Fatalf("FunctionCall.Name = %q, want the recovered real tool name, not the generic \"mcp\" wrapper", call.FunctionCall.Name)
	}
}

// A non-bridge tool call (a real, specific name already) must pass through
// unchanged -- piGenericBridgeToolName's detection must not be over-eager.
func TestPiTranscriptPartsLeavesASpecificToolNameUnchanged(t *testing.T) {
	content := []piTranscriptContent{{Type: "toolCall", ID: "call_1", Name: "read_file"}}
	parts := piTranscriptParts(content)
	call, ok := parts[0].(llmtypes.ToolCall)
	if !ok || call.FunctionCall.Name != "read_file" {
		t.Fatalf("parts[0] = %#v, want FunctionCall.Name unchanged as read_file", parts[0])
	}
}

func TestPiTranscriptPartsTextOnlyMessageHasNoToolCall(t *testing.T) {
	parts := piTranscriptParts([]piTranscriptContent{{Type: "text", Text: "the real final answer"}})
	if len(parts) != 1 {
		t.Fatalf("parts = %d, want 1 (text only): %#v", len(parts), parts)
	}
	if _, ok := parts[0].(llmtypes.TextContent); !ok {
		t.Fatalf("parts[0] = %#v, want TextContent", parts[0])
	}
}
