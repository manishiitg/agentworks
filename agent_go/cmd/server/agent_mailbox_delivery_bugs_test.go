package server

import (
	"context"
	"testing"
	"time"
)

func TestCallerAgentMailboxConversationMatchesPublishedAgentWithHostedIdentity(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"asker","username":"Asker"},{"id":"owner","username":"Owner"},{"id":"other","username":"Other"}]}`)

	conv := agentConversation{
		ID:   "inbox-pub-1",
		Next: 1,
		Endpoints: [2]agentMessageEndpoint{
			{UserID: "asker", Kind: triggerCallerUser, ID: "asker", Label: "Asker"},
			{
				UserID:         "owner",
				Kind:           triggerCallerCrew,
				ID:             "proj-1",
				Profile:        crewProfileID,
				Label:          "Published Agent",
				Session:        "sess-pub-1",
				ChatKey:        "proj-1:chat:helper",
				PublishedAgent: "proj-1:chat:helper",
				PublishedAsker: "asker",
				ReplyInbox:     "inbox-pub-1",
				External:       false,
			},
		},
		Messages: []agentMessage{{
			ID:       "msg-pub-1",
			Sequence: 1,
			From:     0,
			Text:     "hello published agent",
			SentAt:   time.Now().UTC(),
			Delivery: "delivered",
		}},
	}
	if err := saveAgentMessageStore(t.Context(), agentMessageStore{Conversations: []agentConversation{conv}}); err != nil {
		t.Fatal(err)
	}
	mb, err := currentAgentMailbox()
	if err != nil {
		t.Fatal(err)
	}

	sqlMatches := func(userID string, caller triggerLinkCaller) bool {
		t.Helper()
		a, aArgs := agentMailboxEndpointSQL("a", userID, caller)
		b, bArgs := agentMailboxEndpointSQL("b", userID, caller)
		args := append(append([]any{"inbox-pub-1"}, aArgs...), bArgs...)
		var count int
		if err := mb.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM conversations WHERE inbox_id = ? AND (`+a+` OR `+b+`)`, args...).Scan(&count); err != nil {
			t.Fatalf("SQL prefilter query failed: %v", err)
		}
		return count == 1
	}

	// Hosted bridge calls rewrite the caller's NamedAgentID and Stamp to the owner's hosted identity.
	hostedCaller := triggerLinkCaller{
		NamedAgentID: "hosted-owner-agent",
		Stamp:        triggerCaller{Type: triggerCallerUser, ID: "owner"},
		Chat:         &codeChat{Key: "proj-1:chat:helper", SessionID: "sess-pub-1"},
	}
	if !messageEndpointMatches(conv.Endpoints[1], "owner", hostedCaller) {
		t.Fatal("expected messageEndpointMatches to accept published agent with hosted identity")
	}
	if !sqlMatches("owner", hostedCaller) {
		t.Fatal("agentMailboxEndpointSQL over-filtered published agent with hosted identity")
	}

	// Wrong user, wrong session, or wrong chat key must be refused by both SQL and Go checks.
	if sqlMatches("other", hostedCaller) || messageEndpointMatches(conv.Endpoints[1], "other", hostedCaller) {
		t.Fatal("wrong user matched published endpoint")
	}
	wrongSession := hostedCaller
	wrongSession.Chat = &codeChat{Key: "proj-1:chat:helper", SessionID: "sess-other"}
	if sqlMatches("owner", wrongSession) || messageEndpointMatches(conv.Endpoints[1], "owner", wrongSession) {
		t.Fatal("wrong session matched published endpoint")
	}
	wrongKey := hostedCaller
	wrongKey.Chat = &codeChat{Key: "proj-1:chat:other", SessionID: "sess-pub-1"}
	if sqlMatches("owner", wrongKey) || messageEndpointMatches(conv.Endpoints[1], "owner", wrongKey) {
		t.Fatal("wrong chat key matched published endpoint")
	}
}

func TestMarkReadAgentMessagesDeliveredStampsDeliveredAt(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	now := time.Now().UTC().Add(-time.Minute)
	conv := agentConversation{
		ID:   "inbox-read-1",
		Next: 1,
		Endpoints: [2]agentMessageEndpoint{
			{UserID: "owner", Kind: triggerCallerCrew, ID: "proj-1", Session: "sess-a"},
			{UserID: "owner", Kind: triggerCallerCrew, ID: "proj-1", Session: "sess-b"},
		},
		Messages: []agentMessage{{
			ID:       "msg-read-1",
			Sequence: 1,
			From:     0,
			Text:     "please check",
			SentAt:   now,
			Delivery: "pending",
		}},
	}
	if err := saveAgentMessageStore(t.Context(), agentMessageStore{Conversations: []agentConversation{conv}}); err != nil {
		t.Fatal(err)
	}

	markReadAgentMessagesDelivered(t.Context(), "inbox-read-1", "sess-b", map[string]bool{"msg-read-1": true})

	store, err := readAgentMessageStore(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Conversations) != 1 || len(store.Conversations[0].Messages) != 1 {
		t.Fatalf("unexpected store shape: %+v", store.Conversations)
	}
	msg := store.Conversations[0].Messages[0]
	if msg.Delivery != "delivered" {
		t.Fatalf("Delivery = %q, want delivered", msg.Delivery)
	}
	if msg.DeliveredAt.IsZero() {
		t.Fatal("DeliveredAt was not stamped on read delivery")
	}
}

