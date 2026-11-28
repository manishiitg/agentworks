package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Continue online (#4520) copies the files a chat changed to the server; a .env file may hold key values and never
// leaves the Mac (keys go to the online project's vault by name).
func TestContinueOnlineNeverCopiesEnvFiles(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{"notes/plan.md": "plan", ".env": "TOKEN=x", "src/.env.local": "TOKEN=y"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	call := func(name, args string) llmtypes.ContentPart {
		return llmtypes.ToolCall{ID: name, Type: "function", FunctionCall: &llmtypes.FunctionCall{Name: name, Arguments: args}}
	}
	plan := &continueOnlinePlan{Chat: "Fix", Skipped: []string{}, history: []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{
		call("Write", `{"file_path":"notes/plan.md"}`),
		call("Edit", `{"file_path":"`+filepath.Join(root, ".env")+`"}`),
		call("apply_patch", `{"input":"*** Update File: src/.env.local\n"}`),
		call("Read", `{"file_path":"notes/other.md"}`),
	}}}}
	continueOnlineCollectFiles(plan, crewroot.Roots{Files: root, Resources: filepath.Join(root, ".agentworks")})
	if len(plan.Files) != 1 || plan.Files[0].To != "from-mac/notes/plan.md" {
		t.Fatalf("files = %+v, want only from-mac/notes/plan.md", plan.Files)
	}
	if len(plan.Skipped) != 2 {
		t.Fatalf("skipped = %v, want both .env files named as not copied", plan.Skipped)
	}
}
