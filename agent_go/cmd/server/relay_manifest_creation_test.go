package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise the UI creation endpoint: the starter must not depend on a manifest
// that this handler has not written yet.
func TestCreateRelayManifestUsesNativeStarter(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENT_PRODUCTS", "agentworks,relays")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"admin"}]}`)
	files := &mockWorkspaceAPI{files: map[string]string{}}
	host := httptest.NewServer(files)
	t.Cleanup(host.Close)
	t.Setenv("WORKSPACE_API_URL", host.URL)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"label":"Native Relay","kind":"relay","workspace_path":"Workflow/native-relay"}`))
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"}))
	out := httptest.NewRecorder()
	(&StreamingAPI{}).handleCreateWorkflowManifest(out, req)
	if out.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", out.Code, out.Body.String())
	}
	files.mu.Lock()
	defer files.mu.Unlock()
	var manifest WorkflowManifest
	if err := json.Unmarshal([]byte(files.files[manifestPath("Workflow/native-relay")]), &manifest); err != nil || !isDBOSRelay(&manifest) {
		t.Fatalf("manifest: %+v %v", manifest, err)
	}
	if source := files.files["Workflow/native-relay/relay.py"]; source != defaultNativeDBOSRelaySource {
		t.Fatalf("wrong starter: %s", source)
	}
}
