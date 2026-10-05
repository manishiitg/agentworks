package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestReadNewClaudeTranscriptMessagesKeepsQueuedHumanTurnInTranscriptOrder(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")

	writeTranscriptFixture(t, transcriptPath, []string{
		`{"type":"assistant","timestamp":"2026-09-20T21:05:15.228Z","message":{"role":"assistant","content":[{"type":"text","text":"Checking the remaining tabs now."}]}}`,
		`{"type":"queue-operation","timestamp":"2026-09-20T21:05:26.603Z","operation":"enqueue","content":"<pasted_content id=\"bc83\">\nsi sheet111 tab used for anything?\n</pasted_content id=\"bc83\">"}`,
		`{"type":"assistant","timestamp":"2026-09-20T21:05:34.320Z","message":{"role":"assistant","content":[{"type":"text","text":"I found another static sibling tab."}]}}`,
		// Claude writes the durable queued command only after the in-flight tool
		// result, at the point where it becomes the next human turn.
		`{"type":"attachment","timestamp":"2026-09-20T21:05:26.602Z","attachment":{"type":"queued_command","prompt":"<pasted_content id=\"bc83\">\nsi sheet111 tab used for anything?\n</pasted_content id=\"bc83\">","source_uuid":"85501f51-df07-4ee5-958c-9cb472e999d5","commandMode":"prompt","origin":{"kind":"human"},"humanTurn":true}}`,
		`{"type":"assistant","timestamp":"2026-09-20T21:06:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"No — Sheet11 is not referenced anywhere else."}]}}`,
	})

	messages, maxTimestamp, err := readNewClaudeTranscriptMessages(transcriptPath, time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []builderConversationMessage{
		{Role: "ai", Parts: []builderConversationPart{{Text: "Checking the remaining tabs now."}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "I found another static sibling tab."}}},
		{Role: "human", Parts: []builderConversationPart{{Text: "si sheet111 tab used for anything?"}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "No — Sheet11 is not referenced anywhere else."}}},
	}
	if !builderConversationHistoriesEqual(messages, want) {
		t.Fatalf("queued human turn was not preserved at its transcript boundary:\n got: %+v\nwant: %+v", messages, want)
	}
	// The real failure already had all assistant updates on disk. Recovery must
	// still insert the missing human boundary between them, not treat the native
	// transcript as an all-or-nothing replacement.
	persistedWithoutQueuedTurn := []builderConversationMessage{want[0], want[1], want[3]}
	merged := mergeBuilderConversationHistory(persistedWithoutQueuedTurn, messages)
	if !builderConversationHistoriesEqual(merged, want) {
		t.Fatalf("merge did not repair the missing queued human boundary:\n got: %+v\nwant: %+v", merged, want)
	}
	wantMax, _ := time.Parse(time.RFC3339Nano, "2026-09-20T21:06:01.000Z")
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

