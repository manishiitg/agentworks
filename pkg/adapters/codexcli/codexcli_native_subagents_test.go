package codexcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestNativeStepToolsAndScopedChildConversations(t *testing.T) {
	for _, item := range []*codexExecItem{
		{Type: "file_change", ID: "patch", Status: "completed", Changes: json.RawMessage(`[{"path":"result.json","kind":"add"}]`)},
		{Type: "collab_tool_call", ID: "delegate", Tool: "spawn_agent", Prompt: "Read the evidence", ReceiverThreadIDs: []string{"child"}, AgentsStates: json.RawMessage(`{"child":{"status":"completed","message":"verified"}}`)},
	} {
		if !isCodexToolItem(item.Type) || codexToolItemName(item) == "" || codexToolItemArgs(item) == "" || codexToolItemResult(item) == "" {
			t.Fatalf("native item lost its tool lifecycle: %+v", item)
		}
	}
	root := t.TempDir()
	cwd := t.TempDir()
	start := time.Now().UTC()
	write := func(id, parent string) {
		path := filepath.Join(root, "sessions", "2026", "10", "14", "rollout-"+id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		encoder := json.NewEncoder(file)
		rows := []map[string]interface{}{
			{"type": "session_meta", "timestamp": start, "payload": map[string]interface{}{"id": id, "cwd": cwd, "base_instructions": map[string]string{"text": "Native child rules"}, "source": map[string]interface{}{"subagent": map[string]interface{}{"thread_spawn": map[string]string{"parent_thread_id": parent}}}}},
			{"type": "response_item", "timestamp": start, "payload": map[string]interface{}{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": "Inspect evidence"}}}},
			{"type": "response_item", "timestamp": start, "payload": map[string]interface{}{"type": "custom_tool_call", "name": "apply_patch", "call_id": "native-patch", "input": "*** Begin Patch\n*** Add File: result.json\n+{}\n*** End Patch"}},
			{"type": "response_item", "timestamp": start, "payload": map[string]interface{}{"type": "custom_tool_call_output", "call_id": "native-patch", "output": "Saved result.json"}},
			{"type": "response_item", "timestamp": start, "payload": map[string]interface{}{"type": "message", "role": "assistant", "phase": "final_answer", "content": []map[string]string{{"type": "output_text", "text": "Evidence verified"}}}},
		}
		for _, row := range rows {
			if err := encoder.Encode(row); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("child", "parent")
	write("nested", "child")
	write("foreign", "other-parent")
	children := readCodexNativeSubagents("parent", cwd, root)
	if len(children) != 2 {
		t.Fatalf("wrong parent association: %+v", children)
	}
	for _, child := range children {
		if child.ID == "foreign" || child.Status != "completed" || len(child.Messages) != 5 {
			t.Fatalf("incomplete child conversation: %+v", child)
		}
		if child.Messages[0].Role != llmtypes.ChatMessageTypeSystem || child.Messages[1].Role != llmtypes.ChatMessageTypeHuman {
			t.Fatal("child system/user context was dropped")
		}
		call, ok := child.Messages[2].Parts[0].(llmtypes.ToolCall)
		if !ok || call.FunctionCall == nil || !strings.Contains(call.FunctionCall.Arguments, "Begin Patch") {
			t.Fatal("native patch input was dropped")
		}
	}

	// #3132: the chat opens the child by the spawnAgent call that started it.
	if _, ok := CodexSubagentConversation("chat", "spawn-1"); ok {
		t.Fatal("read a child no spawn call started")
	}
	codexChatHomes.Store("chat", root)
	rememberCodexChild("chat", json.RawMessage(`{"id":"spawn-1","type":"collabAgentToolCall","tool":"spawnAgent","receiverThreadIds":["child"],"senderThreadId":"parent","status":"inProgress","agentsStates":{}}`))
	live, ok := CodexSubagentConversation("chat", "spawn-1")
	if !ok || live.ID != "child" || live.ToolUseID != "spawn-1" || live.Status != "completed" || len(live.Messages) != 5 {
		t.Fatalf("child by spawn call: %+v %t", live, ok)
	}
	if _, ok := CodexSubagentConversation("other-chat", "spawn-1"); ok {
		t.Fatal("another chat read this chat's child")
	}
	// Codex 0.170+ reports the spawn as a subAgentActivity item (seen live):
	// its start is the spawn step and opens the same child; its completion is
	// no step.
	started := json.RawMessage(`{"type":"subAgentActivity","id":"call_spawn2","kind":"started","agentThreadId":"child","agentPath":"/root/helper"}`)
	rememberCodexChild("chat", started)
	if live, ok := CodexSubagentConversation("chat", "call_spawn2"); !ok || live.ID != "child" || live.ToolUseID != "call_spawn2" {
		t.Fatalf("child by subAgentActivity: %+v %t", live, ok)
	}
	if step := codexAppToolItem(started); step == nil || codexToolItemName(step) != "spawnAgent" || !strings.Contains(codexToolItemArgs(step), "child") {
		t.Fatalf("subAgentActivity start is not a spawnAgent step: %+v", step)
	}
	if step := codexAppToolItem(json.RawMessage(`{"type":"subAgentActivity","id":"subagent-completed-x","kind":"completed","agentThreadId":"child"}`)); step != nil {
		t.Fatalf("a helper's completion became a step: %+v", step)
	}
}
