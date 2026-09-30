package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/liveattach"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"
)

func nativeInputObserverFixture(t *testing.T) *nativeTerminalObserver {
	t.Helper()
	snapshot := terminals.Snapshot{SessionID: t.Name(), TmuxSession: "native-test-" + t.Name()}
	t.Cleanup(func() { tmuxinput.Default.ClearInteractiveDraft(snapshot.TmuxSession) })
	return &nativeTerminalObserver{api: &StreamingAPI{eventStore: storeevents.NewEventStore(100)}, snapshot: snapshot,
		nativeCounts: map[string]int{}, coveredCounts: map[string]int{}, eventCounts: map[string]int{}}
}

func nativeInputRows(texts ...string) []builderConversationMessage {
	var rows []builderConversationMessage
	for _, text := range texts {
		rows = append(rows, builderConversationMessage{Role: "human", Parts: []builderConversationPart{{Text: text}}})
	}
	return rows
}

func nativeInputUserEvent(id, text string) storeevents.Event {
	return storeevents.Event{ID: id, Type: "user_message", Data: unifiedevents.NewAgentEvent(unifiedevents.NewUserMessageEvent(0, text, "user"))}
}

func TestNativeTerminalRecordsRepeatedPromptsAndDeduplicatesAPISteers(t *testing.T) {
	o := nativeInputObserverFixture(t)
	o.consume(nativeInputRows("old"), "missing.jsonl", true)
	store := o.api.eventStore
	store.BeginDeferredSteer(o.snapshot.SessionID, nativeInputUserEvent("api1", "repeat"))
	t.Cleanup(func() { store.CompleteDeferredSteer(o.snapshot.SessionID, nativeInputUserEvent("api1", "repeat")) })
	o.consume(nativeInputRows("old", "repeat"), "missing.jsonl", false)
	if got := len(store.GetAllEventsRaw(o.snapshot.SessionID)); got != 0 {
		t.Fatalf("API input duplicated before ack: %d events", got)
	}
	store.CompleteDeferredSteer(o.snapshot.SessionID, nativeInputUserEvent("api1", "repeat"))
	for count := 2; count <= 3; count++ {
		tmuxinput.Default.NoteInteractiveInput(o.snapshot.TmuxSession, []byte("repeat\r"))
		texts := []string{"old"}
		for i := 0; i < count; i++ {
			texts = append(texts, "repeat")
		}
		o.consume(nativeInputRows(texts...), "missing.jsonl", false)
		if tmuxinput.Default.HasInteractiveDraft(o.snapshot.TmuxSession) {
			t.Fatal("durable user row did not release draft")
		}
	}
	rows := store.GetAllEventsRaw(o.snapshot.SessionID)
	ids := map[string]bool{}
	for _, row := range rows {
		if row.Type == "user_message" {
			if ids[row.ID] {
				t.Fatal("repeated native prompt reused an id")
			}
			ids[row.ID] = true
		}
	}
	if len(ids) != 3 {
		t.Fatalf("user bubbles=%d want one API + two native", len(ids))
	}
	o.consume(nativeInputRows("old", "repeat", "repeat", "repeat"), "missing.jsonl", false)
	if counts := nativeTerminalEventUserCounts(store, o.snapshot.SessionID); counts["repeat"] != 3 {
		t.Fatalf("poll duplicated prompts: %v", counts)
	}
}

func TestNativeTerminalFirstPromptAfterMissingTranscript(t *testing.T) {
	o := nativeInputObserverFixture(t)
	o.poll(true) // a new CLI has not created its native log yet
	tmuxinput.Default.NoteInteractiveInput(o.snapshot.TmuxSession, []byte("first\r"))
	o.consume(nativeInputRows("historical", "first"), "new.jsonl", false)
	counts := nativeTerminalEventUserCounts(o.api.eventStore, o.snapshot.SessionID)
	if counts["first"] != 1 || counts["historical"] != 0 {
		t.Fatalf("lost first input or replayed history: %v", counts)
	}
}

