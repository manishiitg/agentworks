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

	"github.com/gin-gonic/gin"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wh "github.com/manishiitg/coding-agent-loop/workspace/handlers"
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
			t.Setenv("MULTI_USER_MODE", "")
			old := viper.Get("docs-dir")
			viper.Set("docs-dir", docs)
			t.Cleanup(func() { viper.Set("docs-dir", old) })
			withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"other","username":"other","can_create":true}]}`)
			files := gin.New()
			files.PUT("/api/documents/*filepath", wh.UpdateDocument)
			files.GET("/api/documents/*filepath", wh.GetDocument)
			files.GET("/api/documents", wh.ListDocuments)
			files.POST("/api/folders", wh.CreateFolder)
			workspace := httptest.NewServer(files)
			defer workspace.Close()
			t.Setenv("WORKSPACE_API_URL", workspace.URL)
			registry := agentprofiles.NewRegistry()
			registerWorkCrewProfile(t, registry)
			api := &StreamingAPI{agentProfiles: registry, activeSessions: map[string]*ActiveSessionInfo{}}
			owner := &UserClaims{UserID: "owner", Username: "owner"}
			ctx := context.WithValue(t.Context(), UserContextKey, owner)
			options := crewCreationOptions{}
			if split {
				options.LocalRoot = project
			}
			_, err := writeCrewCreationManifests(ctx, "owner", workCrewTestProfile(nil), "work", "", "Example", "", "", "local-id", "Crew/local", nil, options)
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
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			loseResponse = true
			if w := call("server", "owner", true); w.Code != 409 {
				t.Fatal("lost response not reported")
			}
			pending, _ := crewroot.ReadSync(local.ID)
			if pending.PendingAction != "server" {
				t.Fatal("lost retry state")
			}
			write(serverRoots.Files, "note.txt", "server work after handoff")
			if w := call("server", "owner", true); w.Code != 200 {
				t.Fatalf("retry: %d %s", w.Code, w.Body.String())
			}
			checkWrite(local.FilesPath()+"/note.txt", 409)
			if w := call("local", "owner", true); w.Code != 200 {
				t.Fatalf("return: %d %s", w.Code, w.Body.String())
			}
			raw, err := os.ReadFile(filepath.Join(local.Files, "note.txt"))
			if err != nil || string(raw) != "server work after handoff" {
				t.Fatalf("returned file: %q %v", raw, err)
			}
			checkWrite(local.FilesPath()+"/new.txt", 200)
		})
	}
}
