package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// An Antigravity chat's helper row opens its subagent's own conversation (#3970): agy names the row by the
// child's conversation id, which is read from the chat's own agy store. Another chat finds nothing.
func TestAgyHelperRowOpensItsSubagentConversation(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "parent-conv.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{nativeObservers: map[string]*nativeTerminalObserver{"chat-a": {provider: "agy-cli", nativeID: "parent-conv", home: home}}}
	old := agySubagentConversationIn
	defer func() { agySubagentConversationIn = old }()
	agySubagentConversationIn = func(parent, child, store string) (llmtypes.NativeSubagentTranscript, bool) {
		if parent != "parent-conv" || child != "child-1" || store != home {
			return llmtypes.NativeSubagentTranscript{}, false
		}
		return llmtypes.NativeSubagentTranscript{ID: child, ParentID: parent, Provider: "agy-cli", Status: "running"}, true
	}
	if child, ok := api.agySubagentConversation("chat-a", "child-1"); !ok || child.ID != "child-1" {
		t.Fatalf("agy subagent = %+v, %t", child, ok)
	}
	if _, ok := api.agySubagentConversation("chat-b", "child-1"); ok {
		t.Fatal("another chat opened this chat's subagent")
	}
}
