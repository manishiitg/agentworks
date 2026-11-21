package server

import (
	"path/filepath"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func idleAnswerTestStore(t *testing.T, sid string, chunks ...string) *StreamingAPI {
	t.Helper()
	journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	store := storeevents.NewEventStore(200)
	t.Cleanup(store.Stop)
	store.SetDurableJournal(journal)
	if err := store.SetSessionPersistenceClass(sid, storeevents.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	add := func(id, kind string, data agentevents.EventData) {
		store.AddEvent(sid, storeevents.Event{ID: id, Type: kind, Timestamp: time.Now(), SessionID: sid, ExecutionKind: "main", Data: agentevents.NewAgentEvent(data)})
	}
	if len(chunks) > 0 {
		add("start", "streaming_start", &agentevents.StreamingStartEvent{})
		for i, chunk := range chunks {
			add("chunk-"+string(rune('a'+i)), "streaming_chunk", &agentevents.StreamingChunkEvent{Content: chunk, ChunkIndex: i + 1, Source: "content", IsDelta: true})
		}
	}
	return &StreamingAPI{eventStore: store}
}

// agentworks#3128 (PLAT-872-117): a Cursor turn that goes quiet without turn-ended reaches the idle limit. The
// answer it already streamed is saved in the chat history right after the person's message, replacing the
// cancel placeholder, instead of being lost.
func TestKeepStreamedAnswerOnIdleSavesTheCursorAnswer(t *testing.T) {
	const sid = "idle-cursor-chat"
	api := idleAnswerTestStore(t, sid, "The fix is ", "in the bridge.")
	defer takeNativeTurnAnswers(sid)
	if !api.keepStreamedAnswerOnIdle(sid, "cursor-cli", "why did it hang?") {
		t.Fatal("the streamed Cursor answer was not kept")
	}
	history := []llmtypes.MessageContent{
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "earlier"),
		llmtypes.TextParts(llmtypes.ChatMessageTypeAI, "earlier answer"),
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "why did it hang?"),
		llmtypes.TextParts(llmtypes.ChatMessageTypeAI, canceledNoResponseText),
	}
	saved := api.historyWithNativeTurnAnswers(sid, history)
	if len(saved) != 4 || saved[3].Role != llmtypes.ChatMessageTypeAI || historyText(saved[3]) != "The fix is in the bridge." || historyText(saved[2]) != "why did it hang?" {
		t.Fatalf("saved history = %#v", saved)
	}
	// The answer is saved once: a second save of the same turn adds nothing.
	if again := api.historyWithNativeTurnAnswers(sid, saved); len(again) != 4 {
		t.Fatalf("answer saved twice: %#v", again)
	}
}

// Other providers keep today's idle behaviour (Claude has its own release), and a turn with no streamed prose has
// nothing to keep.
func TestKeepStreamedAnswerOnIdleOnlyCursorWithStreamedText(t *testing.T) {
	for name, tc := range map[string]struct {
		provider string
		chunks   []string
	}{
		"codex":         {provider: "codex-cli", chunks: []string{"partial"}},
		"claude":        {provider: "claude-code", chunks: []string{"partial"}},
		"cursor silent": {provider: "cursor-cli"},
		"cursor blank":  {provider: "cursor-cli", chunks: []string{"  "}},
	} {
		t.Run(name, func(t *testing.T) {
			sid := "idle-" + name
			api := idleAnswerTestStore(t, sid, tc.chunks...)
			defer takeNativeTurnAnswers(sid)
			if api.keepStreamedAnswerOnIdle(sid, tc.provider, "hi") {
				t.Fatal("kept an answer it should not")
			}
			if answers := takeNativeTurnAnswers(sid); len(answers) != 0 {
				t.Fatalf("recorded %d answers", len(answers))
			}
		})
	}
	if (*StreamingAPI)(nil).keepStreamedAnswerOnIdle("x", "cursor-cli", "hi") {
		t.Fatal("nil api kept an answer")
	}
}
