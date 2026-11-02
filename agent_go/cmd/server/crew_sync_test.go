package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/nativersync"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	wh "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
	"github.com/spf13/viper"
)

// PLAT-856-387: use real HTTP handlers and files to prove ownership, both root
// shapes, a lost handoff response, and the locked-source/runtime distinction.
func TestCrewSyncHTTPHandoffRetryAndOwnerBoundary(t *testing.T) {
	for _, split := range []bool{false, true} {
		t.Run(map[bool]string{false: "managed", true: "selected-folder"}[split], func(t *testing.T) {
			desktopTestEnvironment(t)
			base := t.TempDir()
			docs := filepath.Join(base, "docs")
			project := filepath.Join(base, "project")
			for _, dir := range []string{docs, project} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			docs, _ = filepath.EvalSymlinks(docs)
			project, _ = filepath.EvalSymlinks(project)
			t.Setenv("WORKSPACE_DOCS_PATH", docs)
			t.Setenv("LOCAL_MODE", "true")
			t.Setenv("DEFAULT_USER_ID", "owner")
			t.Setenv("MULTI_USER_MODE", "")
			old := viper.Get("docs-dir")
			viper.Set("docs-dir", docs)
			t.Cleanup(func() { viper.Set("docs-dir", old) })
			withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"other","username":"other","can_create":true}]}`)
			files := gin.New()
			var failRegistryWrite atomic.Bool
			files.PUT("/api/documents/*filepath", func(c *gin.Context) {
				if strings.HasSuffix(c.Param("filepath"), "/"+productConversationRegistryFile) && failRegistryWrite.CompareAndSwap(true, false) {
					c.String(500, "injected registry write failure")
					return
				}
				wh.UpdateDocument(c)
			})
			files.GET("/api/documents/*filepath", wh.GetDocument)
			files.GET("/api/documents", wh.ListDocuments)
			files.POST("/api/folders", wh.CreateFolder)
			workspace := httptest.NewServer(files)
			defer workspace.Close()
			t.Setenv("WORKSPACE_API_URL", workspace.URL)
			registry := agentprofiles.NewRegistry()
			if err := registry.RegisterProfile(workCrewTestProfile([]agentprofiles.ProviderOption{{ID: "muse-cli", Label: "Muse", Provider: "muse-cli", ModelID: "muse-spark-1.3-contributor", Models: []string{"muse-spark-1.3-contributor"}}})); err != nil {
				t.Fatal(err)
			}
			journal, err := events.OpenSQLiteEventJournal(filepath.Join(base, "events.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			eventStore := events.NewEventStore(10)
			t.Cleanup(eventStore.Stop)
			eventStore.SetDurableJournal(journal)
			api := &StreamingAPI{agentProfiles: registry, activeSessions: map[string]*ActiveSessionInfo{}, eventStore: eventStore, runtimeCoordinator: NewRuntimeCoordinator()}
			owner := &UserClaims{UserID: "owner", Username: "owner"}
			ctx := context.WithValue(t.Context(), UserContextKey, owner)
			options := crewCreationOptions{}
			if split {
				options.LocalRoot = project
			}
			_, err = writeCrewCreationManifests(ctx, "owner", workCrewTestProfile(nil), "work", "", "Example", "", "", "local-id", "Crew/local", nil, options)
			if err != nil {
				t.Fatal(err)
			}
			local, _, err := crewStorageRoots("Crew/local")
			if err != nil {
				t.Fatal(err)
			}
			write := func(root, name, content string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0770); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0660); err != nil {
					t.Fatal(err)
				}
			}
			oldTransfer := func(path string, value any) []byte {
				t.Helper()
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var legacy map[string]any
				if err = json.Unmarshal(raw, &legacy); err != nil {
					t.Fatal(err)
				}
				snapshot := legacy["snapshot"].(map[string]any)
				delete(snapshot, "payloads")
				snapshot["content"].(map[string]any)["bundle"] = "b2xkLWJ1bmRsZQ=="
				raw, err = json.Marshal(legacy)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				return raw
			}
			checkOldBackup := func(path string, want []byte) {
				t.Helper()
				backups, err := filepath.Glob(path + ".*.bak")
				if err != nil || len(backups) != 1 {
					t.Fatalf("old transfer backup: %v %v", backups, err)
				}
				got, err := os.ReadFile(backups[0])
				if err != nil || !bytes.Equal(got, want) {
					t.Fatal("old transfer backup changed bytes")
				}
			}
			write(local.Files, "note.txt", "local work")
			write(local.Resources, "skills/example/SKILL.md", "Useful skill")
			write(local.Files, ".env", "API_KEY=example")
			write(local.Files, ".cache/state", "keep private")
			listing := httptest.NewRecorder()
			files.ServeHTTP(listing, httptest.NewRequest("GET", "/api/documents?folder="+local.Virtual+"&max_depth=5", nil))
			if listing.Code != 200 || !strings.Contains(listing.Body.String(), local.Virtual+"/skills/example/SKILL.md") {
				t.Fatalf("local resource listing: %d %s", listing.Code, listing.Body.String())
			}
			loseResponse := false
			dropUpload, dropDownload := false, false
			var pieceHook func(*http.Request)
			var offline atomic.Bool
			var forcedErrorCode atomic.Value
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if offline.Load() {
					w.WriteHeader(503)
					return
				}
				if r.Header.Get("Authorization") != "Bearer saved-login" || r.Header.Get("X-AgentWorks-Desktop") != "" {
					t.Error("credential boundary crossed")
					w.WriteHeader(403)
					return
				}
				if r.URL.Path == "/api/crew-relay/connect" {
					api.handleCrewRelayConnect(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, owner)))
					return
				}
				if code, _ := forcedErrorCode.Load().(string); code != "" {
					externalError(w, 409, code, "Both copies were kept.")
					return
				}
				r = r.WithContext(context.WithValue(r.Context(), UserContextKey, owner))
				r = mux.SetURLVars(r, map[string]string{"project": strings.TrimPrefix(r.URL.Path, "/api/crew-sync/")})
				if pieceHook != nil && r.URL.Query().Get("transfer") != "" {
					pieceHook(r)
				}
				response := httptest.NewRecorder()
				api.handleCrewSync(response, r)
				if dropUpload && r.Method == http.MethodPut && response.Code == 204 {
					dropUpload = false
					w.WriteHeader(502)
					return
				}
				if loseResponse && response.Code == 200 && bytes.Contains(response.Body.Bytes(), []byte(`"editing":"server"`)) {
					loseResponse = false
					w.WriteHeader(502)
					return
				}
				for key, values := range response.Header() {
					w.Header()[key] = values
				}
				w.WriteHeader(response.Code)
				if dropDownload && r.Method == http.MethodGet && r.URL.Query().Get("transfer") != "" && response.Code == 200 {
					dropDownload = false
					_, _ = w.Write(response.Body.Bytes()[:response.Body.Len()/2])
					return
				}
				_, _ = w.Write(response.Body.Bytes())
			}))
			defer remote.Close()
			if err := writeDesktopConnections("owner", []desktopConnection{{ID: "saved", Server: remote.URL, Token: "saved-login", User: UserInfo{ID: "owner"}}}); err != nil {
				t.Fatal(err)
			}
			// The machine-readable code, not human wording, determines forks.
			for _, code := range []string{crewNativeDivergedCode, "sync_unavailable"} {
				forcedErrorCode.Store(code)
				_, err := desktopCrewSyncCall(ctx, owner.UserID, &desktopConnection{Server: remote.URL, Token: "saved-login"}, "fixture", crewSyncRequest{Action: "status"})
				if errors.Is(err, sessiontransfer.ErrDiverged) != (code == crewNativeDivergedCode) {
					t.Fatal("remote conflict identity depended on wording")
				}
			}
			forcedErrorCode.Store("")
			call := func(action, who string, proof bool) *httptest.ResponseRecorder {
				t.Helper()
				r := desktopTestRequest("POST", "/", `{"action":"`+action+`"}`, who)
				if !proof {
					r.Header.Del("X-AgentWorks-Desktop")
				}
				r = mux.SetURLVars(r, map[string]string{"project": "local-id", "connection": "saved"})
				w := httptest.NewRecorder()
				api.handleDesktopCrewSync(w, r)
				return w
			}
			if w := call("update", "owner", false); w.Code != 403 {
				t.Fatalf("desktop proof: %d", w.Code)
			}
			if w := call("update", "other", true); w.Code < 400 {
				t.Fatal("another owner paired private Crew")
			}
			w := call("update", "owner", true)
			if w.Code != 200 {
				t.Fatalf("pair: %d %s", w.Code, w.Body.String())
			}
			var out crewSyncReply
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			serverRoots, err := api.crewSyncRoots(ctx, owner, out.Binding.PeerProjectID)
			if err != nil {
				t.Fatal(err)
			}
			if serverRoots.SeparateFiles() != split {
				t.Fatal("root shape changed")
			}
			checkCrewChatRelayOwnerBoundary(t, api, serverRoots.ID, out.Binding.PairID)
			checkCrewRelaySocketEndToEnd(t, api, serverRoots.ID, out.Binding.PairID)
			for _, item := range []struct{ root, name, want string }{{serverRoots.Files, "note.txt", "local work"}, {serverRoots.Resources, "skills/example/SKILL.md", "Useful skill"}} {
				raw, err := os.ReadFile(filepath.Join(item.root, item.name))
				if err != nil || string(raw) != item.want {
					t.Fatalf("file %s: %q %v", item.name, raw, err)
				}
			}
			// Owner 2 Nov (PLAT-856-509): the owner's own server gets the project's secret files; other hidden state stays.
			if _, err := os.Stat(filepath.Join(serverRoots.Files, ".env")); err != nil {
				t.Fatal("secret file did not reach the owner's server")
			}
			if _, err := os.Stat(filepath.Join(serverRoots.Files, ".cache/state")); !os.IsNotExist(err) {
				t.Fatal("copied private file")
			}
			// PLAT-856-509: a retried relay create (lost reply) returns the same empty twin, which never takes files.
			twin, err := api.crewSync(ctx, owner, "", crewSyncRequest{Action: "create", Mode: "relay", PairID: "relay-pair", SourceID: "mac-crew", Name: "Mac Crew"})
			if err != nil {
				t.Fatal(err)
			}
			again, err := api.crewSync(ctx, owner, "", crewSyncRequest{Action: "create", Mode: "relay", PairID: "retry-pair", SourceID: "mac-crew", Name: "Mac Crew"})
			if err != nil || again.ProjectID != twin.ProjectID || again.Binding == nil || again.Binding.PairID != "relay-pair" || again.Binding.Mode != "relay" {
				t.Fatalf("a retried relay create made a second twin: %+v %v", again, err)
			}
			if _, err := api.crewSync(ctx, owner, twin.ProjectID, crewSyncRequest{Action: "push", PairID: "relay-pair", TransferID: "t", Editing: "local"}); err == nil {
				t.Fatal("a relay twin accepted files")
			}
			if _, err := api.crewSync(t.Context(), crewWriter("other"), serverRoots.ID, crewSyncRequest{Action: "status"}); err == nil {
				t.Fatal("other owner accessed sync")
			}
			// PLAT-856-509: lose a piece reply after durable storage. Retry
			// resumes that exact artifact; the working-folder lock is free.
			largePath := filepath.Join(local.Files, "resumable.bin")
			large, e := os.Create(largePath)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = io.CopyN(large, rand.Reader, 12<<20); e != nil {
				t.Fatal(e)
			}
			large.Close()
			lockChecked := false
			pieceHook = func(r *http.Request) {
				if r.Method != http.MethodPut || lockChecked {
					return
				}
				lockChecked = true
				check, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				release, e := wf.LockWorkspace(check, local.Files)
				if e != nil {
					t.Errorf("upload holds chat's folder lock: %v", e)
					return
				}
				release()
				if _, e = api.crewHandoffMain(check, owner, local.ID); e != nil {
					t.Errorf("chat unavailable during upload: %v", e)
				} else {
					t.Logf("[CREW_SYNC_PROOF] at=%s chat_open_during_upload=ok", time.Now().UTC().Format(time.RFC3339Nano))
				}
				write(local.Files, "saved-during-upload.txt", "newer work stays local until next capture")
			}
			dropUpload = true
			if w = call("update", "owner", true); w.Code != 409 || dropUpload || !lockChecked {
				t.Fatalf("did not exercise interrupted upload: %d %s", w.Code, w.Body.String())
			}
			pieceHook = nil
			pendingPath, _ := crewSyncManifestPath(local.ID)
			var upload crewSyncRequest
			if e = readCrewTransfer(pendingPath+".pending", &upload); e != nil {
				t.Fatal(e)
			}
			probe := func(who, pair string) *httptest.ResponseRecorder {
				q := url.Values{"transfer": {upload.TransferID}, "pair": {pair}, "direction": {"upload"}, "blob": {"content"}}
				req := httptest.NewRequest("HEAD", "/?"+q.Encode(), nil).WithContext(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: who, Username: who}))
				req = mux.SetURLVars(req, map[string]string{"project": serverRoots.ID})
				result := httptest.NewRecorder()
				api.handleCrewSync(result, req)
				return result
			}
			if got := probe("other", upload.PairID); got.Code != 403 {
				t.Fatalf("other owner piece: %d", got.Code)
			}
			if got := probe("owner", "wrong-pair"); got.Code != 409 {
				t.Fatalf("wrong pair piece: %d", got.Code)
			} else {
				t.Logf("[CREW_SYNC_PROOF] at=%s wrong_pair_status=%d", time.Now().UTC().Format(time.RFC3339Nano), got.Code)
			}
			got := probe("owner", upload.PairID)
			offset, _ := strconv.ParseInt(got.Header().Get("Upload-Offset"), 10, 64)
			if got.Code != 200 || offset != crewSyncPieceBytes {
				t.Fatalf("saved offset: %d %d", got.Code, offset)
			}
			t.Logf("[CREW_SYNC_PROOF] at=%s interrupted_upload_saved_offset=%d", time.Now().UTC().Format(time.RFC3339Nano), offset)
			// An independently edited destination is never overwritten at apply.
			write(serverRoots.Files, "resumable.bin", "destination work")
			if w = call("update", "owner", true); w.Code != 409 {
				t.Fatal("overwrote independent destination file")
			}
			actual, _ := os.ReadFile(filepath.Join(serverRoots.Files, "resumable.bin"))
			if string(actual) != "destination work" {
				t.Fatal("lost destination work")
			}
			if e = os.Remove(filepath.Join(serverRoots.Files, "resumable.bin")); e != nil {
				t.Fatal(e)
			}
			if w = call("update", "owner", true); w.Code != 200 {
				t.Fatalf("resumed upload: %d %s", w.Code, w.Body.String())
			}
			digest := func(path string) [32]byte {
				f, e := os.Open(path)
				if e != nil {
					t.Fatal(e)
				}
				defer f.Close()
				h := sha256.New()
				if _, e = io.Copy(h, f); e != nil {
					t.Fatal(e)
				}
				var result [32]byte
				copy(result[:], h.Sum(nil))
				return result
			}
			if digest(largePath) != digest(filepath.Join(serverRoots.Files, "resumable.bin")) {
				t.Fatal("resumed bytes differ")
			}
			t.Logf("[CREW_SYNC_PROOF] at=%s resumed_upload=ok sha256_match=true", time.Now().UTC().Format(time.RFC3339Nano))
			if _, e = os.Stat(filepath.Join(serverRoots.Files, "saved-during-upload.txt")); !os.IsNotExist(e) {
				t.Fatal("acknowledged uncaptured newer work")
			}
			if w = call("update", "owner", true); w.Code != 200 {
				t.Fatalf("next capture: %d %s", w.Code, w.Body.String())
			}
			if _, e = os.Stat(filepath.Join(serverRoots.Files, "saved-during-upload.txt")); e != nil {
				t.Fatal("newer source edit never synced")
			}

			// PLAT-856-509: a no-change scan must not keep a new scratch bundle.
			if w = call("update", "owner", true); w.Code != 200 {
				t.Fatalf("unchanged scan: %d %s", w.Code, w.Body.String())
			}
			if scratch, err := filepath.Glob(filepath.Join(local.Private, "file-snapshots", "sync-bundle-*")); err != nil || len(scratch) != 0 {
				t.Fatalf("unchanged sync retained capture bundles: %v %v", scratch, err)
			}

			// PLAT-856-509: upgrading an interrupted inline upload automatically
			// preserves its old record and recaptures current working files.
			legacyPath, _ := crewSyncManifestPath(local.ID)
			legacyPath += ".pending"
			legacyBytes := oldTransfer(legacyPath, upload)
			legacyBinding, _ := crewroot.ReadSync(local.ID)
			legacyBinding.PendingAction, legacyBinding.Editing = "update", "server"
			if err := crewroot.WriteSync(local.ID, *legacyBinding); err != nil {
				t.Fatal(err)
			}
			write(local.Files, "after-upgrade.txt", "current working file")
			if w = call("update", "owner", true); w.Code != 200 {
				t.Fatalf("old upload recapture: %d %s", w.Code, w.Body.String())
			}
			checkOldBackup(legacyPath, legacyBytes)
			if got, err := os.ReadFile(filepath.Join(serverRoots.Files, "after-upgrade.txt")); err != nil || string(got) != "current working file" {
				t.Fatalf("upgrade lost source work: %q %v", got, err)
			}
			legacyBinding, _ = crewroot.ReadSync(local.ID)
			if legacyBinding.PendingAction != "" || legacyBinding.Editing != "local" {
				t.Fatal("old upload kept stale execution state")
			}

			for _, kind := range []string{"bound-agent", "product"} {
				claims := crewWriter("owner")
				if kind == "bound-agent" {
					claims.AccessToken.BoundAgentID = "native-agent"
				} else {
					claims.AccessToken.ProjectKind = "crew"
					claims.AccessToken.ProjectIdentity = "local-id"
				}
				request := httptest.NewRequest("POST", "/", nil).WithContext(context.WithValue(t.Context(), UserContextKey, claims))
				response := httptest.NewRecorder()
				api.pairedCrewSyncAction(response, request, "local-id", "server")
				if response.Code != 403 {
					t.Fatalf("%s token received saved-login authority: %d %s", kind, response.Code, response.Body.String())
				}
			}
			checkWrite := func(path string, want int) {
				t.Helper()
				data, _ := json.Marshal(map[string]string{"content": "changed"})
				w := httptest.NewRecorder()
				files.ServeHTTP(w, httptest.NewRequest("PUT", "/api/documents/"+path, bytes.NewReader(data)))
				if w.Code != want {
					t.Fatalf("write %s: %d %s", path, w.Code, w.Body.String())
				}
			}
			checkWrite(serverRoots.FilesPath()+"/note.txt", 409)
			checkWrite(serverRoots.Virtual+"/outputs/result.txt", 200)
			checkWrite(serverRoots.Virtual+"/runs/example/result.txt", 200)
			grants := crewroot.SyncWriteGrants(docs, []string{serverRoots.Files, serverRoots.Resources, filepath.Join(serverRoots.Private, "runs")})
			for _, p := range grants {
				if p == serverRoots.Files || p == serverRoots.Resources {
					t.Fatal("locked native source still writable")
				}
			}
			api.activeSessions["busy"] = &ActiveSessionInfo{WorkspacePath: local.Virtual, Status: "running"}
			if w := call("server", "owner", true); w.Code != 409 {
				t.Fatal("handoff under active turn")
			}
			delete(api.activeSessions, "busy")
			// PLAT-856-442: the same logical conversation must survive both
			// editing handoffs, without importing a native runtime or another tab.
			main, err := api.crewHandoffMain(ctx, owner, local.ID)
			if err != nil {
				t.Fatal(err)
			}
			writeHistory := func(record ProductConversationRecord, text string) {
				t.Helper()
				p, ok := workProjectChatHistoryConversationPath("owner", record.WorkspacePath, record.SessionID, time.Now())
				if !ok {
					t.Fatal("private main chat path missing")
				}
				data := map[string]any{"user_id": "owner", "session_id": record.SessionID,
					"conversation_history": []map[string]any{{"Role": "human", "Parts": []map[string]any{{"Type": "text", "Text": text}}}},
					"runtime":              map[string]any{"provider": "claude-code", "external_session_id": "do-not-transfer-native-session"},
					"terminal_snapshots":   []string{"do-not-transfer-terminal"},
					"ui_events":            []map[string]any{{"type": "streaming_chunk", "data": map[string]any{"type": "streaming_chunk", "content": "do-not-transfer-terminal-event", "metadata": map[string]any{"kind": "terminal", "tmux_session": "do-not-transfer-native-handle"}}}}}
				raw, _ := json.Marshal(data)
				if err := writeRawFileToWorkspace(ctx, p, string(raw)); err != nil {
					t.Fatal(err)
				}
			}
			// A background receipt racing a local turn must retry without
			// replacing its registry or falling back to a fresh native chat.
			api.activeSessions[main.SessionID] = &ActiveSessionInfo{SessionID: main.SessionID, WorkspacePath: local.Virtual, Status: "running"}
			busy := &crewHandoffConversation{ConversationID: main.ConversationID}
			if err := api.importCrewHandoffConversation(ctx, owner, local, "busy-pair", "busy-transfer", busy, main); !errors.Is(err, errCrewNativeDestinationBusy) {
				t.Fatalf("running destination import must retry: %v", err)
			}
			unchanged, err := api.crewHandoffMain(ctx, owner, local.ID)
			if err != nil || unchanged.SessionID != main.SessionID || unchanged.FreshNative != main.FreshNative {
				t.Fatal("busy import replaced the current conversation")
			}
			delete(api.activeSessions, main.SessionID)
			writeHistory(main, "Remember the project marker: blue lantern")
			// PLAT-856-442: imported JSON alone returned 404 to the actual chat
			// screen. The same events endpoint must show it only to its owner.
			checkTranscript := func(record ProductConversationRecord, marker string) {
				t.Helper()
				projectID := strings.Split(record.ConversationKey, ":")[0]
				request := httptest.NewRequest("GET", "/api/workspace-state", nil).WithContext(ctx)
				restored, ok := api.validateWorkspaceReturn(request, &workspaceDestination{Surface: "work", ID: projectID, SessionID: record.SessionID}, []homeWork{{Surface: "work", ID: projectID, WorkspacePath: record.WorkspacePath}})
				if !ok || restored == nil || restored.SessionID != record.SessionID {
					t.Fatalf("imported archive without native runtime cannot reopen: %+v", restored)
				}
				for _, person := range []string{"owner", "other"} {
					r := httptest.NewRequest("GET", "/api/sessions/"+record.SessionID+"/events?durable_chat=1&limit=40&view=compact&workspace_path="+record.WorkspacePath, nil)
					r = mux.SetURLVars(r, map[string]string{"session_id": record.SessionID})
					r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: person}))
					w := httptest.NewRecorder()
					api.handleGetSessionEvents(w, r)
					if person == "other" {
						if w.Code != 404 {
							t.Fatalf("imported transcript exposed: %d %s", w.Code, w.Body.String())
						}
					} else if w.Code != 200 || !strings.Contains(w.Body.String(), marker) || strings.Contains(w.Body.String(), "do-not-transfer") {
						t.Fatalf("imported transcript missing or contains source runtime: %d %s", w.Code, w.Body.String())
					}
				}
			}
			loseResponse = true
			if w := call("server", "owner", true); w.Code != 409 {
				t.Fatal("lost response not reported")
			}
			pending, _ := crewroot.ReadSync(local.ID)
			if pending.PendingAction != "server" {
				t.Fatal("lost retry state")
			}
			serverChat, err := api.crewHandoffMain(ctx, owner, serverRoots.ID)
			if err != nil || serverChat.ConversationID != main.ConversationID || serverChat.SessionID == main.SessionID {
				t.Fatalf("handoff identity: %+v %v", serverChat, err)
			}
			checkTranscript(serverChat, "blue lantern")
			// PLAT-856-442: the browser marks an automatic-switch send as a
			// continuation. Removing its old session header caused a real 422.
			profile, err := api.agentProfiles.Resolve("work", 0, owner.UserID)
			if err != nil {
				t.Fatal(err)
			}
			continued := httptest.NewRequest("POST", "/api/agent-profiles/work/query", nil).WithContext(ctx)
			continued.Header.Set("X-Conversation-Continuation", "true")
			if _, err = api.resolveAgentProfileConversation(continued, profile, serverRoots.ID, ""); err == nil || !strings.Contains(err.Error(), "explicit session ID") {
				t.Fatalf("missing continuation header did not reproduce: %v", err)
			}
			if err = api.bindCrewHandoffContinuation(continued, serverRoots.ID); err != nil {
				t.Fatal(err)
			}
			bound, err := api.resolveAgentProfileConversation(continued, profile, serverRoots.ID, "")
			if err != nil || bound.SessionID != serverChat.SessionID {
				t.Fatalf("handoff continuation did not bind: %+v %v", bound, err)
			}
			target, found, err := resolveProductResumeTarget("owner", serverChat)
			if err != nil || !found || target.Runtime != nil || len(target.History) == 0 {
				t.Fatalf("archive continuation missing: %+v %v", target, err)
			}
			saved, err := ReadChatHistoryConversation("owner", serverChat.SessionID, serverChat.WorkspacePath)
			if err != nil || !bytes.Contains(saved, []byte("blue lantern")) || bytes.Contains(saved, []byte("do-not-transfer")) {
				t.Fatalf("history allowlist failed: %v", err)
			}
			writeHistory(serverChat, "Remember the project marker: blue lantern; server continued")
			write(serverRoots.Files, "note.txt", "server work after handoff")
			if w := call("server", "owner", true); w.Code != 200 {
				t.Fatalf("retry: %d %s", w.Code, w.Body.String())
			}
			saved, _ = ReadChatHistoryConversation("owner", serverChat.SessionID, serverChat.WorkspacePath)
			if !bytes.Contains(saved, []byte("server continued")) {
				t.Fatal("handoff retry replaced the newer server turn")
			}
			checkWrite(local.FilesPath()+"/note.txt", 409)
			// Authority refresh must not write an old snapshot over a transfer
			// that recorded PendingAction after the refresh read its binding.
			stale, _ := crewroot.ReadSync(local.ID)
			stale.Editing, stale.PendingAction = "local", ""
			fresh, _ := crewroot.ReadSync(local.ID)
			fresh.PendingAction = "download"
			if err := crewroot.WriteSync(local.ID, *fresh); err != nil {
				t.Fatal(err)
			}
			if _, err := api.refreshCrewSyncAuthority(ctx, owner, local, stale); err != nil {
				t.Fatal(err)
			}
			preserved, _ := crewroot.ReadSync(local.ID)
			if preserved.PendingAction != "download" || stale.PendingAction != "download" {
				t.Fatal("stale authority refresh dropped the pending transfer")
			}
			// A stale local authority reply must not roll back a completed
			// handoff and unlock source writes while the server owns editing.
			beforeHandoff := *preserved
			beforeHandoff.Editing, beforeHandoff.LastTransfer = "local", "old-transfer"
			if _, err := updateCrewSyncEditing(ctx, local, beforeHandoff, "local"); err != nil {
				t.Fatal(err)
			}
			stillServer, _ := crewroot.ReadSync(local.ID)
			if stillServer.Editing != "server" || stillServer.PendingAction != "download" {
				t.Fatal("stale authority rolled back the completed handoff")
			}
			serverLarge, e := os.OpenFile(filepath.Join(serverRoots.Files, "resumable.bin"), os.O_WRONLY|os.O_TRUNC, 0600)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = io.CopyN(serverLarge, rand.Reader, 12<<20); e != nil {
				t.Fatal(e)
			}
			serverLarge.Close()
			dropDownload = true
			if w := call("download", "owner", true); w.Code != 409 || dropDownload {
				t.Fatalf("interrupted download: %d %s", w.Code, w.Body.String())
			}
			resumedDownload := false
			pieceHook = func(r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Query().Get("direction") == "download" {
					offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
					if offset > 0 {
						resumedDownload = true
						t.Logf("[CREW_SYNC_PROOF] at=%s resumed_download_offset=%d", time.Now().UTC().Format(time.RFC3339Nano), offset)
					}
				}
			}
			if w := call("download", "owner", true); w.Code != 200 {
				t.Fatalf("background return: %d %s", w.Code, w.Body.String())
			}
			pieceHook = nil
			if !resumedDownload || digest(largePath) != digest(filepath.Join(serverRoots.Files, "resumable.bin")) {
				t.Fatal("download did not resume correct bytes")
			}
			t.Logf("[CREW_SYNC_PROOF] at=%s resumed_download=ok sha256_match=true", time.Now().UTC().Format(time.RFC3339Nano))
			mirror, _ := crewroot.ReadSync(local.ID)
			if mirror.Editing != "server" {
				t.Fatal("background copy moved editing")
			}
			mirrored, _ := os.ReadFile(filepath.Join(local.Files, "note.txt"))
			if string(mirrored) != "server work after handoff" {
				t.Fatal("server edit did not reach Mac")
			}
			write(serverRoots.Files, "note.txt", "latest server work")
			// A newer return is real work even after the server has switched
			// editing to local: download must serve it until it is acknowledged.
			serverBinding, _ := crewroot.ReadSync(serverRoots.ID)
			pendingReturn, err := api.crewSync(ctx, owner, serverRoots.ID, crewSyncRequest{Action: "pull", PairID: serverBinding.PairID})
			if err != nil || pendingReturn.Snapshot == nil {
				t.Fatalf("prepare pending return: %+v %v", pendingReturn, err)
			}
			stillPending, err := api.crewSync(ctx, owner, serverRoots.ID, crewSyncRequest{Action: "download", PairID: serverBinding.PairID})
			if err != nil || stillPending.Snapshot == nil || stillPending.TransferID != pendingReturn.TransferID {
				t.Fatalf("newer return was discarded: %+v %v", stillPending, err)
			}

			// A pre-upgrade return is recaptured even after editing has already
			// moved locally. Its saved conversation must still accompany it.
			legacyReturn, _ := crewSyncManifestPath(serverRoots.ID)
			legacyReturn += ".return"
			legacyReturnBytes := oldTransfer(legacyReturn, pendingReturn)
			upgradedReturn, err := api.crewSync(ctx, owner, serverRoots.ID, crewSyncRequest{Action: "download", PairID: serverBinding.PairID})
			if err != nil || upgradedReturn.Snapshot == nil || len(upgradedReturn.Snapshot.Payloads) == 0 || upgradedReturn.TransferID == pendingReturn.TransferID {
				t.Fatalf("old return recapture failed: %v", err)
			}
			beforeChat, _ := json.Marshal(pendingReturn.Conversation)
			afterChat, _ := json.Marshal(upgradedReturn.Conversation)
			if !bytes.Equal(beforeChat, afterChat) {
				t.Fatal("old return lost its conversation")
			}
			checkOldBackup(legacyReturn, legacyReturnBytes)

			// Server Editing is already local, but the Mac has not imported or
			// acknowledged the return. A send must finish it before choosing a CLI.
			pendingLocal, _ := crewroot.ReadSync(local.ID)
			pendingLocal.PendingAction = "local"
			pendingLocal.Editing = "local"
			if err := crewroot.WriteSync(local.ID, *pendingLocal); err != nil {
				t.Fatal(err)
			}
			send := httptest.NewRequest("POST", "/api/agent-profiles/work/query", nil).WithContext(ctx)
			send.Header.Set("X-Session-ID", main.SessionID)
			offline.Store(true)
			blocked := httptest.NewRecorder()
			if !api.ensureCrewNativeExecution(blocked, send, "work", local.ID) || blocked.Code != 409 {
				t.Fatal("interrupted pending return allowed a stale offline send")
			}
			offline.Store(false)
			gate := httptest.NewRecorder()
			if api.ensureCrewNativeExecution(gate, send, "work", local.ID) {
				t.Fatalf("pending return did not finish before send: %d %s", gate.Code, gate.Body.String())
			}
			returned, err := api.crewHandoffMain(ctx, owner, local.ID)
			if err != nil || returned.ConversationID != main.ConversationID || returned.SessionID == main.SessionID || returned.SessionID == serverChat.SessionID {
				t.Fatalf("return identity: %+v %v", returned, err)
			}
			if send.Header.Get("X-Session-ID") != returned.SessionID {
				t.Fatal("send would run on stale session before pending import")
			}
			pendingLocal, _ = crewroot.ReadSync(local.ID)
			if pendingLocal.PendingAction != "" {
				t.Fatal("pending return was not acknowledged before send")
			}
			// PLAT-856-485 / PR633: an ordinary stale tab must still fail
			// continuation validation, rather than join the current conversation.
			staleSend := httptest.NewRequest("POST", "/api/agent-profiles/work/query", nil).WithContext(ctx)
			staleSend.Header.Set("X-Session-ID", main.SessionID)
			if api.ensureCrewNativeExecution(httptest.NewRecorder(), staleSend, "work", local.ID) {
				t.Fatal("ordinary local continuation unexpectedly required a handoff")
			}
			if staleSend.Header.Get("X-Session-ID") != main.SessionID {
				t.Fatal("ordinary stale continuation was rebound to a different conversation")
			}
			// PLAT-856-442: a background download queued before the return
			// must not retry forever after editing is back on the Mac.
			staleDownload, _ := crewroot.ReadSync(local.ID)
			staleDownload.PendingAction = "download"
			if err := crewroot.WriteSync(local.ID, *staleDownload); err != nil {
				t.Fatal(err)
			}
			if w := call("download", "owner", true); w.Code != 200 {
				t.Fatalf("obsolete background download: %d %s", w.Code, w.Body.String())
			}
			settled, _ := crewroot.ReadSync(local.ID)
			if settled.PendingAction != "" || settled.Editing != "local" {
				t.Fatalf("obsolete download was not settled: %+v", settled)
			}
			offline.Store(true)
			if api.ensureCrewNativeExecution(httptest.NewRecorder(), send, "work", local.ID) {
				t.Fatal("completed local handoff blocked ordinary offline work")
			}
			offline.Store(false)
			checkTranscript(returned, "server continued")
			// A failed binding write leaves journal rows behind. Retrying the same
			// transfer must finish without duplicates, including beyond the normal
			// builder restore's twelve-message window.
			replay, err := api.captureCrewHandoffConversation(ctx, owner, local, returned)
			if err != nil {
				t.Fatal(err)
			}
			for len(replay.History) < 15 {
				replay.History = append(replay.History, replay.History[0])
			}
			failRegistryWrite.Store(true)
			if err = api.importCrewHandoffConversation(ctx, owner, local, "test-pair", "journal-retry", replay, returned); err == nil || !strings.Contains(err.Error(), "write product conversation registry") {
				t.Fatalf("expected failure after journal import: %v", err)
			}
			if err = api.importCrewHandoffConversation(ctx, owner, local, "test-pair", "journal-retry", replay, returned); err != nil {
				t.Fatal(err)
			}
			retried, err := api.crewHandoffMain(ctx, owner, local.ID)
			if err != nil {
				t.Fatal(err)
			}
			page, err := eventStore.ReadDurableChatPage(retried.SessionID, events.DurableEventPageOptions{Limit: 100})
			if err != nil || len(page.Events) != 15 {
				t.Fatalf("journal retry duplicated or truncated dialogue: %d %v", len(page.Events), err)
			}
			checkTranscript(retried, "server continued")
			t.Run("Muse native import", func(t *testing.T) {
				if _, err := exec.LookPath("muse"); err != nil {
					t.Skip("Muse native import needs Muse installed; archive, owner and secret checks still run")
				}
				checkCrewMuseNativeHandoff(t, api, ctx, owner, local, retried, replay)
			})
			saved, err = ReadChatHistoryConversation("owner", returned.SessionID, returned.WorkspacePath)
			if err != nil || !bytes.Contains(saved, []byte("server continued")) || bytes.Contains(saved, []byte("do-not-transfer")) {
				t.Fatalf("return lost conversation or copied runtime: %v", err)
			}
			raw, err := os.ReadFile(filepath.Join(local.Files, "note.txt"))
			if err != nil || string(raw) != "latest server work" {
				t.Fatalf("returned file: %q %v", raw, err)
			}
			checkWrite(local.FilesPath()+"/new.txt", 200)
			// Exercise the actual watcher, saved-login HTTP and real files.
			// PLAT-856-440: an unavailable selected drive at startup must recover
			// when it returns, without restarting the app or changing its pairing.
			if split {
				crewAutoStates.Delete(local.ID)
				if err := os.Rename(local.Files, local.Files+"-unavailable"); err != nil {
					t.Fatal(err)
				}
			}
			stop := api.startDesktopCrewSync()
			defer stop()
			if split {
				deadline := time.Now().Add(5 * time.Second)
				for crewAutoStatus(local.ID) == nil && time.Now().Before(deadline) {
					time.Sleep(50 * time.Millisecond)
				}
				if status := crewAutoStatus(local.ID); status == nil || status.State != "attention" {
					t.Fatalf("missing folder was not reported: %+v", status)
				}
				if err := os.Rename(local.Files+"-unavailable", local.Files); err != nil {
					t.Fatal(err)
				}
			}
			awaitFile := func(root, name, want string) {
				t.Helper()
				deadline := time.Now().Add(15 * time.Second)
				for time.Now().Before(deadline) {
					data, _ := os.ReadFile(filepath.Join(root, name))
					if string(data) == want {
						return
					}
					time.Sleep(50 * time.Millisecond)
				}
				t.Fatalf("automatic copy missing %s; status=%+v", name, crewAutoStatus(local.ID))
			}
			write(local.Files, "automatic.txt", "saved locally")
			awaitFile(serverRoots.Files, "automatic.txt", "saved locally")
			offline.Store(true)
			write(local.Files, "automatic.txt", "saved offline")
			time.Sleep(3500 * time.Millisecond)
			if data, _ := os.ReadFile(filepath.Join(local.Files, "automatic.txt")); string(data) != "saved offline" {
				t.Fatal("offline local edit lost")
			}
			offline.Store(false)
			awaitFile(serverRoots.Files, "automatic.txt", "saved offline")
			// PLAT-856-509: force a status refresh in the released-lock window.
			// Its old server authority must not reverse a pending handoff.
			refreshedPending := false
			pieceHook = func(r *http.Request) {
				observed, err := crewroot.ReadSync(local.ID)
				if err != nil || observed == nil || observed.PendingAction != "server" || r.Method != http.MethodPut {
					return
				}
				refreshedPending = true
				updated, err := updateCrewSyncEditing(ctx, local, *observed, "local")
				if err != nil || updated.Editing != "server" || updated.PendingAction != "server" {
					t.Errorf("status reversed the pending handoff: %+v %v", updated, err)
				}
			}
			if w := call("server", "owner", true); w.Code != 200 {
				t.Fatalf("automatic handoff: %s", w.Body.String())
			}
			pieceHook = nil
			if !refreshedPending {
				t.Fatal("pending handoff authority refresh was not exercised")
			}
			write(serverRoots.Files, "automatic.txt", "saved on server")
			awaitFile(local.Files, "automatic.txt", "saved on server")

			// PLAT-856-442: a private-history symlink must not redirect the
			// under-lock direct writer outside the admitted destination Crew.
			stop()
			// The watcher may have applied bytes before its receipt was cancelled.
			if w := call("download", "owner", true); w.Code != 200 {
				t.Fatalf("settle watcher receipt: %d %s", w.Code, w.Body.String())
			}
			builder := filepath.Join(local.Private, "builder")
			if err := os.Rename(builder, builder+"-saved"); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			if err := os.Symlink(outside, builder); err != nil {
				t.Fatal(err)
			}
			if w := call("local", "owner", true); w.Code != 409 || !strings.Contains(w.Body.String(), "outside the Crew") {
				t.Fatalf("history symlink accepted or wrong refusal: %d %s", w.Code, w.Body.String())
			}
			if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
				t.Fatal("handoff wrote outside private storage")
			}
			if err := os.Remove(builder); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(builder+"-saved", builder); err != nil {
				t.Fatal(err)
			}
			if w := call("local", "owner", true); w.Code != 200 {
				t.Fatalf("history retry after removing symlink: %d %s", w.Code, w.Body.String())
			}

			checkCrewNativeSyncAdmission(t, api, ctx, owner, serverRoots)
			checkCrewDependencySecretBoundary(t, api, ctx, owner, local, serverRoots)
			checkCrewSyncOff(t, api, ctx, local, serverRoots, &offline)

		})
	}
}

// The existing HTTP fixture supplies both managed and selected-folder Crews.
// Pin the new private-store/owner/conflict rules without another server fixture.
func checkCrewMuseNativeHandoff(t *testing.T, api *StreamingAPI, ctx context.Context, owner *UserClaims, roots crewroot.Roots, current ProductConversationRecord, history *crewHandoffConversation) {
	t.Helper()
	oldOS := cliHostOS
	cliHostOS = "linux"
	defer func() { cliHostOS = oldOS }()
	store := defaultProductConversationRegistryStore()
	registry := productConversationRegistryPath(owner.UserID)
	doc, err := store.loadDocument(ctx, registry)
	if err != nil {
		t.Fatal(err)
	}
	key := productConversationRegistryEntryKey("work", current.ConversationKey)
	// PLAT-856-442: a new destination has not run a turn yet; its selected
	// Muse account is in the agent identity, while the runtime still has Codex.
	current.Provider = "codex-cli"
	current.ConnectionID = "global:codex-cli"
	if doc.Agents == nil {
		doc.Agents = map[string]ProductAgentIdentity{}
	}
	doc.Agents[key] = ProductAgentIdentity{Engine: "muse-cli", ModelID: "muse-spark-1.3-contributor", ConnectionID: "global:muse-cli"}
	doc.Entries[key] = current
	if err = store.writeDocument(ctx, registry, doc); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	id := "11111111-1111-4111-8111-111111111111"
	row, _ := json.Marshal(map[string]any{"stream": map[string]string{"kind": "session", "id": id}, "payload_type": "runtime.session.metadata", "payload": map[string]any{"record": map[string]any{"workspace_root": source, "build": map[string]string{"semver": "1.4.14"}}}})
	row = append(row, '\n')
	moved := *history
	sourceHome := t.TempDir()
	sourcePath := filepath.Join(sourceHome, "muse", "sessions", "2026", "11", "01", id, "session.jsonl")
	if err = os.MkdirAll(filepath.Dir(sourcePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(sourcePath, row, 0600); err != nil {
		t.Fatal(err)
	}
	moved.Native, err = sessiontransfer.ExportMuse(sourceHome, id, source, "structured")
	if err != nil {
		t.Fatal(err)
	}
	if err = stageCrewNative(roots, moved.Native); err != nil {
		t.Fatal(err)
	}
	// PLAT-856-442: a failed next transfer must not invalidate offline resume.
	if err = saveCrewNativeMirror(roots, &moved); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(moved.Native.Root, "session.jsonl")
	if err = os.WriteFile(staged+".partial", []byte("interrupted replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(staged+".partial", staged); err != nil {
		t.Fatal(err)
	}
	mirror, err := readCrewNativeMirror(roots)
	if err != nil {
		t.Fatalf("last complete native mirror lost: %v", err)
	}
	// The same native ID with rewritten history must not quietly replace the
	// completed mirror when an offline branch reconnects.
	var forkRow map[string]any
	json.Unmarshal(row, &forkRow)
	forkRow["offline_fork"] = true
	forkBytes, _ := json.Marshal(forkRow)
	if err = os.WriteFile(sourcePath, append(forkBytes, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	fork := moved
	fork.Native, err = sessiontransfer.ExportMuse(sourceHome, id, source, "structured")
	if err != nil {
		t.Fatal(err)
	}
	if err = stageCrewNative(roots, fork.Native); err != nil {
		t.Fatal(err)
	}
	if err = saveCrewNativeMirror(roots, &fork); !errors.Is(err, sessiontransfer.ErrDiverged) {
		t.Fatalf("offline fork replaced mirror: %v", err)
	}
	if _, err = readCrewNativeMirror(roots); err != nil {
		t.Fatal(err)
	}
	moved = *mirror.Conversation
	if err = api.importCrewHandoffConversation(ctx, owner, roots, "native-pair", "native-first", &moved, current); err != nil {
		t.Fatal(err)
	}
	imported, err := api.crewHandoffMain(ctx, owner, roots.ID)
	if err != nil || imported.FreshNative {
		t.Fatalf("native import did not bind: %+v %v", imported, err)
	}
	runtime, ok, err := ReadChatHistoryRuntimeForSession(owner.UserID, imported.SessionID, imported.WorkspacePath)
	expectedDir, dirErr := crewCLIWorkingDir(roots.Virtual, owner.UserID, imported.SessionID, "muse-cli", false)
	if err != nil || dirErr != nil || !ok || runtime == nil || !runtime.NativeHandoff || runtime.ExternalSessionID != id || runtime.AgentSessionHandle.ConnectionID != "global:muse-cli" || runtime.AgentSessionHandle.Provider.WorkingDir != expectedDir {
		t.Fatalf("native runtime: %+v %v", runtime, err)
	}
	home, private, err := api.crewProgramStore(ctx, owner, roots, imported, "structured", false)
	if err != nil || !strings.HasPrefix(home, private+string(filepath.Separator)) {
		t.Fatalf("shared Muse store: %s %s %v", home, private, err)
	}
	path := filepath.Join(home, "muse", "sessions", "2026", "11", "01", id, "session.jsonl")
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, row) {
		t.Fatalf("private native journal: %v", err)
	}
	if _, _, err = api.crewProgramStore(ctx, &UserClaims{UserID: "other"}, roots, imported, "structured", false); err == nil {
		t.Fatal("other owner obtained a native store")
	}
	if err = os.Rename(home, home+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(t.TempDir(), home); err != nil {
		t.Fatal(err)
	}
	_, _, linkErr := api.crewProgramStore(ctx, owner, roots, imported, "structured", false)
	os.Remove(home)
	os.Rename(home+"-saved", home)
	if linkErr == nil {
		t.Fatal("private Muse home followed a symlink")
	}
	// A new destination runtime must not conceal changes to its previous copy.
	advanced := append(append([]byte{}, row...), []byte("{\"payload_type\":\"local.new.turn\"}\n")...)
	if err = os.WriteFile(path, advanced, 0600); err != nil {
		t.Fatal(err)
	}
	if err = api.importCrewHandoffConversation(ctx, owner, roots, "native-pair", "native-conflict", &moved, imported); err != nil {
		t.Fatal(err)
	}
	fallback, err := api.crewHandoffMain(ctx, owner, roots.ID)
	if err != nil || !fallback.FreshNative || !strings.Contains(moved.Notice, "saved conversation history") {
		t.Fatalf("conflict did not fall back clearly: %+v %s %v", fallback, moved.Notice, err)
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, advanced) {
		t.Fatal("native conflict overwrote the destination copy")
	}
	// Caching metadata must never turn a corrupt journal into a valid resume.
	if _, err := readCrewNativeMirrorManifest(roots); err != nil {
		t.Fatal(err)
	}
	mirrorFile := filepath.Join(mirror.Conversation.Native.Root, "session.jsonl")
	original, err := os.ReadFile(mirrorFile)
	if err != nil {
		t.Fatal(err)
	}
	defer os.WriteFile(mirrorFile, original, 0600)
	if err := os.WriteFile(mirrorFile, []byte("corrupt journal"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCrewNativeMirror(roots); err == nil {
		t.Fatal("cached manifest bypassed journal validation")
	}
}

// The allowed upgrade proves these refusals are the owner/pair/location rules,
// rather than an absent binary or a websocket that can never be opened.
func checkCrewNativeSyncAdmission(t *testing.T, api *StreamingAPI, ctx context.Context, owner *UserClaims, roots crewroot.Roots) {
	t.Helper()
	t.Run("native-websocket-admission", func(t *testing.T) {
		if _, err := nativersync.Binary(); err != nil {
			t.Skip(err)
		}
		original, err := crewroot.ReadSync(roots.ID)
		if err != nil || original == nil {
			t.Fatal("paired fixture unavailable")
		}
		defer crewroot.WriteSync(roots.ID, *original)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			person := owner
			if r.URL.Query().Get("who") == "other" {
				person = &UserClaims{UserID: "other", Username: "other"}
			}
			r = r.WithContext(context.WithValue(ctx, UserContextKey, person))
			r = mux.SetURLVars(r, map[string]string{"project": roots.ID})
			api.handleCrewNativeSync(w, r)
		}))
		defer server.Close()
		for _, tc := range []struct {
			who, pair, editing string
			status             int
		}{
			{"other", original.PairID, "local", 403},
			{"owner", "wrong-pair", "local", 409},
			{"owner", original.PairID, "server", 409},
			{"owner", original.PairID, "local", 101},
		} {
			binding := *original
			binding.Editing = tc.editing
			if err := crewroot.WriteSync(roots.ID, binding); err != nil {
				t.Fatal(err)
			}
			address := "ws" + strings.TrimPrefix(server.URL, "http") + "?pair=" + tc.pair + "&direction=push&who=" + tc.who
			conn, response, err := websocket.DefaultDialer.DialContext(ctx, address, nil)
			if conn != nil {
				conn.Close()
			}
			if response == nil || response.StatusCode != tc.status || (tc.status == 101 && err != nil) {
				t.Fatalf("native admission who=%s pair=%s editing=%s: response=%v error=%v", tc.who, tc.pair, tc.editing, response, err)
			}
			if tc.status != 101 {
				response.Body.Close()
			}
		}
	})
}

// PLAT-856-509 / PR836: old staged-index baselines must not become empty
// when an app upgrade replaces the stored patch with a fingerprint.
func TestCrewSyncMigratesStagedGitBaseline(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	local, remote := t.TempDir(), t.TempDir()
	run := func(root string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run(local, "init", "-b", "main")
	file := filepath.Join(local, "note.txt")
	if err := os.WriteFile(file, []byte("committed"), 0600); err != nil {
		t.Fatal(err)
	}
	run(local, "add", "note.txt")
	run(local, "-c", "user.name=Example", "-c", "user.email=owner@example.com", "commit", "-m", "initial")
	if err := os.WriteFile(file, []byte("staged"), 0600); err != nil {
		t.Fatal(err)
	}
	run(local, "add", "note.txt")
	first, err := crewsync.CaptureGit(t.Context(), local, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer discardCapturedCrewPayloads(&crewsync.Snapshot{Git: first})
	if err = crewsync.ApplyGit(t.Context(), remote, first); err != nil {
		t.Fatal(err)
	}
	path, err := crewSyncManifestPath("index-upgrade")
	if err != nil {
		t.Fatal(err)
	}
	path += ".git"
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(map[string]any{"head": first.Head, "branch": first.Branch, "index_patch": first.IndexPatch})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	baseline, err := readCrewSyncGit("index-upgrade")
	if err != nil {
		t.Fatal(err)
	}
	run(local, "reset", "--mixed", "HEAD")
	next, err := crewsync.CaptureGit(t.Context(), local, baseline)
	if err != nil {
		t.Fatal(err)
	}
	defer discardCapturedCrewPayloads(&crewsync.Snapshot{Git: next})
	if err = crewsync.CheckGit(t.Context(), remote, next); err != nil {
		t.Fatalf("unchanged old staging rejected: %v", err)
	}
	if err = crewsync.ApplyGit(t.Context(), remote, next); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(path + ".*.bak")
	if len(backups) != 1 {
		t.Fatalf("missing old-index backup: %v", backups)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil || !bytes.Equal(backup, legacy) {
		t.Fatal("old-index backup changed")
	}
	stored, err := os.ReadFile(path)
	if err != nil || bytes.Contains(stored, []byte("index_patch")) || !bytes.Contains(stored, []byte("index_hash")) {
		t.Fatal("old patch was not replaced with hash")
	}
	if _, err = readCrewSyncGit("index-upgrade"); err != nil {
		t.Fatal(err)
	}
	backups, _ = filepath.Glob(path + ".*.bak")
	if len(backups) != 1 {
		t.Fatal("index baseline migrated twice")
	}
}
