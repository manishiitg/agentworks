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
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wh "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
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
			registerWorkCrewProfile(t, registry)
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
			write(local.Resources, "skills/example/SKILL.md", "Useful skill")
			write(local.Files, ".env", "keep private")
			listing := httptest.NewRecorder()
			files.ServeHTTP(listing, httptest.NewRequest("GET", "/api/documents?folder="+local.Virtual+"&max_depth=5", nil))
			if listing.Code != 200 || !strings.Contains(listing.Body.String(), local.Virtual+"/skills/example/SKILL.md") {
				t.Fatalf("local resource listing: %d %s", listing.Code, listing.Body.String())
			}
			loseResponse := false
			var offline atomic.Bool
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
				r = r.WithContext(context.WithValue(r.Context(), UserContextKey, owner))
				r = mux.SetURLVars(r, map[string]string{"project": strings.TrimPrefix(r.URL.Path, "/api/crew-sync/")})
				response := httptest.NewRecorder()
				api.handleCrewSync(response, r)
				if loseResponse && response.Code == 200 && bytes.Contains(response.Body.Bytes(), []byte(`"editing":"server"`)) {
					loseResponse = false
					w.WriteHeader(502)
					return
				}
				w.WriteHeader(response.Code)
				_, _ = w.Write(response.Body.Bytes())
			}))
			defer remote.Close()
			if err := writeDesktopConnections("owner", []desktopConnection{{ID: "saved", Server: remote.URL, Token: "saved-login", User: UserInfo{ID: "owner"}}}); err != nil {
				t.Fatal(err)
			}
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
			for _, item := range []struct{ root, name, want string }{{serverRoots.Files, "note.txt", "local work"}, {serverRoots.Resources, "skills/example/SKILL.md", "Useful skill"}} {
				raw, err := os.ReadFile(filepath.Join(item.root, item.name))
				if err != nil || string(raw) != item.want {
					t.Fatalf("file %s: %q %v", item.name, raw, err)
				}
			}
			if _, err := os.Stat(filepath.Join(serverRoots.Files, ".env")); !os.IsNotExist(err) {
				t.Fatal("copied private file")
			}
			if _, err := api.crewSync(t.Context(), crewWriter("other"), serverRoots.ID, crewSyncRequest{Action: "status"}); err == nil {
				t.Fatal("other owner accessed sync")
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
			if w := call("download", "owner", true); w.Code != 200 {
				t.Fatalf("background return: %d %s", w.Code, w.Body.String())
			}
			mirror, _ := crewroot.ReadSync(local.ID)
			if mirror.Editing != "server" {
				t.Fatal("background copy moved editing")
			}
			mirrored, _ := os.ReadFile(filepath.Join(local.Files, "note.txt"))
			if string(mirrored) != "server work after handoff" {
				t.Fatal("server edit did not reach Mac")
			}
			write(serverRoots.Files, "note.txt", "latest server work")

			if w := call("local", "owner", true); w.Code != 200 {
				t.Fatalf("return: %d %s", w.Code, w.Body.String())
			}
			returned, err := api.crewHandoffMain(ctx, owner, local.ID)
			if err != nil || returned.ConversationID != main.ConversationID || returned.SessionID == main.SessionID || returned.SessionID == serverChat.SessionID {
				t.Fatalf("return identity: %+v %v", returned, err)
			}
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
			checkCrewMuseNativeHandoff(t, api, ctx, owner, local, retried, replay)
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
			if w := call("server", "owner", true); w.Code != 200 {
				t.Fatalf("automatic handoff: %s", w.Body.String())
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
	current.Provider = "muse-cli"
	current.ConnectionID = "global:muse-cli"
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
	moved.Muse, err = musecli.ExportNativeSession(sourceHome, id, source)
	if err != nil {
		t.Fatal(err)
	}
	if err = stageCrewMuse(roots, moved.Muse); err != nil {
		t.Fatal(err)
	}
	// PLAT-856-442: a failed next transfer must not invalidate offline resume.
	if err = saveCrewNativeMirror(roots, &moved); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(moved.Muse.Root, "session.jsonl")
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
	fork.Muse, err = musecli.ExportNativeSession(sourceHome, id, source)
	if err != nil {
		t.Fatal(err)
	}
	if err = stageCrewMuse(roots, fork.Muse); err != nil {
		t.Fatal(err)
	}
	if err = saveCrewNativeMirror(roots, &fork); err == nil || !strings.Contains(err.Error(), "both copies were kept") {
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
	home, private, err := api.crewMuseStore(ctx, owner, roots, imported)
	if err != nil || !strings.HasPrefix(home, private+string(filepath.Separator)) {
		t.Fatalf("shared Muse store: %s %s %v", home, private, err)
	}
	path := filepath.Join(home, "muse", "sessions", "2026", "11", "01", id, "session.jsonl")
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, row) {
		t.Fatalf("private native journal: %v", err)
	}
	if _, _, err = api.crewMuseStore(ctx, &UserClaims{UserID: "other"}, roots, imported); err == nil {
		t.Fatal("other owner obtained a native store")
	}
	if err = os.Rename(home, home+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(t.TempDir(), home); err != nil {
		t.Fatal(err)
	}
	_, _, linkErr := api.crewMuseStore(ctx, owner, roots, imported)
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
