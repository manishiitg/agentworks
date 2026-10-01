package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/setupagent"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

func TestSetupChatCreatesDraftWithoutPublishing(t *testing.T) {
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
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"draft1","type":"function","function":{"name":"save_draft","arguments":"{\"group_id\":\"g\",\"name\":\"one project\",\"rules\":[{\"public_name\":\"p__read\",\"fingerprint\":\"v1\",\"conditions\":[{\"path\":\"/project\",\"op\":\"equals\",\"value\":\"one\"}]}]}"}}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Draft saved for review."}}]}`))
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
	r := httptest.NewRequest(http.MethodPost, "/api/admin/setup/chat", bytes.NewBufferString(`{"model":"other","messages":[{"role":"user","content":"Create a draft for group g on project one"}]}`))
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Draft saved") {
		t.Fatalf("setup chat: %d %s", w.Code, w.Body.String())
	}
	packages := s.ListPackages("w")
	if len(packages) != 1 || packages[0].Status != "draft" || called != 2 {
		t.Fatalf("setup result: packages=%+v model calls=%d", packages, called)
	}
	if len(requestedModels) != 2 || requestedModels[0] != "other" || requestedModels[1] != "other" {
		t.Fatalf("model selection was not used for every completion: %v", requestedModels)
	}
}

func TestSharedSetupToolEndpointCannotPublishAndRecordsActor(t *testing.T) {
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
		{"secret", `{"operation":"revoke","arguments":{}}`, 400},
		{"secret", `{"operation":"inspect_environment","arguments":[]} `, 400},
		{"secret", `{"operation":"inspect_environment","arguments":{}} {}`, 400},
		{"secret", `{"operation":"save_draft","arguments":{"group_id":"g","name":"one project","rules":[{"public_name":"p__read","fingerprint":"v1","conditions":[{"path":"/project","op":"equals","value":"one"}]}]}}`, 200},
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
	if len(packages) != 1 || packages[0].Status != "draft" {
		t.Fatal("setup changed live policy")
	}
	events := s.ListPolicyEvents("w")
	if len(events) != 1 || events[0].Actor != "product-admin" {
		t.Fatalf("incorrect policy attribution: %+v", events)
	}
}