func TestAgentDeliveryTimeoutAndDeliveredAt(t *testing.T) {
	// Pre-turn timeout must never be counted as delivered.
	if agentDeliveryTimedOutRunning(false, false, context.DeadlineExceeded, context.DeadlineExceeded) {
		t.Fatal("pre-turn deadline exceeded was wrongly treated as still running")
	}
	if !agentDeliveryTimedOutRunning(false, true, context.DeadlineExceeded, context.DeadlineExceeded) {
		t.Fatal("in-turn deadline exceeded should be treated as still running")
	}
	if agentDeliveryTimedOutRunning(true, true, context.DeadlineExceeded, context.DeadlineExceeded) {
		t.Fatal("steered deadline exceeded should not be treated as still running")
	}

	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	root := crewOwnerTabsFixture(t, env)
	ctx := internalBotRequestContext(context.Background(), "owner")
	profile, err := env.svc.registry.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := resolveProductConversationBinding(ctx, "owner", profile, "beta:chat:reviewer")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, "owner", profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}

	oldTurn := agentMessageTurn
	oldWait := agentMessageDeliveryWait
	t.Cleanup(func() {
		agentMessageTurn = oldTurn
		agentMessageDeliveryWait = oldWait
	})
	agentMessageDeliveryWait = 40 * time.Millisecond

	endpoint := agentMessageEndpoint{
		UserID:  "owner",
		Kind:    triggerCallerCrew,
		ID:      "beta",
		Profile: "work",
		Path:    root,
		Label:   "Beta",
		Session: conversation.SessionID,
		ChatKey: conversation.ConversationKey,
	}

	waitForMessage := func(inboxID, msgID string) agentMessage {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			agentMessageMu.Lock()
			st, readErr := readAgentMessageStore(ctx)
			agentMessageMu.Unlock()
			if readErr == nil {
				for _, c := range st.Conversations {
					if c.ID == inboxID {
						for _, m := range c.Messages {
							if m.ID == msgID && m.Delivery != "pending" && m.Delivery != "dispatching" {
								return m
							}
						}
					}
				}
			}
			time.Sleep(15 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s/%s delivery to settle", inboxID, msgID)
		return agentMessage{}
	}

	// 1) Normal internal delivery stamps DeliveredAt.
	agentMessageTurn = func(_ *StreamingAPI, _ context.Context, _ map[string]interface{}, _, _ string) (internalSessionTurnResult, error) {
		return internalSessionTurnResult{}, nil
	}
	if err := saveAgentMessageStore(ctx, agentMessageStore{Conversations: []agentConversation{{
		ID:        "inbox-normal",
		Next:      1,
		Endpoints: [2]agentMessageEndpoint{endpoint, endpoint},
		Messages:  []agentMessage{{ID: "msg-normal", Sequence: 1, From: 0, Text: "hi", SentAt: time.Now().UTC(), Delivery: "pending"}},
	}}}); err != nil {
		t.Fatal(err)
	}
	env.api.kickAgentMessages("inbox-normal")
	got := waitForMessage("inbox-normal", "msg-normal")
	if got.Delivery != "delivered" || got.DeliveredAt.IsZero() {
		t.Fatalf("normal delivery = %q delivered_at=%v, want delivered + stamped", got.Delivery, got.DeliveredAt)
	}

	// 2) Turn that actually started and outlived deliveryWait is counted as delivered + stamped.
	agentMessageTurn = func(_ *StreamingAPI, turnCtx context.Context, _ map[string]interface{}, _, _ string) (internalSessionTurnResult, error) {
		<-turnCtx.Done()
		return internalSessionTurnResult{}, turnCtx.Err()
	}
	if err := saveAgentMessageStore(ctx, agentMessageStore{Conversations: []agentConversation{{
		ID:        "inbox-timeout-started",
		Next:      1,
		Endpoints: [2]agentMessageEndpoint{endpoint, endpoint},
		Messages:  []agentMessage{{ID: "msg-timeout-started", Sequence: 1, From: 0, Text: "slow turn", SentAt: time.Now().UTC(), Delivery: "pending"}},
	}}}); err != nil {
		t.Fatal(err)
	}
	env.api.kickAgentMessages("inbox-timeout-started")
	got = waitForMessage("inbox-timeout-started", "msg-timeout-started")
	if got.Delivery != "delivered" || got.DeliveredAt.IsZero() {
		t.Fatalf("in-turn timeout = %q delivered_at=%v, want delivered + stamped", got.Delivery, got.DeliveredAt)
	}
}