// TestRefreshLatestBuilderConversationFromNativeTranscript is the
// fail-before/pass-after regression test for PLAT-178: before this fix,
// nothing in the restore path ever looked at Claude Code's own transcript,
// so a stale conversation_history snapshot (frozen at whatever the last
// full turn saved) was returned as-is forever, even when the CLI's own
// transcript already had more recent messages on disk. This proves the
// missing messages are recovered.
func TestRefreshLatestBuilderConversationFromNativeTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workingDir := filepath.Join(home, "ai-work", "mcp-agent-builder-go", "workspace-docs", "Workflow", "substack")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatalf("failed to create working dir: %v", err)
	}

	slug := claudeNativeTranscriptProjectSlug(workingDir)
	transcriptDir := filepath.Join(home, ".claude", "projects", slug)
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatalf("failed to create transcript dir: %v", err)
	}
	nativeSessionID := "71ed3dcb-fe3c-412d-94cb-e62226fc4648"
	transcriptPath := filepath.Join(transcriptDir, nativeSessionID+".jsonl")
	writeTranscriptFixture(t, transcriptPath, []string{
		`{"type":"user","timestamp":"2026-08-22T08:25:00.000Z","message":{"role":"user","content":"can you also check follow health"}}`,
		`{"type":"assistant","timestamp":"2026-08-22T08:26:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Added the Follow Health panel and confirmed it validates."}]}}`,
	})

	conv := builderConversationLog{
		SessionID: "b5e39872-4e4e-4645-8059-6d6e7a1231db",
		PhaseID:   "workflow-builder",
		UpdatedAt: "2026-08-22T13:51:26+05:30",
		ConversationHistory: []builderConversationMessage{
			{Role: "human", Parts: []builderConversationPart{{Text: "can you check the workflow upgrades and do them"}}},
			{Role: "ai", Parts: []builderConversationPart{{Text: "Yes - both bugs you reported are fixed."}}},
		},
	}

	record := map[string]interface{}{
		"session_id": conv.SessionID,
		"phase_id":   conv.PhaseID,
		"updated_at": conv.UpdatedAt,
		"conversation_history": []map[string]interface{}{
			{"Role": "human", "Parts": []map[string]interface{}{{"Text": "can you check the workflow upgrades and do them"}}},
			{"Role": "ai", "Parts": []map[string]interface{}{{"Text": "Yes - both bugs you reported are fixed."}}},
		},
		"runtime": map[string]interface{}{
			"provider":            "claude-code",
			"external_session_id": nativeSessionID,
			"agent_session_handle": map[string]interface{}{
				"provider": map[string]interface{}{
					"provider":          "claude-code",
					"native_session_id": nativeSessionID,
					"working_dir":       workingDir,
				},
			},
		},
	}
	rawContent, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("failed to marshal fixture record: %v", err)
	}

	api := &StreamingAPI{}
	setupNativeRecoveryCanonicalFixture(t, "Workflow/substack/builder/conversation/2026-08-22/session-b5e39872-conversation.json", string(rawContent))
	refreshed := api.refreshLatestBuilderConversationFromNativeTranscript(context.Background(), "Workflow/substack/builder/conversation/2026-08-22/session-b5e39872-conversation.json", string(rawContent), conv)

	if refreshed.Revision != 1 {
		t.Fatalf("recovered canonical revision=%d, want 1", refreshed.Revision)
	}
	if len(refreshed.ConversationHistory) != 4 {
		t.Fatalf("expected 4 messages after catch-up (2 original + 2 from native transcript), got %d: %+v", len(refreshed.ConversationHistory), refreshed.ConversationHistory)
	}
	last := refreshed.ConversationHistory[len(refreshed.ConversationHistory)-1]
	if last.Role != "ai" || last.Parts[0].Text != "Added the Follow Health panel and confirmed it validates." {
		t.Fatalf("expected the merge to append the native transcript's newest assistant message last, got %+v", last)
	}
	secondToLast := refreshed.ConversationHistory[len(refreshed.ConversationHistory)-2]
	if secondToLast.Role != "human" || secondToLast.Parts[0].Text != "can you also check follow health" {
		t.Fatalf("expected the merge to append the native transcript's user message before the assistant reply, got %+v", secondToLast)
	}

	wantUpdatedAt, _ := time.Parse(time.RFC3339Nano, "2026-08-22T08:26:00.000Z")
	gotUpdatedAt := parseBuilderConversationUpdatedAt(refreshed.UpdatedAt)
	if !gotUpdatedAt.Equal(wantUpdatedAt) {
		t.Fatalf("expected updated_at to advance to the native transcript's newest timestamp, got %v want %v", gotUpdatedAt, wantUpdatedAt)
	}
}

