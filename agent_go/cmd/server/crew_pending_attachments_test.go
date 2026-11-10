package server

import (
	"context"
	"strings"
	"testing"
)

// PLAT-746-828: an upload planned by someone else, in another Crew/tab/session,
// or never planned must not become an attachment to this person's message.
func TestPendingChatAttachmentRequiresSameCallerProjectConversationAndSession(t *testing.T) {
	hash := strings.Repeat("a", 64)
	conversation := "project-example:chat:side"
	req := QueryRequest{AgentProfileID: crewProfileID, AgentProfileConversationKey: conversation, Query: "Read the image", PendingAttachments: []pendingChatAttachment{{Hash: hash, Name: "ignored-client-path"}}}
	key := pendingAttachmentKey("person", "session", req, hash)
	api := &StreamingAPI{}
	api.planPendingChatUpload(key)
	if err := api.validatePendingChatAttachments("person", "session", req); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, user, session, conversation, hash string }{
		{"other person", "other", "session", conversation, hash},
		{"other project", "person", "session", "other-project:chat:side", hash},
		{"other tab", "person", "session", "project-example:chat:another", hash},
		{"other session", "person", "another", conversation, hash},
		{"unplanned hash", "person", "session", conversation, strings.Repeat("b", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := req
			input.AgentProfileConversationKey = tc.conversation
			input.PendingAttachments = []pendingChatAttachment{{Hash: tc.hash}}
			if err := api.validatePendingChatAttachments(tc.user, tc.session, input); err == nil {
				t.Fatal("accepted an upload outside its planned conversation identity")
			}
		})
	}
	// Only the authorized writer's committed path reaches the prompt, not Name.
	api.finishPendingChatUpload(key, "Crew/example/uploads/image.png")
	if err := api.resolvePendingChatAttachments(context.Background(), "person", "session", &req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Query, "Crew/example/uploads/image.png") || strings.Contains(req.Query, "ignored-client-path") || len(req.PendingAttachments) != 0 {
		t.Fatalf("wrong resolved request: %#v", req)
	}
}

func TestUnfinishedAttachmentCancellationNeverSendsTextAlone(t *testing.T) {
	req := QueryRequest{AgentProfileID: crewProfileID, AgentProfileConversationKey: "project-example:chat:side", Query: "Read the image", PendingAttachments: []pendingChatAttachment{{Hash: strings.Repeat("a", 64)}}}
	api := &StreamingAPI{}
	api.planPendingChatUpload(pendingAttachmentKey("person", "session", req, req.PendingAttachments[0].Hash))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := api.resolvePendingChatAttachments(ctx, "person", "session", &req); err == nil {
		t.Fatal("unfinished attachment did not refuse delivery")
	}
	if req.Query != "Read the image" || len(req.PendingAttachments) != 1 {
		t.Fatal("failed upload consumed or partially modified the message")
	}
	other := req
	other.PendingAttachments = []pendingChatAttachment{{Hash: strings.Repeat("b", 64)}}
	if pendingAttachmentSubmissionQuery(req) == pendingAttachmentSubmissionQuery(other) {
		t.Fatal("submission identity ignored different attachments")
	}
}

// Reviewer follow-up: a failed write refuses promptly, and a new plan can retry.
func TestFailedPendingAttachmentRefusesImmediatelyAndCanBePlannedAgain(t *testing.T) {
	api := &StreamingAPI{}
	req := QueryRequest{AgentProfileID: crewProfileID, AgentProfileConversationKey: "project-example:chat:side", Query: "Read image", PendingAttachments: []pendingChatAttachment{{Hash: strings.Repeat("a", 64)}}}
	key := pendingAttachmentKey("person", "session", req, req.PendingAttachments[0].Hash)
	api.planPendingChatUpload(key)
	entry, err := api.pendingChatUpload(key)
	if err != nil {
		t.Fatal(err)
	}
	api.failPendingChatUpload(key)
	select {
	case <-entry.Ready:
	default:
		t.Fatal("failed file did not wake its waiting message")
	}
	if err := api.validatePendingChatAttachments("person", "session", req); err == nil {
		t.Fatal("failed upload accepted")
	}
	api.planPendingChatUpload(key)
	api.finishPendingChatUpload(key, "Crew/example/uploads/retry.png")
	if err := api.resolvePendingChatAttachments(context.Background(), "person", "session", &req); err != nil {
		t.Fatal(err)
	}
}

