package musecli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestQuestionReaderCommittedRowsAndSettlement(t *testing.T) {
	owner := "question-reader-test"
	path := filepath.Join(t.TempDir(), "session.jsonl")
	key, _ := musePersistentKey(owner)
	musePersistentPool.Lock()
	musePersistentPool.m[key] = &musePersistentSession{nativeSessionID: "native-question-test", logPath: path, userChoice: true}
	musePersistentPool.Unlock()
	t.Cleanup(func() { musePersistentPool.Lock(); delete(musePersistentPool.m, key); musePersistentPool.Unlock() })
	requested := `{"sequence":1,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-1","event":{"kind":"user_input_prompt_requested","prompt_id":"prompt-1","questions":[{"id":"color","header":"Color","question":"Choose a color","options":[{"label":"Red"},{"label":"Blue","description":"The sky"}]}]}}}`
	settled := `{"sequence":2,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-1","event":{"kind":"user_input_prompt_settled","prompt_id":"prompt-1","outcome":"answered","answers":[{"id":"color","selected_label":"Blue"}]}}}`
	if err := os.WriteFile(path, []byte(requested), 0600); err != nil {
		t.Fatal(err)
	}
	reader := NewQuestionReaderForOwner(owner)
	rows, err := reader.Poll()
	if err != nil || len(rows) != 0 {
		t.Fatalf("uncommitted row: %+v %v", rows, err)
	}
	if err := os.WriteFile(path, []byte(requested+"\n"+settled+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err = reader.Poll()
	if err != nil || len(rows) != 2 || rows[0].Questions[0].Options[1].Description != "The sky" || rows[1].Answers[0].SelectedLabel != "Blue" {
		t.Fatalf("rows: %+v %v", rows, err)
	}
	if err := SubmitQuestionAnswers(context.Background(), owner, "prompt-1", []QuestionAnswer{{ID: "color", SelectedLabel: "Red"}}); err == nil {
		t.Fatal("settled prompt was accepted")
	}
}

func TestMuseUserChoiceWaitsWithoutAutoSelecting(t *testing.T) {
	opts := &llmtypes.CallOptions{}
	WithUserChoice(true)(opts)
	ctx := museWithAutoAnswer(context.Background(), opts)
	if pending, err := museHandlePendingQuestion(ctx, "must-not-touch-tmux", museQuestionFixture); !pending || err != nil {
		t.Fatalf("choice mode: %v %v", pending, err)
	}
}

func TestMuseReadWidgetCheckboxWrappedAndTruncatedRows(t *testing.T) {
	w, ok := museReadWidget(museCheckboxFixture)
	if !ok || len(w.rows) != 5 || w.cursor != 0 || !w.rows[0].box || w.rows[0].checked || !w.rows[4].submit {
		t.Fatalf("checkbox widget: %+v %v", w, ok)
	}
	if row, err := museFindRow(w, "Password reset"); err != nil || row != 1 {
		t.Fatalf("label with description column: %d %v", row, err)
	}
	// When one label prefixes another, the exact row wins.
	prefixed := strings.Replace(museCheckboxFixture, "3. [ ] Article reactions             Let readers react to articles", "3. [ ] Password", 1)
	if w, _ := museReadWidget(prefixed); true {
		if row, err := museFindRow(w, "Password"); err != nil || row != 2 {
			t.Fatalf("exact row: %d %v", row, err)
		}
	}
	checked := strings.Replace(museCheckboxFixture, "3. [ ] Article", "3. [x] Article", 1)
	if w, _ := museReadWidget(checked); !w.rows[2].checked {
		t.Fatal("checked state not read")
	}
	long := "An option label long enough that the TUI wraps it onto the next line"
	wrapped := strings.Replace(museCheckboxFixture, "  2. [ ] Password reset                 Forgot-password flow with email reset links\n",
		"  2. [ ] An option label long enough that the TUI\n         wraps it onto the next line\n", 1)
	if w, ok := museReadWidget(wrapped); !ok {
		t.Fatal("wrapped widget not read")
	} else if row, err := museFindRow(w, long); err != nil || row != 1 {
		t.Fatalf("wrapped label: %d %v", row, err)
	}
	truncated := strings.Replace(museCheckboxFixture, "Password reset                 Forgot-password flow with email reset links", "An option label long enough…", 1)
	if w, _ := museReadWidget(truncated); w.rows[1].text == "" {
		t.Fatal("truncated row missing")
	} else if row, err := museFindRow(w, long); err != nil || row != 1 {
		t.Fatalf("truncated label: %d %v", row, err)
	}
}

func TestMuseValidateMultiSelectAnswer(t *testing.T) {
	q := Question{ID: "f", Options: []QuestionOption{{Label: "A"}, {Label: "B"}, {Label: "C"}}, Selection: &QuestionSelection{Mode: "multiple", MinSelections: 1, MaxSelections: 2}}
	for _, bad := range [][]string{nil, {"A", "B", "C"}, {"A", "A"}, {"Z"}} {
		if museValidateAnswer(q, QuestionAnswer{ID: "f", SelectedLabels: bad}) == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	if err := museValidateAnswer(q, QuestionAnswer{ID: "f", SelectedLabels: []string{"C", "A"}}); err != nil {
		t.Fatal(err)
	}
	if got := FirstOptionAnswers([]Question{q, {ID: "s", Options: []QuestionOption{{Label: "X"}}}}); got[0].SelectedLabels[0] != "A" || got[1].SelectedLabel != "X" {
		t.Fatalf("first-option answers: %+v", got)
	}
}