func TestRefreshLatestBuilderConversationFromNativeTranscriptNoOpsWithoutClaudeCodeRuntime(t *testing.T) {
	conv := builderConversationLog{
		SessionID: "some-session",
		UpdatedAt: "2026-08-22T13:51:26+05:30",
		ConversationHistory: []builderConversationMessage{
			{Role: "human", Parts: []builderConversationPart{{Text: "hi"}}},
		},
	}
	record := map[string]interface{}{
		"session_id":           conv.SessionID,
		"updated_at":           conv.UpdatedAt,
		"conversation_history": conv.ConversationHistory,
	}
	rawContent, _ := json.Marshal(record)

	api := &StreamingAPI{}
	setupNativeRecoveryCanonicalFixture(t, "Workflow/some/path.json", string(rawContent))
	refreshed := api.refreshLatestBuilderConversationFromNativeTranscript(context.Background(), "Workflow/some/path.json", string(rawContent), conv)

	if len(refreshed.ConversationHistory) != 1 {
		t.Fatalf("expected no-op when runtime info is absent, got %d messages", len(refreshed.ConversationHistory))
	}
}

func TestSyncWorkflowBuilderConversationFromNativeTranscriptUpdatesConversationAndIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const userID = "alice"
	const workspacePath = "_users/alice/Chats/Work/projects/salesoutreach"
	const sessionID = "retained-live-input"
	const nativeSessionID = "native-retained-live-input"
	workingDir := filepath.Join(home, "workspace-docs", "Workflow", "salesoutreach")
	transcriptDir := filepath.Join(home, ".claude", "projects", claudeNativeTranscriptProjectSlug(workingDir))
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTranscriptFixture(t, filepath.Join(transcriptDir, nativeSessionID+".jsonl"), []string{
		`{"type":"user","timestamp":"2026-08-30T05:00:00Z","message":{"role":"user","content":"first live request"}}`,
		`{"type":"assistant","timestamp":"2026-08-30T05:01:00Z","message":{"role":"assistant","content":[{"type":"text","text":"first live reply"}]}}`,
	})

	conversationPath := workspacePath + "/builder/conversation/2026-08-30/session-" + sessionID + "-conversation.json"
	record := map[string]interface{}{
		"session_id": sessionID,
		"user_id":    userID,
		"agent_mode": "workflow_phase",
		"updated_at": "2026-08-30T05:00:00Z",
		"conversation_history": []map[string]interface{}{
			{"Role": "human", "Parts": []map[string]string{{"Text": "first live request"}}},
		},
		"runtime": map[string]interface{}{
			"provider": "claude-code",
			"agent_session_handle": map[string]interface{}{
				"provider": map[string]interface{}{
					"provider": "claude-code", "native_session_id": nativeSessionID, "working_dir": workingDir,
				},
			},
		},
	}
	content, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	workspace := &mockWorkspaceAPI{files: map[string]string{conversationPath: string(content)}}
	workspaceServer := httptest.NewServer(workspace)
	defer workspaceServer.Close()
	t.Setenv("WORKSPACE_API_URL", workspaceServer.URL)

	eventStore := storeevents.NewEventStore(100)
	defer eventStore.Stop()
	eventStore.SetSessionOwner(sessionID, userID)
	api := &StreamingAPI{eventStore: eventStore}
	changed, supported := api.syncWorkflowBuilderConversationFromNativeTranscript(context.Background(), userID, sessionID, workspacePath)
	if !supported || !changed {
		t.Fatalf("sync changed/supported = %v/%v, want true/true", changed, supported)
	}

	var persisted builderConversationLog
	if err := json.Unmarshal([]byte(workspace.files[conversationPath]), &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.ConversationHistory) != 2 || persisted.ConversationHistory[1].Parts[0].Text != "first live reply" {
		t.Fatalf("persisted history = %+v, want the native assistant reply", persisted.ConversationHistory)
	}
	indexPath := workspacePath + "/builder/conversation/" + chatHistoryIndexFileName
	if _, found := workspace.files[indexPath]; !found {
		t.Fatalf("native sync did not update %s", indexPath)
	}
	events := eventStore.GetAllEventsRaw(sessionID)
	if len(events) != 1 || !storeevents.IsTranscriptMessage(events[0]) {
		t.Fatalf("live recovery events = %+v, want one transcript message", events)
	}
	payload := eventPayloadMap(events[0])
	if got, _ := payload["content"].(string); got != "first live reply" {
		t.Fatalf("live recovered content = %q, want first live reply", got)
	}
	if changed, supported := api.syncWorkflowBuilderConversationFromNativeTranscript(context.Background(), userID, sessionID, workspacePath); !supported || changed {
		t.Fatalf("second sync changed/supported = %v/%v, want false/true", changed, supported)
	}
	if got := len(eventStore.GetAllEventsRaw(sessionID)); got != 1 {
		t.Fatalf("second sync published duplicate live reply: %d events", got)
	}

	// Simulate a backend restart. Durable history is already current, so an
	// empty live EventStore is not evidence that the historical reply is new.
	// The browser restores that reply from the canonical conversation.
	eventStore.RemoveSession(sessionID)
	// The restored browser/session re-registers its authenticated owner.
	eventStore.SetSessionOwner(sessionID, userID)
	if changed, supported := api.syncWorkflowBuilderConversationFromNativeTranscript(context.Background(), userID, sessionID, workspacePath); !supported || changed {
		t.Fatalf("restart repair changed/supported = %v/%v, want false/true", changed, supported)
	}
	restoredEvents := eventStore.GetAllEventsRaw(sessionID)
	if len(restoredEvents) != 0 {
		t.Fatalf("restart replayed canonical history as new live events: %+v", restoredEvents)
	}

	// A further restart also remains quiet.
	var durableRecord map[string]interface{}
	if err := json.Unmarshal([]byte(workspace.files[conversationPath]), &durableRecord); err != nil {
		t.Fatal(err)
	}
	durableRecord["ui_events"] = restoredEvents
	durableContent, err := json.Marshal(durableRecord)
	if err != nil {
		t.Fatal(err)
	}
	workspace.files[conversationPath] = string(durableContent)
	eventStore.RemoveSession(sessionID)
	eventStore.SetSessionOwner(sessionID, userID)
	if changed, supported := api.syncWorkflowBuilderConversationFromNativeTranscript(context.Background(), userID, sessionID, workspacePath); !supported || changed {
		t.Fatalf("durable restart changed/supported = %v/%v, want false/true", changed, supported)
	}
	if got := len(eventStore.GetAllEventsRaw(sessionID)); got != 0 {
		t.Fatalf("durable reply was replayed after restart: %d events", got)
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

func TestNativeTranscriptMergePreservesPersistedStructuredToolEntries(t *testing.T) {
	record := map[string]interface{}{
		"conversation_history": []map[string]interface{}{
			{"Role": "human", "Parts": []map[string]string{{"Text": "original request"}}},
			{"Role": "tool", "Parts": []map[string]interface{}{{"FunctionResponse": map[string]string{"Name": "inspect", "Result": "raw structured result"}}}},
			{"Role": "ai", "Parts": []map[string]string{{"Text": "original response"}}},
		},
	}
	persisted := []builderConversationMessage{
		{Role: "human", Parts: []builderConversationPart{{Text: "original request"}}},
		{Role: "tool"},
		{Role: "ai", Parts: []builderConversationPart{{Text: "original response"}}},
	}
	native := []builderConversationMessage{
		{Role: "human", Parts: []builderConversationPart{{Text: "original request"}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "original response"}}},
		{Role: "human", Parts: []builderConversationPart{{Text: "later live request"}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "later live response"}}},
	}

	merged := mergeBuilderConversationRecordHistory(record, persisted, native)
	encoded, err := json.Marshal(merged)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"FunctionResponse"`) || !strings.Contains(string(encoded), `"raw structured result"`) {
		t.Fatalf("structured tool entry was lost: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"later live response"`) {
		t.Fatalf("native reply was not added: %s", encoded)
	}
}

