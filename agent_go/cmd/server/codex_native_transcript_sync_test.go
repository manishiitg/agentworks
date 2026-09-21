package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

// Rows shaped exactly like a real Codex 0.147 rollout: session_meta,
// injected AGENTS.md preamble, developer instructions, a typed user turn,
// reasoning + tool call/output, the assistant reply, and the event_msg
// duplicates that must not double-count.
func codexRolloutFixture() []string {
	return []string{
		`{"timestamp":"2026-09-01T16:08:16.488Z","type":"session_meta","payload":{"id":"thread-1","cwd":"/tmp/ws"}}`,
		`{"timestamp":"2026-09-01T16:08:19.925Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /tmp/ws\n\nbe nice"}]}}`,
		`{"timestamp":"2026-09-01T16:08:19.930Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"developer preamble"}]}}`,
		`{"timestamp":"2026-09-01T16:08:19.945Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"something is wrong in our reports"}]}}`,
		`{"timestamp":"2026-09-01T16:08:20.000Z","type":"event_msg","payload":{"type":"user_message","message":"something is wrong in our reports"}}`,
		`{"timestamp":"2026-09-01T16:08:25.000Z","type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking"}]}}`,
		`{"timestamp":"2026-09-01T16:08:26.000Z","type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"ls"}}`,
		`{"timestamp":"2026-09-01T16:08:27.000Z","type":"response_item","payload":{"type":"custom_tool_call_output","output":"db.sqlite"}}`,
		`{"timestamp":"2026-09-01T16:08:30.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The daily actions table is empty because the ingest step failed."}]}}`,
		`{"timestamp":"2026-09-01T16:08:31.000Z","type":"event_msg","payload":{"type":"agent_message","message":"The daily actions table is empty because the ingest step failed."}}`,
		`{"timestamp":"2026-09-04T06:48:22.060Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"is there a schedule which proposes new strategies"}]}}`,
		`{"timestamp":"2026-09-04T06:48:58.974Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Yes - Weekly Strategy Discovery runs every Monday."}]}}`,
	}
}

