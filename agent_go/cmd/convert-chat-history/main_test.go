package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
)

func writeCopy(t *testing.T, path string, texts ...string) {
	t.Helper()
	var history []map[string]interface{}
	history = append(history, map[string]interface{}{"Role": "system", "Parts": []map[string]string{{"Text": "prompt"}}})
	for i, text := range texts {
		role := "human"
		if i%2 == 1 {
			role = "ai"
		}
		history = append(history, map[string]interface{}{"Role": role, "Parts": []map[string]string{{"Text": text}}})
	}
	raw, _ := json.Marshal(map[string]interface{}{"session_id": "s1", "user_id": "u", "title": "Invoices", "updated_at": "2026-11-03T10:00:00Z", "conversation_history": history})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A chat saved under several day folders becomes one log with every turn in
// order, including a newer cut-down copy (a past restart cut a chat to 2
// turns), and the merge is reported. Goals chats and the old files are left
// untouched.
func TestConvertMergesCopiesAndKeepsTurnOrder(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "_system", "crew-data", "c1", "builder", "conversation")
	writeCopy(t, filepath.Join(store, "2026-10-07", "session-s1-conversation.json"), "q1", "a1")
	writeCopy(t, filepath.Join(store, "2026-10-28", "session-s1-conversation.json"), "q1", "a1", "q2", "a2", "q3", "a3")
	writeCopy(t, filepath.Join(store, "2026-11-03", "session-s1-conversation.json"), "q3", "a3")
	goals := filepath.Join(root, "Workflow", "wf", "builder", "conversation", "2026-11-03", "session-g-conversation.json")
	writeCopy(t, goals, "goal")

	var dry bytes.Buffer
	if report, err := run([]string{root}, false, false, &dry); err != nil || report.Converted != 1 || report.Chats != 1 {
		t.Fatalf("dry run report=%+v err=%v\n%s", report, err, dry.String())
	}
	if _, err := os.Stat(filepath.Join(store, "session-s1")); !os.IsNotExist(err) {
		t.Fatal("dry run must not write")
	}

	var out bytes.Buffer
	report, err := run([]string{root}, true, false, &out)
	if err != nil || report.Converted != 1 || report.Merged != 1 || report.Turns != 3 {
		t.Fatalf("report=%+v err=%v\n%s", report, err, out.String())
	}
	if !strings.Contains(out.String(), "MERGED ") {
		t.Fatalf("a merged chat must be listed:\n%s", out.String())
	}
	raw, err := chatlog.Record(filepath.Join(store, "session-s1"), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Title   string `json:"title"`
		History []struct {
			Parts []struct{ Text string }
		} `json:"conversation_history"`
	}
	_ = json.Unmarshal(raw, &record)
	var texts []string
	for _, m := range record.History {
		texts = append(texts, m.Parts[0].Text)
	}
	if got := strings.Join(texts, ","); got != "prompt,q1,a1,q2,a2,q3,a3" || record.Title != "Invoices" {
		t.Fatalf("converted = %s title=%q", got, record.Title)
	}
	if _, err := os.Stat(filepath.Join(store, "2026-11-03", "session-s1-conversation.json")); err != nil {
		t.Fatal("old copies must stay for the operator's backup")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(goals)), "session-g")); !os.IsNotExist(err) {
		t.Fatal("Goals chats must not be converted")
	}
	// Running it again converts nothing.
	if again, err := run([]string{root}, true, false, &bytes.Buffer{}); err != nil || again.Converted != 0 || again.Skipped != 1 {
		t.Fatalf("second run = %+v %v", again, err)
	}
}

// Two copies each hold turns the other lacks (showcase: the older copy had 71
// person turns, the newer 52, each with turns the other did not): the result
// has all of them in time order, and --reconvert rebuilds an already
// converted chat with the merge while keeping turns added since.
func TestConvertMergesCopiesThatEachLackTurnsAndReconverts(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "_users", "u", "chat_history")
	writeCopy(t, filepath.Join(store, "2026-10-20", "session-s1-conversation.json"), "q1", "a1", "q2", "a2", "q3", "a3")
	writeCopy(t, filepath.Join(store, "2026-10-23", "session-s1-conversation.json"), "q1", "a1", "q4", "a4")
	dir := filepath.Join(store, "session-s1")
	// Converted earlier by the newest-copy rule, then used: one new turn.
	if _, err := chatlog.ConvertRecord(dir, mustRead(t, filepath.Join(store, "2026-10-23", "session-s1-conversation.json")), "converted"); err != nil {
		t.Fatal(err)
	}
	newTurn := chatlog.Turn{ID: "query_9", At: "2027-01-01T00:00:00Z", Messages: []json.RawMessage{json.RawMessage(`{"Role":"human","Parts":[{"Text":"q5"}]}`)}}
	if _, err := chatlog.AppendTurns(dir, []chatlog.Turn{newTurn}, nil, chatlog.ModeReplace); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	report, err := run([]string{root}, true, true, &out)
	if err != nil || report.Converted != 1 || report.Merged != 1 {
		t.Fatalf("report=%+v err=%v\n%s", report, err, out.String())
	}
	if !strings.Contains(out.String(), "kept_from(day:kept/person_turns)=2026-10-20:3/3,2026-10-23:1/2") {
		t.Fatalf("per-copy counts missing:\n%s", out.String())
	}
	raw, err := chatlog.Record(dir, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		History []struct {
			Parts []struct{ Text string }
		} `json:"conversation_history"`
	}
	_ = json.Unmarshal(raw, &record)
	var texts []string
	for _, m := range record.History {
		texts = append(texts, m.Parts[0].Text)
	}
	if got := strings.Join(texts, ","); got != "prompt,q1,a1,q2,a2,q3,a3,q4,a4,q5" {
		t.Fatalf("merged = %s", got)
	}
	backups, _ := filepath.Glob(filepath.Join(root, "_users", "u", "chat_history-reconvert-backup", "*", "session-s1", chatlog.TurnsFile))
	if len(backups) != 1 {
		t.Fatalf("the earlier chat folder must be kept aside, found %v", backups)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
