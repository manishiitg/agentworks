package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

// The pushed branch carries only the chat's own uncommitted files on top of HEAD: other uncommitted work in the same
// folder stays on the Mac (it was about to go along with an unrelated edit, #4520).
func TestContinueOnlineBranchHasOnlyTheChatsFiles(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		out, err := continueOnlineGitErr(root, 30*time.Second, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	git("init", "-q")
	git("config", "user.email", "owner@example.com")
	git("config", "user.name", "Owner")
	git("remote", "add", "origin", "https://example.com/repo.git")
	for _, name := range []string{"chat.txt", "other.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("old\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".")
	git("commit", "-q", "-m", "base")
	for _, name := range []string{"chat.txt", "other.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("new\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	plan := &continueOnlinePlan{Chat: "Fix", Skipped: []string{}, history: []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{
		llmtypes.ToolCall{ID: "1", Type: "function", FunctionCall: &llmtypes.FunctionCall{Name: "Edit", Arguments: `{"file_path":"` + filepath.Join(root, "chat.txt") + `"}`}},
	}}}}
	continueOnlineCollectFiles(plan, crewroot.Roots{Files: t.TempDir()})
	if len(plan.Repos) != 1 || plan.Repos[0].Changed != 1 {
		t.Fatalf("repos = %+v, want the repository with the chat's one changed file", plan.Repos)
	}
	sha, err := continueOnlineSnapshot(plan.Repos[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := git("show", sha+":chat.txt"); got != "new" {
		t.Fatalf("chat.txt on the branch = %q, want the chat's change", got)
	}
	if got := git("show", sha+":other.txt"); got != "old" {
		t.Fatalf("other.txt on the branch = %q, want it untouched", got)
	}
	if git("status", "--porcelain", "--", "other.txt") == "" {
		t.Fatal("the folder's own uncommitted change was lost")
	}
}
