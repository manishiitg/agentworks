package events

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// gatedJournal blocks Append for one session until released, and assigns
// sequences like the SQLite journal (max(last+1, event.Sequence)).
type gatedJournal struct {
	mu        sync.Mutex
	last      map[string]int64
	ids       map[string]map[string]bool
	gateFor   string
	gate      chan struct{}
	entered   chan struct{}
	enterOnce sync.Once
	delay     time.Duration
}

func newGatedJournal(gateFor string) *gatedJournal {
	return &gatedJournal{
		last: make(map[string]int64), ids: make(map[string]map[string]bool),
		gateFor: gateFor, gate: make(chan struct{}), entered: make(chan struct{}),
	}
}

func (j *gatedJournal) Append(sessionID string, event Event) (Event, bool, error) {
	if sessionID == j.gateFor {
		j.enterOnce.Do(func() { close(j.entered) })
		<-j.gate
	}
	if j.delay > 0 {
		time.Sleep(j.delay)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.ids[sessionID] == nil {
		j.ids[sessionID] = make(map[string]bool)
	}
	if j.ids[sessionID][event.ID] {
		return event, false, nil
	}
	j.ids[sessionID][event.ID] = true
	next := j.last[sessionID] + 1
	if event.Sequence > next {
		next = event.Sequence
	}
	j.last[sessionID] = next
	event.Sequence = next
	return event, true, nil
}
func (j *gatedJournal) LoadTail(string, int) ([]Event, error) { return []Event{}, nil }
func (j *gatedJournal) Close() error                          { return nil }

func TestSlowDurableAppendDoesNotBlockOtherSessions(t *testing.T) {
	journal := newGatedJournal("slow-chat")
	store := NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	classifyInteractiveTestSession(t, store, "slow-chat")
	classifyInteractiveTestSession(t, store, "fast-chat")

	slowDone := make(chan struct{})
	go func() {
		store.AddEvent("slow-chat", journalTestEvent("slow-1", "waiting on disk"))
		close(slowDone)
	}()
	<-journal.entered

	fastDone := make(chan struct{})
	go func() {
		store.AddEvent("fast-chat", journalTestEvent("fast-1", "not blocked"))
		_ = store.GetEvents("slow-chat", GetEventsOptions{SinceIndex: -1})
		close(fastDone)
	}()
	select {
	case <-fastDone:
	case <-time.After(2 * time.Second):
		close(journal.gate)
		t.Fatal("another session's AddEvent/GetEvents waited on a slow durable append")
	}
	if got := len(store.GetAllEventsRaw("fast-chat")); got != 1 {
		t.Fatalf("fast chat events = %d, want 1", got)
	}
	close(journal.gate)
	<-slowDone
	if got := len(store.GetAllEventsRaw("slow-chat")); got != 1 {
		t.Fatalf("slow chat events = %d, want 1", got)
	}
}

func TestConcurrentAppendsKeepPerSessionOrder(t *testing.T) {
	journal := newGatedJournal("")
	journal.delay = time.Millisecond
	store := NewEventStore(1000)
	defer store.Stop()
	store.SetDurableJournal(journal)
	classifyInteractiveTestSession(t, store, "chat")

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				store.AddEvent("chat", journalTestEvent(fmt.Sprintf("durable-%d", i), "message"))
			} else {
				store.AddEvent("chat", streamingTestEvent(fmt.Sprintf("live-%d", i), "chunk"))
			}
		}(i)
	}
	wg.Wait()
	events := store.GetAllEventsRaw("chat")
	if len(events) != 40 {
		t.Fatalf("events = %d, want 40", len(events))
	}
	for i := 1; i < len(events); i++ {
		if events[i].Sequence <= events[i-1].Sequence {
			t.Fatalf("sequence not increasing at %d: %d then %d", i, events[i-1].Sequence, events[i].Sequence)
		}
	}
}

func TestJournaledChatIsAdoptedWhenFirstEventArrivesAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.sqlite")
	journal, err := OpenSQLiteEventJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	first := NewEventStore(100)
	first.SetDurableJournal(journal)
	classifyInteractiveTestSession(t, first, "chat-1")
	first.AddEvent("chat-1", journalTestEvent("event-1", "before restart"))
	first.Stop()

	reopened, err := OpenSQLiteEventJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	second := NewEventStore(100)
	defer second.Stop()
	second.SetDurableJournal(reopened)
	// A background notification lands before any query or restore poll.
	second.AddEvent("chat-1", journalTestEvent("event-2", "notification after restart"))
	if !second.IsDurableChatSession("chat-1") {
		t.Fatal("journaled chat stayed live-only after restart")
	}
	if err := second.SetSessionPersistenceClass("chat-1", SessionPersistenceInteractiveChat); err != nil {
		t.Fatalf("restore classification refused for journaled chat: %v", err)
	}
	page, err := second.ReadDurableChatPage("chat-1", DurableEventPageOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := journalEventIDs(page.Events); len(got) != 2 || got[1] != "event-2" {
		t.Fatalf("durable page = %v, want [event-1 event-2]", got)
	}
	if page.Events[1].Sequence <= page.Events[0].Sequence {
		t.Fatalf("post-restart sequence regressed: %+v", page.Events)
	}

	// Unknown sessions still fail closed to live-only.
	second.AddEvent("unknown", journalTestEvent("event-x", "live"))
	if second.IsDurableChatSession("unknown") {
		t.Fatal("unjournaled session was promoted to durable")
	}
}

