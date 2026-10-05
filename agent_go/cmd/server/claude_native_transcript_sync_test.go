package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentevents "github.com/manishiitg/mcpagent/events"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

func TestClaudeNativeTranscriptProjectSlugMatchesEscapingScheme(t *testing.T) {
	got := claudeNativeTranscriptProjectSlug("/Users/mipl/ai-work/mcp-agent-builder-go/workspace-docs/Workflow/substack")
	want := "-Users-mipl-ai-work-mcp-agent-builder-go-workspace-docs-Workflow-substack"
	if got != want {
		t.Fatalf("unexpected slug:\n got: %s\nwant: %s", got, want)
	}
}

func TestExtractClaudeTranscriptText(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "plain string content",
			content: `"can you check the workflow upgrades and do them"`,
			want:    "can you check the workflow upgrades and do them",
		},
		{
			name:    "assistant text block",
			content: `[{"type":"text","text":"Done - fixed both bugs."}]`,
			want:    "Done - fixed both bugs.",
		},
		{
			name:    "assistant text block mixed with thinking and tool_use is filtered to text only",
			content: `[{"type":"thinking","thinking":"internal reasoning"},{"type":"text","text":"Here is the answer."},{"type":"tool_use","id":"t1","name":"get_api_spec","input":{}}]`,
			want:    "Here is the answer.",
		},
		{
			name:    "tool_result-only content yields no visible text",
			content: `[{"tool_use_id":"toolu_1","type":"tool_result","content":[{"type":"text","text":"raw tool output"}]}]`,
			want:    "",
		},
		{
			name:    "empty content",
			content: ``,
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractClaudeTranscriptText(json.RawMessage(tc.content))
			if got != tc.want {
				t.Fatalf("unexpected text:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func writeTranscriptFixture(t *testing.T, path string, lines []string) {
	t.Helper()
	content := ""
	for _, line := range lines {
		content += line + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write transcript fixture: %v", err)
	}
}

func TestReadNewClaudeTranscriptMessagesFiltersBySinceAndKeepsOnlyRealChatText(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")

	writeTranscriptFixture(t, transcriptPath, []string{
		`{"type":"user","timestamp":"2026-08-22T08:00:00.000Z","message":{"role":"user","content":"before cutoff, should be excluded"}}`,
		`{"type":"user","timestamp":"2026-08-22T08:21:26.000Z","message":{"role":"user","content":"new question after resume"}}`,
		`{"type":"assistant","timestamp":"2026-08-22T08:22:00.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"..."},{"type":"text","text":"Here is the fix."}]}}`,
		`{"type":"user","timestamp":"2026-08-22T08:22:05.000Z","message":{"role":"user","content":[{"tool_use_id":"toolu_1","type":"tool_result","content":[{"type":"text","text":"tool output, not a chat message"}]}]}}`,
		`{"type":"queue-operation","timestamp":"2026-08-22T08:22:10.000Z","operation":"enqueue","content":"pasted text while busy"}`,
		`{"type":"system","timestamp":"2026-08-22T08:22:15.000Z","subtype":"turn_duration"}`,
	})

	// since = 2026-08-22T13:51:26+05:30 == 2026-08-22T08:21:26Z
	since, err := time.Parse(time.RFC3339, "2026-08-22T13:51:26+05:30")
	if err != nil {
		t.Fatalf("failed to parse since: %v", err)
	}

	messages, maxTimestamp, err := readNewClaudeTranscriptMessages(transcriptPath, since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(messages) != 1 {
		t.Fatalf("expected exactly 1 real chat message after cutoff (tool_result/queue-operation/system must be excluded), got %d: %+v", len(messages), messages)
	}
	if messages[0].Role != "ai" {
		t.Fatalf("unexpected role: %q", messages[0].Role)
	}
	if messages[0].Parts[0].Text != "Here is the fix." {
		t.Fatalf("unexpected text: %q", messages[0].Parts[0].Text)
	}

	wantMax, _ := time.Parse(time.RFC3339Nano, "2026-08-22T08:22:00.000Z")
	if !maxTimestamp.Equal(wantMax) {
		t.Fatalf("unexpected max timestamp: got %v want %v", maxTimestamp, wantMax)
	}
}

func TestReadNewClaudeTranscriptMessagesIgnoresNonHumanAttachments(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeTranscriptFixture(t, transcriptPath, []string{
		`{"type":"attachment","timestamp":"2026-09-20T21:05:10.493Z","attachment":{"type":"total_tokens_reminder","text":"<total_tokens>14972482 tokens left</total_tokens>"}}`,
		`{"type":"attachment","timestamp":"2026-09-20T21:05:26.602Z","attachment":{"type":"queued_command","prompt":"automated prompt","origin":{"kind":"system"},"humanTurn":false}}`,
	})

	messages, _, err := readNewClaudeTranscriptMessages(transcriptPath, time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected non-human attachments to stay hidden, got %+v", messages)
	}
}

func TestReadNewClaudeTranscriptMessagesReturnsNothingWhenNoEntriesAreNewer(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeTranscriptFixture(t, transcriptPath, []string{
		`{"type":"user","timestamp":"2026-08-22T08:00:00.000Z","message":{"role":"user","content":"old message"}}`,
	})

	since, _ := time.Parse(time.RFC3339, "2026-08-22T13:51:26+05:30")
	messages, _, err := readNewClaudeTranscriptMessages(transcriptPath, since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected no new messages, got %d", len(messages))
	}
}

func TestPublishNativeTranscriptRecoveredAssistantMessagesSkipsAlreadyVisibleReply(t *testing.T) {
	store := storeevents.NewEventStore(100)
	defer store.Stop()
	api := &StreamingAPI{eventStore: store}
	const sessionID = "cursor-retained"

	chunk := &agentevents.StreamingChunkEvent{Content: "Here are the links.", Source: agentevents.StreamingChunkSourceTranscript}
	store.AddEvent(sessionID, storeevents.Event{
		ID: "already-live", Type: "streaming_chunk", SessionID: sessionID,
		ExecutionKind: "main_agent", Data: agentevents.NewAgentEvent(chunk),
	})
	current := []builderConversationMessage{{Role: "human", Parts: []builderConversationPart{{Text: "tell me links"}}}}
	refreshed := append(current, builderConversationMessage{Role: "ai", Parts: []builderConversationPart{{Text: "Here are the links."}}})

	api.publishNativeTranscriptRecoveredAssistantMessages(sessionID, current, refreshed, nil)
	if got := len(store.GetAllEventsRaw(sessionID)); got != 1 {
		t.Fatalf("already-visible reply was duplicated: %d events", got)
	}
}

func TestPublishNativeTranscriptRecoveredAssistantMessagesSkipsDurableReplyAfterRestart(t *testing.T) {
	store := storeevents.NewEventStore(100)
	defer store.Stop()
	api := &StreamingAPI{eventStore: store}
	const sessionID = "cursor-restarted"

	oldChunk := &agentevents.StreamingChunkEvent{Content: "An older reply.", Source: agentevents.StreamingChunkSourceTranscript}
	durableUIEvents := []storeevents.Event{{
		ID: "persisted-old-reply", Type: "streaming_chunk", SessionID: sessionID,
		ExecutionKind: "main_agent", Data: agentevents.NewAgentEvent(oldChunk),
	}}
	refreshed := []builderConversationMessage{
		{Role: "human", Parts: []builderConversationPart{{Text: "old request"}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "An older reply."}}},
		{Role: "human", Parts: []builderConversationPart{{Text: "new request"}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "The newly recovered reply."}}},
	}

	published := api.publishNativeTranscriptRecoveredAssistantMessages(sessionID, nil, refreshed, durableUIEvents)
	if published != 1 {
		t.Fatalf("published = %d, want only the newly recovered reply", published)
	}
	events := store.GetAllEventsRaw(sessionID)
	if len(events) != 1 {
		t.Fatalf("live events = %d, want one", len(events))
	}
	payload := eventPayloadMap(events[0])
	if got, _ := payload["content"].(string); got != "The newly recovered reply." {
		t.Fatalf("published content = %q, replayed an older durable reply", got)
	}
}

func TestFilterNativeContinuityMessagesRemovesSyntheticHandoffs(t *testing.T) {
	messages := []builderConversationMessage{
		{Role: "human", Parts: []builderConversationPart{{Text: "[AGENTWORKS CONVERSATION CONTINUITY]\ninternal\n[/AGENTWORKS CONVERSATION CONTINUITY]"}}},
		{Role: "human", Parts: []builderConversationPart{{Text: "real user message"}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "real reply"}}},
	}
	filtered := filterNativeContinuityMessages(messages)
	if len(filtered) != 2 || builderConversationMessageText(filtered[0]) != "real user message" {
		t.Fatalf("synthetic continuity handoff leaked into durable history: %+v", filtered)
	}
}

// Agy's first message carries the system prompt ("System instructions:\n..."); it must not be
// published as a second user row when the transcript is read back.
func TestFilterNativeContinuityMessagesDropsAgySystemInstructionsRow(t *testing.T) {
	messages := []builderConversationMessage{
		{Role: "human", Parts: []builderConversationPart{{Text: "System instructions:\n# Workflow Builder Agent\n\nlong prompt\n\nhello"}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "real reply"}}},
		{Role: "human", Parts: []builderConversationPart{{Text: "System instructions are documented in the wiki, can you summarise them?"}}},
	}
	filtered := filterNativeContinuityMessages(messages)
	if len(filtered) != 2 || builderConversationMessageText(filtered[0]) != "real reply" {
		t.Fatalf("Agy's system prompt row leaked, or a real user message was dropped: %+v", filtered)
	}
}
