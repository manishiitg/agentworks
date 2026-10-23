package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

func TestMCPLaptopSettingsUseOwnerGrantsWithoutCommandAccess(t *testing.T) {
	f := newMCPLaptopFixture(t)
	api := f.api
	const root = "Crew/dashboard-team"
	owner := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:write"}, AllCrews: true}}
	call := func(c *UserClaims, action string, extra map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		args := map[string]any{"action": action, "workspace_id": "work:dashboard-team"}
		for k, v := range extra {
			args[k] = v
		}
		r := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, c))
		w := httptest.NewRecorder()
		api.externalLaptopAccessCall(w, r, args)
		return w
	}
	if w := call(owner, "get", nil); w.Code != 200 {
		t.Fatalf("owner MCP get: %d %s", w.Code, w.Body.String())
	}
	file, err := laptopSettingsPath("owner", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	seed := crewLaptopSettings{DeviceID: "offline-laptop", Folders: []localfiles.FolderSelection{{ID: "reports", Path: "/home/person/Reports", Access: "write"}}}
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	folders := []any{map[string]any{"id": "reports", "path": "/home/person/Reports", "access": "read"}}
	if w := call(owner, "set", map[string]any{"device_id": "offline-laptop", "folders": folders}); w.Code != 200 {
		t.Fatalf("offline downgrade: %d %s", w.Code, w.Body.String())
	}
	saved, err := readLaptopSettings("owner", root)
	if err != nil || len(saved.Folders) != 1 || saved.Folders[0].Access != "read" {
		t.Fatalf("same website settings: %+v %v", saved, err)
	}
	folders[0].(map[string]any)["access"] = "write"
	if w := call(owner, "set", map[string]any{"device_id": "offline-laptop", "folders": folders}); w.Code != 400 {
		t.Fatalf("expanded offline grant: %d", w.Code)
	}
	other := *owner
	other.UserID = "other"
	other.Username = "other"
	if w := call(&other, "get", nil); w.Code != 403 {
		t.Fatalf("other owner got access: %d", w.Code)
	}
	if _, err := defaultProjectOwners().AddCoOwner("work", "dashboard-team", "other"); err != nil {
		t.Fatal(err)
	}
	if w := call(&other, "get", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "offline-laptop") {
		t.Fatalf("co-owner shared settings: %d %s", w.Code, w.Body.String())
	}
	if coPath, err := laptopSettingsPath("other", root); err != nil || coPath != file {
		t.Fatal("co-owner has a separate settings file")
	}
	if w := call(&other, "set", map[string]any{"device_id": "offline-laptop", "folders": folders}); w.Code != 400 {
		t.Fatalf("co-owner expanded an offline grant: %d", w.Code)
	}
	for _, token := range []*accesstokens.Token{{ProjectKind: "crew"}, {BoundAgentID: "agent"}, {KnowledgebaseIdentityID: "brain"}, {Scopes: []string{"devices:connect"}}} {
		c := *owner
		c.AccessToken = token
		if w := call(&c, "get", nil); w.Code != 403 {
			t.Fatalf("non-person connection got laptop settings: %d", w.Code)
		}
	}
	if _, err := api.laptopSettingsDeviceCall(t.Context(), owner, "offline-laptop", localfiles.Request{Operation: "shell", Command: "pwd"}); err == nil {
		t.Fatal("settings adapter accepted a command")
	}
	if err := api.validateLaptopConnection(owner, &laptopConnectionTarget{DeviceID: "offline-laptop"}); err == nil {
		t.Fatal("MCP settings also enabled runtime laptop tools")
	}
	if w := call(owner, "remove", nil); w.Code != 200 {
		t.Fatalf("offline removal: %d %s", w.Code, w.Body.String())
	}
	saved, err = readLaptopSettings("owner", root)
	if err != nil || saved.DeviceID != "" || len(saved.Folders) != 0 {
		t.Fatalf("removal not shared with website: %+v %v", saved, err)
	}
}

