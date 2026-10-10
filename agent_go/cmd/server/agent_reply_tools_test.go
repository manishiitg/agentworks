package server

import (
	"context"
	"testing"
)

// A guest can explicitly answer its existing inbox without acquiring the
// ability to initiate work in arbitrary agents or restore the removed result tool.
func TestGuestMessagingOnlyRepliesWithinExistingInbox(t *testing.T) {
	inner := &recordingRegistrar{}
	reg := agentReplyOnlyRegistrar{inner}
	calls := 0
	execute := func(context.Context, map[string]interface{}) (string, error) { calls++; return "accepted", nil }
	for _, name := range []string{"send_message", "read_agent_messages", "schedule_message_wakeup", "call_function", "return_function_result"} {
		if err := reg.RegisterCustomTool(name, "", nil, execute, ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(inner.tools) != 3 {
		t.Fatalf("guest tools=%v", inner.tools)
	}
	for _, args := range []map[string]interface{}{{"target": "crew:other", "message": "start work"}, {"inbox_id": "inbox-current", "target": "crew:other"}} {
		if _, err := inner.tools["send_message"].exec(context.Background(), args); err == nil {
			t.Fatal("guest selected arbitrary recipient")
		}
	}
	if _, err := inner.tools["send_message"].exec(context.Background(), map[string]interface{}{"inbox_id": "inbox-current", "message": "explicit reply"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("allowed executions=%d", calls)
	}
}
