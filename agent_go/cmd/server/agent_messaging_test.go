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
	env.api.agentProfiles = env.svc.registry
	crewOwnerTabsFixture(t, env)
	ctx := internalBotRequestContext(context.Background(), "owner")
	target, err := resolveTriggerTarget(ctx, GetUserFromContext(ctx), "crew:Beta")
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
	receiver := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ProfileID: "work", ID: target.stampID()}, Label: target.Label, Path: target.Path, Chat: &codeChat{SessionID: conv.Endpoints[1].Session}}
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

// A Crew that sets a wakeup from the chat a message started must be woken in
// that same chat. Its caller key says "session:<id>" (the main-chat form), which
// resolved to a chat that does not exist and left the wakeup undelivered.

func TestFixedMainAndPhysicalMessageChatsStayPinned(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	root := crewOwnerTabsFixture(t, env)
	ctx := internalBotRequestContext(context.Background(), "owner")
	profile, err := env.svc.registry.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	binding, owned, err := resolveConversationBindingForUser(ctx, "owner", profile, "beta")
	if err != nil || !owned {
		t.Fatal(err)
	}
	store := defaultProductConversationRegistryStore()
	original, err := store.resolveOrCreate(ctx, "owner", profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	targetBinding, err := resolveProductConversationBinding(ctx, "owner", profile, "beta:chat:reviewer")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.resolveOrCreate(ctx, "owner", profile, targetBinding, "")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := agentMessageEndpoint{UserID: "owner", Kind: triggerCallerCrew, ID: "beta", Profile: "work", Path: root, Label: "Beta", Session: original.SessionID, ChatKey: "beta"}
	old := agentMessageTurn
	t.Cleanup(func() { agentMessageTurn = old })
	received := ""
	agentMessageTurn = func(_ *StreamingAPI, _ context.Context, _ map[string]interface{}, session, _ string) (internalSessionTurnResult, error) {
		received = session
		return internalSessionTurnResult{}, nil
	}
	for _, check := range []struct{ key, want string }{{"beta", original.SessionID}, {target.ConversationKey, target.SessionID}, {"session:" + original.SessionID, original.SessionID}} {
		// Each address carries its own receiving chat's session.
		endpoint.ChatKey, endpoint.Session = check.key, check.want
		conversation := agentConversation{ID: "message-pointer-test", Endpoints: [2]agentMessageEndpoint{endpoint, endpoint}}
		if _, err := env.api.deliverAgentMessage(ctx, conversation, agentMessage{From: 0, Text: "wake up"}); err != nil {
			t.Fatal(err)
		}
		if received != check.want {
			t.Fatalf("address %q reached %s, want %s", check.key, received, check.want)
		}
	}
}

// Owner, 2026-11-11: only a same-person reply to what a chat asked may enter its running turn; another person's
// agent, a guest or Run chat, and the asker's own follow-up keep their own turn and admission.
func TestOnlySamePersonRepliesSteerIntoTheAskingTurn(t *testing.T) {
	asker := agentMessageEndpoint{UserID: "owner", Kind: triggerCallerCrew, ID: "crew", Session: "product-asker", Mode: "builder"}
	replier := agentMessageEndpoint{UserID: "owner", Kind: triggerCallerUser, ID: "reviewer", External: true}
	conv := func(a, r agentMessageEndpoint) agentConversation {
		return agentConversation{ID: "inbox-1", Endpoints: [2]agentMessageEndpoint{a, r}}
	}
	other := replier
	other.UserID = "colleague"
	runAsker := asker
	runAsker.Mode = "run"
	guestAsker := asker
	guestAsker.Guest = "colleague"
	for _, tc := range []struct {
		name string
		c    agentConversation
		m    agentMessage
		want bool
	}{
		{"reply from own agent", conv(asker, replier), agentMessage{From: 1}, true},
		{"asker follow-up", conv(asker, replier), agentMessage{From: 0}, false},
		{"another person's agent", conv(asker, other), agentMessage{From: 1}, false},
		{"Run chat asker", conv(runAsker, replier), agentMessage{From: 1}, false},
		{"guest asker", conv(guestAsker, replier), agentMessage{From: 1}, false},
		{"person ping", conv(asker, replier), agentMessage{From: 1, Kind: "ping"}, false},
	} {
		if got := agentMessageSteerableReply(tc.c, tc.m); got != tc.want {
			t.Errorf("%s: steerable=%v, want %v", tc.name, got, tc.want)
		}
	}
}
