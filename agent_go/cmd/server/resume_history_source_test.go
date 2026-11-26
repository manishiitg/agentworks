package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-876: repeated turns must reuse the loaded parse, while another
// writer's replacement, a native-log append, or missing history forces reload.
func TestProductResumeReusesCanonicalHistoryUntilSourceChanges(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	crew := registerTestCrew(t, "demo-resume-cache", "owner", true)
	private, ok := resolveLocalWorkflowDir(crew)
	if !ok {
		t.Fatal("Crew storage missing")
	}
	dir := filepath.Join(private, "builder", "conversation", "2026-11-13")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, chatHistoryConversationFileName("cache-chat"))
	save := func(messages int) []llmtypes.MessageContent {
		t.Helper()
		history := make([]llmtypes.MessageContent, messages)
		for i := range history {
			history[i] = llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "saved message")
		}
		data, _ := json.Marshal(map[string]any{"session_id": "cache-chat", "user_id": "owner", "conversation_history": history, "runtime": map[string]any{"kind": "coding_agent", "provider": "cursor-cli", "transport": "sdk", "resume_supported": true}})
		if err := chatlog.Save(path, data); err != nil {
			t.Fatal(err)
		}
		return history
	}
	save(100)
	api := &StreamingAPI{}
	record := ProductConversationRecord{SessionID: "cache-chat", WorkspacePath: crew}
	load := func() *resolvedResumeTarget {
		t.Helper()
		target, found, err := api.resolveProductResumeTarget("owner", record)
		if err != nil || !found || target == nil || target.source == nil {
			t.Fatalf("resolve: found=%v err=%v target=%#v", found, err, target)
		}
		api.restoreResolvedResumeTarget(record.SessionID, target, "")
		return target
	}
	first := load()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			warm, found, err := api.resolveProductResumeTarget("owner", record)
			if err != nil || !found || &warm.History[0] != &first.History[0] {
				t.Error("unchanged conversation parsed again")
			}
		}()
	}
	wg.Wait()
	if _, ok := api.reusableProductResumeTarget("another-person", crew, record.SessionID); ok {
		t.Fatal("private history reused across people")
	}
	if _, ok := api.reusableProductResumeTarget("owner", "Crew/another", record.SessionID); ok {
		t.Fatal("history reused across projects")
	}
	if api.resumeHistorySources[record.SessionID].target.History != nil {
		t.Fatal("cache holds a second history")
	}
	// A new native log line changes the actual source, not just the header.
	row, _ := json.Marshal(llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "new native input"))
	if err := chatlog.Append(path, []json.RawMessage{row}, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.reusableProductResumeTarget("owner", crew, record.SessionID); ok {
		t.Fatal("native append reused stale history")
	}
	changed := load()
	if len(changed.History) != 101 {
		t.Fatalf("append missing: %d", len(changed.History))
	}
	// Own persistence can publish its already-parsed snapshot and new stamp.
	written := save(102)
	api.rememberWrittenResumeHistory("owner", record.SessionID, changed.ConversationPath, written, changed.Runtime)
	warm := load()
	if &warm.History[0] != &written[0] {
		t.Fatal("own snapshot was parsed again")
	}
	api.conversationMux.Lock()
	delete(api.conversationHistory, record.SessionID)
	api.conversationMux.Unlock()
	if _, ok := api.reusableProductResumeTarget("owner", crew, record.SessionID); ok {
		t.Fatal("missing canonical history reused")
	}
	api.restoreResolvedResumeTarget(record.SessionID, warm, "")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.reusableProductResumeTarget("owner", crew, record.SessionID); ok {
		t.Fatal("missing source reused")
	}

}
