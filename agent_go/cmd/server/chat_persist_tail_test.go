package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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
	var failAppend, failResume atomic.Bool
	router := gin.New()
	router.Any("/api/documents/*filepath", func(c *gin.Context) {
		p := strings.TrimPrefix(c.Param("filepath"), "/")
		if p == path+"/append-history" && failAppend.Swap(false) {
			c.JSON(500, gin.H{"error": "injected append failure"})
			return
		}
		if p == chatHistoryResumeSnapshotPath(path) && c.Request.Method == http.MethodPut && failResume.Swap(false) {
			c.JSON(500, gin.H{"error": "injected projection failure"})
			return
		}
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
	compare := func(incoming []llmtypes.MessageContent, wantFast bool, fallback ...bool) {
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
		if wantFast && len(fallback) == 0 && (fullReads.Load() != reads || fullWrites.Load() != writes) {
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

	// An append refusal and an error after append acceptance both fall back to
	// full alignment; neither may drop the new rows or duplicate an accepted tail.
	retry := append(append([]llmtypes.MessageContent(nil), next...), llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "after failure"))
	failAppend.Store(true)
	compare(retry, true, true)
	retry = append(append([]llmtypes.MessageContent(nil), retry...), llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "after projection failure"))
	failResume.Store(true)
	compare(retry, true, true)
	// The turn save and the whole-record relay/snapshot writer use the same
	// chatConversationMutex over read/modify/write, not two independent locks.
	raceInput := append(append([]llmtypes.MessageContent(nil), retry...), llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "concurrent save"))
	before := read()
	expectedRace, err := mergeChatConversationSnapshots(before, map[string]interface{}{"conversation_history": raceInput})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(map[string]interface{}{"session_id": session, "user_id": "owner", "conversation_history": raceInput})
	start := make(chan struct{})
	errs := make(chan error, 1)
	var writers sync.WaitGroup
	writers.Add(2)
	go func() {
		defer writers.Done()
		<-start
		api.persistChatConversationToPathWithTerminalSession(session, "", "multi-agent", "owner", raceInput, nil, nil, path)
	}()
	go func() {
		defer writers.Done()
		<-start
		errs <- persistRawConversationSnapshot(context.Background(), path, string(snapshot))
	}()
	close(start)
	writers.Wait()
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	gotRace, _ := json.Marshal(read()["conversation_history"])
	wantRace, _ := json.Marshal(expectedRace)
	var gotRaceValue, wantRaceValue interface{}
	json.Unmarshal(gotRace, &gotRaceValue)
	json.Unmarshal(wantRace, &wantRaceValue)
	if !reflect.DeepEqual(gotRaceValue, wantRaceValue) {
		t.Fatal("concurrent saves duplicated or lost the tail")
	}
	// Reprime from the current saved snapshot after the independent writer.
	data, err := chatlog.Load(local)
	if err != nil {
		t.Fatal(err)
	}
	racedTarget, found, err := parseRestoredChatHistoryPersistTarget(data, session, path)
	if err != nil || !found {
		t.Fatal("cannot re-read race result")
	}
	api.rememberWrittenResumeHistory("owner", session, path, racedTarget.History, nil)
	next = racedTarget.History
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

// #4080: 15 chats loading 7k-18k saved messages at once froze a laptop. A
// coding turn now reads only the recent window; its save must still equal the
// full merge (file, resume page positions and turn counts) without reading or
// rewriting the whole history.
func TestCodingTurnRecentWindowSavesLikeTheFullHistory(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	priorDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", priorDocs) })
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"}]}`)
	const root = "Workflow/example"
	const session = "recent-window"
	path := root + "/builder/conversation/2026-11-26/" + chatHistoryConversationFileName(session)
	local := filepath.Join(docs, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
		t.Fatal(err)
	}
	saved := []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "old system")}
	for i := 0; i < 2000; i++ {
		saved = append(saved, llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "question"), llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "step"), llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "answer"))
	}
	raw, _ := json.Marshal(map[string]interface{}{"session_id": session, "user_id": "owner", "title": "Long chat", "conversation_history": saved})
	if err := chatlog.Save(local, raw); err != nil {
		t.Fatal(err)
	}
	var fullReads, fullWrites atomic.Int64
	router := gin.New()
	router.Any("/api/documents/*filepath", func(c *gin.Context) {
		p := strings.TrimPrefix(c.Param("filepath"), "/")
		if p == path && c.Request.Method == http.MethodGet && c.Query("header_only") != "true" {
			fullReads.Add(1)
		}
		if p == path && c.Request.Method == http.MethodPut {
			fullWrites.Add(1)
		}
		workspacehandlers.HandleDocumentRequest(c)
	})
	upstream := httptest.NewServer(router)
	defer upstream.Close()
	t.Setenv("WORKSPACE_API_URL", upstream.URL)

	source := statResumeHistory("owner", root, path)
	window, ok, base, baseTurns, err := readRecentChatHistoryPersistTarget(source, session)
	if err != nil || !ok {
		t.Fatalf("recent window: ok=%t err=%v", ok, err)
	}
	if base != len(saved)-1-recentResumeTurns*3 || baseTurns != 2000-recentResumeTurns || window.History[0].Role != llmtypes.ChatMessageTypeSystem {
		t.Fatalf("window base=%d turns=%d messages=%d", base, baseTurns, len(window.History))
	}
	api := &StreamingAPI{}
	api.restoreResolvedResumeTarget(session, &resolvedResumeTarget{SessionID: session, WorkspacePath: root, ConversationPath: path, History: window.History, source: source, Base: base, BaseTurns: baseTurns}, "")

	incoming := append([]llmtypes.MessageContent(nil), window.History...)
	incoming[0] = llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "new system")
	incoming = append(incoming, llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "new question"), llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "new answer"))
	before, _ := chatlog.Load(local)
	var beforeDoc map[string]interface{}
	json.Unmarshal(before, &beforeDoc)
	expected, err := mergeChatConversationSnapshots(beforeDoc, map[string]interface{}{"conversation_history": append(append([]llmtypes.MessageContent{incoming[0]}, saved[1:]...), incoming[len(incoming)-2:]...)})
	if err != nil {
		t.Fatal(err)
	}
	api.persistChatConversationToPathWithTerminalSession(session, "", "multi-agent", "owner", incoming, nil, nil, path)
	if fullReads.Load() != 0 || fullWrites.Load() != 0 {
		t.Fatalf("window save read or rewrote the whole chat: reads=%d writes=%d", fullReads.Load(), fullWrites.Load())
	}
	after, _ := chatlog.Load(local)
	var afterDoc map[string]interface{}
	json.Unmarshal(after, &afterDoc)
	got, _ := json.Marshal(afterDoc["conversation_history"])
	want, _ := json.Marshal(expected)
	var gotValue, wantValue interface{}
	json.Unmarshal(got, &gotValue)
	json.Unmarshal(want, &wantValue)
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatal("window save differs from the full merge")
	}
	snapshot, err := os.ReadFile(filepath.Join(docs, filepath.FromSlash(chatHistoryResumeSnapshotPath(path))))
	if err != nil {
		t.Fatal(err)
	}
	projected := boundChatHistoryResumeSnapshot(projectChatHistoryConversationForResumePage(after, chatHistoryResumeSnapshotTurns, 0))
	var savedProjection, fullProjection interface{}
	if json.Unmarshal(snapshot, &savedProjection) != nil || json.Unmarshal(projected, &fullProjection) != nil || !reflect.DeepEqual(savedProjection, fullProjection) {
		t.Fatal("window resume page differs from the full chat's (positions or turn counts)")
	}
}

