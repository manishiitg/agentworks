package codexcli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// /plan: the plan turn selects Codex's plan mode with the thread's model and effort; the
// first ordinary turn after it selects the default mode again; later turns send nothing.
func TestCodexCollaborationModeForPlanTurns(t *testing.T) {
	opts := &llmtypes.CallOptions{Metadata: &llmtypes.Metadata{Custom: map[string]interface{}{MetadataKeyReasoningEffort: "high"}}}
	s := &codexAppSession{model: "gpt-5.5"}
	if mode := codexCollaborationMode(s, false, opts); mode != nil {
		t.Fatalf("ordinary turn sent a mode: %v", mode)
	}
	mode := codexCollaborationMode(s, true, opts)
	settings, _ := mode["settings"].(map[string]any)
	if mode["mode"] != "plan" || settings["model"] != "gpt-5.5" || settings["reasoning_effort"] != "high" || settings["developer_instructions"] != nil {
		t.Fatalf("plan turn mode: %v", mode)
	}
	s.planMode = true
	if mode := codexCollaborationMode(s, false, opts); mode == nil || mode["mode"] != "default" {
		t.Fatalf("turn after a plan did not return to default: %v", mode)
	}
	if !llmtypes.NativePlanModeForSession(llmtypes.WithNativePlanMode(context.Background(), "chat-1"), "chat-1") ||
		llmtypes.NativePlanModeForSession(llmtypes.WithNativePlanMode(context.Background(), "chat-1"), "helper-2") {
		t.Fatal("plan mode must belong to its own chat only")
	}
}

// Live Codex 0.171 (24 Nov): in plan mode the plan arrives only as a "plan" item after a short agent message.
// Both reach the chat, and the turn's answer carries the plan.
func TestCodexAppServerShowsPlanItem(t *testing.T) {
	r := &codexRPC{events: make(chan codexRPCMessage, 10), done: make(chan struct{})}
	send := func(method, fields string) {
		r.events <- codexRPCMessage{Method: method, Params: json.RawMessage(`{"threadId":"thread","turnId":"turn",` + fields + `}`)}
	}
	send("item/completed", `"item":{"id":"a","type":"agentMessage","text":"I will check the README, then plan."}`)
	send("item/started", `"item":{"id":"p","type":"plan","text":""}`)
	send("item/completed", `"item":{"id":"p","type":"plan","text":"1. Read the README.\n2. Add the line."}`)
	send("turn/completed", `"turn":{"id":"turn","status":"completed"}`)
	var output strings.Builder
	response, err := (&CodexCLIAdapter{}).consumeAppTurn(context.Background(), &codexAppSession{threadID: "thread"}, r, "thread", "turn", func(ch llmtypes.StreamChunk) {
		if ch.Type == llmtypes.StreamChunkTypeContent {
			output.WriteString(ch.Content)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "1. Read the README.") || !strings.Contains(response.Choices[0].Content, "2. Add the line.") {
		t.Fatalf("plan missing: %q / %q", output.String(), response.Choices[0].Content)
	}
}
