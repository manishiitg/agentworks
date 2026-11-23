package cursorcli

import (
	"context"
	"testing"
)

func TestCursorNativeRecordBoundary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := writeCursorStoreFixture(t, "/project", "native", []string{
		`{"role":"user","content":[{"type":"text","text":"<user_query>Question</user_query>"}]}`,
		`{"role":"assistant","content":[{"type":"text","text":"Answer"},{"type":"redacted-reasoning","text":"PRIVATE_THOUGHT"}]}`,
	})
	rows, last, err := ReadNativeRecordRows(context.Background(), path, "native", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1].Kind != "reply" || rows[1].Text != "Answer" || last == "" {
		t.Fatalf("unexpected native records: %+v", rows)
	}
	again, _, err := ReadNativeRecordRows(context.Background(), path, "native", last)
	if err != nil || len(again) != 0 {
		t.Fatal("committed blob replayed")
	}
	if _, _, err := ReadNativeRecordRows(context.Background(), path, "native", "missing-boundary"); err == nil {
		t.Fatal("changed native root was silently accepted")
	}
}

// A Cursor task call is a subagent row that closes when its result arrives.
func TestCursorNativeSubagentRowOpensAndCloses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := writeCursorStoreFixture(t, "/project", "native", []string{
		`{"role":"assistant","content":[{"type":"tool-call","toolCallId":"call-1","toolName":"task"},{"type":"tool-call","toolCallId":"call-2","toolName":"read"}]}`,
		`{"role":"tool","content":[{"type":"tool-result","toolCallId":"call-1","toolName":"task","result":{"status":"success","value":{"isBackground":false}}},{"type":"tool-result","toolCallId":"call-2","toolName":"read","result":"x"}]}`,
	})
	rows, _, err := ReadNativeRecordRows(context.Background(), path, "native", "")
	if err != nil {
		t.Fatal(err)
	}
	var helpers []string
	for _, r := range rows {
		if r.Kind == "helper" {
			helpers = append(helpers, r.TaskID+":"+r.State)
		}
	}
	if len(helpers) != 2 || helpers[0] != "call-1:started" || helpers[1] != "call-1:finished" {
		t.Fatalf("want only the task call's row to open and close, got %v", helpers)
	}
}

func TestCursorTaskLaunchAcknowledgementIsNotCompletion(t *testing.T) {
	for _, raw := range []string{`{"status":"success","value":{"isBackground":true}}`, `"launched"`, `{"status":"success","value":{}}`} {
		if got := cursorTaskResultState([]byte(raw)); got != "" {
			t.Fatalf("launch/unknown closed helper: %s", got)
		}
	}
	if got := cursorTaskResultState([]byte(`{"status":"error","error":{"message":"private"}}`)); got != "failed" {
		t.Fatalf("native error did not close helper: %s", got)
	}
}
