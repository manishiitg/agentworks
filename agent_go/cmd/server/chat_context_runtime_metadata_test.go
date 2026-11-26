package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
)

// PLAT-746-876: the context GET's runtime resolver must work without loading
// any message bodies; the native runtime header is authoritative for resume.
func TestChatContextReadsRuntimeWithoutResolvingFullHistory(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	crew := registerTestCrew(t, "demo-context-metadata", "owner", true)
	private, ok := resolveLocalWorkflowDir(crew)
	if !ok {
		t.Fatal("Crew storage missing")
	}
	dir := filepath.Join(private, "builder", "conversation", "2026-11-13")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, chatHistoryConversationFileName("ctx-chat"))
	if err := chatlog.Save(path, []byte(`{"session_id":"ctx-chat","user_id":"owner","conversation_history":[{"Role":"human","Parts":[{"Text":"saved question"}]}],"runtime":{"kind":"coding_agent","provider":"cursor-cli","model_id":"example-model","transport":"sdk","resume_supported":true,"external_session_id":"native-session"}}`)); err != nil {
		t.Fatal(err)
	}
	// Prime the normal persisted discovery index before losing the log. This
	// isolates the runtime GET from the separate initial index-building path.
	if _, found, err := FindChatHistoryConversationPathForSession("owner", "ctx-chat", crew); err != nil || !found {
		t.Fatalf("index: %t %v", found, err)
	}
	if err := os.WriteFile(chatlog.HistoryLogPath(path), []byte("not a message log"), 0600); err != nil {
		t.Fatal(err)
	}
	// The path lookup reads file names only (it used to parse up to 1000
	// whole transcripts per turn), so a transcript that does not parse is
	// still found by its name.
	other := filepath.Join(dir, chatHistoryConversationFileName("unparsed-chat"))
	if err := os.WriteFile(other, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, found, err := FindChatHistoryConversationPathForSession("owner", "unparsed-chat", crew); err != nil || !found || got == "" {
		t.Fatalf("lookup by file name: %q %t %v", got, found, err)
	}
	record := ProductConversationRecord{SessionID: "ctx-chat", WorkspacePath: crew, Provider: "claude-code", ModelID: "old-model"}
	provider, model, transport, handle := chatContextRuntime("owner", chatContextTab{Record: record})
	if provider != "cursor-cli" || model != "example-model" || transport != "sdk" || !handle {
		t.Fatalf("runtime was not read from header: %s %s %s %t", provider, model, transport, handle)
	}
	if runtime := chatContextRuntimeMetadata("another-person", record); runtime != nil {
		t.Fatal("exposed owner's Builder runtime to another person")
	}
}
