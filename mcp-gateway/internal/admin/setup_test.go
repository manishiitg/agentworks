package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/catalog"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/setupagent"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

func TestSetupChatAppliesPermissionsImmediately(t *testing.T) {
	called := 0
	var requestedModels []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode model request: %v", err)
		}
		requestedModels = append(requestedModels, request.Model)
		if called == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"draft1","type":"function","function":{"name":"save_permissions","arguments":"{\"group_id\":\"g\",\"name\":\"one project\",\"rules\":[{\"public_name\":\"p__read\",\"fingerprint\":\"v1\",\"conditions\":[{\"path\":\"/project\",\"op\":\"equals\",\"value\":\"one\"}]}]}"}}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Permissions saved and active."}}]}`))
	}))
	defer model.Close()
	s := store.NewMemoryStore()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddGroup(store.Group{ID: "g", WorkspaceID: "w"})
	s.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive})
	tool := s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "p__read", Fingerprint: "v1", InputSchema: []byte(`{"type":"object","properties":{"project":{"type":"string"}}}`)})
	s.ApproveTool("w", tool.PublicName, tool.Fingerprint, tool.Version)
	a := &Admin{Store: s, WorkspaceID: "w", HumanToken: "secret", SetupAgent: &setupagent.Agent{Endpoint: model.URL, Model: "fake", AvailableModels: []string{"other", "fake"}}}
	mux := http.NewServeMux()
	a.setupRoutes(mux)
	status := httptest.NewRequest(http.MethodGet, "/api/admin/setup/status", nil)
	status.Header.Set("Authorization", "Bearer secret")
	statusResponse := httptest.NewRecorder()
	mux.ServeHTTP(statusResponse, status)
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), `"models":["fake","other"]`) {
		t.Fatalf("setup status: %d %s", statusResponse.Code, statusResponse.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodPost, "/api/admin/setup/chat", bytes.NewBufferString(`{"model":"unlisted","messages":[{"role":"user","content":"hello"}]}`))
	invalid.Header.Set("Authorization", "Bearer secret")
	invalidResponse := httptest.NewRecorder()
	mux.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest || called != 0 {
		t.Fatalf("unlisted model accepted: %d %s", invalidResponse.Code, invalidResponse.Body.String())
	}
	r := httptest.NewRequest(http.MethodPost, "/api/admin/setup/chat", bytes.NewBufferString(`{"model":"other","messages":[{"role":"user","content":"Apply permissions for group g on project one"}]}`))
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Permissions saved") {
		t.Fatalf("setup chat: %d %s", w.Code, w.Body.String())
	}
	packages := s.ListPackages("w")
	if len(packages) != 1 || packages[0].Status != "published" || called != 2 {
		t.Fatalf("setup result: packages=%+v model calls=%d", packages, called)
	}
	if len(requestedModels) != 2 || requestedModels[0] != "other" || requestedModels[1] != "other" {
		t.Fatalf("model selection was not used for every completion: %v", requestedModels)
	}
}