func TestMergeBuilderConversationHistoryRecoversRepliesBetweenPersistedLiveInputs(t *testing.T) {
	persisted := []builderConversationMessage{
		{Role: "human", Parts: []builderConversationPart{{Text: "first live input"}}},
		{Role: "human", Parts: []builderConversationPart{{Text: "second live input"}}},
	}
	native := []builderConversationMessage{
		{Role: "human", Parts: []builderConversationPart{{Text: "first live input"}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "first reply"}}},
		{Role: "human", Parts: []builderConversationPart{{Text: "second live input"}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "second reply"}}},
	}

	merged := mergeBuilderConversationHistory(persisted, native)
	if len(merged) != 4 {
		t.Fatalf("merged history has %d messages, want 4: %+v", len(merged), merged)
	}
	want := []string{"first live input", "first reply", "second live input", "second reply"}
	for index, text := range want {
		if got := merged[index].Parts[0].Text; got != text {
			t.Fatalf("merged[%d] = %q, want %q; full history: %+v", index, got, text, merged)
		}
	}
}

// A second message sent while Codex is still answering the first is saved BEFORE that reply, but Codex's own
// transcript has it after the reply. The sync used to append it again: the same user message twice, the second
// with no reply after it (Upwork chat, 2026-10-05).
func TestMergeBuilderConversationHistoryDoesNotDuplicateALiveInputSavedBeforeTheEarlierReply(t *testing.T) {
	human := func(text string) builderConversationMessage {
		return builderConversationMessage{Role: "human", Parts: []builderConversationPart{{Text: text}}}
	}
	ai := func(text string) builderConversationMessage {
		return builderConversationMessage{Role: "ai", Parts: []builderConversationPart{{Text: text}}}
	}
	persisted := []builderConversationMessage{human("change to browser"), human("if mcp is giving error"), ai("restriction found")}
	native := []builderConversationMessage{human("change to browser"), ai("restriction found"), human("if mcp is giving error"), ai("If MCP had a connection error")}

	merged := mergeBuilderConversationHistory(persisted, native)
	humans := 0
	for _, message := range merged {
		if message.Role == "human" && message.Parts[0].Text == "if mcp is giving error" {
			humans++
		}
	}
	if humans != 1 || len(merged) != 4 || merged[3].Parts[0].Text != "If MCP had a connection error" {
		t.Fatalf("the live input was duplicated or the reply lost (%d copies): %+v", humans, merged)
	}
	// A genuine repeat (same text sent again after a reply) is kept.
	repeatPersisted := []builderConversationMessage{human("retry"), ai("failed"), human("retry")}
	repeatNative := []builderConversationMessage{human("retry"), ai("failed"), human("retry"), ai("ok")}
	if got := mergeBuilderConversationHistory(repeatPersisted, repeatNative); len(got) != 4 {
		t.Fatalf("a real repeat was collapsed: %+v", got)
	}
}