// #4288: when a coding CLI relaunches with a changed system prompt (e.g. after
// TEAM_INSTRUCTIONS changed), server.go merges resolvedTarget.History with the
// turn's history via mergeRestoredChatHistory before saving. That merge must
// replace the leading system message instead of duplicating it into the body,
// keeping the save on the tail-append path with zero full-file reads/writes.
func TestCodingTurnRecentWindowWithUpdatedSystemPromptStaysOnTailAppend(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	priorDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", priorDocs) })
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"}]}`)
	const root = "Workflow/example"
	const session = "relaunch-system-change"
	path := root + "/builder/conversation/2026-11-26/" + chatHistoryConversationFileName(session)
	local := filepath.Join(docs, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
		t.Fatal(err)
	}
	saved := []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "old system")}
	for i := 0; i < 60; i++ {
		saved = append(saved, llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "question"), llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "answer"))
	}
	raw, _ := json.Marshal(map[string]interface{}{"session_id": session, "user_id": "owner", "title": "Relaunch test", "conversation_history": saved})
	if err := chatlog.Save(local, raw); err != nil {
		t.Fatal(err)
	}
	var fullReads, fullWrites atomic.Int64
	router := gin.New()
	router.Any("/api/documents/*filepath", func(c *gin.Context) {
		p := strings.TrimPrefix(c.Param("filepath"), "/")
		if p == path && c.Request.Method == http.MethodGet && c.Query("header_only") != "true" {
			fullReads.Add(1)
		}
		if p == path && c.Request.Method == http.MethodPut {
			fullWrites.Add(1)
		}
		workspacehandlers.HandleDocumentRequest(c)
	})
	upstream := httptest.NewServer(router)
	defer upstream.Close()
	t.Setenv("WORKSPACE_API_URL", upstream.URL)

	source := statResumeHistory("owner", root, path)
	window, ok, base, baseTurns, err := readRecentChatHistoryPersistTarget(source, session)
	if err != nil || !ok {
		t.Fatalf("recent window: ok=%t err=%v", ok, err)
	}
	api := &StreamingAPI{}
	api.restoreResolvedResumeTarget(session, &resolvedResumeTarget{SessionID: session, WorkspacePath: root, ConversationPath: path, History: window.History, source: source, Base: base, BaseTurns: baseTurns}, "")

	// Simulate a relaunched coding CLI returning a new system prompt + the new exchange,
	// merged through mergeNativeContinuationChatHistory and then mergeRestoredChatHistory (server.go:8276).
	cliTurn := []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "new system after team instructions edit"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "broadcast notice"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "noted"),
	}
	persisted := mergeNativeContinuationChatHistory(window.History, cliTurn)
	api.conversationMux.Lock()
	api.replaceConversationHistoryLocked(session, persisted)
	api.conversationMux.Unlock()
	forDisk := mergeRestoredChatHistory(window.History, persisted)
	if !appendableHistoryShape(forDisk) {
		t.Fatalf("merged history lost appendable shape (system message leaked into body)")
	}

	api.persistChatConversationToPathWithTerminalSession(session, "", "multi-agent", "owner", forDisk, nil, nil, path)
	if fullReads.Load() != 0 || fullWrites.Load() != 0 {
		t.Fatalf("relaunch save fell back to full file read/write: reads=%d writes=%d", fullReads.Load(), fullWrites.Load())
	}

	after, err := chatlog.Load(local)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		History []llmtypes.MessageContent `json:"conversation_history"`
	}
	if err := json.Unmarshal(after, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.History) != len(saved)+2 {
		t.Fatalf("saved len = %d, want %d", len(doc.History), len(saved)+2)
	}
	if doc.History[0].Role != llmtypes.ChatMessageTypeSystem {
		t.Fatalf("history[0].Role = %q, want system", doc.History[0].Role)
	}
	for i := 1; i < len(doc.History); i++ {
		if doc.History[i].Role == llmtypes.ChatMessageTypeSystem {
			t.Fatalf("unexpected system message inside body at index %d", i)
		}
	}
}

