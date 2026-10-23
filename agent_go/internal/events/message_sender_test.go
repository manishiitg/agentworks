package events

import "testing"

func TestDeliveredAgentAttributionIsScopedAndDurable(t *testing.T) {
	const session, text = "chat-delivery", "[Agent message from Autopilot] a request"
	store := openClientMessageTestStore(t, session)
	clear := store.ExpectAgentMessageSender(session, text, map[string]interface{}{
		"source": "agent_message", "sender_label": "Autopilot", "display_content": "a request",
		"agent_message_inbox": "inbox-test", "message_id": "msg-test",
	})
	child := journalTestEvent("child", text)
	child.ExecutionID = "child-execution"
	child.ExecutionKind = "agent_item"
	store.AddEvent(session, child)
	steer := journalTestEvent("human-steer", text)
	userMessageEventData(&steer).Metadata = map[string]interface{}{"source": "coding_agent_live_input"}
	store.AddEvent(session, steer)
	root := journalTestEvent("delivered", text)
	root.ExecutionKind = "main_agent"
	store.AddEvent(session, root)
	clear()
	store.AddEvent(session, journalTestEvent("typed-copy", text))
	clear = store.ExpectAgentMessageSender(session, text, map[string]interface{}{"source": "agent_message"})
	clear() // A turn failing before its first input cannot mark the next human turn.
	store.AddEvent(session, journalTestEvent("after-failure", text))
	rows := durableRows(t, store, session)
	marked := 0
	for _, row := range rows {
		metadata := userMessageMetadata(t, row)
		if metadata["source"] == "agent_message" {
			marked++
			if row.ID != "delivered" || metadata["agent_message_inbox"] != "inbox-test" || metadata["display_content"] != "a request" {
				t.Fatalf("wrong durable attribution: %s %#v", row.ID, metadata)
			}
		}
	}
	if marked != 1 {
		t.Fatalf("marked %d messages, want only the delivered root", marked)
	}
}
