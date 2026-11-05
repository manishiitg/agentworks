package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

const sharedSecretsTestWorkflow = "Workflow/renewals"
const sharedSecretsTestProductWorkspace = "_users/a1/Chats/Work/projects/demo"

// sharedSecretsTestAPI builds a StreamingAPI over a filesystem chat store with
// one workflow whose manifest names alice (a1) as owner and carol (c3) as
// reader; dan (d4) is a member with no access to it.
func sharedSecretsTestAPI(t *testing.T) (*StreamingAPI, string) {
	t.Helper()
	t.Setenv("AUTH_SECRET", "plat-271-test-auth-secret-0123456789abcdef")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
	  {"id":"a1","username":"alice","can_create":true,"products":[]},
	  {"id":"c3","username":"carol","can_create":false,"products":[]},
	  {"id":"d4","username":"dan","can_create":true,"products":[]}
	]}`)
	manifest := WorkflowManifest{ID: "wf_shared", Label: "renewals", CreatedBy: "a1", Access: &WorkflowAccess{Owners: []string{"a1"}, Readers: []string{"c3"}}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	ws := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		manifestPath(sharedSecretsTestWorkflow):         string(raw),
		manifestPath(sharedSecretsTestProductWorkspace): string(raw),
	}})
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	root := t.TempDir()
	store, err := chathistory.NewFilesystemStore(root)
	if err != nil {
		t.Fatalf("NewFilesystemStore: %v", err)
	}
	return &StreamingAPI{chatStore: store}, root
}

func sharedSecretsRequest(method, target, userID string, body interface{}) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, target, &buf)
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: userID}))
}

// All in Vault (PLAT-856): the website's Add form saves a project's secret in its owner's own Vault and selects it on
// the project; only the owner may, the value never comes back, and Remove only stops the project using it.
func TestProjectSecretFormSavesInOwnerVaultOwnerOnly(t *testing.T) {
	api, _ := sharedSecretsTestAPI(t)
	vault := newOwnerVaultFake(t)
	ctx := context.Background()
	const value = "test-only-renewals-token"
	store := func(userID, workspace string) *httptest.ResponseRecorder {
		encrypted, err := encryptSecretValueWithAAD(value, []byte(userID))
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		api.handleStoreWorkflowSecret(w, sharedSecretsRequest(http.MethodPut, "/api/secrets/workflow/store", userID, storeSecretRequest{Name: "TOKEN", EncryptedValue: encrypted, WorkspacePath: workspace}))
		return w
	}
	selection := func(workspace string) []string {
		names, err := productSelectedGlobalSecrets(ctx, crewProfileID, workspace)
		if err != nil {
			t.Fatal(err)
		}
		return *names
	}
	full := vault.vaultName(t, "a1", "TOKEN")
	// Someone without access and a reader are refused before anything is saved.
	for _, who := range []string{"d4", "c3"} {
		if w := store(who, sharedSecretsTestWorkflow); w.Code != http.StatusForbidden {
			t.Fatalf("store as %s: status %d, want 403", who, w.Code)
		}
	}
	if vault.has(full) || len(selection(sharedSecretsTestWorkflow)) != 0 {
		t.Fatal("a refused store saved or selected a secret")
	}
	w := store("a1", sharedSecretsTestWorkflow)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), value) {
		t.Fatalf("store as owner: status %d body %s", w.Code, w.Body.String())
	}
	managedGlobalsMu.RLock()
	saved := managedGlobals[full]
	managedGlobalsMu.RUnlock()
	if saved != value || !vault.has(full) || !slices.Equal(selection(sharedSecretsTestWorkflow), []string{full}) {
		t.Fatalf("the value is not in the owner's Vault and selected: selection=%v", selection(sharedSecretsTestWorkflow))
	}
	// The browser's product-relative path is the same runtime workspace the agent reads.
	if w := store("a1", "Chats/Work/projects/demo"); w.Code != http.StatusOK || !slices.Contains(selection(sharedSecretsTestProductWorkspace), full) {
		t.Fatalf("store using browser path: status %d selection %v", w.Code, selection(sharedSecretsTestProductWorkspace))
	}
	remove := func(userID string) int {
		w := httptest.NewRecorder()
		req := sharedSecretsRequest(http.MethodDelete, "/api/secrets/workflow/store/TOKEN?workspace_path="+sharedSecretsTestWorkflow, userID, nil)
		api.handleDeleteWorkflowSecret(w, mux.SetURLVars(req, map[string]string{"name": "TOKEN"}))
		return w.Code
	}
	if code := remove("c3"); code != http.StatusForbidden {
		t.Fatalf("remove as reader: status %d, want 403", code)
	}
	if code := remove("a1"); code != http.StatusOK || len(selection(sharedSecretsTestWorkflow)) != 0 || !vault.has(full) {
		t.Fatalf("remove must only stop the project using it: status %d selection %v", code, selection(sharedSecretsTestWorkflow))
	}
}
