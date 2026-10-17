package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

// Inspecting saved choices must neither reveal a value nor perform the
// migration-on-read that the older secret-list endpoint intentionally does.
func TestSecretSourcesMCPAndWebsitePreserveCopiesAndPermissions(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("CAPLAYER_SERVICE_URL", "")
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.api.chatStore = store
	managedGlobalsMu.Lock()
	oldGlobals, oldManaged := globalSecrets, managedGlobals
	globalSecrets = []globalSecretEntry{{Name: "ENV_KEY", Value: "environment-value-canary"}}
	managedGlobals = map[string]string{"BOTH": "vault-value-canary", "UNSELECTED": "vault-value-canary"}
	managedGlobalsMu.Unlock()
	t.Cleanup(func() {
		managedGlobalsMu.Lock()
		globalSecrets, managedGlobals = oldGlobals, oldManaged
		managedGlobalsMu.Unlock()
	})
	const workspace = "Workflow/invoices"
	const manifest = `{"id":"invoices","created_by":"owner","access":{"owners":["owner"],"readers":["reader"]},"capabilities":{"selected_secrets":["BOTH"],"selected_global_secret_names":["BOTH","UNSELECTED","ENV_KEY"]}}`
	f.write(t, workspace+"/workflow.json", manifest)
	for _, name := range []string{"BOTH", "UNSELECTED"} {
		if err := store.UpsertWorkflowSecret(t.Context(), chathistory.SharedWorkflowSecretsUserID, workspace, name, "ciphertext-canary"); err != nil {
			t.Fatal(err)
		}
	}
	check := func(w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != 200 {
			t.Fatalf("source inspection: %d %s", w.Code, w.Body)
		}
		body := w.Body.String()
		for _, value := range []string{"ciphertext-canary", "vault-value-canary", "environment-value-canary"} {
			if strings.Contains(body, value) {
				t.Fatal("source inspection exposed stored data")
			}
		}
		var snapshot secretSourceView
		if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		rows := map[string]secretSourceRow{}
		for _, row := range snapshot.Secrets {
			rows[row.Name] = row
		}
		if snapshot.Status != "available" || rows["BOTH"].ConfiguredSource != "product" || !strings.Contains(rows["BOTH"].Explanation, "also exists in the company Vault") || rows["UNSELECTED"].ConfiguredSource != "vault" || !strings.Contains(rows["UNSELECTED"].Explanation, "not selected") || !rows["ENV_KEY"].EnvironmentOwned {
			t.Fatalf("source precedence not explained: %s", body)
		}
	}
	check(f.call(t, "reader", "settings", map[string]any{"action": "secret_sources", "workflow_id": "invoices"}))
	w := httptest.NewRecorder()
	f.api.handleSecretSources(w, adminRequest("GET", "/api/secrets/workflow/sources?workspace_path=Workflow/invoices", "", &UserClaims{UserID: "reader"}, nil))
	check(w)
	if got := f.read(t, workspace+"/workflow.json"); got != manifest {
		t.Fatal("inspection rewrote the saved manifest")
	}
	if w := f.call(t, "outsider", "settings", map[string]any{"action": "secret_sources", "workflow_id": "invoices"}); w.Code != 404 {
		t.Fatalf("unrelated product: %d %s", w.Code, w.Body)
	}
	// Empty shared storage plus an older personal record is reported as
	// legacy, not missing; neither the source nor destination is changed.
	if err := store.DeleteWorkflowSecret(t.Context(), chathistory.SharedWorkflowSecretsUserID, workspace, "BOTH"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteWorkflowSecret(t.Context(), chathistory.SharedWorkflowSecretsUserID, workspace, "UNSELECTED"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertWorkflowSecret(t.Context(), "owner", workspace, "BOTH", "legacy-ciphertext-canary"); err != nil {
		t.Fatal(err)
	}
	checkLegacy := f.call(t, "reader", "get_secret_sources", map[string]any{"workflow_id": "invoices"})
	if checkLegacy.Code != 200 || !strings.Contains(checkLegacy.Body.String(), `"configured_source":"legacy"`) || strings.Contains(checkLegacy.Body.String(), "legacy-ciphertext-canary") {
		t.Fatalf("legacy inspection: %d %s", checkLegacy.Code, checkLegacy.Body)
	}
	legacy, _ := store.ListWorkflowSecrets(t.Context(), "owner", workspace)
	shared, _ := store.ListWorkflowSecrets(t.Context(), chathistory.SharedWorkflowSecretsUserID, workspace)
	if len(legacy) != 1 || len(shared) != 0 || f.read(t, workspace+"/workflow.json") != manifest {
		t.Fatal("inspection migrated or removed a legacy record")
	}
	// A failed permission read is explicit, never an empty success.
	vault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer vault.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", vault.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	partial := f.call(t, "reader", "get_secret_sources", map[string]any{"workflow_id": "invoices"})
	if partial.Code != 200 || !strings.Contains(partial.Body.String(), `"status":"partial"`) || !strings.Contains(partial.Body.String(), `"vault_available":null`) {
		t.Fatalf("Vault failure hidden: %d %s", partial.Code, partial.Body)
	}
	f.write(t, workspace+"/workflow.json", `{"id":"invoices","label":"Invoice processing","access":{"owners":["owner"]},"capabilities":{"selected_secrets":42}}`)
	invalid := f.api.secretSources(t.Context(), "owner", workspace)
	if invalid.Status != "unavailable" || len(invalid.Secrets) != 0 {
		t.Fatalf("malformed saved selection: %+v", invalid)
	}
	missing := f.api.secretSources(t.Context(), "owner", "Workflow/missing-settings")
	if missing.Status != "unavailable" || len(missing.Secrets) != 0 {
		t.Fatalf("missing settings: %+v", missing)
	}
	f.write(t, workspace+"/workflow.json", manifest)
	f.api.chatStore = nil
	unavailable := f.call(t, "owner", "get_secret_sources", map[string]any{"workflow_id": "invoices"})
	if unavailable.Code != 200 || !strings.Contains(unavailable.Body.String(), `"status":"unavailable"`) {
		t.Fatalf("store failure hidden: %d %s", unavailable.Code, unavailable.Body)
	}
}

func TestSecretSourcesCrewAndOwnCodeTargets(t *testing.T) {
	t.Run("Crew", func(t *testing.T) {
		f, _ := newExternalCrewCommandFixture(t)
		store, err := chathistory.NewFilesystemStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		f.api.chatStore = store
		if err := store.UpsertWorkflowSecret(t.Context(), chathistory.SharedWorkflowSecretsUserID, externalCommandCrewRoot, "TEAM_KEY", "crew-ciphertext-canary"); err != nil {
			t.Fatal(err)
		}
		before := f.read(t, externalCommandCrewRoot+"/workflow.json")
		w := f.call(t, "owner", "settings", map[string]any{"action": "secret_sources", "crew_id": "team"})
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"available"`) || f.read(t, externalCommandCrewRoot+"/workflow.json") != before {
			t.Fatalf("Crew source view: %d %s", w.Code, w.Body)
		}
		w = f.call(t, "other", "settings", map[string]any{"action": "secret_sources", "crew_id": "team"})
		if w.Code != 200 || !strings.Contains(w.Body.String(), "TEAM_KEY") || strings.Contains(w.Body.String(), "crew-ciphertext-canary") || strings.Contains(w.Body.String(), `"owner"`) {
			t.Fatalf("shared Crew names-only access: %d %s", w.Code, w.Body)
		}
		httpResult := httptest.NewRecorder()
		f.api.handleSecretSources(httpResult, adminRequest("GET", "/api/secrets/workflow/sources?workspace_path=Crew/test-team", "", &UserClaims{UserID: "other"}, nil))
		if httpResult.Code == 200 || strings.Contains(httpResult.Body.String(), "TEAM_KEY") {
			t.Fatalf("shared Crew HTTP source access: %d %s", httpResult.Code, httpResult.Body)
		}
	})
	t.Run("Code", func(t *testing.T) {
		api, _ := newCodeAdminFixture(t, false)
		store, err := chathistory.NewFilesystemStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		api.chatStore = store
		args := map[string]any{"action": "secret_sources", "code_id": "c0de0001-0000"}
		w := codeReviewCall(api, &UserClaims{UserID: "owner"}, "settings", args)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"available"`) {
			t.Fatalf("own Code source view: %d %s", w.Code, w.Body)
		}
		w = codeReviewCall(api, &UserClaims{UserID: "other"}, "settings", args)
		if w.Code != 404 {
			t.Fatalf("other person's Code source view: %d %s", w.Code, w.Body)
		}
		httpResult := httptest.NewRecorder()
		api.handleSecretSources(httpResult, adminRequest("GET", "/api/secrets/workflow/sources?workspace_path="+codePrivacyOwnerRoot, "", &UserClaims{UserID: "other"}, nil))
		if httpResult.Code == 200 {
			t.Fatalf("another person's Code HTTP source access: %d %s", httpResult.Code, httpResult.Body)
		}
		w = codeReviewCall(api, codeReviewToken("owner", "workflows:read"), "settings", args)
		if w.Code != 403 {
			t.Fatalf("restricted machine connection: %d %s", w.Code, w.Body)
		}
	})
}