// #4309: when a restored chat runs under a different live session ID than its
// persisted session ID, persistChatConversationToPathWithTerminalSession must
// look up resumeHistorySources by the live session so tail-append still fires.
func TestPersistChatTailUsesLiveSessionKeyForRestoredChat(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	priorDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", priorDocs) })
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"}]}`)
	const root = "Workflow/example"
	const persistSession = "persisted-old-session"
	const liveSession = "live-current-session"
	path := root + "/builder/conversation/2026-11-26/" + chatHistoryConversationFileName(persistSession)
	local := filepath.Join(docs, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
		t.Fatal(err)
	}
	saved := []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "system"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "first"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "reply"),
	}
	raw, _ := json.Marshal(map[string]interface{}{"session_id": persistSession, "user_id": "owner", "title": "Restored", "conversation_history": saved})
	if err := chatlog.Save(local, raw); err != nil {
		t.Fatal(err)
	}
	var fullReads, fullWrites atomic.Int64
	router := gin.New()
	router.Any("/api/documents/*filepath", func(c *gin.Context) {
		p := strings.TrimPrefix(c.Param("filepath"), "/")
		if p == path && c.Request.Method == http.MethodGet && c.Query("header_only") != "true" {
			fullReads.Add(1)
		}
		if p == path && c.Request.Method == http.MethodPut {
			fullWrites.Add(1)
		}
		workspacehandlers.HandleDocumentRequest(c)
	})
	upstream := httptest.NewServer(router)
	defer upstream.Close()
	t.Setenv("WORKSPACE_API_URL", upstream.URL)

	source := statResumeHistory("owner", root, path)
	api := &StreamingAPI{}
	// Restored under liveSession (the current tab's session), persisting to persistSession.
	api.restoreResolvedResumeTarget(liveSession, &resolvedResumeTarget{
		SessionID:        persistSession,
		WorkspacePath:    root,
		ConversationPath: path,
		History:          saved,
		source:           source,
		Base:             0,
		BaseTurns:        0,
	}, "")
	// Mark prefixStable as a loaded window/written source would have.
	api.rememberWrittenResumeHistory("owner", liveSession, path, saved, nil)

	incoming := append(append([]llmtypes.MessageContent(nil), saved...),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "follow-up"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "done"),
	)
	api.conversationMux.Lock()
	api.replaceConversationHistoryLocked(liveSession, incoming)
	api.conversationMux.Unlock()

	api.persistChatConversationToPathWithTerminalSession(persistSession, liveSession, "multi-agent", "owner", incoming, nil, nil, path)
	if fullReads.Load() != 0 || fullWrites.Load() != 0 {
		t.Fatalf("restored live-session save fell back to full rewrite: reads=%d writes=%d", fullReads.Load(), fullWrites.Load())
	}
}

