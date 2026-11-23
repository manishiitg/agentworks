package claudecode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A chat started before its CLI was confined keeps its conversation in the unconfined home; the
// confined launch must find it in the private home, or --resume exits with "No conversation found".
func TestClaudeAdoptResumedConversationCopiesOnlyThisFolders(t *testing.T) {
	shared, private, work := t.TempDir(), t.TempDir(), t.TempDir()
	slug := claudeTranscriptProjectSlug(work)
	src := filepath.Join(shared, ".claude", "projects", slug)
	if err := os.MkdirAll(filepath.Join(src, "conv-1", "subagents"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(src, "conv-1.jsonl"), []byte("history\n"), 0o600)
	_ = os.WriteFile(filepath.Join(src, "conv-1", "subagents", "a.jsonl"), []byte("child\n"), 0o600)
	_ = os.WriteFile(filepath.Join(src, "other.jsonl"), []byte("not this one\n"), 0o600)
	policy := &llmtypes.CLISecurityPolicy{PrivateHome: private, CredentialHome: shared}

	claudeAdoptResumedConversation(policy, []string{"claude", "--resume", "conv-1"}, work)

	dst := filepath.Join(private, ".claude", "projects", slug)
	if data, err := os.ReadFile(filepath.Join(dst, "conv-1.jsonl")); err != nil || string(data) != "history\n" {
		t.Fatalf("conversation not adopted: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "conv-1", "subagents", "a.jsonl")); err != nil {
		t.Fatalf("subagent transcript not adopted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "other.jsonl")); err == nil {
		t.Fatal("copied a conversation that was not resumed")
	}
	// An existing private copy is kept, never overwritten.
	_ = os.WriteFile(filepath.Join(dst, "conv-1.jsonl"), []byte("newer\n"), 0o600)
	claudeAdoptResumedConversation(policy, []string{"claude", "--resume", "conv-1"}, work)
	if data, _ := os.ReadFile(filepath.Join(dst, "conv-1.jsonl")); string(data) != "newer\n" {
		t.Fatal("overwrote the private copy")
	}
	// Not a resume, or an id that is a path: nothing happens.
	claudeAdoptResumedConversation(policy, []string{"claude", "--resume", "../x"}, work)
	claudeAdoptResumedConversation(policy, []string{"claude"}, work)
}
