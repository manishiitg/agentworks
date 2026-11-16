package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// #2762: a recipient of the former sessions-message route could not reply in
// its original inbox although a fresh addressed send worked. Both tool calls
// must bind to the same registered identities, not invent a second thread.
func TestBridgeHostedRecipientRepliesInOriginalThread(t *testing.T) {
	env := newTriggerLinkEnv(t)
	crewOwnerTabsFixture(t, env)
	env.api.agentProfiles = env.svc.registry
	ctx := internalBotRequestContext(t.Context(), "owner")
	profile, err := env.svc.registry.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	records := []ProductConversationRecord{}
	root := ""
	for _, key := range []string{"beta:chat:sender", "beta:chat:recipient"} {
		binding, e := resolveProductConversationBinding(ctx, "owner", profile, key)
		if e != nil {
			t.Fatal(e)
		}
		r, e := defaultProductConversationRegistryStore().resolveOrCreate(ctx, "owner", profile, binding, "")
		if e != nil {
			t.Fatal(e)
		}
		records = append(records, r)
		root = binding.WorkspacePath
	}
	tools := func(record ProductConversationRecord) *recordingRegistrar {
		reg := &recordingRegistrar{}
		resolve := func(context.Context) (triggerLinkCaller, error) {
			return triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ProfileID: "work", ID: "beta"}, Path: root, Label: "Agent", Chat: &codeChat{OwnerID: "owner", Key: record.ConversationKey, ID: record.ConversationKey, SessionID: record.SessionID}}, nil
		}
		if e := env.api.registerAgentMessagingTools(reg, "owner", record.SessionID, QueryRequest{}, resolve, nil); e != nil {
			t.Fatal(e)
		}
		return reg
	}
	delivered := make(chan string, 2)
	oldTurn := agentMessageTurn
	agentMessageTurn = func(_ *StreamingAPI, _ context.Context, request map[string]interface{}, session, owner string) (internalSessionTurnResult, error) {
		delivered <- session + ":" + fmt.Sprint(request["agent_message_inbox"])
		return internalSessionTurnResult{}, nil
	}
	t.Cleanup(func() { agentMessageTurn = oldTurn })
	waitDelivery := func(session, inbox string) {
		t.Helper()
		select {
		case got := <-delivered:
			if got != session+":"+inbox {
				t.Fatalf("wrong receiving chat/thread: %s", got)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("named hosted delivery never reached the normal turn boundary")
		}
	}
	sender, recipient := tools(records[0]), tools(records[1])
	raw, err := sender.tools["send_message"].exec(ctx, map[string]interface{}{"target": bridgeChatAddress("owner", "work", "beta", records[1].ConversationKey), "message": "Please check this", "submission_id": "first-request"})
	if err != nil {
		t.Fatal(err)
	}
	var first map[string]interface{}
	if json.Unmarshal([]byte(raw), &first) != nil {
		t.Fatal("invalid send receipt")
	}
	inbox, _ := first["inbox_id"].(string)
	if inbox == "" {
		t.Fatal("missing original reply thread")
	}
	waitDelivery(records[1].SessionID, inbox)
	raw, err = recipient.tools["send_message"].exec(ctx, map[string]interface{}{"inbox_id": inbox, "message": "Checked", "submission_id": "first-reply"})
	if err != nil {
		t.Fatalf("recipient could not reply in original Bridge thread: %v", err)
	}
	var reply map[string]interface{}
	if json.Unmarshal([]byte(raw), &reply) != nil || reply["inbox_id"] != inbox {
		t.Fatal("reply created a different conversation", raw)
	}
	waitDelivery(records[0].SessionID, inbox)
	snapshot, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range snapshot.Conversations {
		if c.ID != inbox {
			continue
		}
		if c.Endpoints[0].NamedAgentID == "" || c.Endpoints[1].NamedAgentID == "" {
			t.Fatal("a peer endpoint bypassed named Bridge registration")
		}
		if len(c.Messages) != 2 || c.Messages[0].From == c.Messages[1].From {
			t.Fatal("original thread did not retain the recipient reply")
		}
		named, err := namedAgentByID(ctx, "owner", c.Endpoints[1].NamedAgentID)
		if err != nil || !namedAgentOnline(named) || agentDeliveryMode(named) != "live" {
			t.Fatalf("hosted contact incorrectly needs an external attachment: %v", err)
		}
		// JSON allocates distinct binding pointers after a restart; the
		// original source is still the same endpoint for retry dedupe.
		encoded, err := json.Marshal(c.Endpoints[0])
		if err != nil {
			t.Fatal(err)
		}
		var reopened agentMessageEndpoint
		if err := json.Unmarshal(encoded, &reopened); err != nil || !sameAgentMessageEndpoint(reopened, c.Endpoints[0]) {
			t.Fatalf("reopen changed the sender identity: %v", err)
		}
		return
	}
	t.Fatal("original Bridge thread disappeared")
}