// PR2048 review: queue replay must retain the accepted optimistic/submission ID.
func TestPendingAttachmentReplayRetainsSubmissionFingerprintAndAllowsClaimedSteer(t *testing.T) {
	user := GetDefaultUserID()
	req := QueryRequest{AgentProfileID: crewProfileID, AgentProfileConversationKey: "project-example:chat:side", Query: "Read image", PendingAttachments: []pendingChatAttachment{{Hash: strings.Repeat("a", 64)}}}
	turn := queuedConversationTurn{ID: "turn", SubmissionID: "submission", UserID: user, SessionID: "chat", Request: req, Principal: queuedPrincipalFromContext(internalBotRequestContext(context.Background(), user, nil))}
	ctx, err := (&StreamingAPI{}).queuedConversationTurnContext(turn, nil)
	if err != nil {
		t.Fatal(err)
	}
	submitted := ctx.Value(chatSubmissionContextKey{}).(chatSubmissionContext)
	if submitted.Message != pendingAttachmentSubmissionQuery(req) || submitted.ID != queuedTurnDisplayID(turn) {
		t.Fatal("queue replay no longer matches the admitted submission")
	}
	if !shouldQueuePendingAttachments(context.Background(), req) {
		t.Fatal("new upload message bypasses durable admission")
	}
	if shouldQueuePendingAttachments(withConversationTurnQueueExecution(ctx), req) || shouldQueuePendingAttachments(context.WithValue(ctx, queuedMessageSteerKey{}, true), req) {
		t.Fatal("claimed delivery would be sent back into queue admission")
	}
}

// PR2048 review: recalling a completed queued upload must restore its image,
// not only its text. Incomplete uploads cannot be removed for editing yet.
func TestPendingAttachmentRecallProjectsOnlyReadyPathsForSameConversation(t *testing.T) {
	api := &StreamingAPI{}
	req := QueryRequest{AgentProfileID: crewProfileID, AgentProfileConversationKey: "project-example:chat:side", Query: "Read image", PendingAttachments: []pendingChatAttachment{{Hash: strings.Repeat("a", 64)}}}
	turn := queuedConversationTurn{ID: "turn", UserID: "person", SessionID: "chat", Request: req}
	key := pendingAttachmentKey(turn.UserID, turn.SessionID, req, req.PendingAttachments[0].Hash)
	api.planPendingChatUpload(key)
	rows := []PendingQueuedMessage{{MessageID: turn.ID, Content: req.Query}}
	api.projectPendingChatAttachments(turn.UserID, turn.SessionID, []queuedConversationTurn{turn}, rows)
	if !rows[0].AttachmentPending || len(rows[0].AttachmentPaths) != 0 {
		t.Fatal("incomplete attachment can be recalled without its file")
	}
	api.finishPendingChatUpload(key, "Crew/example/uploads/image.png")
	rows = []PendingQueuedMessage{{MessageID: turn.ID, Content: req.Query}}
	api.projectPendingChatAttachments(turn.UserID, turn.SessionID, []queuedConversationTurn{turn}, rows)
	if rows[0].AttachmentPending || len(rows[0].AttachmentPaths) != 1 || rows[0].AttachmentPaths[0] != "Crew/example/uploads/image.png" {
		t.Fatal("completed queued image was lost on recall")
	}
	rows = []PendingQueuedMessage{{MessageID: turn.ID}}
	api.projectPendingChatAttachments("other", turn.SessionID, []queuedConversationTurn{turn}, rows)
	if len(rows[0].AttachmentPaths) != 0 {
		t.Fatal("queued attachment path crossed people")
	}
}