func TestSharedSetupToolEndpointAppliesPermissionsAndRecordsActor(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddGroup(store.Group{ID: "g", WorkspaceID: "w"})
	s.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive})
	tool := s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "p__read", Fingerprint: "v1", InputSchema: []byte(`{"type":"object","properties":{"project":{"type":"string"}}}`)})
	s.ApproveTool("w", tool.PublicName, tool.Fingerprint, tool.Version)
	a := &Admin{Store: s, WorkspaceID: "w", HumanToken: "secret"}
	mux := http.NewServeMux()
	a.setupRoutes(mux)
	for _, tc := range []struct {
		token, body string
		status      int
	}{
		{"", `{"operation":"inspect_environment","arguments":{}}`, 401},
		{"secret", `{"operation":"publish","arguments":{}}`, 400},
		{"secret", `{"operation":"save_draft","arguments":{}}`, 400},
		{"secret", `{"operation":"revoke","arguments":{}}`, 400},
		{"secret", `{"operation":"inspect_environment","arguments":[]} `, 400},
		{"secret", `{"operation":"inspect_environment","arguments":{}} {}`, 400},
		{"secret", `{"operation":"save_permissions","arguments":{"group_id":"g","name":"one project","rules":[{"public_name":"p__read","fingerprint":"v1","conditions":[{"path":"/project","op":"equals","value":"one"}]}]}}`, 200},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/setup/tool", strings.NewReader(tc.body))
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		req.Header.Set("X-CapLayer-Actor", "product-admin")
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != tc.status {
			t.Fatalf("%s: got %d %s, want %d", tc.body, res.Code, res.Body, tc.status)
		}
	}
	packages := s.ListPackages("w")
	if len(packages) != 1 || packages[0].Status != "published" {
		t.Fatal("setup did not apply permissions")
	}
	events := s.ListPolicyEvents("w")
	if len(events) != 1 || events[0].Actor != "product-admin" || events[0].Action != "save_permissions" {
		t.Fatalf("incorrect policy attribution: %+v", events)
	}
}

func TestSetupToolCreatesSeparateNamedCatalogAccounts(t *testing.T) {
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	gw := mcpserver.New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{})
	gw.SetSharedOAuth(func(context.Context, string, string, string) (string, error) {
		t.Fatal("connection creation requested another account's credential")
		return "", nil
	})
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"Notion":{"url":"https://mcp.notion.com/mcp","oauth":{"client_id":"app"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &Admin{Store: st, Gateway: gw, WorkspaceID: "w", Catalog: cat}
	for _, label := range []string{"Notion · Engineering", "Notion · Sales"} {
		payload, _ := json.Marshal(map[string]string{"provider": "Notion", "label": label})
		result, err := a.setupTool(context.Background(), "connect_server", payload, "admin")
		if err != nil {
			t.Fatal(err)
		}
		row := result.(map[string]any)
		c := row["connector"].(store.Connector)
		if c.Label != label || c.OAuthCredentialID != c.ID || c.Status != store.StatusAuthRequired || row["group_access_assigned"] != false || row["sign_in_required"] != true {
			t.Fatal("chat created an incorrect connection")
		}
	}
	connections := st.ListConnectors("w")
	if len(connections) != 2 || connections[0].InstanceSlug == connections[1].InstanceSlug {
		t.Fatal("chat replaced an existing account")
	}
	if _, err := a.setupTool(context.Background(), "connect_server", json.RawMessage(`{"provider":"Notion","url":"https://evil.example/mcp"}`)); err == nil {
		t.Fatal("catalog URL override accepted")
	}
	if _, err := a.setupTool(context.Background(), "connect_server", json.RawMessage(`{"provider":"Notion","token":"credential"}`)); err == nil {
		t.Fatal("chat accepted credentials")
	}
}

func TestSetupInspectionReturnsLiveToolSchemaAndAnnotations(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive})
	tool := s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "notion__read", Fingerprint: "v1", Description: "Retrieve a page", InputSchema: []byte(`{"type":"object","properties":{"page_id":{"type":"string"}}}`), Annotations: []byte(`{"readOnlyHint":true,"destructiveHint":false}`)})
	s.ApproveTool("w", tool.PublicName, tool.Fingerprint, tool.Version)
	a := &Admin{Store: s, WorkspaceID: "w", Catalog: &catalog.Catalog{}}
	for _, op := range []string{"inspect_environment", "inspect_tool"} {
		result, err := a.setupTool(context.Background(), op, json.RawMessage(`{"public_name":"notion__read"}`))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(result)
		if err != nil || !strings.Contains(string(raw), `"readOnlyHint":true`) {
			t.Fatalf("missing live annotations: %s %v", raw, err)
		}
		if op == "inspect_tool" && !strings.Contains(string(raw), `"page_id"`) {
			t.Fatal("missing actual input schema")
		}
	}
}