func TestNativeTerminalSeedRestoresInputModes(t *testing.T) {
	seed := string(buildLiveAttachSeed(liveattach.Reply{}, liveattach.Reply{Lines: []string{"screen"}}, liveattach.Reply{Lines: []string{"4,2|1,0,1,1,0,0,1,1,0"}}))
	for _, escape := range []string{"\x1b[?1049h", "\x1b[?25l", "\x1b[?1h", "\x1b=", "\x1b[?1003h", "\x1b[?1006h", "\x1b[3;5H"} {
		if !strings.Contains(seed, escape) {
			t.Fatalf("missing input mode %q: %q", escape, seed)
		}
	}
}

func TestNativeTerminalDraftRejectsChatWithoutDelivery(t *testing.T) {
	o := nativeInputObserverFixture(t)
	store := terminals.NewStore()
	store.HandleEvent(o.snapshot.SessionID, terminalRouteChunkEvent(o.snapshot.SessionID, "main:"+o.snapshot.SessionID, o.snapshot.TmuxSession, "pane", 1))
	o.api.terminalStore = store
	tmuxinput.Default.NoteInteractiveInput(o.snapshot.TmuxSession, []byte("unfinished"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	if !o.api.tryDeliverQueryAsLiveInput(rec, req, o.snapshot.SessionID, "new input", "new-query") || rec.Code != http.StatusLocked {
		t.Fatalf("draft not rejected before delivery: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// Real tmux control-mode and WebSocket input: UTF-8, slash text, arrow bytes,
// Ctrl+C and native bracketed paste, without contacting a coding provider.
func TestNativeTerminalRealTmuxKeyboardAndPaste(t *testing.T) {
	if testing.Short() {
		t.Skip("real tmux")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	session := "native-input-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("byte receiver unavailable")
	}
	script := `stty raw -echo; printf '\033[?2004hREADY\r\n'; perl -e '$|=1; while(sysread(STDIN, $b, 1)) { printf "%02x\r\n", ord($b) }'`
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "-x", "100", "-y", "60", "sh", "-c", script).CombinedOutput(); err != nil {
		t.Skipf("cannot start scratch tmux: %v %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-session", "-t", session).Run()
		tmuxinput.Default.ClearInteractiveDraft(session)
	})
	time.Sleep(150 * time.Millisecond)
	sessionID := t.Name()
	terminalID := sessionID + ":main:" + sessionID
	store := terminals.NewStore()
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, session, "pane", 1))
	api := &StreamingAPI{terminalStore: store, liveAttach: newLiveAttachManager()}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.handleTerminalStream(w, mux.SetURLVars(r, map[string]string{"terminal_id": terminalID}))
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/stream?cols=100&rows=60", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err = conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	raw := []byte("/model π\x1b[A\x1b[B\x03")
	if err = conn.WriteMessage(websocket.BinaryMessage, raw); err != nil {
		t.Fatal(err)
	}
	if err = conn.WriteJSON(map[string]string{"type": "paste", "text": "a\nb"}); err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte{}, raw...), []byte("\x1b[200~a\nb\x1b[201~")...)
	var received []byte
	deadline := time.Now().Add(4 * time.Second)
	for len(received) < len(want) && time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		out, e := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-t", session).Output()
		cancel()
		if e != nil {
			t.Fatal(e)
		}
		received = nil
		for _, line := range strings.Split(string(out), "\n") {
			if value, e := strconv.ParseUint(strings.TrimSpace(line), 16, 8); e == nil {
				received = append(received, byte(value))
			}
		}
		if len(received) < len(want) {
			time.Sleep(25 * time.Millisecond)
		}
	}
	if fmt.Sprintf("%x", received) != fmt.Sprintf("%x", want) {
		t.Fatalf("native bytes changed: got=%x want=%x", received, want)
	}
}
