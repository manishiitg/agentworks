package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	pkgevents "github.com/manishiitg/mcpagent/events"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func openClientIdentityTestStore(t *testing.T, sessionID string) *internalevents.EventStore {
	t.Helper()
	journal, err := internalevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := internalevents.NewEventStore(100)
	t.Cleanup(store.Stop)
	store.SetDurableJournal(journal)
	if err := store.SetSessionPersistenceClass(sessionID, internalevents.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	return store
}

func durableChatIDs(t *testing.T, store *internalevents.EventStore, sessionID string) []string {
	t.Helper()
	page, err := store.ReadDurableChatPage(sessionID, internalevents.DurableEventPageOptions{Limit: 100, FromStart: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(page.Events))
	for _, event := range page.Events {
		ids = append(ids, event.ID)
	}
	return ids
}

func testUserMessage(id, content string) internalevents.Event {
	return internalevents.Event{
		ID: id, Type: string(pkgevents.UserMessage), Timestamp: time.Now(),
		Data: pkgevents.NewAgentEvent(pkgevents.NewUserMessageEvent(0, content, "user")),
	}
}

func testAssistantMessage(id, content string) internalevents.Event {
	return internalevents.Event{
		ID: id, Type: string(pkgevents.StreamingChunk), Timestamp: time.Now(),
		Data: pkgevents.NewAgentEvent(&pkgevents.StreamingChunkEvent{Content: content, Source: "transcript"}),
	}
}

// Live server A incident: B was sent while the CLI was still answering A. The CLI
// took B only after finishing A, but B was recorded at send time, so A's
// answer rendered under B. B must be recorded where the CLI took it.
func TestSteeredMessageIsRecordedAfterTheInFlightAnswer(t *testing.T) {
	const session = "crew-steer"
	store := openClientIdentityTestStore(t, session)
	release := make(chan struct{})
	api := &StreamingAPI{eventStore: store,
		internalDurableAckHandler: func(ctx context.Context, _ llmproviders.Provider, _, _ string) (llmtypes.DurableAck, error) {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return llmtypes.DurableAck{Outcome: llmtypes.DurableAckConfirmed, ProofSource: "transcript"}, nil
		},
	}

	store.AddEvent(session, testUserMessage("user:sub-a", "this ticket <url>"))
	api.recordLiveCodingAgentUserMessage(session, "whats the title of this ticket?", string(llmproviders.ProviderClaudeCode), "steer-b", "sent_to_cli", "sub-b")
	store.AddEvent(session, testAssistantMessage("answer-a", "This is WEB-1681 ..."))
	close(release)
	confirmed := waitForLiveInputConfirmed(t, store, session)
	store.AddEvent(session, testAssistantMessage("answer-b", "Centralize shared database migrations in lib-core"))

	want := []string{"user:sub-a", "answer-a", "user:sub-b", "steer-b:confirmed", "answer-b"}
	got := durableChatIDs(t, store, session)
	if len(got) != len(want) {
		t.Fatalf("journal order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("journal order = %v, want %v", got, want)
		}
	}
	page, err := store.ReadDurableChatPage(session, internalevents.DurableEventPageOptions{Limit: 100, FromStart: true})
	if err != nil {
		t.Fatal(err)
	}
	if page.Events[2].Timestamp.Before(page.Events[1].Timestamp) {
		t.Fatalf("steered row dated %s before the answer it followed (%s)", page.Events[2].Timestamp, page.Events[1].Timestamp)
	}
	payload, ok := confirmed.Data.Data.(*pkgevents.LiveInputConfirmedEvent)
	if !ok || payload.Metadata["client_message_id"] != "sub-b" || payload.MessageID != "steer-b" {
		t.Fatalf("receipt = %#v, want message steer-b carrying client id sub-b", confirmed.Data.Data)
	}
}

// Fast-answer race: the CLI took B and journalled the start of its answer
// before the durable ack arrived. B's answer must still follow B.
func TestSteeredAnswerJournalledBeforeTheAckStillFollowsTheQuestion(t *testing.T) {
	const session = "crew-steer-race"
	store := openClientIdentityTestStore(t, session)
	release := make(chan struct{})
	api := &StreamingAPI{eventStore: store,
		internalDurableAckHandler: func(ctx context.Context, _ llmproviders.Provider, _, _ string) (llmtypes.DurableAck, error) {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return llmtypes.DurableAck{Outcome: llmtypes.DurableAckConfirmed, ProofSource: "transcript"}, nil
		},
	}
	completion := func(id, text string) internalevents.Event {
		return internalevents.Event{ID: id, Type: "unified_completion", Timestamp: time.Now(), Data: &pkgevents.AgentEvent{
			Type: pkgevents.EventType("unified_completion"),
			Data: internalevents.NewGenericEventData("unified_completion", map[string]interface{}{"final_result": text}),
		}}
	}

	store.AddEvent(session, testUserMessage("user:sub-a", "this ticket <url>"))
	api.recordLiveCodingAgentUserMessage(session, "whats the title of this ticket?", string(llmproviders.ProviderClaudeCode), "steer-b", "sent_to_cli", "sub-b")
	store.AddEvent(session, testAssistantMessage("answer-a", "This is WEB-1681 ..."))
	store.AddEvent(session, completion("completion-a", "This is WEB-1681 ..."))
	// The CLI took B and answered before the ack watcher confirmed it.
	store.AddEvent(session, testAssistantMessage("answer-b", "Centralize shared database migrations in lib-core"))
	store.AddEvent(session, completion("completion-b", "Centralize shared database migrations in lib-core"))
	close(release)
	waitForLiveInputConfirmed(t, store, session)

	want := []string{"user:sub-a", "answer-a", "completion-a", "user:sub-b", "answer-b", "completion-b", "steer-b:confirmed"}
	got := durableChatIDs(t, store, session)
	if len(got) != len(want) {
		t.Fatalf("journal order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("journal order = %v, want %v", got, want)
		}
	}
}

func TestSteerWithoutDurableAckIsRecordedImmediately(t *testing.T) {
	const session = "steer-no-ack"
	store := openClientIdentityTestStore(t, session)
	api := &StreamingAPI{eventStore: store}
	api.recordLiveCodingAgentUserMessage(session, "hello", "api-provider", "steer-1", "sent_to_cli", "sub-1")
	if got := durableChatIDs(t, store, session); len(got) != 1 || got[0] != "user:sub-1" {
		t.Fatalf("journal = %v, want the steered message recorded at once as user:sub-1", got)
	}
}

func TestBackgroundSteerKeepsServerIdentity(t *testing.T) {
	const session = "steer-background"
	store := openClientIdentityTestStore(t, session)
	api := &StreamingAPI{eventStore: store}
	api.recordLiveCodingAgentUserMessage(session, "status?", "api-provider", "steer-bg", "queued_for_injection", "")
	if got := durableChatIDs(t, store, session); len(got) != 1 || got[0] != "steer-bg" {
		t.Fatalf("journal = %v, want the server message id", got)
	}
}

func TestKeyedQueuedTurnIsRecordedWhenItRuns(t *testing.T) {
	const session = "queued-keyed"
	store := openClientIdentityTestStore(t, session)
	api := &StreamingAPI{eventStore: store}
	api.recordQueuedConversationUserMessage(session, queuedConversationTurn{
		ID: "turn-1", SessionID: session, SubmissionID: "sub-q", Request: QueryRequest{Query: "after this"},
	})
	if got := durableChatIDs(t, store, session); len(got) != 0 {
		t.Fatalf("keyed queued turn recorded at enqueue: %v", got)
	}
	// When the queued turn runs, its own user_message takes the client id.
	store.ExpectClientUserMessage(session, "sub-q", "after this")
	store.AddEvent(session, testUserMessage("bridge-echo", "after this"))
	if got := durableChatIDs(t, store, session); len(got) != 1 || got[0] != "user:sub-q" {
		t.Fatalf("journal = %v, want user:sub-q", got)
	}

	// Unkeyed producers (bots) keep the enqueue-time row.
	api.recordQueuedConversationUserMessage(session, queuedConversationTurn{
		ID: "turn-bot", SessionID: session, Request: QueryRequest{Query: "from slack"},
	})
	if got := durableChatIDs(t, store, session); len(got) != 2 || got[1] != "turn-bot" {
		t.Fatalf("journal = %v, want the bot turn recorded", got)
	}
}

func TestClientMessageIDFromContextRejectsUnsafeKeys(t *testing.T) {
	ctx := context.WithValue(context.Background(), chatSubmissionContextKey{}, chatSubmissionContext{ID: "9f1c-abc_2.x"})
	if got := clientMessageIDFromContext(ctx); got != "9f1c-abc_2.x" {
		t.Fatalf("client id = %q", got)
	}
	for _, bad := range []string{"", "has space", "slash/x", "<script>"} {
		ctx := context.WithValue(context.Background(), chatSubmissionContextKey{}, chatSubmissionContext{ID: bad})
		if got := clientMessageIDFromContext(ctx); got != "" {
			t.Fatalf("unsafe key %q accepted as %q", bad, got)
		}
	}
}
