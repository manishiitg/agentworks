package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-1024: a stale placement never gets repinned or linked by name.
// The exact owned tab becomes a private source; old working files stay inert.
func TestCrewOnlineConversionOwnSourceNoPrivateFilesOrNativeFallback(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newTriggerLinkEnv(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner"})
	root := crewProjectRootByID("alpha")
	record := ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: "alpha:chat:old-online", ConversationID: "conversation-old-online", SessionID: "old-online-session", WorkspacePath: root}
	original := ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: "alpha:chat:original", ConversationID: "conversation-original", SessionID: "original-session", WorkspacePath: root}
	key := productConversationRegistryEntryKey(crewProfileID, record.ConversationKey)
	otherKey := productConversationRegistryEntryKey(crewProfileID, original.ConversationKey)
	doc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{key: record, otherKey: original}, Agents: map[string]ProductAgentIdentity{key: {Name: "Helper"}, otherKey: {Name: "Helper"}}}
	store := defaultProductConversationRegistryStore()
	if err := store.writeDocument(ctx, productConversationRegistryPath("owner"), doc); err != nil {
		t.Fatal(err)
	}
	id, _, err := registerCrewPutOnlineServerSource(ctx, "owner", "alpha", record.ConversationKey)
	if err != nil {
		t.Fatal(err)
	}
	originalID, _, err := registerCrewPutOnlineServerSource(ctx, "owner", "alpha", original.ConversationKey)
	if err != nil {
		t.Fatal(err)
	}
	team, err := readCrewTeam("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err = writeCrewTeamWithAgents(team, func(rows map[string]crewTeamRegisteredAgent) { a := rows[id]; a.InTeam = true; rows[id] = a }); err != nil {
		t.Fatal(err)
	}
	path, ok := workProjectChatHistoryConversationPath("owner", root, record.SessionID, time.Now())
	if !ok {
		t.Fatal("source path")
	}
	raw, _ := json.Marshal(map[string]any{"user_id": "owner", "session_id": record.SessionID, "conversation_history": []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Private source history"}}}}})
	if err = writeRawFileToWorkspace(ctx, path, string(raw)); err != nil {
		t.Fatal(err)
	}
	placement := crewAgentPlacement{Name: "Helper", Tab: "old-online", Location: "server", SessionID: record.SessionID, ConversationID: record.ConversationID, TransferID: "old-transfer", PairID: "stale-original-pair", SplitFiles: true}
	if err = writeCrewAgentPlacement("alpha", placement); err != nil {
		t.Fatal(err)
	}
	roots, found, err := crewStorageRoots(root)
	if err != nil || !found {
		t.Fatal("roots", err)
	}
	scoped, err := crewAgentScopedRoots(roots, placement.Tab)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(scoped.Files, "private.txt"), []byte("not shared"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = crewPublishedAgent("alpha", placement.Tab); err == nil {
		t.Fatal("fixture did not reproduce stale placement")
	}
	call := func(claims *UserClaims) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/crew-teams/alpha/convert-online", bytes.NewReader([]byte("{}")))
		req = mux.SetURLVars(req.WithContext(context.WithValue(t.Context(), UserContextKey, claims)), map[string]string{"project": "alpha"})
		w := httptest.NewRecorder()
		env.api.handleCrewConvertOnline(w, req)
		return w
	}
	if w := call(&UserClaims{UserID: "other"}); w.Code != 403 {
		t.Fatalf("teammate conversion: %d", w.Code)
	}
	if w := call(&UserClaims{UserID: "owner", Scope: "run"}); w.Code != 403 {
		t.Fatalf("scoped conversion: %d", w.Code)
	}
	w := call(&UserClaims{UserID: "owner"})
	if w.Code != 200 {
		t.Fatalf("owner conversion: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Agents []crewOnlineConversion `json:"agents"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Agents) != 1 || !out.Agents[0].Ready || out.Agents[0].Agent != id || out.Agents[0].FilesKeptAside != 1 {
		t.Fatalf("conversion result: %s %v", w.Body.String(), err)
	}
	team, err = readCrewTeam("alpha")
	if err != nil || team.Agents[id].InTeam || team.Agents[originalID].ChatKey != original.ConversationKey {
		t.Fatal("identity/privacy or unrelated source changed", err)
	}
	after, err := readCrewAgentPlacements("alpha")
	if err != nil || len(after) != 0 {
		t.Fatal("placement not retired", err)
	}
	address, err := crewRetiredOnlineSource("alpha", record.ConversationKey)
	if err != nil || address != crewTeamAgentBridgeAddress("owner", id) {
		t.Fatal("missing own Ask target", err)
	}
	if address, err = crewRetiredOnlineSource("alpha", original.ConversationKey); err != nil || address != "" {
		t.Fatal("same-name original was retired", err)
	}
	if _, err = crewPublishedAgent("alpha", placement.Tab); err == nil || !strings.Contains(err.Error(), "Ask") {
		t.Fatal("retired source can resume person-slot", err)
	}
	resolveBody, _ := json.Marshal(map[string]string{"conversation_key": record.ConversationKey})
	resolveReq := httptest.NewRequest(http.MethodPost, "/api/agent-profiles/work/conversation", bytes.NewReader(resolveBody)).WithContext(ctx)
	resolveReq = mux.SetURLVars(resolveReq, map[string]string{"id": crewProfileID})
	resolved := httptest.NewRecorder()
	env.api.handleResolveAgentProfileConversation(resolved, resolveReq)
	if resolved.Code != http.StatusUnprocessableEntity || !strings.Contains(resolved.Body.String(), "Ask") {
		t.Fatalf("retired /conversation resolved native target: %d %s", resolved.Code, resolved.Body.String())
	}
	if data, err := os.ReadFile(filepath.Join(scoped.Files, "private.txt")); err != nil || string(data) != "not shared" {
		t.Fatal("selected files lost", err)
	}
	homeRoots, found, err := crewStorageRoots(crewProjectRootByID(team.OnlineHome))
	if err != nil || !found {
		t.Fatal("home roots", err)
	}
	if _, err = os.Stat(filepath.Join(homeRoots.Files, "private.txt")); !os.IsNotExist(err) {
		t.Fatal("private selected files mounted in home", err)
	}
	backups, err := filepath.Glob(filepath.Join(roots.Private, "before-online-conversion", "*", "placements.json"))
	if err != nil || len(backups) != 1 {
		t.Fatal("plain backup missing", err)
	}
	info, err := os.Stat(filepath.Dir(backups[0]))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("backup not private", err)
	}
	// A repeated one-off action has no placement left to convert or new identity.
	if w = call(&UserClaims{UserID: "owner"}); w.Code != 200 {
		t.Fatal("repeat conversion", w.Code)
	}
}