func writeCodexRolloutFixture(t *testing.T, root, date, threadID string, lines []string) string {
	t.Helper()
	dir := filepath.Join(root, strings.ReplaceAll(date, "-", string(filepath.Separator)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-"+date+"T21-38-16-"+threadID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadCodexTranscriptMessagesKeepsOnlyTypedTurns(t *testing.T) {
	root := t.TempDir()
	path := writeCodexRolloutFixture(t, root, "2026-09-01", "thread-1", codexRolloutFixture())

	messages, maxTimestamp, err := readCodexTranscriptMessages(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []builderConversationMessage{
		{Role: "human", Parts: []builderConversationPart{{Text: "something is wrong in our reports"}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "The daily actions table is empty because the ingest step failed."}}},
		{Role: "human", Parts: []builderConversationPart{{Text: "is there a schedule which proposes new strategies"}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "Yes - Weekly Strategy Discovery runs every Monday."}}},
	}
	if len(messages) != len(want) {
		t.Fatalf("got %d messages, want %d: %+v", len(messages), len(want), messages)
	}
	for i := range want {
		if builderConversationMessageKey(messages[i]) != builderConversationMessageKey(want[i]) {
			t.Fatalf("message %d = %+v, want %+v", i, messages[i], want[i])
		}
	}
	wantTS, _ := time.Parse(time.RFC3339Nano, "2026-09-04T06:48:58.974Z")
	if !maxTimestamp.Equal(wantTS) {
		t.Fatalf("max timestamp = %v, want %v", maxTimestamp, wantTS)
	}
}

func TestResolveCodexNativeTranscriptPathHonoursCodexHomeAndFindsAcrossDates(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	root := filepath.Join(codexHome, "sessions")
	writeCodexRolloutFixture(t, root, "2026-08-30", "other-thread", codexRolloutFixture())
	want := writeCodexRolloutFixture(t, root, "2026-09-01", "thread-1", codexRolloutFixture())

	got, err := resolveCodexNativeTranscriptPath("thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("resolved %q, want %q", got, want)
	}
	if got, _ := resolveCodexNativeTranscriptPath("missing"); got != "" {
		t.Fatalf("expected no path for an unknown thread, got %q", got)
	}
}

// The real bug: a Codex builder chat persisted through 10:44, then continued
// for an hour via retained live input. Resume showed the 10:44 snapshot.
func TestRefreshLatestBuilderConversationFromCodexRollout(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	const nativeSessionID = "01a05dba-758d-72b0-8248-d100125d5593"
	writeCodexRolloutFixture(t, filepath.Join(codexHome, "sessions"), "2026-09-01", nativeSessionID, codexRolloutFixture())

	conv := builderConversationLog{
		SessionID: "b445627b-deae-407c-a05e-02fcd7343d7c",
		PhaseID:   "workflow-builder",
		UpdatedAt: "2026-09-04T10:44:30+05:30",
		ConversationHistory: []builderConversationMessage{
			{Role: "human", Parts: []builderConversationPart{{Text: "something is wrong in our reports"}}},
			{Role: "ai", Parts: []builderConversationPart{{Text: "The daily actions table is empty because the ingest step failed."}}},
		},
	}
	record := map[string]interface{}{
		"session_id": conv.SessionID,
		"phase_id":   conv.PhaseID,
		"updated_at": conv.UpdatedAt,
		"conversation_history": []map[string]interface{}{
			{"Role": "human", "Parts": []map[string]interface{}{{"Text": "something is wrong in our reports"}}},
			{"Role": "ai", "Parts": []map[string]interface{}{{"Text": "The daily actions table is empty because the ingest step failed."}}},
		},
		"runtime": map[string]interface{}{
			"provider":            "codex-cli",
			"external_session_id": nativeSessionID,
			"agent_session_handle": map[string]interface{}{
				"provider": map[string]interface{}{
					"provider":          "codex-cli",
					"native_session_id": nativeSessionID,
					"working_dir":       "/tmp/ws",
				},
			},
		},
	}
	rawContent, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}

	api := &StreamingAPI{}
	setupNativeRecoveryCanonicalFixture(t, "Workflow/social-media/builder/conversation/2026-09-01/session-b445627b-conversation.json", string(rawContent))
	refreshed := api.refreshLatestBuilderConversationFromNativeTranscript(context.Background(), "Workflow/social-media/builder/conversation/2026-09-01/session-b445627b-conversation.json", string(rawContent), conv)

	if len(refreshed.ConversationHistory) != 4 {
		t.Fatalf("expected 4 messages after catch-up, got %d: %+v", len(refreshed.ConversationHistory), refreshed.ConversationHistory)
	}
	last := refreshed.ConversationHistory[3]
	if last.Role != "ai" || last.Parts[0].Text != "Yes - Weekly Strategy Discovery runs every Monday." {
		t.Fatalf("expected the rollout's newest assistant reply last, got %+v", last)
	}
	wantUpdatedAt, _ := time.Parse(time.RFC3339Nano, "2026-09-04T06:48:58.974Z")
	if got := parseBuilderConversationUpdatedAt(refreshed.UpdatedAt); !got.Equal(wantUpdatedAt) {
		t.Fatalf("updated_at = %v, want %v", got, wantUpdatedAt)
	}
}

func TestNativeTranscriptSyncSupportedProvider(t *testing.T) {
	for provider, want := range map[string]bool{"claude-code": true, "codex-cli": true, "Codex-CLI": true, "cursor-cli": true, "pi-cli": true, "muse-cli": true, "Muse-CLI": true, "agy-cli": true, "Agy-CLI": true, "gemini-cli": false, "": false} {
		if got := nativeTranscriptSyncSupportedProvider(provider); got != want {
			t.Fatalf("provider %q supported = %v, want %v", provider, got, want)
		}
	}

	// Keep chat-history recovery aligned with the provider registry. A newly
	// registered persistent CLI must not gain retained live input without also
	// gaining a readable transcript and a native catch-up branch; otherwise it
	// can reproduce PLAT-178 even while every older provider remains green.
	for _, contract := range llmproviders.CodingAgentProviderContracts() {
		if !contract.UsesPersistentSession || !contract.SupportsLiveInput {
			continue
		}
		if !contract.AdapterReadsTranscript {
			t.Errorf("persistent live-input provider %q does not declare transcript reading", contract.Provider)
		}
		if !nativeTranscriptSyncSupportedProvider(string(contract.Provider)) {
			t.Errorf("persistent live-input provider %q is missing native chat-history recovery", contract.Provider)
		}
	}
}