func TestRefreshLatestBuilderConversationIgnoresLiveInputUpdatedAtAsTranscriptCursor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workingDir := filepath.Join(home, "workspace-docs", "Workflow", "test")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nativeSessionID := "native-live-input-session"
	transcriptDir := filepath.Join(home, ".claude", "projects", claudeNativeTranscriptProjectSlug(workingDir))
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTranscriptFixture(t, filepath.Join(transcriptDir, nativeSessionID+".jsonl"), []string{
		`{"type":"user","timestamp":"2026-08-22T08:01:00Z","message":{"role":"user","content":"first live input"}}`,
		`{"type":"assistant","timestamp":"2026-08-22T08:02:00Z","message":{"role":"assistant","content":[{"type":"text","text":"first reply"}]}}`,
		`{"type":"user","timestamp":"2026-08-22T08:03:00.500Z","message":{"role":"user","content":"second live input"}}`,
		`{"type":"assistant","timestamp":"2026-08-22T08:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"second reply"}]}}`,
	})

	conv := builderConversationLog{
		UpdatedAt: "2026-08-22T08:03:00Z", // advanced by persisting the second human message
		ConversationHistory: []builderConversationMessage{
			{Role: "human", Parts: []builderConversationPart{{Text: "first live input"}}},
			{Role: "human", Parts: []builderConversationPart{{Text: "second live input"}}},
		},
	}
	record := map[string]interface{}{
		"updated_at":           conv.UpdatedAt,
		"conversation_history": conv.ConversationHistory,
		"runtime": map[string]interface{}{
			"provider":            "claude-code",
			"external_session_id": nativeSessionID,
			"agent_session_handle": map[string]interface{}{
				"provider": map[string]interface{}{
					"provider":          "claude-code",
					"native_session_id": nativeSessionID,
					"working_dir":       workingDir,
				},
			},
		},
	}
	rawContent, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}

	setupNativeRecoveryCanonicalFixture(t, "Workflow/test/builder/conversation/session.json", string(rawContent))
	refreshed := (&StreamingAPI{}).refreshLatestBuilderConversationFromNativeTranscript(
		context.Background(), "Workflow/test/builder/conversation/session.json", string(rawContent), conv,
	)
	want := []string{"first live input", "first reply", "second live input", "second reply"}
	if len(refreshed.ConversationHistory) != len(want) {
		t.Fatalf("refreshed history has %d messages, want %d: %+v", len(refreshed.ConversationHistory), len(want), refreshed.ConversationHistory)
	}
	for index, text := range want {
		if got := refreshed.ConversationHistory[index].Parts[0].Text; got != text {
			t.Fatalf("refreshed[%d] = %q, want %q; full history: %+v", index, got, text, refreshed.ConversationHistory)
		}
	}
}

