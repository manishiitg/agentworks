package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
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
	got, partial := listLocalNativeSessions(context.Background(), root, []localNativeSource{claude})
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
	if current, _ := listLocalNativeSessions(context.Background(), root, []localNativeSource{claude}); len(current) != 0 {
		t.Fatal("removed session still available")
	}
	codex := localNativeSource{Provider: "codex-cli", ConnectionID: "global:codex-cli", Root: t.TempDir(), Label: "Your local login"}
	recognitionWrite(t, filepath.Join(codex.Root, "2026", "10", "27", "rollout-start-native-codex.jsonl"), fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":\"native-codex\",\"cwd\":%q}}\n", root))
	if rows, _ := listLocalNativeSessions(context.Background(), root, []localNativeSource{codex}); len(rows) != 1 || rows[0].NativeID != "native-codex" {
		t.Fatalf("Codex metadata not found: %+v", rows)
	}
}

// Real workspace handlers + private registry: own HTTP/MCP/chat discovery,
// original account, stable side tab and durable native handle, without a model.
func TestLocalProjectRecognitionAndResumeHTTP(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("MULTI_USER_MODE", "")
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "claude-code,codex-cli")
	t.Setenv("AGENTWORKS_PROVIDER_POLICY", `{"claude-code":{"available_to":"all"},"codex-cli":{"available_to":"all"}}`)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
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
	f.write(t, virtual+"/product.json", `{"schema_version":1,"product":"work","id":"local-example","title":"Local example","session_id":"local-main","owner_id":"default"}`)
	f.write(t, virtual+"/workflow.json", `{"id":"local-example","label":"Local example","created_by":"default"}`)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: project, ProjectID: project, OwnerID: "default", Shared: true, Private: true, CanonicalRoot: virtual, LocalRoot: root}); err != nil {
		t.Fatal(err)
	}
	recognitionWrite(t, filepath.Join(root, "CLAUDE.md"), "Existing project guidance")
	path := filepath.Join(home, ".claude", "projects", claudeNativeTranscriptProjectSlug(root), "native-example.jsonl")
	recognitionWrite(t, path, fmt.Sprintf("{\"type\":\"user\",\"sessionId\":\"native-example\",\"cwd\":%q,\"timestamp\":\"2026-10-27T10:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"Explain the project\"}}\n{\"type\":\"assistant\",\"sessionId\":\"native-example\",\"cwd\":%q,\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"It has two modules.\"}]}}\n", root, root))
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
	body := fmt.Sprintf(`{"project_id":%q,"id":%q}`, project, data.Sessions[0].ID)
	if denied := request(http.MethodPost, "", body, limited); denied.Code != 403 {
		t.Fatalf("read token resumed: %d %s", denied.Code, denied.Body.String())
	}
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
	if err != nil || !found || runtime.AgentSessionHandle.Provider.NativeSessionID != "native-example" || runtime.AgentSessionHandle.ConnectionID != "global:claude-code" {
		t.Fatalf("lost original native/account: %+v %v", runtime, err)
	}
	currentProfile, _ := f.api.agentProfiles.Resolve("work", 0, "default")
	applyCrewNativeToolPolicy(&currentProfile, true, true)
	if runtime.WorkshopMode != crewCLIMode(false) || runtime.AgentToolsMode != normalizeAgentToolsMode(currentProfile.Runtime.AgentTools.Mode) || runtime.AgentSessionHandle.Provider.WorkingDir != root {
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
}
