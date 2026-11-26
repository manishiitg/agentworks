package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

// After the chat moved off Claude, the new agent must get each finished
// task's output whichever arrives first, the task's end or its last output.
func TestClaudeFinishedTasksKeepOutputForTheNextAgent(t *testing.T) {
	list := &claudeFinishedTaskList{tasks: map[string][]claudeFinishedTask{}, outputs: map[string]string{}}
	list.noteOutput("chat", "t1", "build ok")
	list.noteState("chat", claudecode.SDKHelperTask{TaskID: "t1", Description: "Build", State: "finished"})
	list.noteState("chat", claudecode.SDKHelperTask{TaskID: "t2", Command: "npm test", State: "failed"})
	list.noteOutput("chat", "t2", "2 tests failed")
	list.noteState("chat", claudecode.SDKHelperTask{TaskID: "t3", State: "progress"})
	text := claudeFinishedTasksText(list.take("chat"))
	if !strings.Contains(text, "build ok") || !strings.Contains(text, "2 tests failed") || strings.Contains(text, "t3") {
		t.Fatalf("hand-off text:\n%s", text)
	}
	if len(list.take("chat")) != 0 || len(list.outputs) != 0 {
		t.Fatal("taking the tasks must clear them")
	}
}