// #4080: sending checked an open tab's session by listing the project's chats
// (up to 2,000 files read, 0.2-15.6 s per send). The direct lookup must give
// the same answer: in this project only when its transcript is in the
// project's own folder, in the workspace its runtime names.
func TestProjectChatSessionWorkspaceFromFileNamesAndHeader(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	priorDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", priorDocs) })
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"}]}`)
	const root = "Workflow/example"
	save := func(session string, runtime map[string]interface{}) {
		t.Helper()
		local := filepath.Join(docs, filepath.FromSlash(root+"/builder/conversation/2026-11-26/"+chatHistoryConversationFileName(session)))
		if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
			t.Fatal(err)
		}
		record := map[string]interface{}{"session_id": session, "user_id": "owner", "conversation_history": []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "hi")}}
		if runtime != nil {
			record["runtime"] = runtime
		}
		raw, _ := json.Marshal(record)
		if err := chatlog.Save(local, raw); err != nil {
			t.Fatal(err)
		}
	}
	save("in-project", nil)
	save("ran-elsewhere", map[string]interface{}{"workspace_path": "Workflow/other"})
	if ws, ok := projectChatSessionWorkspace("owner", "in-project", root); !ok || ws != normalizeConversationWorkspace(root) {
		t.Fatalf("project chat: workspace=%q found=%t", ws, ok)
	}
	if ws, ok := projectChatSessionWorkspace("owner", "ran-elsewhere", root); !ok || ws != normalizeConversationWorkspace("Workflow/other") {
		t.Fatalf("runtime workspace ignored: %q found=%t", ws, ok)
	}
	if _, ok := projectChatSessionWorkspace("owner", "not-here", root); ok {
		t.Fatal("a session with no transcript in the project counted as the project's")
	}
}
