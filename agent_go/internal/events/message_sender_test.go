package events

import (
	"encoding/json"
	pkgevents "github.com/manishiitg/mcpagent/events"
	"reflect"
	"strings"
	"testing"
)

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

// PLAT-856-509: a shared agent's ask is queued with a server-made client id; its sender must still be stamped, or the
// sender reads none of their own answers. Another client id (a typed copy) is never stamped.
func TestDeliveredAttributionKeepsServerClientID(t *testing.T) {
	const session, text = "chat-shared", "[Message from b] hi"
	store := openClientMessageTestStore(t, session)
	clear := store.ExpectAgentMessageSender(session, text, map[string]interface{}{"source": "agent_message", "sender_user_id": "b", "client_message_id": "shared:p:b:1"})
	typed := journalTestEvent("typed", text)
	userMessageEventData(&typed).Metadata = map[string]interface{}{"client_message_id": "browser-1"}
	store.AddEvent(session, typed)
	asked := journalTestEvent("asked", text)
	userMessageEventData(&asked).Metadata = map[string]interface{}{"client_message_id": "shared:p:b:1"}
	store.AddEvent(session, asked)
	clear()
	for _, row := range durableRows(t, store, session) {
		if got := userMessageMetadata(t, row)["sender_user_id"]; (row.ID == "asked") != (got == "b") {
			t.Fatalf("%s: sender %v", row.ID, got)
		}
	}
}

// PLAT-746: an incoming queued card must not indent an unrelated reply.
func TestAgentReplyRequiresExactDeliveredTurnAndClearsForHumanSteer(t *testing.T) {
	const session = "reply-attribution"
	// A populated event must serialize every declared wire field. This guards
	// the manual writer, which previously silently dropped the reply link.
	wireEvent := journalTestEvent("wire", "review")
	fields := reflect.ValueOf(&wireEvent).Elem()
	for i := 0; i < fields.NumField(); i++ {
		if fields.Field(i).Kind() == reflect.String {
			fields.Field(i).SetString("present")
		}
	}
	wireEvent.Sequence = 1
	encoded, err := json.Marshal(wireEvent)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]interface{}
	if err := json.Unmarshal(encoded, &keys); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < fields.NumField(); i++ {
		key := strings.Split(fields.Type().Field(i).Tag.Get("json"), ",")[0]
		if key != "" && key != "-" {
			if _, ok := keys[key]; !ok {
				t.Errorf("declared Event wire field %s is omitted", key)
			}
		}
	}

	store := openClientMessageTestStore(t, session)
	input := journalTestEvent("agent-input", "review")
	input.Data.TurnID = "turn-agent"
	userMessageEventData(&input).Metadata = map[string]interface{}{"source": "agent_message", "message_id": "msg-review"}
	store.AddEvent(session, input)
	check := func(id, turn, want string) {
		t.Helper()
		reply := Event{ID: id, Type: "agent_response", Data: pkgevents.NewAgentEvent(NewGenericEventData("agent_response", map[string]interface{}{"content": "reply"}))}
		reply.Data.TurnID = turn
		store.AddEvent(session, reply)
		rows := durableRows(t, store, session)
		for _, row := range rows {
			if row.ID == id {
				if row.ReplyToAgentMessageID != want {
					t.Fatalf("%s: got %q want %q", id, row.ReplyToAgentMessageID, want)
				}
				// The actual API and journal use Event.MarshalJSON, not struct tags.
				encoded, err := json.Marshal(row)
				if err != nil {
					t.Fatal(err)
				}
				var restored Event
				if err := json.Unmarshal(encoded, &restored); err != nil {
					t.Fatal(err)
				}
				if restored.ReplyToAgentMessageID != want {
					t.Fatalf("%s: JSON lost reply link: got %q want %q", id, restored.ReplyToAgentMessageID, want)
				}
				var wire map[string]interface{}
				if err := json.Unmarshal(encoded, &wire); err != nil {
					t.Fatal(err)
				}
				if _, exists := wire["reply_to_agent_message_id"]; exists != (want != "") {
					t.Fatalf("%s: unrelated reply must omit the link", id)
				}
				return
			}
		}
		t.Fatalf("missing %s", id)
	}
	check("unrelated", "turn-human", "")
	check("unknown", "", "")
	check("related", "turn-agent", "msg-review")
	queued := journalTestEvent("queued", "next incoming")
	queued.Data.TurnID = "turn-agent"
	userMessageEventData(&queued).Metadata = map[string]interface{}{"source": "agent_message", "message_id": "msg-queued", "delivery_status": "queued_for_turn"}
	store.AddEvent(session, queued)
	check("still-related", "turn-agent", "msg-review")
	human := journalTestEvent("steer", "answer me")
	human.Data.TurnID = "turn-agent"
	store.AddEvent(session, human)
	check("after-steer", "turn-agent", "")
}
