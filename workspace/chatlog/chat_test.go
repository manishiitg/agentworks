package chatlog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func msg(role, text string) json.RawMessage {
	return json.RawMessage(`{"Role":"` + role + `","Parts":[{"Text":"` + text + `"}]}`)
}

func turn(id, at string, texts ...string) Turn {
	t := Turn{ID: id, At: at}
	for i, text := range texts {
		role := "human"
		if i%2 == 1 {
			role = "ai"
		}
		t.Messages = append(t.Messages, msg(role, text))
	}
	return t
}

func historyTexts(t *testing.T, dir string, lastTurns int) string {
	t.Helper()
	raw, err := Record(dir, lastTurns, false)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		History []struct{ Parts []struct{ Text string } } `json:"conversation_history"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	var out []string
	for _, m := range record.History {
		out = append(out, m.Parts[0].Text)
	}
	return strings.Join(out, ",")
}

// A save never rewrites what is stored: the earlier turns stay byte-identical,
// and an amend (same turn id) is a new line that wins on read.
func TestAppendKeepsEarlierTurnsByteIdenticalAndAmendWins(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "builder", "conversation", "session-s")
	if _, err := AppendTurns(dir, []Turn{turn("t1", "2026-11-03T10:00:00Z", "hi", "hello")}, map[string]json.RawMessage{"title": json.RawMessage(`"Chat"`)}, ModeReplace); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, TurnsFile))
	if _, err := AppendTurns(dir, []Turn{{ID: "t2", At: "2026-11-03T10:05:00Z", Messages: []json.RawMessage{msg("system", "prompt"), msg("human", "q2")}}}, nil, ModeReplace); err != nil {
		t.Fatal(err)
	}
	// The running turn gets its answer later: an amend, not an in-place edit.
	if _, err := AppendTurns(dir, []Turn{{ID: "t2", Messages: []json.RawMessage{msg("ai", "a2")}}}, nil, ModeExtend); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, TurnsFile))
	if !bytes.HasPrefix(after, before) {
		t.Fatalf("earlier lines changed:\nbefore %s\nafter  %s", before, after)
	}
	if got := historyTexts(t, dir, 0); got != "prompt,hi,hello,q2,a2" {
		t.Fatalf("history = %s", got)
	}
	header, _ := readHeaderMap(dir)
	if headerInt(header, hTurns) != 2 || headerInt(header, hMessages) != 4 || string(header["title"]) != `"Chat"` {
		t.Fatalf("header = %s", mustJSON(header))
	}
}

// A crash mid-append leaves a line without its newline: readers skip it, and
// the next append keeps its bytes in turns.jsonl.torn instead of dropping them.
func TestTornLastLineIsSkippedAndKept(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "chat_history", "session-s")
	if _, err := AppendTurns(dir, []Turn{turn("t1", "2026-11-03T10:00:00Z", "hi", "hello")}, nil, ModeReplace); err != nil {
		t.Fatal(err)
	}
	file, _ := os.OpenFile(filepath.Join(dir, TurnsFile), os.O_WRONLY|os.O_APPEND, 0)
	_, _ = file.WriteString(`{"id":"t2","messages":[{"Role":"human"`)
	file.Close()
	if got := historyTexts(t, dir, 0); got != "hi,hello" {
		t.Fatalf("torn line must be skipped, got %s", got)
	}
	if got := historyTexts(t, dir, 1); got != "hi,hello" {
		t.Fatalf("page read must skip the torn line, got %s", got)
	}
	stats, err := AppendTurns(dir, []Turn{turn("t3", "2026-11-03T10:10:00Z", "q3")}, nil, ModeReplace)
	if err != nil || stats.TornMoved == 0 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	torn, _ := os.ReadFile(filepath.Join(dir, TurnsFile+TornSuffix))
	if !strings.Contains(string(torn), `{"id":"t2"`) {
		t.Fatalf("torn bytes not kept: %q", torn)
	}
	if got := historyTexts(t, dir, 0); got != "hi,hello,q3" {
		t.Fatalf("history after repair = %s", got)
	}
}

func TestPageReadReturnsLastTurns(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "chat_history", "session-s")
	for i, id := range []string{"a", "b", "c", "d"} {
		at := "2026-11-03T10:0" + string(rune('0'+i)) + ":00Z"
		if _, err := AppendTurns(dir, []Turn{turn(id, at, "q"+id, "r"+id)}, nil, ModeReplace); err != nil {
			t.Fatal(err)
		}
	}
	if got := historyTexts(t, dir, 2); got != "qc,rc,qd,rd" {
		t.Fatalf("page = %s", got)
	}
	chat, err := ReadChat(dir, 2)
	if err != nil || chat.TotalTurns != 4 || len(chat.Turns) != 2 {
		t.Fatalf("chat=%+v err=%v", chat, err)
	}
}

func seqTurn(id, at string, seqs ...int) Turn {
	t := Turn{ID: id, At: at}
	for _, seq := range seqs {
		t.UIEvents = append(t.UIEvents, json.RawMessage(`{"id":"e`+itoa(seq)+`","sequence":`+itoa(seq)+`}`))
	}
	return t
}

func itoa(n int) string { return strconv.Itoa(n) }

func eventSeqs(events []json.RawMessage) []int {
	var out []int
	for _, raw := range events {
		out = append(out, eventSequence(raw))
	}
	return out
}

// An amend of an old turn is appended after newer turns but keeps its old
// time and events: a reader after seq 10 must still get turn B, and a page of
// the newest turn must be B, not the late-appended amend of A.
func TestReadsAfterAPositionFollowTimeNotAppendOrder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "chat_history", "session-s")
	for _, turn := range []Turn{seqTurn("A", "2026-11-04T10:00:00Z", 1, 10), seqTurn("B", "2026-11-04T10:05:00Z", 11, 20)} {
		if _, err := AppendTurns(dir, []Turn{turn}, nil, ModeReplace); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := AppendTurns(dir, []Turn{seqTurn("A", "", 1, 10)}, nil, ModeReplace); err != nil {
		t.Fatal(err)
	}
	events, _, err := EventsAfter(dir, 10, nil)
	if err != nil || fmt.Sprint(eventSeqs(events)) != "[11 20]" {
		t.Fatalf("after 10 = %v err=%v, want B's events", eventSeqs(events), err)
	}
	chat, err := ReadChat(dir, 1)
	if err != nil || len(chat.Turns) != 1 || chat.Turns[0].ID != "B" {
		t.Fatalf("newest page = %+v err=%v, want B", chat.Turns, err)
	}
}

// A poll with nothing new reads no lines; a line appended without its header
// update (a crash between the two writes) is still found.
func TestPollWithNothingNewReadsNoLinesButFindsAnUncountedAppend(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "chat_history", "session-s")
	if _, err := AppendTurns(dir, []Turn{seqTurn("A", "2026-11-04T10:00:00Z", 1, 2)}, nil, ModeReplace); err != nil {
		t.Fatal(err)
	}
	if events, scanned, err := EventsAfter(dir, 2, nil); err != nil || scanned != 0 || len(events) != 0 {
		t.Fatalf("nothing new: events=%d scanned=%d err=%v", len(events), scanned, err)
	}
	line, _ := json.Marshal(seqTurn("B", "2026-11-04T10:05:00Z", 3, 4))
	file, _ := os.OpenFile(filepath.Join(dir, TurnsFile), os.O_WRONLY|os.O_APPEND, 0)
	_, _ = file.Write(append(line, '\n'))
	file.Close()
	if events, _, err := EventsAfter(dir, 2, nil); err != nil || fmt.Sprint(eventSeqs(events)) != "[3 4]" {
		t.Fatalf("uncounted append: %v err=%v", eventSeqs(events), err)
	}
}

// Old saved events land on the converted turn they belong to: by their own
// turn id (matched through the person message), then by time; only events
// that match neither go on the last turn.
func TestConvertPutsEachEventOnItsOwnTurn(t *testing.T) {
	ev := func(id, kind, turn, content, at string) string {
		ts := ""
		if at != "" {
			ts = `,"timestamp":"` + at + `"`
		}
		return `{"id":"` + id + `","type":"` + kind + `"` + ts + `,"data":{"data":{"content":"` + content + `","metadata":{"turn_id":"` + turn + `"}}}}`
	}
	record := `{"session_id":"s","conversation_history":[` + string(msg("human", "first question")) + `,` + string(msg("ai", "a1")) + `,` +
		string(msg("human", "second question")) + `,` + string(msg("ai", "a2")) + `,` + string(msg("human", "third")) + `],"ui_events":[` +
		strings.Join([]string{
			ev("e1", "user_message", "tA", "first question", "2026-11-04T10:00:00Z"),
			ev("e2", "unified_completion", "tA", "", "2026-11-04T10:01:00Z"),
			ev("e3", "user_message", "tB", "second question", "2026-11-04T10:05:00Z"),
			ev("e4", "tool_call_start", "tB", "", "2026-11-04T10:05:30Z"),
			ev("e5", "streaming_chunk", "", "", "2026-11-04T10:06:00Z"),
			ev("e6", "notice", "", "", ""),
		}, ",") + `]}`
	plan, err := PlanRecord([]byte(record), "c")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, turn := range plan.Turns {
		var ids []string
		for _, raw := range turn.UIEvents {
			ids = append(ids, eventID(raw))
		}
		got = append(got, strings.Join(ids, "+"))
	}
	if strings.Join(got, " | ") != "e1+e2 | e3+e4+e5 | e6" {
		t.Fatalf("events per turn = %q", got)
	}
	if plan.Turns[0].At != "2026-11-04T10:00:00Z" || !laterTime(plan.Turns[2].At, plan.Turns[1].At) {
		t.Fatalf("turn times = %s %s %s", plan.Turns[0].At, plan.Turns[1].At, plan.Turns[2].At)
	}
}
