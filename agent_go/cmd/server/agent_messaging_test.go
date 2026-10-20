package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// Exercise delivery with the real workspace transport and permissions, while
// replacing only the model turn. Final prose never becomes a conversational reply.
func TestAgentMessagesRequireExplicitRepliesAndKeepInboxOwnership(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.eventStore = openClientIdentityTestStore(t, "attribution-test")
	ctx := internalBotRequestContext(context.Background(), "owner")
	target, err := resolveTriggerTarget(ctx, GetUserFromContext(ctx), "workflow:Reports")
	if err != nil {
		t.Fatal(err)
	}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerUser, ID: "owner"}, Label: "External agent"}
	old := agentMessageTurn
	agentMessageTurn = func(api *StreamingAPI, turnCtx context.Context, request map[string]interface{}, session, _ string) (internalSessionTurnResult, error) {
		if err := api.eventStore.SetSessionPersistenceClass(session, internalevents.SessionPersistenceInteractiveChat); err != nil {
			return internalSessionTurnResult{}, err
		}
		query, _ := request["query"].(string)
		clear := api.expectDeliveredAgentMessage(turnCtx, session, query)
		defer clear()
		api.eventStore.AddEvent(session, testUserMessage("delivered-input", query))
		return internalSessionTurnResult{FinalResponse: "implicit final answer must not be forwarded"}, nil
	}
	t.Cleanup(func() { agentMessageTurn = old })
	receipt, err := env.api.sendAgentMessage(ctx, "owner", caller, target, "Please check this", "", "submission-first")
	if err != nil {
		t.Fatal(err)
	}
	id := receipt["inbox_id"].(string)
	if receipt["accepted"] != true || receipt["call_id"] != nil {
		t.Fatalf("not a message acknowledgement: %v", receipt)
	}
	var conv agentConversation
	deadline := time.Now().Add(5 * time.Second)
	for {
		agentMessageMu.Lock()
		s, readErr := readAgentMessageStore(ctx)
		agentMessageMu.Unlock()
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, c := range s.Conversations {
			if c.ID == id {
				conv = c
			}
		}
		if len(conv.Messages) > 0 && conv.Messages[0].Delivery == "delivered" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("delivery did not finish: %+v", conv)
		}
		time.Sleep(10 * time.Millisecond)
	}
	page, err := env.api.readAgentMessages(ctx, "owner", caller, id, 0, 30, 0)
	if err != nil || len(page["messages"].([]map[string]interface{})) != 0 {
		t.Fatalf("final chat leaked: %v %v", page, err)
	}
	chat := conv.Endpoints[1].Session
	// Request JSON and pasted text cannot manufacture the private delivery context.
	clear := env.api.expectDeliveredAgentMessage(ctx, chat, agentMessageText(conv, conv.Messages[0]))
	env.api.eventStore.AddEvent(chat, testUserMessage("typed-envelope", agentMessageText(conv, conv.Messages[0])))
	clear()
	saved, err := env.api.eventStore.ReadDurableChatPage(chat, internalevents.DurableEventPageOptions{Limit: 10, FromStart: true})
	if err != nil || len(saved.Events) != 2 {
		t.Fatalf("durable inputs: %d %v", len(saved.Events), err)
	}
	for index, event := range saved.Events {
		raw, _ := json.Marshal(event.Data.Data)
		var payload struct {
			Metadata map[string]interface{} `json:"metadata"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if index == 0 && (payload.Metadata["source"] != "agent_message" || payload.Metadata["agent_message_inbox"] != id || payload.Metadata["display_content"] != "Please check this") {
			t.Fatalf("missing trusted durable fields: %#v", payload.Metadata)
		}
		if index == 1 && payload.Metadata["source"] == "agent_message" {
			t.Fatal("typed envelope acquired agent identity")
		}
	}
	receiver := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerWorkflow, ID: target.stampID()}, Label: target.Label, Path: target.Path, Chat: &codeChat{SessionID: conv.Endpoints[1].Session}}
	if _, err = env.api.sendAgentMessage(ctx, "owner", receiver, triggerTarget{}, "My explicit reply", id, ""); err != nil {
		t.Fatal(err)
	}
	page, err = env.api.readAgentMessages(ctx, "owner", caller, id, 0, 30, 0)
	if err != nil || len(page["messages"].([]map[string]interface{})) != 1 {
		t.Fatalf("explicit reply missing: %v %v", page, err)
	}
	next := page["next_cursor"].(int)
	page, err = env.api.readAgentMessages(ctx, "owner", caller, id, next, 30, 0)
	if err != nil || len(page["messages"].([]map[string]interface{})) != 0 {
		t.Fatalf("cursor reread: %v %v", page, err)
	}
	retry, err := env.api.sendAgentMessage(ctx, "owner", caller, target, "Please check this", "", "submission-first")
	if err != nil || retry["message_id"] != receipt["message_id"] {
		t.Fatalf("uncertain first-send duplicate: %v %v", retry, err)
	}
	fresh, err := env.api.sendAgentMessage(ctx, "owner", caller, target, "Independent agent", "", "")
	if err != nil || fresh["inbox_id"] == id {
		t.Fatalf("fresh agent inbox merged: %v %v", fresh, err)
	}
	// Wait for the second delivery before restoring the injected model turn.
	secondID := fresh["inbox_id"].(string)
	for time.Now().Before(deadline) {
		agentMessageMu.Lock()
		s, _ := readAgentMessageStore(ctx)
		agentMessageMu.Unlock()
		done := false
		for _, c := range s.Conversations {
			if c.ID == secondID && len(c.Messages) > 0 && c.Messages[0].Delivery == "delivered" {
				done = true
			}
		}
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	foreign := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerUser, ID: "other"}}
	if _, err = env.api.readAgentMessages(ctx, "other", foreign, id, 0, 30, 0); err == nil {
		t.Fatal("foreign caller read inbox")
	}
	if _, err = env.api.readAgentMessages(ctx, "owner", caller, "", 0, 30, 0); err == nil {
		t.Fatal("ambiguous cursor accepted")
	}
}

func TestAgentMessageWakeupsStayExplicit(t *testing.T) {
	env := newTriggerLinkEnv(t)
	ctx := internalBotRequestContext(context.Background(), "owner")
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ID: "alpha", ProfileID: "work"}, Label: "Alpha", Path: linkAlphaPath, Chat: &codeChat{Key: "session:alpha", SessionID: "alpha"}}
	target, err := resolveTriggerTarget(ctx, GetUserFromContext(ctx), "crew:Beta")
	if err != nil {
		t.Fatal(err)
	}
	c := agentConversation{ID: "inbox-existing", Endpoints: [2]agentMessageEndpoint{callerMessageEndpoint("owner", caller), targetMessageEndpoint("owner", target)}}
	agentMessageMu.Lock()
	err = saveAgentMessageStore(ctx, agentMessageStore{Conversations: []agentConversation{c}})
	agentMessageMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	wake, err := env.api.scheduleAgentMessageWakeup(ctx, "owner", caller, "schedule", "", "Read inbox and decide whether to follow up", 600)
	if err != nil {
		t.Fatal(err)
	}
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	agentMessageMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if materializeAgentWakeups(&s, time.Now()) {
		t.Fatal("timer fired early")
	}
	if !materializeAgentWakeups(&s, time.Now().Add(601*time.Second)) {
		t.Fatal("due timer not materialized")
	}
	if materializeAgentWakeups(&s, time.Now().Add(time.Hour)) {
		t.Fatal("timer resent repeatedly")
	}
	if s.Wakeups[0].ID != wake["wakeup_id"] || s.Conversations[1].Endpoints[1].Session != "alpha" || !s.Conversations[1].Messages[0].Wakeup {
		t.Fatalf("timer lost exact session: %+v", s)
	}
	if n := expirePendingAgentMessages(&s, time.Now().Add(48*time.Hour)); n != 0 || s.Conversations[1].Messages[0].Delivery != "pending" {
		t.Fatal("peer backlog expiry cleared a scheduled wakeup")
	}
}

// A Crew that sets a wakeup from the chat a message started must be woken in
// that same chat. Its caller key says "session:<id>" (the main-chat form), which
// resolved to a chat that does not exist and left the wakeup undelivered.
func TestWakeupFromAMessageChatWakesThatChat(t *testing.T) {
	env := newTriggerLinkEnv(t)
	ctx := internalBotRequestContext(context.Background(), "owner")
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ID: "alpha", ProfileID: "work"}, Label: "Alpha", Path: linkAlphaPath, Chat: &codeChat{Key: "session:alpha", SessionID: "alpha"}}
	known := agentConversation{ID: "inbox-known", Endpoints: [2]agentMessageEndpoint{
		{UserID: "owner", Kind: triggerCallerUser, ID: "owner", External: true},
		{UserID: "owner", Kind: triggerCallerCrew, ID: "alpha", Profile: "work", Session: "alpha", ChatKey: "messages:inbox-known"}}}
	agentMessageMu.Lock()
	err := saveAgentMessageStore(ctx, agentMessageStore{Conversations: []agentConversation{known}})
	agentMessageMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = env.api.scheduleAgentMessageWakeup(ctx, "owner", caller, "schedule", "", "Check back", 60); err != nil {
		t.Fatal(err)
	}
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	agentMessageMu.Unlock()
	if err != nil || len(s.Wakeups) != 1 || s.Wakeups[0].Endpoint.ChatKey != "messages:inbox-known" {
		t.Fatalf("wakeup not bound to the message chat: %+v %v", s.Wakeups, err)
	}
}

func TestFixedMainAndPhysicalChatWakeupsStayPinned(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	ctx := internalBotRequestContext(context.Background(), "owner")
	profile, err := env.svc.registry.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	binding, owned, err := resolveConversationBindingForUser(ctx, "owner", profile, "alpha")
	if err != nil || !owned {
		t.Fatal(err)
	}
	store := defaultProductConversationRegistryStore()
	original, err := store.resolveOrCreate(ctx, "owner", profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	targetBinding, err := resolveProductConversationBinding(ctx, "owner", profile, "alpha:chat:reviewer")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.resolveOrCreate(ctx, "owner", profile, targetBinding, "")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := agentMessageEndpoint{UserID: "owner", Kind: triggerCallerCrew, ID: "alpha", Profile: "work", Path: linkAlphaPath, Label: "Alpha", Session: original.SessionID, ChatKey: "alpha"}
	old := agentMessageTurn
	t.Cleanup(func() { agentMessageTurn = old })
	received := ""
	agentMessageTurn = func(_ *StreamingAPI, _ context.Context, _ map[string]interface{}, session, _ string) (internalSessionTurnResult, error) {
		received = session
		return internalSessionTurnResult{}, nil
	}
	for _, check := range []struct{ key, want string }{{"alpha", original.SessionID}, {target.ConversationKey, target.SessionID}, {"session:" + original.SessionID, original.SessionID}} {
		// Each address carries its own receiving chat's session.
		endpoint.ChatKey, endpoint.Session = check.key, check.want
		conversation := agentConversation{ID: "wakeup-pointer-test", Endpoints: [2]agentMessageEndpoint{endpoint, endpoint}}
		if _, err := env.api.deliverAgentMessage(ctx, conversation, agentMessage{From: 0, Text: "wake up", Wakeup: true}); err != nil {
			t.Fatal(err)
		}
		if received != check.want {
			t.Fatalf("address %q reached %s, want %s", check.key, received, check.want)
		}
	}
}