// Real authenticated MCP HTTP -> outbound laptop WebSocket -> real executor ->
// normal Crew guarded writer. All files, identities and credentials are fixtures.
func TestMCPLaptopCommandAndBinaryCopyUseSavedFolderAccess(t *testing.T) {
	for _, goal := range []bool{false, true} {
		name := "Crew"
		if goal {
			name = "Goal"
		}
		t.Run(name, func(t *testing.T) {
			f := newMCPLaptopFixture(t)
			root := "Crew/dashboard-team"
			target := map[string]any{"workspace_id": "work:dashboard-team"}
			if goal {
				root = "Workflow/invoices"
				target = map[string]any{"workflow_id": "wf_invoices"}
			}
			goalManifest := `{"schema_version":1,"id":"wf_invoices","label":"Invoices","capabilities":{},"access":{"owners":["owner"],"editors":["other"]}}`
			if goal {
				f.write(t, root+"/workflow.json", goalManifest)
			}
			routes := http.NewServeMux()
			routes.HandleFunc("GET /api/external/v1/devices/connect", f.api.handleLocalDeviceConnect)
			routes.HandleFunc("POST /api/external/v1/call", f.api.handleExternalCall)
			server := httptest.NewServer(AuthMiddleware(routes))
			defer server.Close()
			t.Setenv("PUBLIC_URL", server.URL)
			store, err := openMCPOAuthStore()
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			device, verification, err := store.CreateCLIDevice(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := store.DecideCLIDevice(t.Context(), verification, &UserClaims{UserID: "owner", Username: "owner"}, true); err != nil {
				t.Fatal(err)
			}
			grant, personToken, _, err := store.PollCLIDevice(t.Context(), device, server.URL+"/api/external/v1")
			if err != nil {
				t.Fatal(err)
			}
			if !mcpOAuthTokenForGrant(grant).Allows("devices:connect") || !mcpOAuthTokenForGrant(grant).Allows("crews:write") {
				t.Fatal("fixture did not use the combined one-CLI grant")
			}
			base := t.TempDir()
			folder := filepath.Join(base, "files")
			if err := os.Mkdir(folder, 0700); err != nil {
				t.Fatal(err)
			}
			payload := []byte{0, 1, 2, 255, 42}
			if err := os.WriteFile(filepath.Join(folder, "sample.bin"), payload, 0600); err != nil {
				t.Fatal(err)
			}
			executor, err := localfiles.OpenSettings("mcp-laptop", filepath.Join(base, "executor"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer executor.Close()
			client, err := agentworksclient.New(server.URL, personToken)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			connected := make(chan struct{})
			finished := make(chan error, 1)
			go func() { finished <- client.ServeExecutor(ctx, executor, func() { close(connected) }) }()
			select {
			case <-connected:
			case err := <-finished:
				t.Fatal(err)
			case <-time.After(5 * time.Second):
				t.Fatal("laptop connect timeout")
			}
			for _, person := range []string{"owner", "other"} {
				vault, err := f.api.myVaultsOperation(t.Context(), person, map[string]any{"operation": "laptops"})
				if err != nil || strings.Contains(vault, `"device_id":"mcp-laptop"`) != (person == "owner") {
					t.Fatalf("personal Vault computers: %s %v", vault, err)
				}
			}
			call := func(name string, args map[string]any, status int, project ...bool) []byte {
				t.Helper()
				if len(project) == 0 || project[0] {
					for key, value := range target {
						args[key] = value
					}
				}
				data, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
				r, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/api/external/v1/call", bytes.NewReader(data))
				r.Header.Set("Authorization", "Bearer "+personToken)
				r.Header.Set("Content-Type", "application/json")
				response, err := http.DefaultClient.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, _ := io.ReadAll(response.Body)
				if response.StatusCode != status {
					t.Fatalf("%s: %d %s", name, response.StatusCode, body)
				}
				return body
			}
			if computers := call("manage_laptop_access", map[string]any{"action": "devices"}, 200, false); !strings.Contains(string(computers), "mcp-laptop") {
				t.Fatalf("MCP My Vault computers: %s", computers)
			}
			call("manage_laptop_access", map[string]any{"action": "get"}, 400, false)
			folders := []any{map[string]any{"id": "files", "path": folder, "access": "read"}}
			call("manage_laptop_access", map[string]any{"action": "set", "device_id": "mcp-laptop", "folders": folders}, 200)
			call("execute_local_shell_command", map[string]any{"folder": "files", "command": "printf laptop-mcp"}, 403)
			call("copy_local_file_to_project", map[string]any{"folder": "files", "local_path": "sample.bin", "project_path": "uploads/sample.bin"}, 200)
			copied, err := os.ReadFile(filepath.Join(f.docs, root, "uploads/sample.bin"))
			if err != nil || !bytes.Equal(copied, payload) {
				t.Fatalf("binary copy: %v %v", copied, err)
			}
			folders[0].(map[string]any)["access"] = "write"
			call("manage_laptop_access", map[string]any{"action": "set", "device_id": "mcp-laptop", "folders": folders}, 200)
			// Read & write covers files only: the agent can list and write, but commands need their own switch.
			call("execute_local_shell_command", map[string]any{"folder": "files", "command": "printf laptop-mcp"}, 403)
			call("write_local_file", map[string]any{"folder": "files", "path": "notes/hello.txt", "content": "hello from the crew"}, 200)
			if written, err := os.ReadFile(filepath.Join(folder, "notes/hello.txt")); err != nil || string(written) != "hello from the crew" {
				t.Fatalf("written file: %q %v", written, err)
			}
			if listed := call("list_local_files", map[string]any{"folder": "files", "path": "notes"}, 200); !strings.Contains(string(listed), "hello.txt") {
				t.Fatalf("listing: %s", listed)
			}
			call("manage_laptop_access", map[string]any{"action": "set", "device_id": "mcp-laptop", "folders": folders, "commands": true}, 200)
			if !goal {
				if _, err := defaultProjectOwners().AddCoOwner("work", "dashboard-team", "other"); err != nil {
					t.Fatal(err)
				}
			}
			co := &UserClaims{UserID: "other", Username: "other", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read", "crews:write", "builder:chat", "workflows:read", "files:read", "runs:execute"}, AllCrews: true, AllWorkflows: true}}
			coRequest := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", nil).WithContext(context.WithValue(t.Context(), UserContextKey, co))
			coResult := httptest.NewRecorder()
			coArgs := map[string]any{"folder": "files", "path": "notes/co-owner.txt", "content": "shared project"}
			for key, value := range target {
				coArgs[key] = value
			}
			f.api.externalLaptopToolCall(coResult, coRequest, "write_local_file", coArgs)
			if coResult.Code != 200 {
				t.Fatalf("co-owner laptop: %d %s", coResult.Code, coResult.Body.String())
			}
			if written, err := os.ReadFile(filepath.Join(folder, "notes/co-owner.txt")); err != nil || string(written) != "shared project" {
				t.Fatalf("co-owner laptop write: %q %v", written, err)
			}
			if goal {
				f.write(t, root+"/workflow.json", strings.Replace(goalManifest, `"editors":["other"]`, `"readers":["other"]`, 1))
			} else if _, err := defaultProjectOwners().RemoveCoOwner("work", "dashboard-team", "other"); err != nil {
				t.Fatal(err)
			}
			denied := httptest.NewRecorder()
			f.api.externalLaptopToolCall(denied, coRequest, "list_local_files", target)
			if denied.Code != 403 {
				t.Fatalf("removed co-owner laptop access: %d %s", denied.Code, denied.Body.String())
			}
			{
				result := call("execute_local_shell_command", map[string]any{"folder": "files", "command": "printf laptop-mcp", "timeout": 10}, 200)
				if !strings.Contains(string(result), "laptop-mcp") {
					t.Fatalf("missing local stdout: %s", result)
				}
			}
			call("manage_laptop_access", map[string]any{"action": "remove"}, 200)
			call("execute_local_shell_command", map[string]any{"folder": "files", "command": "printf should-not-run"}, 400)
		})
	}
}

// Use the real workspace document/list/write handlers, so project discovery
// and the stable main-chat registry have the same behavior as a server.
func newMCPLaptopFixture(t *testing.T) *externalToolsFixture {
	t.Helper()
	tokenTestSetup(t)
	f := newExternalToolsFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work","agentworks","mcp-gateway"]},{"id":"other","username":"other","can_create":true,"products":["work","agentworks","mcp-gateway"]}]}`)
	const root = "Crew/dashboard-team"
	f.write(t, root+"/product.json", `{"schema_version":1,"product":"work","id":"dashboard-team","title":"Laptop test","session_id":"fixture-main-session"}`)
	f.write(t, root+"/workflow.json", `{"schema_version":1,"id":"dashboard-team","capabilities":{}}`)
	registry := withProjectOwnerRegistry(t)
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "dashboard-team", ProjectID: "dashboard-team", OwnerID: "owner", CanonicalRoot: root}); err != nil {
		t.Fatal(err)
	}
	f.api.agentProfiles = agentprofiles.NewRegistry()
	profile := routeTestProfile("work", true, "")
	profile.Product = "work"
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: "Crew"}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}
	if err := f.api.agentProfiles.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	router.PUT("/api/documents/*filepath", workspacehandlers.UpdateDocument)
	router.POST("/api/shared-file-write", workspacehandlers.SharedFileWrite)
	upstream := httptest.NewServer(router)
	t.Cleanup(upstream.Close)
	t.Setenv("WORKSPACE_API_URL", upstream.URL)
	return f
}
