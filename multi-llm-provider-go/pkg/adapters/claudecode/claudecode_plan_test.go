package claudecode

import (
	"context"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Exercise the actual structured event consumer, not just the task reducer.
func TestClaudeStructuredTaskResultsPublishPlan(t *testing.T) {
	dir := t.TempDir()
	frames := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"create","name":"TaskCreate","input":{"subject":"Inspect"}}]}}
{"type":"user","tool_use_result":{"task":{"id":"1","subject":"Inspect"}},"message":{"content":[{"type":"tool_result","tool_use_id":"create","content":"Task #1 created successfully: Inspect"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"fail","name":"TaskUpdate","input":{"taskId":"1","status":"completed"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"fail","is_error":true,"content":"failure"}]}}
{"type":"assistant","parent_tool_use_id":"helper","message":{"content":[{"type":"tool_use","id":"child","name":"TodoWrite","input":{"todos":[]}}]}}
{"type":"user","parent_tool_use_id":"helper","message":{"content":[{"type":"tool_result","tool_use_id":"child","content":"ok"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"update","name":"TaskUpdate","input":{"taskId":"1","status":"completed"}}]}}
{"type":"user","tool_use_result":{"success":true},"message":{"content":[{"type":"tool_result","tool_use_id":"update","content":"Updated task #1 status"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"list","name":"TaskList","input":{}}]}}
{"type":"user","tool_use_result":{"tasks":[]},"message":{"content":[{"type":"tool_result","tool_use_id":"list","content":"No tasks found"}]}}
{"type":"result","is_error":false,"result":"Done"}
`
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\ncat <<'EVENTS'\n"+frames+"EVENTS\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	a := NewClaudeCodeInteractiveAdapter("claude-opus-5-5", &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch := make(chan llmtypes.StreamChunk, 64)
	_, err := a.generateContentStructured(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "hello"}}}}, &llmtypes.CallOptions{StreamChan: ch, Metadata: &llmtypes.Metadata{Custom: map[string]interface{}{MetadataKeyWorkingDir: dir}}})
	if err != nil {
		t.Fatal(err)
	}
	var plans []*llmtypes.CodingAgentPlan
	for chunk := range ch {
		if chunk.CodingAgentPlan != nil {
			plans = append(plans, chunk.CodingAgentPlan)
		}
	}
	if len(plans) != 3 || len(plans[0].Items) != 1 || plans[0].Items[0].Status != "pending" || plans[1].Items[0].Status != "completed" || len(plans[2].Items) != 0 {
		t.Fatalf("wrong plan sequence: %+v", plans)
	}
}
