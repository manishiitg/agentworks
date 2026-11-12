package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// PLAT-746: a busy chat's pending person inputs are claimed together, not
// executed as three obsolete turns. Source identities/receipts remain separate.
func TestQueuedMessagesCombineAtClaimAndRetainEachResult(t *testing.T) {
	files := map[string]string{}
	api := newConversationTurnQueueTestAPI(files)
	const user, session = "owner", "chat"
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user})
	now := time.Now().UTC()
	turns := []queuedConversationTurn{}
	for i := 0; i < 3; i++ {
		turns = append(turns, queuedConversationTurn{ID: fmt.Sprint("row-", i), UserID: user, SessionID: session, SubmissionID: fmt.Sprint("typed-", i), ComposerInput: true, Request: QueryRequest{Query: fmt.Sprint("task-", i), AgentMode: "multi-agent"}, CreatedAt: now.Add(time.Duration(i-3) * time.Minute)})
	}
	if err := api.writeConversationTurnQueue(ctx, user, turns); err != nil {
		t.Fatal(err)
	}
	api.registerConversationQueueOwnerLocked(user, session)
	claimed, ok, err := api.claimOldestConversationTurn(ctx, session)
	if err != nil || !ok || len(claimed.Batch) != 3 {
		t.Fatalf("batch not claimed: ok=%t members=%d err=%v", ok, len(claimed.Batch), err)
	}
	for _, row := range api.mustReadTurnQueueForTest(t, user) {
		if row.StartedAt == nil || row.BatchID != claimed.ID {
			t.Fatal("source row not durably claimed with batch")
		}
	}
	if _, ok, err := api.claimOldestConversationTurn(ctx, session); ok || err != nil {
		t.Fatal("claimed batch was dispatched twice")
	}
	prepared, err := api.prepareQueuedBatch(ctx, claimed, now)
	if err != nil {
		t.Fatal(err)
	}
	previous := -1
	for _, row := range turns {
		at := strings.Index(prepared.Request.Query, row.Request.Query)
		if at <= previous || !strings.Contains(prepared.Request.Query, row.CreatedAt.Format("2006-01-02 15:04 MST")) {
			t.Fatal("batch lost chronology/timestamp")
		}
		previous = at
	}
	if prepared.Principal.DeliveredMessage["source"] != "queued_batch" {
		t.Fatal("batch falsely attributes all messages to one sender")
	}
	result := internalSessionTurnResult{QueryID: "one-execution", FinalResponse: "combined answer"}
	for _, row := range claimed.Batch {
		waiter := api.registerQueuedTurnWaiter(row.ID, nil)
		api.finishQueuedConversationTurn(row, result, nil)
		if got := <-waiter; got.Err != nil || got.Result.QueryID != result.QueryID {
			t.Fatal("source waiter lost exact combined execution result")
		}
	}
	if len(api.mustReadTurnQueueForTest(t, user)) != 0 {
		t.Fatal("batch left source rows to replay")
	}
}

