package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

func recognitionWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLocalProjectRecognitionPreservesInstructionsAndSkipsOutside(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"CLAUDE.md", "AGENTS.md", "app/AGENTS.md", ".claude/skills/review/SKILL.md", ".agents/skills/research/SKILL.md", ".agentworks/skills/report/SKILL.md", "node_modules/pkg/CLAUDE.md"} {
		recognitionWrite(t, filepath.Join(root, path), "original instructions")
	}
	outside := t.TempDir()
	recognitionWrite(t, filepath.Join(outside, "CLAUDE.md"), "outside")
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	got := recognizeLocalProject(context.Background(), root)
	if len(got.Instructions) != 3 || len(got.Skills) != 3 {
		t.Fatalf("unexpected discovery: %+v", got)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md")); string(data) != "original instructions" {
		t.Fatal("changed existing instructions")
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "original instructions") || strings.Contains(string(raw), outside) {
		t.Fatal("recognition leaked file bodies/outside path")
	}
}

func TestLocalProjectNativeDiscoveryMatchesCwdAccountAndCurrentFile(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	history := t.TempDir()
	claude := localNativeSource{Provider: "claude-code", ConnectionID: "global:claude-code", Root: history, Label: "Your local login"}
	path := filepath.Join(history, "same-slug", "native-one.jsonl")
	recognitionWrite(t, path, fmt.Sprintf("{\"type\":\"user\",\"sessionId\":\"native-one\",\"cwd\":%q,\"message\":{\"role\":\"user\",\"content\":\"Explain this project\"}}\n", root))
	recognitionWrite(t, filepath.Join(history, "similar-slug", "native-other.jsonl"), fmt.Sprintf("{\"type\":\"user\",\"sessionId\":\"native-other\",\"cwd\":%q,\"message\":{\"content\":\"private elsewhere\"}}\n", root+"-other"))
	got, partial := listLocalNativeSessions(context.Background(), root, []localNativeSource{claude}, nil)
	if partial || len(got) != 1 || got[0].Title != "Explain this project" {
		t.Fatalf("wrong sessions: %+v partial=%t", got, partial)
	}
	other := claude
	other.ConnectionID = "another-own-account"
	if localNativeSessionKey(other, "native-one") == got[0].ID {
		t.Fatal("session identity lost account")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if current, _ := listLocalNativeSessions(context.Background(), root, []localNativeSource{claude}, nil); len(current) != 0 {
		t.Fatal("removed session still available")
	}
	codex := localNativeSource{Provider: "codex-cli", ConnectionID: "global:codex-cli", Root: t.TempDir(), Label: "Your local login"}
	recognitionWrite(t, filepath.Join(codex.Root, "2026", "10", "27", "rollout-start-native-codex.jsonl"), fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":\"native-codex\",\"cwd\":%q}}\n", root))
	if rows, _ := listLocalNativeSessions(context.Background(), root, []localNativeSource{codex}, nil); len(rows) != 1 || rows[0].NativeID != "native-codex" {
		t.Fatalf("Codex metadata not found: %+v", rows)
	}
}

