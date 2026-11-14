package server

import (
	"encoding/json"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// These are the same durable helper facts used by NativeBackgroundTaskLine,
// not provider-specific UI rows or estimates of the parent's token usage.
func TestNativeSubagentRowsUseSharedDurableFacts(t *testing.T) {
	journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	store := storeevents.NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	store.SetSessionOwner("chat", "person")
	if err := store.SetSessionPersistenceClass("chat", storeevents.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{eventStore: store}
	start := time.Now().Add(-time.Minute).UTC()
	record := llmtypes.NativeRecord{ID: "sdk-task:one:started", Provider: "cursor-cli", SessionID: "native", TaskID: "one", TaskRunID: "run", TurnID: "launch-turn", Kind: "helper", State: "started", Timestamp: start}
	api.observeCursorSubagentRow("not-a-chat", record)
	if len(store.GetAllEventsRaw("not-a-chat")) != 0 {
		t.Fatal("helper escaped its owned durable chat")
	}
	api.observeCursorSubagentRow("chat", record)
	for _, provider := range []string{"muse-cli", "agy-cli"} {
		r := record
		r.Provider = provider
		r.ID = provider + ":start"
		if err := api.saveNativeCodingActivity("chat", r); err != nil {
			t.Fatal(err)
		}
	}
	running := api.activeNativeBackground("chat")
	if len(running) != 3 {
		t.Fatalf("running helpers=%d", len(running))
	}
	identities := map[string]bool{}
	for _, r := range running {
		if identities[r.WorkID] || r.Tokens != nil || r.TurnID != "launch-turn" {
			t.Fatal("provider/turn identity or unreported tokens changed")
		}
		identities[r.WorkID] = true
	}
	for _, r := range running {
		r.ID += "-end"
		r.Timestamp = start.Add(30 * time.Second)
		r.State = "finished"
		if r.Provider == "muse-cli" {
			r.State = "failed"
		}
		if err := api.saveNativeCodingActivity("chat", r); err != nil {
			t.Fatal(err)
		}
	}
	if len(api.activeNativeBackground("chat")) != 0 {
		t.Fatal("ended helper still running")
	}
	page, err := journal.ReadPage("chat", storeevents.DurableEventPageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(page.Events)
	if err != nil || len(page.Events) != 6 {
		t.Fatalf("durable helper events=%s err=%v", raw, err)
	}
	for _, r := range running {
		ended := api.nativeBackgroundFacts("chat", llmtypes.NativeRecord{Provider: r.Provider, SessionID: r.SessionID, Kind: "helper", TaskID: r.TaskID, TaskRunID: r.TaskRunID, State: "progress"})
		if ended.WorkID != r.WorkID || ended.EndedAt.IsZero() || ended.Tokens != nil {
			t.Fatal("replay lost canonical task facts")
		}
	}
}

func TestNativeSubagentStampRechecksKnownChildWhileParentUnchanged(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent.db")
	child := filepath.Join(dir, "child.db")
	for _, p := range []string{parent, child} {
		if err := os.WriteFile(p, []byte("initial"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tasks := map[string]string{"child": "notified", "../../unrelated": "notified"}
	before := nativeSubagentFileStamp("agy-cli", parent, tasks)
	if err := os.WriteFile(child+"-wal", []byte("child descendant receipt"), 0600); err != nil {
		t.Fatal(err)
	}
	after := nativeSubagentFileStamp("agy-cli", parent, tasks)
	if before == after {
		t.Fatal("quiet parent's child receipt was never rechecked")
	}
	if nativeSubagentFileStamp("agy-cli", parent, map[string]string{"../../unrelated": "notified"}) != nativeTerminalFileStamp(parent) {
		t.Fatal("unadmitted sibling path reached observer")
	}
	if nativeSubagentFileStamp("muse-cli", parent, tasks) != nativeTerminalFileStamp(parent) {
		t.Fatal("other providers acquired a new child scan")
	}
}
