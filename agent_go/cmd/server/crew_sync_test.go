package server

import (
	"bytes"
	"context"
	"crypto/rand"
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
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	wh "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
	"github.com/spf13/viper"
)

// PLAT-856-509: actual HTTP and disk paths prove one-way move, owner-only
// archives, resumable pieces, lost receipts, schedule rebinding and Mac preservation.
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
			files.DELETE("/api/folders/*folderpath", wh.DeleteFolder)
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
			write(local.Files, "note.txt", "local work")
			// PLAT-856-509: an empty Git index must roundtrip without requiring
			// a zero-byte upload that the binary-piece endpoint cannot send.
			for _, args := range [][]string{{"init", "-b", "main"}, {"add", "note.txt"}, {"-c", "user.name=Example", "-c", "user.email=owner@example.com", "commit", "-m", "initial"}} {
				cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", local.Files}, args...)...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("initial Git repository: %v %s", err, out)
				}
			}
			write(local.Resources, "skills/example/SKILL.md", "Useful skill")
			write(local.Files, ".env", "API_KEY=example")
			write(local.Files, ".cache/state", "keep private")
			for name, value := range map[string]string{"MEMORY.md": "project memory", "db/reports/.dashboard-state/home/state.json": `{"id":"home","published":"rev-one"}`, "db/reports/managed/home/rev-one/index.html": "<h1>Published</h1>", "assets/chart.csv": "score\n7", "outputs/result.txt": "saved output", "code/triggers/watch.py": "print('{}')"} {
				write(local.Resources, name, value)
			}
			write(local.Private, "knowledgebase/documents/guide.md", "Crew knowledge")
			write(local.Private, "builder/private.txt", "private runtime")

			listing := httptest.NewRecorder()
			files.ServeHTTP(listing, httptest.NewRequest("GET", "/api/documents?folder="+local.Virtual+"&max_depth=5", nil))
			if listing.Code != 200 || !strings.Contains(listing.Body.String(), local.Virtual+"/skills/example/SKILL.md") {
				t.Fatalf("local resource listing: %d %s", listing.Code, listing.Body.String())
			}
			loseResponse := false
			failActivation := false
			dropUpload := false
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
				if failActivation && r.Method == http.MethodPost && r.URL.Query().Get("transfer") == "" {
					raw, _ := io.ReadAll(r.Body)
					r.Body = io.NopCloser(bytes.NewReader(raw))
					var call crewSyncRequest
					json.Unmarshal(raw, &call)
					if call.Action == "push" {
						failActivation = false
						externalError(w, 409, "fixture_activation_failed", "injected import failure")
						return
					}
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
			if w := call("server", "owner", false); w.Code != 403 {
				t.Fatalf("desktop proof: %d", w.Code)
			}
			if w := call("server", "other", true); w.Code < 400 {
				t.Fatal("another owner moved a private Crew")
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
				api.pairedCrewSyncAction(response, request, "local-id", "server", "")
				if response.Code != 403 {
					t.Fatalf("%s token received saved-login authority: %d %s", kind, response.Code, response.Body.String())
				}
			}
			main, err := api.crewHandoffMain(ctx, owner, local.ID)
			if err != nil {
				t.Fatal(err)
			}
			store := defaultProductConversationRegistryStore()
			doc, err := store.loadDocument(ctx, productConversationRegistryPath(owner.UserID))
			if err != nil {
				t.Fatal(err)
			}
			side := main
			side.IsMainChat = false
			side.ConversationKey = local.ID + ":chat:helper"
			side.ConversationID = "conversation-helper"
			side.SessionID = "product-helper"
			key := productConversationRegistryEntryKey("work", side.ConversationKey)
			doc.Entries[key] = side
			if doc.Agents == nil {
				doc.Agents = map[string]ProductAgentIdentity{}
			}
			doc.Agents[key] = ProductAgentIdentity{Name: "Helper", Purpose: "Remember the second chat"}
			if err = store.writeDocument(ctx, productConversationRegistryPath(owner.UserID), doc); err != nil {
				t.Fatal(err)
			}
			for _, record := range []ProductConversationRecord{main, side} {
				path, ok := workProjectChatHistoryConversationPath("owner", record.WorkspacePath, record.SessionID, time.Now())
				if !ok {
					t.Fatal("chat path")
				}
				raw, _ := json.Marshal(map[string]any{"user_id": "owner", "session_id": record.SessionID, "conversation_history": []map[string]any{{"Role": "human", "Parts": []map[string]any{{"Type": "text", "Text": "marker " + record.ConversationID}}}}, "terminal_snapshots": []string{"do-not-transfer-terminal"}})
				if err = writeRawFileToWorkspace(ctx, path, string(raw)); err != nil {
					t.Fatal(err)
				}
			}
			schedule := projectactions.Schedule{ID: "morning", Name: "Morning", Project: projectactions.Project{Kind: "crew", ID: local.ID}, When: projectactions.When{Cron: "0 9 * * *", Timezone: "UTC"}, Action: projectactions.Action{Type: "message", Tab: side.ConversationKey}, Input: json.RawMessage(`{"message":"check"}`), RunAs: "owner", Enabled: true}
			if err = writeProjectSchedules(ctx, local.Virtual, []projectactions.Schedule{schedule}); err != nil {
				t.Fatal(err)
			}
			hook := projectWebhookDefinition{ID: "hook-one", Name: "On event", Enabled: true, Project: schedule.Project, RunAs: "owner", Path: "/api/hooks/hook-one", Action: projectactions.Action{Type: "message"}, Input: json.RawMessage(`{"messages":["received"]}`), Auth: projectWebhookAuth{Mode: "bearer", EncryptedSecret: "source-only-ciphertext"}}
			if err = writeProjectWebhookDefinitions(ctx, local.Virtual, []projectWebhookDefinition{hook}); err != nil {
				t.Fatal(err)
			}
			fn := projectFunctionDefinition{ID: "function-one", Name: "check", Description: "Check work", Enabled: true, Action: projectactions.Action{Type: "message", Tab: "Research helper"}, Input: json.RawMessage(`{"messages":["check"]}`)}
			if err = writeProjectFunctionDefinitions(ctx, local.Virtual, []projectFunctionDefinition{fn}); err != nil {
				t.Fatal(err)
			}
			// Incompressible bytes guarantee multiple resumable pieces.
			large := make([]byte, 10<<20)
			if _, err = rand.Read(large); err != nil {
				t.Fatal(err)
			}
			write(local.Files, "resumable.bin", string(large))
			api.activeSessions[main.SessionID] = &ActiveSessionInfo{SessionID: main.SessionID, WorkspacePath: local.Virtual, Status: "running"}
			if w := call("server", "owner", true); w.Code != 409 {
				t.Fatal("moved a running Crew")
			}
			delete(api.activeSessions, main.SessionID)
			dropUpload = true
			lockChecked := false
			pieceHook = func(req *http.Request) {
				if req.Method != http.MethodPut || lockChecked {
					return
				}
				lockChecked = true
				check, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				release, e := wf.LockWorkspace(check, local.Files)
				if e != nil {
					t.Errorf("upload holds working folder: %v", e)
					return
				}
				release()
			}
			if w := call("server", "owner", true); w.Code != 409 || dropUpload || !lockChecked {
				t.Fatalf("interrupt: %d %s", w.Code, w.Body.String())
			}
			pieceHook = nil
			binding, err := crewroot.ReadSync(local.ID)
			if err != nil || binding == nil {
				t.Fatal("missing interrupted binding")
			}
			serverRoots, err := api.crewSyncRoots(ctx, owner, binding.PeerProjectID)
			if err != nil {
				t.Fatal(err)
			}
			checkCrewNativeSyncAdmission(t, api, ctx, owner, serverRoots)
			pending, _ := crewSyncManifestPath(local.ID)
			var upload crewSyncRequest
			if err = readCrewTransfer(pending+".pending", &upload); err != nil {
				t.Fatal(err)
			}
			// Query status while holding the destination apply lock: progress must not block.
			release, err := wf.LockWorkspace(ctx, serverRoots.Files)
			if err != nil {
				t.Fatal(err)
			}
			check, cancel := context.WithTimeout(ctx, time.Second)
			progress, err := api.crewSync(check, owner, serverRoots.ID, crewSyncRequest{Action: "status"})
			cancel()
			release()
			if err != nil || progress.BytesDone == 0 || progress.BytesDone >= progress.BytesTotal {
				t.Fatalf("progress: %+v %v", progress, err)
			}
			for _, person := range []string{"owner", "other"} {
				q := url.Values{"transfer": {upload.TransferID}, "pair": {upload.PairID}, "direction": {"upload"}, "blob": {"content"}}
				req := httptest.NewRequest("HEAD", "/?"+q.Encode(), nil).WithContext(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: person, Username: person}))
				req = mux.SetURLVars(req, map[string]string{"project": serverRoots.ID})
				response := httptest.NewRecorder()
				api.handleCrewSync(response, req)
				want := 200
				if person == "other" {
					want = 403
				}
				if response.Code != want {
					t.Fatalf("piece %s: %d %s", person, response.Code, response.Body.String())
				}
			}
			failActivation = true
			if w := call("server", "owner", true); w.Code != 409 || failActivation || !strings.Contains(w.Body.String(), "paused for this move") {
				t.Fatalf("activation failure not explicit: %d %s", w.Code, w.Body.String())
			}
			if w := call("status", "owner", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"schedules_paused":true`) {
				t.Fatalf("paused move status: %s", w.Body.String())
			}
			stillMoving, _ := crewroot.ReadSync(local.ID)
			if stillMoving == nil || stillMoving.PendingAction != "server" {
				t.Fatal("failed move lost retry")
			}
			loseResponse = true
			if w := call("server", "owner", true); w.Code != 409 || loseResponse {
				t.Fatalf("lost receipt: %d %s", w.Code, w.Body.String())
			}
			if w := call("server", "owner", true); w.Code != 200 {
				t.Fatalf("retry: %d %s", w.Code, w.Body.String())
			}
			receipt, err := readCrewMoveReceipt(local.ID)
			if err != nil || receipt == nil || !receipt.Complete {
				t.Fatalf("retirement: %+v %v", receipt, err)
			}
			if !split && !strings.HasPrefix(receipt.BackupPath, local.Files+" (moved online "+time.Now().Format("2006-01-02")+")") {
				t.Fatalf("managed backup name: %s", receipt.BackupPath)
			}
			localFiles := receipt.BackupPath
			if split {
				localFiles = local.Files
				if receipt.LocalFolderKept != local.Files || receipt.BackupPath != "" {
					t.Fatal("selected folder moved")
				}
			}
			for _, root := range []string{localFiles, serverRoots.Files} {
				data, e := os.ReadFile(filepath.Join(root, "resumable.bin"))
				if e != nil || !bytes.Equal(data, large) {
					t.Fatal("move lost file bytes", e)
				}
			}
			for name, value := range map[string]string{"MEMORY.md": "project memory", "skills/example/SKILL.md": "Useful skill", "db/reports/.dashboard-state/home/state.json": `{"id":"home","published":"rev-one"}`, "db/reports/managed/home/rev-one/index.html": "<h1>Published</h1>", "assets/chart.csv": "score\n7", "outputs/result.txt": "saved output", "code/triggers/watch.py": "print('{}')"} {
				raw, e := os.ReadFile(filepath.Join(serverRoots.Resources, name))
				if e != nil || string(raw) != value {
					t.Fatalf("moved resource %s: %s %v", name, raw, e)
				}
			}
			if raw, e := os.ReadFile(filepath.Join(serverRoots.Private, "knowledgebase/documents/guide.md")); e != nil || string(raw) != "Crew knowledge" {
				t.Fatal("knowledge missing", e)
			}
			if _, e := os.Stat(filepath.Join(serverRoots.Private, "builder/private.txt")); !os.IsNotExist(e) {
				t.Fatal("private runtime copied")
			}
			hooks, e := readProjectWebhookDefinitions(ctx, serverRoots.Virtual)
			if e != nil || len(hooks) != 1 || !hooks[0].Enabled || hooks[0].Project.ID != serverRoots.ID || hooks[0].Auth.EncryptedSecret == hook.Auth.EncryptedSecret {
				t.Fatalf("webhook: %+v %v", hooks, e)
			}
			if secret, e := decryptSecretValueWithAAD(hooks[0].Auth.EncryptedSecret, productWebhookAAD(serverRoots.ID, hooks[0].ID)); e != nil || len(secret) != 64 {
				t.Fatal("fresh destination webhook secret unavailable", e)
			}
			functions, e := readProjectFunctionDefinitions(ctx, serverRoots.Virtual)
			if e != nil || len(functions) != 1 || functions[0].Action.Tab != "Research helper" {
				t.Fatal("function tab lost", e)
			}
			if b, _ := crewroot.ReadSync(local.ID); b != nil {
				t.Fatal("retired Crew kept a relay binding")
			}
			if _, _, ok := api.resolveExternalWorkspace(ctx, owner, local.ID); ok {
				t.Fatal("retired Crew remains listed")
			}
			if w := call("server", "owner", true); w.Code != 200 {
				t.Fatalf("retirement retry: %s", w.Body.String())
			}
			if w := call("status", "other", true); w.Code < 400 {
				t.Fatal("other owner read move receipt")
			}
			raw, err := os.ReadFile(filepath.Join(local.Private, "schedules.json"))
			var stopped projectactions.Schedules
			json.Unmarshal(raw, &stopped)
			if err != nil || len(stopped.Schedules) != 1 || stopped.Schedules[0].Enabled {
				t.Fatal("Mac schedule still enabled", err)
			}
			schedules, err := readProjectSchedules(ctx, serverRoots.Virtual)
			if err != nil || len(schedules) != 1 || !schedules[0].Enabled || schedules[0].Project.ID != serverRoots.ID || schedules[0].Action.Tab != serverRoots.ID+":chat:helper" {
				t.Fatalf("online schedule: %+v %v", schedules, err)
			}
			doc, err = store.loadDocument(ctx, productConversationRegistryPath(owner.UserID))
			if err != nil {
				t.Fatal(err)
			}
			for _, original := range []ProductConversationRecord{main, side} {
				var moved ProductConversationRecord
				for _, record := range doc.Entries {
					if record.WorkspacePath == serverRoots.Virtual && record.ConversationID == original.ConversationID {
						moved = record
						break
					}
				}
				if moved.ConversationID != original.ConversationID || moved.SessionID == original.SessionID {
					t.Fatalf("chat identity source=%s wanted=%s: %+v", original.ConversationKey, original.ConversationID, moved)
				}
				for _, person := range []string{"owner", "other"} {
					req := httptest.NewRequest("GET", "/api/sessions/"+moved.SessionID+"/events?durable_chat=1&limit=40&view=compact&workspace_path="+moved.WorkspacePath, nil)
					req = mux.SetURLVars(req, map[string]string{"session_id": moved.SessionID})
					req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: person}))
					response := httptest.NewRecorder()
					api.handleGetSessionEvents(response, req)
					if person == "other" {
						if response.Code != 404 {
							t.Fatal("private transcript leaked")
						}
					} else if response.Code != 200 || !strings.Contains(response.Body.String(), original.ConversationID) || strings.Contains(response.Body.String(), "do-not-transfer") {
						t.Fatalf("transcript: %d %s", response.Code, response.Body.String())
					}
				}
			}
			onlineMain, err := api.crewHandoffMain(ctx, owner, serverRoots.ID)
			if err != nil {
				t.Fatal(err)
			}
			history, err := api.captureCrewHandoffConversation(ctx, owner, serverRoots, onlineMain)
			if err != nil {
				t.Fatal(err)
			}
			checkCrewMuseNativeHandoff(t, api, ctx, owner, serverRoots, onlineMain, history)
			// Delete cleanup must match one Crew exactly, retaining a similarly named Crew.
			// PLAT-856-509: a retried relay create (lost reply) returns the same empty twin; it lives on the Mac until moved online.
			twin, err := api.crewSync(ctx, owner, "", crewSyncRequest{Action: "create", Mode: "relay", PairID: "relay-pair", SourceID: "mac-crew", Name: "Mac Crew"})
			if err != nil {
				t.Fatal(err)
			}
			again, err := api.crewSync(ctx, owner, "", crewSyncRequest{Action: "create", Mode: "relay", PairID: "retry-pair", SourceID: "mac-crew", Name: "Mac Crew"})
			if err != nil || again.ProjectID != twin.ProjectID || again.Binding == nil || again.Binding.PairID != "relay-pair" || again.Binding.Mode != "relay" {
				t.Fatalf("a retried relay create made a second twin: %+v %v", again, err)
			}
			if !crewRelayTwin(twin.ProjectID) {
				t.Fatal("a new twin is not on the Mac")
			}
			checkCrewReachOff(t, api, owner, twin.ProjectID, "relay-pair", serverRoots.ID, binding.PairID)
			otherPath, _ := crewSyncManifestPath(serverRoots.ID + "-other")
			write(filepath.Dir(otherPath), filepath.Base(otherPath), "keep")
			if err = removeCrewSyncState(serverRoots.ID); err != nil {
				t.Fatal(err)
			}
			paths, err := crewSyncStatePaths(serverRoots.ID)
			if err != nil || len(paths) != 0 {
				t.Fatal("transfer state remains", paths, err)
			}
			if raw, err := os.ReadFile(otherPath); err != nil || string(raw) != "keep" {
				t.Fatal("deleted another Crew's state")
			}
			t.Logf("[CREW_SYNC_PROOF] managed=%t interrupted_upload=resumed lost_receipt=recovered chats=2 schedules=online local_files=preserved", !split)
		})
	}
}

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
	first, err := crewsync.CaptureGit(t.Context(), local, nil, t.TempDir())
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
	patch, err := os.ReadFile(first.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(map[string]any{"head": first.Head, "branch": first.Branch, "index_patch": patch})
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
	next, err := crewsync.CaptureGit(t.Context(), local, baseline, t.TempDir())
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
