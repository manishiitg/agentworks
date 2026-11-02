package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wh "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

func serverFirstEnvironment(t *testing.T) (*StreamingAPI, context.Context) {
	t.Helper()
	desktopTestEnvironment(t)
	docs := t.TempDir()
	docs, _ = filepath.EvalSymlinks(docs)
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("DEFAULT_USER_ID", "owner")
	t.Setenv("MULTI_USER_MODE", "")
	t.Setenv("CAPLAYER_SERVICE_URL", "")
	old := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", old) })
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"other","username":"other","role":"viewer","products":["work"]},{"id":"admin","username":"admin","is_admin":true,"can_create":true}]}`)
	files := gin.New()
	files.PUT("/api/documents/*filepath", wh.UpdateDocument)
	files.GET("/api/documents/*filepath", wh.GetDocument)
	files.GET("/api/documents", wh.ListDocuments)
	files.POST("/api/folders", wh.CreateFolder)
	workspace := httptest.NewServer(files)
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	registry := agentprofiles.NewRegistry()
	if err := registry.RegisterProfile(workCrewTestProfile(nil)); err != nil {
		t.Fatal(err)
	}
	journal, err := events.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := events.NewEventStore(10)
	t.Cleanup(store.Stop)
	store.SetDurableJournal(journal)
	api := &StreamingAPI{agentProfiles: registry, activeSessions: map[string]*ActiveSessionInfo{}, eventStore: store, runtimeCoordinator: NewRuntimeCoordinator()}
	return api, context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
}

type serverFirstFixture struct{ URL, ProjectID, ConversationID string }

// This helper is a second real API process with its own files/state. The test
// must not simulate both ends in one registry: server-first IDs are identical.
func serveServerFirstFixture(t *testing.T) {
	api, ctx := serverFirstEnvironment(t)
	project, err := api.createWorkspaceFromSpec(ctx, GetUserFromContext(ctx), "work", crewSpec{Name: "Existing project", Purpose: "Original server project", Mode: "dev"}, false)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := api.crewSyncRoots(ctx, GetUserFromContext(ctx), project)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("AGENTWORKS_SERVER_FIRST_SPLIT") == "true" {
		if err := os.MkdirAll(filepath.Join(roots.Files, crewroot.FilesSegment), 0700); err != nil {
			t.Fatal(err)
		}
		if err := crewroot.WriteSync(project, crewroot.SyncBinding{ArchiveOnly: os.Getenv("AGENTWORKS_SERVER_FIRST_ARCHIVE") == "true", PairID: "existing-split-pair", Side: "server", Editing: "server", PeerProjectID: project, SplitFiles: true}); err != nil {
			t.Fatal(err)
		}
		roots, err = api.crewSyncRoots(ctx, GetUserFromContext(ctx), project)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(roots.Files, "note.txt"), []byte("server work"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(roots.Resources, "commands", "custom"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roots.Resources, "commands", "custom", "COMMAND.md"), []byte("Server's authored command"), 0600); err != nil {
		t.Fatal(err)
	}
	main, err := api.crewHandoffMain(ctx, GetUserFromContext(ctx), project)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := workProjectChatHistoryConversationPath("owner", main.WorkspacePath, main.SessionID, time.Now())
	if !ok {
		t.Fatal("private conversation path unavailable")
	}
	raw, _ := json.Marshal(map[string]any{"user_id": "owner", "session_id": main.SessionID, "conversation_history": []map[string]any{{"Role": "human", "Parts": []map[string]any{{"Type": "text", "Text": "source-private-message"}}}}})
	if err := writeRawFileToWorkspace(ctx, p, string(raw)); err != nil {
		t.Fatal(err)
	}
	// An administrator/co-owner and a Run person can see this project, but
	// neither is its actual owner and neither may export its private chat.
	if err := defaultProjectOwners().SetRunPeople("work", filepath.Base(roots.Virtual), []string{"other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := defaultProjectOwners().AddCoOwner("work", filepath.Base(roots.Virtual), "admin"); err != nil {
		t.Fatal(err)
	}
	admin, _, ok := api.resolveExternalWorkspace(ctx, &UserClaims{UserID: "admin", Username: "admin"}, project)
	if !ok || !admin.OwnedByCaller || admin.OwnerID != "owner" {
		t.Fatal("admin/co-owner setup must reach the actual-owner boundary")
	}
	lostPairResponse := false
	refuseNextPair := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if actor != "owner" && actor != "other" && actor != "admin" {
			w.WriteHeader(401)
			return
		}
		claims := &UserClaims{UserID: actor, Username: actor}
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		if r.URL.Path == "/fixture/refuse-next" {
			refuseNextPair = true
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/fixture/state" {
			all, _ := defaultProjectOwners().All()
			b, _ := crewroot.ReadSync(project)
			externalJSON(w, map[string]any{"count": len(all), "binding": b})
			return
		}
		r = mux.SetURLVars(r, map[string]string{"project": project})
		if os.Getenv("AGENTWORKS_SERVER_FIRST_SPLIT") != "true" {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var input crewSyncRequest
			_ = json.Unmarshal(raw, &input)
			if input.Action == "pair" && refuseNextPair {
				refuseNextPair = false
				r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "admin", Username: "admin"}))
				api.handleCrewSync(w, r)
				return
			}
			if input.Action == "pair" && !lostPairResponse {
				response := httptest.NewRecorder()
				api.handleCrewSync(response, r)
				if response.Code != 200 {
					w.WriteHeader(response.Code)
					_, _ = w.Write(response.Body.Bytes())
					return
				}
				lostPairResponse = true
				w.WriteHeader(502)
				return
			}
		}
		api.handleCrewSync(w, r)
	}))
	defer server.Close()
	ready, _ := json.Marshal(serverFirstFixture{server.URL, project, main.ConversationID})
	if err := os.WriteFile(os.Getenv("AGENTWORKS_SERVER_FIRST_READY"), ready, 0600); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

// PLAT-856-492: opening an existing server project must retain its identity,
// owner-private conversation and layout, without merging a nonempty folder,
// creating a duplicate server project or allowing a Run/admin export.
func TestCrewSyncServerFirstHTTPIdentityAndOwner(t *testing.T) {
	if os.Getenv("AGENTWORKS_SERVER_FIRST_HELPER") == "true" {
		serveServerFirstFixture(t)
		return
	}
	testCrewSyncServerFirst(t, false)
}

// PLAT-856-508: Windows selects archive before native export/rsync, keeps
// the logical conversation, and applies both directions' files before a send.
func TestWindowsCrewSyncReloadAndFiles(t *testing.T) {
	testCrewSyncServerFirst(t, true)
}

func testCrewSyncServerFirst(t *testing.T, windows bool) {
	t.Helper()
	if windows {
		oldOS := cliHostOS
		cliHostOS = "windows"
		t.Cleanup(func() { cliHostOS = oldOS })
		t.Setenv("AGENTWORKS_RSYNC_BIN", filepath.Join(t.TempDir(), "rsync-must-not-run"))
	}
	for _, split := range []bool{false, true} {
		t.Run(map[bool]string{false: "flat", true: "split"}[split], func(t *testing.T) {
			api, ctx := serverFirstEnvironment(t)
			ready := filepath.Join(t.TempDir(), "ready.json")
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, "-test.run=^TestCrewSyncServerFirstHTTPIdentityAndOwner$", "-test.timeout=3m")
			cmd.Env = append(os.Environ(), "AGENTWORKS_SERVER_FIRST_HELPER=true", "AGENTWORKS_SERVER_FIRST_READY="+ready, "AGENTWORKS_SERVER_FIRST_SPLIT="+map[bool]string{false: "false", true: "true"}[split], "AGENTWORKS_SERVER_FIRST_ARCHIVE="+map[bool]string{false: "false", true: "true"}[windows])
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			logfile, err := os.Create(filepath.Join(t.TempDir(), "server.log"))
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stdout, cmd.Stderr = logfile, logfile
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = stdin.Close()
				if err := cmd.Wait(); err != nil {
					raw, _ := os.ReadFile(logfile.Name())
					t.Errorf("source server: %v\n%s", err, raw)
				}
				_ = logfile.Close()
			})
			var source serverFirstFixture
			deadline := time.Now().Add(20 * time.Second)
			for {
				if raw, e := os.ReadFile(ready); e == nil && json.Unmarshal(raw, &source) == nil {
					break
				}
				if time.Now().After(deadline) {
					raw, _ := os.ReadFile(logfile.Name())
					t.Fatalf("source did not start: %s", raw)
				}
				time.Sleep(20 * time.Millisecond)
			}
			if split {
				// This source already has a split pair. Mac A retains its
				// private receipt from before an interrupted first restore.
				if err := crewroot.WriteSync(source.ProjectID, crewroot.SyncBinding{ArchiveOnly: windows, PairID: "existing-split-pair", Side: "local", Editing: "server", ConnectionID: "saved", PeerProjectID: source.ProjectID, PeerServer: source.URL, SplitFiles: true}); err != nil {
					t.Fatal(err)
				}
			}
			for _, actor := range []string{"owner", "other", "admin"} {
				if err := writeDesktopConnections("owner", []desktopConnection{{ID: "saved", Server: source.URL, Token: actor, User: UserInfo{ID: actor}}}); err != nil {
					t.Fatal(err)
				}
				r := desktopTestRequest(http.MethodGet, "/", "", "owner")
				r = mux.SetURLVars(r, map[string]string{"connection": "saved", "project": source.ProjectID})
				w := httptest.NewRecorder()
				api.handleDesktopCrewOpen(w, r)
				if actor == "owner" {
					if w.Code != 200 {
						t.Fatalf("owner inspection %d: %s", w.Code, w.Body)
					}
				} else if w.Code == 200 || !strings.Contains(w.Body.String(), map[string]string{"other": "interactive", "admin": "owner"}[actor]) {
					t.Fatalf("%s was not refused by ownership: %d %s", actor, w.Code, w.Body)
				}
			}
			if err := writeDesktopConnections("owner", []desktopConnection{{ID: "saved", Server: source.URL, Token: "owner", User: UserInfo{ID: "owner"}}}); err != nil {
				t.Fatal(err)
			}
			open := func(folder string) *httptest.ResponseRecorder {
				body, _ := json.Marshal(map[string]string{"folder": folder})
				r := desktopTestRequest(http.MethodPost, "/", string(body), "owner")
				r = mux.SetURLVars(r, map[string]string{"connection": "saved", "project": source.ProjectID})
				w := httptest.NewRecorder()
				api.handleDesktopCrewOpen(w, r)
				return w
			}
			occupied := t.TempDir()
			marker := filepath.Join(occupied, "keep.txt")
			if err := os.WriteFile(marker, []byte("keep existing files"), 0600); err != nil {
				t.Fatal(err)
			}
			if w := open(occupied); w.Code != 409 || !strings.Contains(w.Body.String(), "empty folder") {
				t.Fatalf("nonempty folder %d: %s", w.Code, w.Body)
			}
			if raw, _ := os.ReadFile(marker); string(raw) != "keep existing files" {
				t.Fatal("existing folder modified")
			}
			folder := filepath.Join(t.TempDir(), "local-project")
			if !split {
				request, _ := http.NewRequest(http.MethodPost, source.URL+"/fixture/refuse-next", nil)
				request.Header.Set("Authorization", "Bearer owner")
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				_ = response.Body.Close()
				if denied := open(folder); denied.Code != 409 || !strings.Contains(denied.Body.String(), "owner") {
					t.Fatalf("owner revoked before pairing: %d %s", denied.Code, denied.Body)
				}
				if receipt, err := crewroot.ReadSync(source.ProjectID); err != nil || receipt != nil {
					t.Fatalf("definite refusal retained a new pairing receipt: %+v %v", receipt, err)
				}
				if failed := open(folder); failed.Code != 409 {
					t.Fatalf("lost first pair response: %d %s", failed.Code, failed.Body)
				}
				if receipt, err := crewroot.ReadSync(source.ProjectID); err != nil || receipt == nil {
					t.Fatalf("original Mac lost its pending pairing receipt: %+v %v", receipt, err)
				}
			}
			w := open(folder)
			if w.Code != 200 {
				t.Fatalf("open existing server project %d: %s", w.Code, w.Body)
			}
			var out crewLocalOpenReply
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.ProjectID != source.ProjectID || out.Binding == nil || out.Binding.Editing != "server" || out.Binding.PeerProjectID != source.ProjectID {
				t.Fatalf("identity/editing changed: %+v", out)
			}
			local, err := api.crewSyncRoots(ctx, GetUserFromContext(ctx), source.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			if local.SeparateFiles() != split {
				t.Fatal("server layout changed")
			}
			if raw, err := os.ReadFile(filepath.Join(local.Files, "note.txt")); err != nil || string(raw) != "server work" {
				t.Fatalf("files in wrong folder: %q %v", raw, err)
			}
			if raw, err := os.ReadFile(filepath.Join(local.Resources, "commands", "custom", "COMMAND.md")); err != nil || string(raw) != "Server's authored command" {
				t.Fatal("server command missing")
			}
			main, err := api.crewHandoffMain(ctx, GetUserFromContext(ctx), source.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			if main.ConversationID != source.ConversationID {
				t.Fatal("logical conversation replaced")
			}
			history, err := ReadChatHistoryConversation("owner", main.SessionID, main.WorkspacePath)
			if err != nil || !bytes.Contains(history, []byte("source-private-message")) {
				t.Fatalf("private conversation missing: %v", err)
			}
			if len(api.activeSessions) != 0 {
				t.Fatal("opening started a local turn")
			}
			if w := open(occupied); w.Code != 200 {
				t.Fatalf("repeat did not reopen same pair: %d %s", w.Code, w.Body)
			}
			all, _ := defaultProjectOwners().All()
			if len(all) != 1 {
				t.Fatal("duplicate local project")
			}
			// PR670: opening an already admitted copy must not erase an
			// upload receipt waiting to replay after its response was lost.
			original, err := crewroot.ReadSync(source.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			pending := *original
			pending.PendingAction = "server"
			pending.Editing = "local"
			pending.LastTransfer = "retained-upload"
			if err := crewroot.WriteSync(source.ProjectID, pending); err != nil {
				t.Fatal(err)
			}
			if reopened := open(occupied); reopened.Code != 200 {
				t.Fatalf("open pending copy: %d %s", reopened.Code, reopened.Body)
			}
			if retained, err := crewroot.ReadSync(source.ProjectID); err != nil || retained == nil || *retained != pending {
				t.Fatalf("opening erased interrupted transfer state: %+v %v", retained, err)
			}
			if err := crewroot.WriteSync(source.ProjectID, *original); err != nil {
				t.Fatal(err)
			}
			// The owner's second Mac has the same saved server account, but
			// no locally admitted receipt. Server status isn't a new pairing.
			stateA := os.Getenv("AGENTWORKS_STATE_ROOT")
			t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
			if err := writeDesktopConnections("owner", []desktopConnection{{ID: "saved", Server: source.URL, Token: "owner", User: UserInfo{ID: "owner"}}}); err != nil {
				t.Fatal(err)
			}
			folderB := filepath.Join(t.TempDir(), "second-mac-copy")
			if denied := open(folderB); denied.Code != 409 || !strings.Contains(denied.Body.String(), "already open on") || !strings.Contains(denied.Body.String(), "not available yet") {
				t.Fatalf("second Mac admitted: %d %s", denied.Code, denied.Body)
			}
			if _, err := os.Stat(folderB); !os.IsNotExist(err) {
				t.Fatal("refused second Mac created a folder")
			}
			if records, _ := defaultProjectOwners().All(); len(records) != 0 {
				t.Fatal("refused second Mac created a project")
			}
			t.Setenv("AGENTWORKS_STATE_ROOT", stateA)
			if again := open(occupied); again.Code != 200 {
				t.Fatalf("original Mac lost its admitted pair: %d %s", again.Code, again.Body)
			}
			send := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
			send.Header.Set("X-Session-ID", main.SessionID)
			gate := httptest.NewRecorder()
			if api.ensureCrewNativeExecution(gate, send, "work", source.ProjectID) {
				t.Fatalf("first local send did not use existing handoff: %d %s", gate.Code, gate.Body)
			}
			b, err := crewroot.ReadSync(source.ProjectID)
			if err != nil || b.Editing != "local" || b.PendingAction != "" {
				t.Fatalf("first send failed to switch: %+v %v", b, err)
			}
			request, _ := http.NewRequest(http.MethodGet, source.URL+"/fixture/state", nil)
			request.Header.Set("Authorization", "Bearer owner")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var state struct {
				Count   int                   `json:"count"`
				Binding *crewroot.SyncBinding `json:"binding"`
			}
			if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
				t.Fatal(err)
			}
			if state.Count != 1 || state.Binding == nil || state.Binding.Editing != "local" {
				t.Fatalf("server duplicated project or did not transfer: %+v", state)
			}
			if windows {
				if !b.ArchiveOnly || !state.Binding.ArchiveOnly {
					t.Fatal("Windows pair did not select archive on both computers")
				}
				main, err = api.crewHandoffMain(ctx, GetUserFromContext(ctx), source.ProjectID)
				if err != nil || !main.FreshNative || main.ConversationID != source.ConversationID {
					t.Fatalf("Windows did not reload its conversation into a fresh session: %+v %v", main, err)
				}
				// Continuous saved-history mirroring must work without rsync too.
				if err := api.syncCrewNativeMirror(ctx, GetUserFromContext(ctx), local, b, desktopConnection{ID: "saved", Server: source.URL, Token: "owner"}); err != nil {
					t.Fatalf("archive mirror: %v", err)
				}
				mirror, err := readCrewNativeMirror(local)
				if err != nil || mirror.Conversation.Native != nil || mirror.Conversation.ConversationID != source.ConversationID {
					t.Fatalf("archive mirror lost history or acquired native state: %+v %v", mirror, err)
				}
				if err := os.WriteFile(filepath.Join(local.Files, "note.txt"), []byte("Windows edit"), 0600); err != nil {
					t.Fatal(err)
				}
				sent := api.runInternalCrewHandoff(ctx, GetUserFromContext(ctx), source.ProjectID, "saved", "server")
				if sent.status >= 400 {
					t.Fatalf("Windows -> server: %d %s", sent.status, sent.body.String())
				}
				returned := api.runInternalCrewHandoff(ctx, GetUserFromContext(ctx), source.ProjectID, "saved", "local")
				if returned.status >= 400 {
					t.Fatalf("server -> Windows: %d %s", returned.status, returned.body.String())
				}
				if content, err := os.ReadFile(filepath.Join(local.Files, "note.txt")); err != nil || string(content) != "Windows edit" {
					t.Fatalf("roundtrip lost file: %q %v", content, err)
				}
				current, err := api.crewHandoffMain(ctx, GetUserFromContext(ctx), source.ProjectID)
				if err != nil || current.ConversationID != source.ConversationID || !current.FreshNative {
					t.Fatalf("roundtrip lost fresh-session continuity: %+v %v", current, err)
				}
				if history, err := ReadChatHistoryConversation("owner", current.SessionID, current.WorkspacePath); err != nil || !bytes.Contains(history, []byte("source-private-message")) {
					t.Fatalf("roundtrip lost remembered conversation: %v", err)
				}
			}
		})
	}
}