func TestDeletedDurableChatDoesNotRecreateRows(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	classifyInteractiveTestSession(t, store, "chat-1")
	store.AddEvent("chat-1", journalTestEvent("event-1", "one"))
	if err := store.DeleteDurableChatSession("chat-1"); err != nil {
		t.Fatal(err)
	}
	// A still-running turn keeps emitting after the delete.
	store.AddEvent("chat-1", journalTestEvent("event-2", "late"))
	if known, _, err := journal.KnownSession("chat-1"); err != nil || known {
		t.Fatalf("deleted chat recreated journal state: known=%v err=%v", known, err)
	}
}

func TestBulkDeletedDurableChatsStayDeletedAndOtherChatsKeepTheirOwner(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	for _, id := range []string{"delete-a", "delete-b", "keep"} {
		classifyInteractiveTestSession(t, store, id)
		if err := journal.RegisterOwner(id, "alice"); err != nil {
			t.Fatal(err)
		}
		store.AddEvent(id, journalTestEvent(id+"-1", "before deletion"))
	}
	if err := store.DeleteDurableChatSessions([]string{"delete-b", "delete-a", "delete-a", ""}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"delete-a", "delete-b"} {
		store.AddEvent(id, journalTestEvent(id+"-late", "late update"))
		if known, _, err := journal.KnownSession(id); err != nil || known {
			t.Fatalf("deleted %s recreated: known=%v err=%v", id, known, err)
		}
		if owner, _ := journal.Owner(id); owner != "" {
			t.Fatalf("deleted %s retained owner %q", id, owner)
		}
	}
	if owner, _ := journal.Owner("keep"); owner != "alice" {
		t.Fatalf("untouched chat owner = %q", owner)
	}
	store.AddEvent("keep", journalTestEvent("keep-2", "after deletion"))
	page, err := journal.ReadPage("keep", DurableEventPageOptions{Limit: 10})
	if err != nil || len(page.Events) != 2 {
		t.Fatalf("untouched chat history = %+v, %v", page, err)
	}
}

func TestHumanFeedbackResolutionIsDurable(t *testing.T) {
	event := Event{
		ID: "resolved-1", Type: "human_feedback_resolved", Timestamp: time.Now(),
		Data: nil,
	}
	if !IsDurableChatEvent(event) {
		t.Fatal("human_feedback_resolved is not retained with interactive chat history")
	}
	if !STRUCTURAL_EVENTS["human_feedback_resolved"] {
		t.Fatal("human_feedback_resolved can be evicted from the capped event window")
	}
	if !ShouldShowEvent("human_feedback_resolved") {
		t.Fatal("human_feedback_resolved is hidden from polling, so the client cannot see it")
	}
}

func TestSessionAppendLocksArePrunedOnceIdle(t *testing.T) {
	journal := newGatedJournal("")
	journal.delay = time.Millisecond
	store := NewEventStore(1000)
	defer store.Stop()
	store.SetDurableJournal(journal)

	var wg sync.WaitGroup
	for s := 0; s < 8; s++ {
		sessionID := fmt.Sprintf("chat-%d", s)
		classifyInteractiveTestSession(t, store, sessionID)
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(sessionID string, i int) {
				defer wg.Done()
				store.AddEvent(sessionID, journalTestEvent(fmt.Sprintf("%s-%d", sessionID, i), "message"))
			}(sessionID, i)
		}
	}
	wg.Wait()

	for s := 0; s < 8; s++ {
		sessionID := fmt.Sprintf("chat-%d", s)
		events := store.GetAllEventsRaw(sessionID)
		if len(events) != 10 {
			t.Fatalf("%s events = %d, want 10", sessionID, len(events))
		}
		for i := 1; i < len(events); i++ {
			if events[i].Sequence <= events[i-1].Sequence {
				t.Fatalf("%s sequence not increasing at %d", sessionID, i)
			}
		}
	}
	store.appendLocksMu.Lock()
	remaining := len(store.appendLocks)
	store.appendLocksMu.Unlock()
	if remaining != 0 {
		t.Fatalf("append lock entries left after all appends finished: %d", remaining)
	}
}
