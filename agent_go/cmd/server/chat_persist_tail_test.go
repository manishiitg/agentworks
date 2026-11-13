package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/spf13/viper"
)

// PLAT-746-876: a proven saved prefix must yield the old full-merge result,
// while writing only its new tail. Changed prefixes still use full alignment.
func TestChatPersistenceAppendsUnchangedPrefixWithIdenticalFullMerge(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	priorDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", priorDocs) })
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"}]}`)
	const root = "Workflow/example"
	const session = "persist-tail"
	path := root + "/builder/conversation/2026-11-13/" + chatHistoryConversationFileName(session)
	local := filepath.Join(docs, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
		t.Fatal(err)
	}
	base := []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "old system")}
	for i := 0; i < 1500; i++ {
		base = append(base, llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "repeat"), llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "saved reply"))
	}
	base = append(base, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: "call-old", Name: "example_tool", Content: "tool result"}}})
	raw, _ := json.Marshal(map[string]interface{}{"session_id": session, "user_id": "owner", "title": "Named chat", "kept_metadata": map[string]interface{}{"pending": true}, "conversation_history": base})
	if err := chatlog.Save(local, raw); err != nil {
		t.Fatal(err)
	}
	var fullReads, fullWrites, appended atomic.Int64
	router := gin.New()
	router.Any("/api/documents/*filepath", func(c *gin.Context) {
		p := strings.TrimPrefix(c.Param("filepath"), "/")
		if p == path && c.Request.Method == http.MethodGet && c.Query("header_only") != "true" {
			fullReads.Add(1)
		}
		if p == path && c.Request.Method == http.MethodPut {
			fullWrites.Add(1)
		}
		if p == path+"/append-history" {
			data, err := io.ReadAll(c.Request.Body)
			if err != nil {
				t.Fatal(err)
			}
			var body workspacehandlers.AppendConversationHistoryRequest
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatal(err)
			}
			appended.Add(int64(len(body.Messages)))
			c.Request.Body = io.NopCloser(strings.NewReader(string(data)))
		}
		workspacehandlers.HandleDocumentRequest(c)
	})
	upstream := httptest.NewServer(router)
	defer upstream.Close()
	t.Setenv("WORKSPACE_API_URL", upstream.URL)
	api := &StreamingAPI{}
	target := &resolvedResumeTarget{SessionID: session, WorkspacePath: root, ConversationPath: path, History: base, source: statResumeHistory("owner", root, path)}
	api.restoreResolvedResumeTarget(session, target, "")
	// Seed the proof of the full snapshot this process just wrote. Production
	// primes it at the end of the ordinary full-save path.
	api.rememberWrittenResumeHistory("owner", session, path, base, nil)
	read := func() map[string]interface{} {
		t.Helper()
		data, err := chatlog.Load(local)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]interface{}
		if err = json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	compare := func(incoming []llmtypes.MessageContent, wantFast bool) {
		t.Helper()
		before := read()
		expected, err := mergeChatConversationSnapshots(before, map[string]interface{}{"conversation_history": incoming})
		if err != nil {
			t.Fatal(err)
		}
		api.conversationMux.Lock()
		api.replaceConversationHistoryLocked(session, incoming)
		api.conversationMux.Unlock()
		_, _, fast := api.persistableChatTail("owner", session, path, incoming)
		if fast != wantFast {
			t.Fatalf("fast path=%t wanted=%t", fast, wantFast)
		}
		reads, writes := fullReads.Load(), fullWrites.Load()
		api.persistChatConversationToPathWithTerminalSession(session, "", "multi-agent", "owner", incoming, nil, nil, path)
		after := read()
		got, _ := json.Marshal(after["conversation_history"])
		want, _ := json.Marshal(expected)
		var gotValue, wantValue interface{}
		if json.Unmarshal(got, &gotValue) != nil || json.Unmarshal(want, &wantValue) != nil || !reflect.DeepEqual(gotValue, wantValue) {
			t.Fatal("incremental persistence differs from full merge")
		}
		if after["title"] != "Named chat" || after["kept_metadata"] == nil {
			t.Fatal("metadata lost")
		}
		if wantFast && (fullReads.Load() != reads || fullWrites.Load() != writes) {
			t.Fatal("fast save read or rewrote complete history")
		}
		// The bounded resume projection must keep full-source ordering/pagination.
		snapshot, err := os.ReadFile(filepath.Join(docs, filepath.FromSlash(chatHistoryResumeSnapshotPath(path))))
		if err != nil {
			t.Fatal(err)
		}
		full, _ := json.Marshal(after)
		projected := boundChatHistoryResumeSnapshot(projectChatHistoryConversationForResumePage(full, chatHistoryResumeSnapshotTurns, 0))
		var savedProjection, fullProjection interface{}
		if json.Unmarshal(snapshot, &savedProjection) != nil || json.Unmarshal(projected, &fullProjection) != nil || !reflect.DeepEqual(savedProjection, fullProjection) {
			t.Fatal("tail resume projection differs from full projection")
		}
	}
	next := append([]llmtypes.MessageContent(nil), base...)
	next[0] = llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "new system")
	next = append(next, llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "repeat"), llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "new reply"))
	compare(next, true)
	if appended.Load() != 3 {
		t.Fatalf("sent more than new system and two-row tail: %d", appended.Load())
	}
	compare(next, true)
	if appended.Load() != 4 {
		t.Fatal("repeat save appended duplicate body rows")
	}
	changed := append([]llmtypes.MessageContent(nil), next...)
	changed[1] = llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "edited older row")
	compare(changed, false)
	// Without a new system, full alignment retains the old one. Do not
	// treat that shape as a direct append of the shorter in-memory record.
	api.conversationMux.RLock()
	withoutSystem := append([]llmtypes.MessageContent(nil), api.conversationHistory[session][1:]...)
	api.conversationMux.RUnlock()
	compare(withoutSystem, false)
	if fullWrites.Load() == 0 {
		t.Fatal("changed prefix bypassed general merger")
	}
	// Cross-person scope and external changes can never use the saved proof.
	if _, _, ok := api.persistableChatTail("another-person", session, path, changed); ok {
		t.Fatal("cross-person prefix reuse")
	}
	if err := os.WriteFile(local, append(raw, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := api.persistableChatTail("owner", session, path, changed); ok {
		t.Fatal("changed source reused")
	}
}