func TestQueuedBatchKeepsPrincipalBoundariesAgentReplyAndAttachments(t *testing.T) {
	api := newConversationTurnQueueTestAPI(map[string]string{})
	now := time.Now().UTC()
	base := queuedConversationTurn{ID: "first", UserID: "owner", SessionID: "chat", SubmissionID: "typed-first", ComposerInput: true, CreatedAt: now.Add(-3 * time.Minute), Request: QueryRequest{Query: "person request", AgentMode: "multi-agent"}}
	other := base
	other.ID = "second"
	other.SubmissionID = "typed-second"
	other.CreatedAt = now.Add(-2 * time.Minute)
	for _, kind := range []string{"guest", "token", "different_owner", "reply_grant", "runtime", "restart", "quota", "steer"} {
		t.Run(kind, func(t *testing.T) {
			first, next := base, other
			switch kind {
			case "guest":
				next.Request.PinRunMode = true
			case "token":
				next.Principal.AccessTokenID = "different-grant"
			case "different_owner":
				next.UserID = "other"
			case "reply_grant":
				first.Request.SharedAgentSender = "visitor"
				next.Request.SharedAgentSender = "visitor"
				first.Principal.DeliveredMessage = map[string]interface{}{"app_agent_reply_to": "inbox-one"}
				next.Principal.DeliveredMessage = map[string]interface{}{"app_agent_reply_to": "inbox-two"}
			case "runtime":
				next.Request.ModelID = "different-model"
			case "restart":
				next.Request.TriggeredBy = "restart_continue"
			case "quota":
				at := now.Add(time.Hour)
				next.RetryAt = &at
			case "steer":
				next.Steering = true
			}
			claimed, _ := api.claimQueuedBatch([]queuedConversationTurn{first, next}, first, nil, now)
			if len(claimed.Batch) != 0 {
				t.Fatal("incompatible authority/runtime/control rows combined")
			}
		})
	}
	// An intervening other-owner head cannot be overtaken by a later same-owner row.
	boundary := other
	boundary.UserID = "other"
	boundary.CreatedAt = base.CreatedAt.Add(time.Second)
	claimed, _ := api.claimQueuedBatch([]queuedConversationTurn{base, other}, base, &boundary, now)
	if len(claimed.Batch) != 0 {
		t.Fatal("batch overtook another principal's earlier head")
	}
	// Each original agent reply instruction stays attached to its own message.
	agent := other
	agent.SubmissionID = ""
	agent.ComposerInput = false
	agent.Request.Query = "Reply using messages/send inbox_id=inbox-example"
	agent.Request.AgentMessageInbox = "inbox-example"
	agent.Principal.DeliveredMessage = map[string]interface{}{"source": "agent_message", "sender_label": "Example agent"}
	agent.Principal.DeliveredMessageUser, agent.Principal.DeliveredMessageSession = agent.UserID, agent.SessionID
	base.Request.AgentProfileID = "work"
	base.Request.AgentProfileConversationKey = "project:chat"
	agent.Request.AgentProfileID = base.Request.AgentProfileID
	agent.Request.AgentProfileConversationKey = base.Request.AgentProfileConversationKey
	hash := strings.Repeat("a", 64)
	agent.Request.PendingAttachments = []pendingChatAttachment{{Hash: hash, Name: "example.png"}}
	key := pendingAttachmentKey(agent.UserID, agent.SessionID, agent.Request, hash)
	api.planPendingChatUpload(key)
	api.finishPendingChatUpload(key, "Crew/example/uploads/example.png")
	claimed, _ = api.claimQueuedBatch([]queuedConversationTurn{base, agent}, base, nil, now)
	if len(claimed.Batch) != 2 {
		t.Fatal("same-owner person/agent rows did not combine")
	}
	prepared, err := api.prepareQueuedBatch(context.Background(), claimed, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{agent.Request.Query, "from Example agent:", "Crew/example/uploads/example.png"} {
		if !strings.Contains(prepared.Request.Query, text) {
			t.Fatal("batch lost agent reply instruction, sender or attachment")
		}
	}
	if prepared.Request.AgentMessageInbox != "inbox-example" || len(prepared.Request.PendingAttachments) != 0 {
		t.Fatal("batch lost agent attendance or left attachments unresolved")
	}
}

func TestQueuedWaitingAgeStartsOnlyAfterFifteenMinutes(t *testing.T) {
	now := time.Now().UTC()
	for _, age := range []time.Duration{14*time.Minute + 59*time.Second, 15 * time.Minute, 40 * time.Minute} {
		row := queuedConversationTurn{ID: "row", UserID: "owner", SessionID: "chat", SubmissionID: "typed", ComposerInput: true, CreatedAt: now.Add(-age), Request: QueryRequest{Query: "pending"}}
		messages := pendingQueuedMessagesFromTurnsAt([]queuedConversationTurn{row}, "owner", "chat", now)
		if len(messages) != 1 || (messages[0].WaitingSince != nil) != (age >= 15*time.Minute) {
			t.Fatalf("wrong age visibility at %s", age)
		}
	}
}