func TestMergeBuilderConversationHistoryKeepsPreNativePrefixAndRepeatedMessages(t *testing.T) {
	persisted := []builderConversationMessage{
		{Role: "human", Parts: []builderConversationPart{{Text: "older persisted context"}}},
		{Role: "human", Parts: []builderConversationPart{{Text: "hi"}}},
		{Role: "human", Parts: []builderConversationPart{{Text: "hi"}}},
	}
	native := []builderConversationMessage{
		{Role: "human", Parts: []builderConversationPart{{Text: "hi"}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "hello"}}},
		{Role: "human", Parts: []builderConversationPart{{Text: "hi"}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "hello again"}}},
	}

	merged := mergeBuilderConversationHistory(persisted, native)
	want := []string{"older persisted context", "hi", "hello", "hi", "hello again"}
	if len(merged) != len(want) {
		t.Fatalf("merged history has %d messages, want %d: %+v", len(merged), len(want), merged)
	}
	for index, text := range want {
		if got := merged[index].Parts[0].Text; got != text {
			t.Fatalf("merged[%d] = %q, want %q; full history: %+v", index, got, text, merged)
		}
	}
}

func TestMergeBuilderConversationHistoryAlignsReplayedTailWithoutDuplicates(t *testing.T) {
	msg := func(role, text string) builderConversationMessage {
		return builderConversationMessage{Role: role, Parts: []builderConversationPart{{Text: text}}}
	}
	persisted := []builderConversationMessage{
		msg("human", "old request"),
		msg("ai", "same status"),
		msg("human", "middle request"),
		msg("ai", "same status"),
		msg("human", "latest request"),
	}
	native := []builderConversationMessage{
		msg("ai", "same status"),
		msg("human", "latest request"),
		msg("ai", "new reply"),
	}
	merged := mergeBuilderConversationHistory(persisted, native)
	want := []string{"old request", "same status", "middle request", "same status", "latest request", "new reply"}
	if len(merged) != len(want) {
		t.Fatalf("merged history has %d messages, want %d: %+v", len(merged), len(want), merged)
	}
	for i, text := range want {
		if got := builderConversationMessageText(merged[i]); got != text {
			t.Fatalf("merged[%d] = %q, want %q", i, got, text)
		}
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
