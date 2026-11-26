package claudecode

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3131: the bridge's live output of Claude's own background command reaches
// the host between turns, on the command's own tool call, stoppable until done.
func TestClaudeSDKTaskOutputReachesHostBetweenTurns(t *testing.T) {
	var got []llmtypes.BackgroundOutput
	llmtypes.SetBackgroundOutputListener(func(output llmtypes.BackgroundOutput) { got = append(got, output) })
	t.Cleanup(func() { llmtypes.SetBackgroundOutputListener(nil) })
	session := &claudeSDKSession{owner: "chat-3131-claude"}
	session.handleKeptLine(`{"type":"sdk_task_output","task_output":{"task_id":"bash_1","tool_use_id":"toolu_1","text":"ready on port 3000\n"}}`)
	session.handleKeptLine(`{"type":"sdk_task_output","task_output":{"task_id":"bash_1","tool_use_id":"toolu_1","text":"","done":true}}`)
	session.handleKeptLine(`{"type":"sdk_task_output","task_output":{"task_id":"","tool_use_id":"toolu_2","text":"no task"}}`)
	if len(got) != 2 {
		t.Fatalf("outputs %+v", got)
	}
	first, last := got[0], got[1]
	if first.Provider != "claude-code" || first.Owner != "chat-3131-claude" || first.ToolCallID != "toolu_1" || first.TaskID != "bash_1" || first.Text != "ready on port 3000\n" || !first.CanStop || first.Done {
		t.Fatalf("running output %+v", first)
	}
	if !last.Done || last.CanStop {
		t.Fatalf("final output %+v", last)
	}
	if reason := claudeUnshownEvent(claudeStreamEvent{Type: "sdk_task_output"}); reason != "" {
		t.Fatalf("task output reported as unshown: %s", reason)
	}
}
