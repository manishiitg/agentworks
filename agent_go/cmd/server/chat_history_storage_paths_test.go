package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/mux"
)

// Real directory moves pin typed history routing and one writer/index key.
// Native provider execution is separate acceptance, not simulated here.
func TestProjectHistoryAliasesPreserveReadsAndRestoreAuthority(t *testing.T) {
	ensureProjectStateRoot(t)
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","products":["work","code"]},{"id":"builder","username":"builder","products":["work","code"]},{"id":"run","username":"run","products":["work","code"]}]}`)
	const legacy = "_users/owner/Chats/Code/projects/history-12345678"
	const canonical = "Crew/history-12345678"
	const session = "server-history"
	rel := "builder/conversation/2026-10-18/" + chatHistoryConversationFileName(session)
	oldPath, newPath := legacy+"/"+rel, canonical+"/"+rel
	rec := projectOwnerRecord{Product: "code", Folder: "history-12345678", ProjectID: "history-project", OwnerID: "owner", Private: true, AudienceConfigured: true, RunPeople: []string{"run"}, CoOwners: []string{"builder"}}
	registry := defaultProjectOwners()
	if err := registry.Register(rec); err != nil {
		t.Fatal(err)
	}
	settings := `{"id":"history-project","product":"code","mode":"dev","created_by":"owner"}`
	ws := &mockWorkspaceAPI{files: map[string]string{legacy + "/product.json": settings, canonical + "/product.json": settings}}
	server := httptest.NewServer(ws)
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	body := []byte(`{"session_id":"server-history","user_id":"owner","agent_mode":"multi-agent","conversation_history":[{"Role":"human","Parts":[{"Text":"PRIVATE_HISTORY_MARKER"}]}],"runtime":{"kind":"coding_agent","provider":"codex-cli","resume_supported":true,"external_session_id":"native-unchanged"},"updated_at":"2026-10-18T09:00:00Z"}`)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(docs, oldPath)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, oldPath), body, 0600); err != nil {
		t.Fatal(err)
	}
	index := newChatHistoryIndex()
	index.Entries[oldPath] = chatHistoryIndexEntry{Session: ChatHistorySession{SessionID: session, UserID: "owner", ConversationPath: oldPath, WorkspacePath: legacy, Title: "Preserved title", UpdatedAt: "2026-10-18T09:00:00Z"}, SourceSize: int64(len(body)), AttributionVerified: true}
	if err := writeLocalChatHistoryIndex(filepath.Join(docs, chatHistoryIndexWorkspacePath("owner", oldPath)), &index); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(docs, "Crew"), 0711); err != nil {
		t.Fatal(err)
	}
	current := legacy
	for _, next := range []string{canonical, legacy, canonical} {
		before, _ := registry.Lookup("code", rec.Folder)
		if err := os.Rename(filepath.Join(docs, current), filepath.Join(docs, next)); err != nil {
			t.Fatal(err)
		}
		if err := registry.compareAndSetStorageLocation("code", rec.Folder, userAccessHash(before), next); err != nil {
			t.Fatal(err)
		}
		current = next
		expected := next + "/" + rel
		for _, spelling := range []string{oldPath, newPath, filepath.Join(docs, oldPath), filepath.Join(docs, newPath)} {
			if path, ok := normalizeRestoredChatHistoryConversationPath("owner", spelling); !ok || path != expected {
				t.Fatalf("owner normalization %s = %s,%v", spelling, path, ok)
			}
			runtime, found, err := ReadChatHistoryRuntimeFromPath("owner", spelling)
			if err != nil || !found || runtime == nil || runtime.ExternalSessionID != "native-unchanged" {
				t.Fatal("runtime lookup lost native identity", err)
			}
		}
		if chatConversationMutex(oldPath) != chatConversationMutex(newPath) || chatConversationMutex(oldPath) != chatConversationMutex(filepath.Join(docs, newPath)) {
			t.Fatal("one transcript acquired different writer locks")
		}
		if chatHistoryIndexMutex(chatHistoryIndexWorkspacePath("owner", oldPath)) != chatHistoryIndexMutex(chatHistoryIndexWorkspacePath("owner", newPath)) {
			t.Fatal("aliases split the index lock")
		}
		loaded, found := readLocalChatHistoryIndex(filepath.Join(docs, legacy, "builder/conversation", chatHistoryIndexFileName))
		if !found || len(loaded.Entries) != 1 || loaded.Entries[expected].Session.Title != "Preserved title" {
			t.Fatal("legacy index did not follow current storage")
		}
		sessions, handled, err := listWorkflowScopedChatHistorySessionsFromDisk("owner", chatHistoryRoot("owner"), legacy, 25, 0, "")
		if err != nil || !handled || len(sessions) != 1 || sessions[0].SessionID != session || sessions[0].ConversationPath != expected {
			t.Fatalf("legacy listing lost transcript: %+v %v", sessions, err)
		}
		// Existing Builder reads include co-owners, while path-based native
		// restoration retains its historical own-project constraint.
		if data, err := ReadChatHistoryConversation("builder", session, canonical); err != nil || !bytes.Contains(data, []byte("PRIVATE_HISTORY_MARKER")) {
			t.Fatal("co-owner lost existing Builder read", err)
		}
		if _, ok := normalizeRestoredChatHistoryConversationPath("builder", newPath); ok {
			t.Fatal("history routing broadened native restore authority")
		}
		if _, err := ReadChatHistoryConversation("run", session, canonical); err == nil {
			t.Fatal("Run gained Builder history")
		}
		// The request has no owner in its path. A same-folder alias must
		// not turn the caller's own logical tree into the owner's history.
		logical := "Chats/Code/projects/" + rec.Folder
		if routed := chatHistoryStoragePath(logical); routed != logical {
			t.Fatal("ownerless history path followed a folder-name alias")
		}
		request := httptest.NewRequest(http.MethodGet, "/api/chat-history/sessions/"+session+"?workspace_path="+url.QueryEscape(logical), nil)
		request = mux.SetURLVars(request, map[string]string{"session_id": session})
		request = request.WithContext(context.WithValue(request.Context(), UserContextKey, &UserClaims{UserID: "run"}))
		response := httptest.NewRecorder()
		getChatHistoryConversationHandler(nil)(response, request)
		if response.Code != http.StatusNotFound && response.Code != http.StatusForbidden {
			t.Fatalf("Run logical history request returned %d", response.Code)
		}
		if _, ok := normalizeRestoredChatHistoryConversationPath("run", newPath); ok {
			t.Fatal("Run gained path-based restoration")
		}
		actual, err := os.ReadFile(filepath.Join(docs, expected))
		if err != nil || !bytes.Equal(actual, body) {
			t.Fatal("routing rewrote transcript bytes", err)
		}
	}
	// Typed duplicate index keys converge without touching transcript text.
	duplicate := newChatHistoryIndex()
	duplicate.Entries[oldPath] = chatHistoryIndexEntry{Session: ChatHistorySession{SessionID: session, Title: "older"}, SourceModifiedAtUnixNano: 1}
	duplicate.Entries[newPath] = chatHistoryIndexEntry{Session: ChatHistorySession{SessionID: session, Title: "newer"}, SourceModifiedAtUnixNano: 2}
	duplicate.normalize()
	if len(duplicate.Entries) != 1 || duplicate.Entries[newPath].Session.Title != "newer" {
		t.Fatal("duplicate alias keys did not converge")
	}
	duplicate.Entries[oldPath] = chatHistoryIndexEntry{Session: ChatHistorySession{SessionID: session, Title: "Custom title", UpdatedAt: "2026-10-18T08:00:00Z"}, SourceModifiedAtUnixNano: 99}
	duplicate.Entries[newPath] = chatHistoryIndexEntry{Session: ChatHistorySession{SessionID: session, Query: "newest", UpdatedAt: "2026-10-18T09:00:00Z"}}
	duplicate.normalize()
	if entry := duplicate.Entries[newPath]; entry.Session.Title != "Custom title" || entry.Session.Query != "newest" {
		t.Fatal("newer writer entry or existing custom title was lost")
	}
	duplicate.Entries[oldPath] = chatHistoryIndexEntry{Session: ChatHistorySession{SessionID: session, Title: "older alias title", UpdatedAt: "2026-10-18T09:00:00Z"}}
	for i := 0; i < 10; i++ {
		duplicate.normalize()
		if duplicate.Entries[newPath].Session.Query != "newest" {
			t.Fatal("equal alias entries were not resolved deterministically")
		}
	}
	encoded, err := json.Marshal(duplicate)
	if err != nil || bytes.Contains(encoded, []byte(oldPath)) {
		t.Fatal("operational index retained retired path", err)
	}
	if _, ok := normalizeRestoredChatHistoryConversationPath("owner", "Crew/unknown/"+rel); ok {
		t.Fatal("unregistered Crew admitted")
	}
}
