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
