package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Exercise delivery with the real workspace transport and permissions, while
// replacing only the model turn. Final prose never becomes a conversational reply.
func TestAgentMessagesRequireExplicitRepliesAndKeepInboxOwnership(t *testing.T) {
	env := newTriggerLinkEnv(t)
	ctx := internalBotRequestContext(context.Background(), "owner")
	target, err := resolveTriggerTarget(ctx, GetUserFromContext(ctx), "workflow:Reports")
	if err != nil {
		t.Fatal(err)
	}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerUser, ID: "owner"}, Label: "External agent"}
	old := agentMessageTurn
	agentMessageTurn = func(_ *StreamingAPI, _ context.Context, _ map[string]interface{}, _, _ string) (internalSessionTurnResult, error) {
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

func TestAgentMessagingOffBlocksRepliesAndWakeupsStayExplicit(t *testing.T) {
	env := newTriggerLinkEnv(t)
	ctx := internalBotRequestContext(context.Background(), "owner")
	env.mock.mu.Lock()
	env.mock.files[linkBetaPath+"/product.json"] = `{"schema_version":1,"product":"work","id":"beta","title":"Beta","capabilities":{"free_text_ask":false}}`
	env.mock.mu.Unlock()
	target, err := resolveTriggerTarget(ctx, GetUserFromContext(ctx), "crew:Beta")
	if err != nil {
		t.Fatal(err)
	}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ID: "alpha", ProfileID: "work"}, Label: "Alpha", Path: linkAlphaPath, Chat: &codeChat{Key: "session:alpha", SessionID: "alpha"}}
	if _, err = env.api.sendAgentMessage(ctx, "owner", caller, target, "Hello", "", ""); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled recipient admitted: %v", err)
	}
	// The same gate applies to a reply in an already-known address.
	c := agentConversation{ID: "inbox-existing", Endpoints: [2]agentMessageEndpoint{callerMessageEndpoint("owner", caller), targetMessageEndpoint("owner", target)}}
	agentMessageMu.Lock()
	err = saveAgentMessageStore(ctx, agentMessageStore{Conversations: []agentConversation{c}})
	agentMessageMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = env.api.sendAgentMessage(ctx, "owner", caller, triggerTarget{}, "A later reply", c.ID, ""); err == nil {
		t.Fatal("existing inbox bypassed messaging off")
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
}