// Real workspace handlers + private registry: own HTTP/MCP/chat discovery,
// original account, stable side tab and durable native handle, without a model.
func TestLocalProjectRecognitionAndResumeHTTP(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("MULTI_USER_MODE", "")
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "claude-code,codex-cli,cursor-cli")
	t.Setenv("AGENTWORKS_PROVIDER_POLICY", `{"claude-code":{"available_to":"all"},"codex-cli":{"available_to":"all"},"cursor-cli":{"available_to":"all"}}`)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CURSOR_CONFIG_DIR", "")
	oldOS := cliHostOS
	cliHostOS = "darwin"
	t.Cleanup(func() { cliHostOS = oldOS })
	withMemoryUserDirectory(t, `{"users":[{"id":"default","can_create":true,"products":["work"]},{"id":"other","can_create":true,"products":["work"]}]}`)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.Mkdir(filepath.Join(root, ".agentworks"), 0700); err != nil {
		t.Fatal(err)
	}
	const project = "local-example"
	const virtual = "Crew/local-example"
	f.write(t, "_system/crew-data/"+project+"/product.json", `{"schema_version":1,"product":"work","id":"local-example","title":"Local example","session_id":"local-main","owner_id":"default"}`)
	f.write(t, "_system/crew-data/"+project+"/workflow.json", `{"id":"local-example","label":"Local example","created_by":"default"}`)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: project, ProjectID: project, OwnerID: "default", Shared: true, Private: true, CanonicalRoot: virtual, LocalRoot: root}); err != nil {
		t.Fatal(err)
	}
	recognitionWrite(t, filepath.Join(root, "CLAUDE.md"), "Existing project guidance")
	path := filepath.Join(home, ".claude", "projects", claudeNativeTranscriptProjectSlug(root), "11111111-1111-4111-8111-111111111111.jsonl")
	recognitionWrite(t, path, fmt.Sprintf("{\"type\":\"user\",\"sessionId\":\"11111111-1111-4111-8111-111111111111\",\"cwd\":%q,\"timestamp\":\"2026-10-27T10:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"Explain the project\"}}\n{\"type\":\"assistant\",\"sessionId\":\"11111111-1111-4111-8111-111111111111\",\"timestamp\":\"2026-10-27T10:00:01Z\",\"cwd\":%q,\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"It has two modules.\"}]}}\n", root, root))
	request := func(method, query, body string, claims *UserClaims) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/agent-profiles/work/local-project"+query, strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		f.api.handleLocalProject(w, r)
		return w
	}
	// An account shared for running models still keeps its owner's chats private.
	providerConnectionsCache.Lock()
	oldLoaded, oldURL, oldRecords := providerConnectionsCache.loaded, providerConnectionsCache.url, providerConnectionsCache.records
	providerConnectionsCache.loaded = true
	providerConnectionsCache.url = getWorkspaceAPIURL()
	providerConnectionsCache.records = []storedProviderConnection{{ProviderConnection: ProviderConnection{ID: "shared-login", Provider: "claude-code", DisplayName: "Shared login", OwnerUserID: "other", AuthMethod: "cli_login", Sharing: &ProviderConnectionSharing{Mode: "users", Users: []string{"default"}}}}}
	providerConnectionsCache.Unlock()
	t.Cleanup(func() {
		providerConnectionsCache.Lock()
		providerConnectionsCache.loaded, providerConnectionsCache.url, providerConnectionsCache.records = oldLoaded, oldURL, oldRecords
		providerConnectionsCache.Unlock()
	})
	sharedHome, err := providerConnectionHome("shared-login")
	if err != nil {
		t.Fatal(err)
	}
	recognitionWrite(t, filepath.Join(sharedHome, ".claude", "projects", "example", "private-shared.jsonl"), fmt.Sprintf(`{"type":"user","sessionId":"private-shared","cwd":%q,"message":{"content":"Other person's private chat"}}`+"\n", root))
	owner := crewWriter("default")
	// PLAT-746-336: preview a chosen folder before creation with the same own
	// account rules; no Crew resources or native session may be written.
	newRoot, _ := filepath.EvalSymlinks(t.TempDir())
	newPath := filepath.Join(home, ".claude", "projects", claudeNativeTranscriptProjectSlug(newRoot), "22222222-2222-4222-8222-222222222222.jsonl")
	recognitionWrite(t, newPath, fmt.Sprintf("{\"type\":\"user\",\"sessionId\":\"22222222-2222-4222-8222-222222222222\",\"cwd\":%q,\"message\":{\"role\":\"user\",\"content\":\"Before creating a Crew\"}}\n", newRoot))
	folderQuery := "?root_path=" + url.QueryEscape(newRoot)
	beforeCreate := request(http.MethodGet, folderQuery+"&sessions=true", "", owner)
	var beforeData localProjectRecognition
	if err := json.Unmarshal(beforeCreate.Body.Bytes(), &beforeData); err != nil || beforeCreate.Code != 200 || len(beforeData.Sessions) != 1 {
		t.Fatalf("folder discovery: %d %s %v", beforeCreate.Code, beforeCreate.Body.String(), err)
	}
	previewQuery := folderQuery + "&native_id=" + beforeData.Sessions[0].ID
	if preview := request(http.MethodGet, previewQuery, "", owner); preview.Code != 200 || !strings.Contains(preview.Body.String(), "Before creating a Crew") {
		t.Fatalf("folder preview: %d %s", preview.Code, preview.Body.String())
	}
	readOnly := crewWriter("default")
	readOnly.AccessToken = &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}
	if denied := request(http.MethodGet, previewQuery, "", readOnly); denied.Code != 403 {
		t.Fatalf("read token previewed unbound folder: %d", denied.Code)
	}
	wmNew := httptest.NewRecorder()
	rNew := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(context.WithValue(context.Background(), UserContextKey, owner))
	f.api.externalCrewChatsCall(wmNew, rNew, map[string]any{"root_path": newRoot, "action": "native_list"})
	if wmNew.Code != 200 || !strings.Contains(wmNew.Body.String(), beforeData.Sessions[0].ID) {
		t.Fatalf("folder MCP: %d %s", wmNew.Code, wmNew.Body.String())
	}
	if wrongFolder := request(http.MethodGet, "?root_path="+url.QueryEscape(t.TempDir())+"&native_id="+beforeData.Sessions[0].ID, "", owner); wrongFolder.Code != 404 {
		t.Fatalf("preview matched another folder: %d", wrongFolder.Code)
	}
	if _, err := os.Stat(filepath.Join(newRoot, ".agentworks")); !os.IsNotExist(err) {
		t.Fatal("preview created Crew resources")
	}
	w := request(http.MethodGet, "?project_id="+project+"&sessions=true", "", owner)
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var data localProjectRecognition
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Instructions) != 1 || len(data.Sessions) != 1 {
		t.Fatalf("list data: %s", w.Body.String())
	}
	available := request(http.MethodGet, "?project_id="+project+"&availability=true", "", owner)
	if available.Code != 200 || !strings.Contains(available.Body.String(), `"can_import_local_chats":true`) {
		t.Fatalf("availability: %d %s", available.Code, available.Body.String())
	}
	// The MCP adapter returns the same canonical discovery, and native chat
	// registers that same action rather than scanning a second path.
	wm := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(context.WithValue(context.Background(), UserContextKey, owner))
	f.api.externalCrewChatsCall(wm, r, map[string]any{"crew_id": project, "action": "project_context"})
	if wm.Code != 200 || !strings.Contains(wm.Body.String(), "CLAUDE.md") {
		t.Fatalf("MCP: %d %s", wm.Code, wm.Body.String())
	}
	if denied := request(http.MethodGet, "?project_id="+project, "", crewWriter("other")); denied.Code != 404 {
		t.Fatalf("other person saw project: %d", denied.Code)
	}
	limited := crewWriter("default")
	limited.AccessToken = &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}
	if denied := request(http.MethodGet, "?project_id="+project+"&native_id="+data.Sessions[0].ID, "", limited); denied.Code != 403 {
		t.Fatalf("read token previewed private native history: %d", denied.Code)
	}
	body := fmt.Sprintf(`{"project_id":%q,"id":%q}`, project, data.Sessions[0].ID)
	if denied := request(http.MethodPost, "", body, limited); denied.Code != 403 {
		t.Fatalf("read token resumed: %d %s", denied.Code, denied.Body.String())
	}
	preview := request(http.MethodGet, "?project_id="+project+"&native_id="+data.Sessions[0].ID, "", owner)
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), "It has two modules.") {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	original, _ := os.ReadFile(path)
	w = request(http.MethodPost, "", body, owner)
	if w.Code != 200 {
		t.Fatalf("resume: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		SessionID string `json:"session_id"`
		Key       string `json:"conversation_key"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if !strings.HasPrefix(response.Key, project+codeChatSideMarker) {
		t.Fatal("replaced main chat")
	}
	runtime, found, err := ReadChatHistoryRuntimeForSession("default", response.SessionID, virtual)
	if err != nil || !found || runtime.AgentSessionHandle.Provider.NativeSessionID == "11111111-1111-4111-8111-111111111111" || runtime.AgentSessionHandle.Provider.NativeSessionID == "" || runtime.AgentSessionHandle.ConnectionID != "global:claude-code" {
		t.Fatalf("lost independent native copy/original account: %+v %v", runtime, err)
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != string(original) {
		t.Fatal("import modified native original")
	}
	if !runtime.NativeHandoff {
		t.Fatal("native copy would be replaced by transcript-only continuation")
	}
	listedAgain := request(http.MethodGet, "?project_id="+project+"&sessions=true", "", owner)
	var afterImport localProjectRecognition
	if json.Unmarshal(listedAgain.Body.Bytes(), &afterImport) != nil || len(afterImport.Sessions) != 1 {
		t.Fatalf("imported copy appeared as original: %s", listedAgain.Body.String())
	}
	currentProfile, _ := f.api.agentProfiles.Resolve("work", 0, "default")
	applyCrewNativeToolPolicy(&currentProfile, true, true)
	wantCwd, cwdErr := crewCLIWorkingDir(virtual, "default", response.SessionID, "claude-code", false)
	if cwdErr != nil {
		t.Fatal(cwdErr)
	}
	if runtime.WorkshopMode != crewCLIMode(false) || runtime.AgentToolsMode != normalizeAgentToolsMode(currentProfile.Runtime.AgentTools.Mode) || runtime.AgentSessionHandle.Provider.WorkingDir != wantCwd {
		t.Fatal("resume changed Crew mode or canonical cwd")
	}
	w = request(http.MethodPost, "", body, owner)
	if w.Code != 200 || !strings.Contains(w.Body.String(), response.SessionID) {
		t.Fatalf("reopen not stable: %d %s", w.Code, w.Body.String())
	}
	if f.upstreamCalls.Load() != 0 {
		t.Fatal("opening started a model turn")
	}
	if _, err := os.Stat(filepath.Join(root, ".agentworks", "builder")); !os.IsNotExist(err) {
		t.Fatal("chat leaked into project resources")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if gone := request(http.MethodPost, "", body, owner); gone.Code != 404 {
		t.Fatalf("resumed removed source: %d", gone.Code)
	}
	// PLAT-746-335: Cursor is the owner's explicit same-conversation exception.
	// Read the real CLI SQLite layout; never reinterpret it as an SDK agent.
	cursorID := "33333333-3333-4333-8333-333333333333"
	config := filepath.Join(home, ".cursor")
	cursorPath := recognitionCursorStore(t, config, root, cursorID)
	cursorOriginal, _ := os.ReadFile(cursorPath)
	listed := request(http.MethodGet, "?project_id="+project+"&sessions=true", "", owner)
	var cursorData localProjectRecognition
	if json.Unmarshal(listed.Body.Bytes(), &cursorData) != nil || len(cursorData.Sessions) != 1 || cursorData.Sessions[0].Provider != "cursor-cli" {
		t.Fatalf("Cursor discovery: %d %s", listed.Code, listed.Body.String())
	}
	cursorKey := cursorData.Sessions[0].ID
	cursorPreview := "?project_id=" + project + "&native_id=" + cursorKey
	if preview := request(http.MethodGet, cursorPreview, "", owner); preview.Code != 200 || !strings.Contains(preview.Body.String(), "Cursor saved answer") {
		t.Fatalf("Cursor preview: %d %s", preview.Code, preview.Body.String())
	}
	if denied := request(http.MethodGet, cursorPreview, "", limited); denied.Code != 403 {
		t.Fatalf("scoped token saw Cursor history: %d", denied.Code)
	}
	if other := request(http.MethodGet, "?root_path="+url.QueryEscape(newRoot)+"&native_id="+cursorKey, "", owner); other.Code != 404 {
		t.Fatal("Cursor history crossed folder")
	}
	// Existing-store adapters need the commands present, but opening a tab must
	// execute neither. These exit if accidentally launched during the request.
	bin := t.TempDir()
	for _, name := range []string{"cursor-agent", "tmux"} {
		recognitionWrite(t, filepath.Join(bin, name), "#!/bin/sh\nexit 99\n")
		if err := os.Chmod(filepath.Join(bin, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cursorBody := fmt.Sprintf(`{"project_id":%q,"id":%q}`, project, cursorKey)
	continued := request(http.MethodPost, "", cursorBody, owner)
	if continued.Code != 200 {
		t.Fatalf("Cursor continue: %d %s", continued.Code, continued.Body.String())
	}
	_ = json.Unmarshal(continued.Body.Bytes(), &response)
	cursorRuntime, found, err := ReadChatHistoryRuntimeForSession("default", response.SessionID, virtual)
	if err != nil || !found || cursorRuntime.AgentSessionHandle.Provider.NativeSessionID != cursorID || cursorRuntime.AgentSessionHandle.Provider.Transport != "tmux" || cursorRuntime.AgentSessionHandle.Provider.NativeFormat != "cursor-cli-sqlite" || cursorRuntime.AgentSessionHandle.ConnectionID != "global:cursor-cli" {
		t.Fatalf("Cursor lost original identity/account/transport: %+v %v", cursorRuntime, err)
	}
	if again := request(http.MethodPost, "", cursorBody, owner); again.Code != 200 || !strings.Contains(again.Body.String(), response.SessionID) {
		t.Fatal("Cursor reopening replaced the tab")
	}
	current, _ := os.ReadFile(cursorPath)
	if !bytes.Equal(current, cursorOriginal) || f.upstreamCalls.Load() != 0 {
		t.Fatal("Cursor preview/open modified source or ran a model")
	}
	// A forged cwd inside the right hashed directory still grants no history.
	recognitionWrite(t, filepath.Join(filepath.Dir(cursorPath), "meta.json"), fmt.Sprintf(`{"schemaVersion":1,"cwd":%q}`, newRoot))
	if gone := request(http.MethodGet, cursorPreview, "", owner); gone.Code != 404 {
		t.Fatal("Cursor preview trusted the hash over metadata")
	}

	// PLAT-746: a large journal must never enter the native-copy path before
	// previewing. Recent-only is an explicit new chat, not native continuation.
	largeID := "33333333-3333-4333-8333-333333333333"
	largePath := filepath.Join(filepath.Dir(path), largeID+".jsonl")
	recognitionWrite(t, largePath, fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-10-27T10:00:00Z","message":{"content":"Old work"}}`+"\n", largeID, root))
	large, err := os.OpenFile(largePath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = large.Seek(128<<20, 0); err != nil {
		t.Fatal(err)
	}
	_, err = large.WriteString("\n" + fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-11-09T10:00:00Z","message":{"content":"Recent work"}}`+"\n", largeID, root))
	large.Close()
	if err != nil {
		t.Fatal(err)
	}
	largeKey := localNativeSessionKey(localNativeSource{Provider: "claude-code", ConnectionID: "global:claude-code"}, largeID)
	largeQuery := "?project_id=" + project + "&native_id=" + largeKey
	if got := request(http.MethodGet, largeQuery, "", owner); got.Code != 200 || !strings.Contains(got.Body.String(), "Recent work") || strings.Contains(got.Body.String(), `"text":"Old work"`) {
		t.Fatalf("large preview: %d %s", got.Code, got.Body.String())
	}
	largeBody := fmt.Sprintf(`{"project_id":%q,"id":%q}`, project, largeKey)
	if got := request(http.MethodPost, "", largeBody, owner); got.Code != 422 || !strings.Contains(got.Body.String(), "native_copy_unavailable") {
		t.Fatalf("large native copy was not refused before staging: %d", got.Code)
	}
	recentBody := fmt.Sprintf(`{"project_id":%q,"id":%q,"recent_only":true}`, project, largeKey)
	got := request(http.MethodPost, "", recentBody, owner)
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"recent_only":true`) {
		t.Fatalf("recent import: %d %s", got.Code, got.Body.String())
	}
	var recent struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(got.Body.Bytes(), &recent)
	rawRecent, err := ReadChatHistoryConversation("default", recent.SessionID, virtual)
	if err != nil || len(rawRecent) > nativeHistoryTextBytes || !strings.Contains(string(rawRecent), "Recent work") || strings.Contains(string(rawRecent), `"Text":"Old work"`) || strings.Contains(string(rawRecent), `"external_session_id"`) {
		t.Fatalf("recent import claimed old context or expanded history: bytes=%d err=%v", len(rawRecent), err)
	}
	if info, err := os.Stat(largePath); err != nil || info.Size() < 128<<20 {
		t.Fatal("large native original changed")
	}

}

func recognitionCursorStore(t *testing.T, config, cwd, id string) string {
	t.Helper()
	directory, err := sessiontransfer.CursorCLIPath(config, cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	recognitionWrite(t, filepath.Join(directory, "meta.json"), fmt.Sprintf(`{"schemaVersion":1,"cwd":%q,"title":"Existing Cursor work"}`, cwd))
	path := filepath.Join(directory, "store.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)`, `CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)`} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	rootID := strings.Repeat("ff", 32)
	if _, err := db.Exec(`INSERT INTO meta(key,value) VALUES('0', ?)`, fmt.Sprintf(`{"latestRootBlobId":%q}`, rootID)); err != nil {
		t.Fatal(err)
	}
	root := []byte{}
	for i, message := range []string{`{"role":"user","content":[{"type":"text","text":"<user_query>Continue my work</user_query>"}]}`, `{"role":"assistant","content":[{"type":"text","text":"Cursor saved answer"}]}`} {
		ref := bytes.Repeat([]byte{byte(i + 1)}, 32)
		root = append(append(root, 0x0a, 32), ref...)
		if _, err := db.Exec(`INSERT INTO blobs(id,data) VALUES(?,?)`, hex.EncodeToString(ref), []byte(message)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO blobs(id,data) VALUES(?,?)`, rootID, root); err != nil {
		t.Fatal(err)
	}
	return path
}
