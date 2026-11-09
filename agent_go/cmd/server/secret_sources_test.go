package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

// Inspecting saved choices never reveals a value or rewrites the selection; it says which selected Vault secrets the
// project's owner can still lend (PLAT-856).
func TestSecretSourcesMCPAndWebsiteExplainLentVaultSecrets(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.api.chatStore = store
	vault := newOwnerVaultFake(t)
	mine := vault.seed(t, f.api, "owner", "", "MINE", "vault-value-canary")
	const workspace = "Workflow/invoices"
	registerWorkflowOwner(workspace, "owner")
	manifest := `{"id":"invoices","created_by":"owner","access":{"owners":["owner"],"readers":["reader"]},"capabilities":{"selected_global_secret_names":["` + mine + `","VLT_B00009__GONE"]}}`
	f.write(t, workspace+"/workflow.json", manifest)
	check := func(w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != 200 || strings.Contains(w.Body.String(), "vault-value-canary") {
			t.Fatalf("source inspection: %d %s", w.Code, w.Body)
		}
		var snapshot secretSourceView
		if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		rows := map[string]secretSourceRow{}
		for _, row := range snapshot.Secrets {
			rows[row.DisplayName] = row
		}
		if snapshot.Status != "available" || rows["MINE"].ConfiguredSource != "vault" || rows["GONE"].ConfiguredSource != "unavailable" {
			t.Fatalf("lent secrets not explained: %s", w.Body)
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
	// A failed permission read is explicit, never an empty success.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer down.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", down.URL)
	partial := f.call(t, "reader", "settings", map[string]any{"action": "secret_sources", "workflow_id": "invoices"})
	if partial.Code != 200 || !strings.Contains(partial.Body.String(), `"status":"partial"`) || !strings.Contains(partial.Body.String(), `"vault_available":null`) {
		t.Fatalf("Vault failure hidden: %d %s", partial.Code, partial.Body)
	}
	f.write(t, workspace+"/workflow.json", `{"id":"invoices","label":"Invoice processing","access":{"owners":["owner"]},"capabilities":{"selected_global_secret_names":42}}`)
	if invalid := f.api.secretSources(t.Context(), "owner", workspace); invalid.Status != "unavailable" || len(invalid.Secrets) != 0 {
		t.Fatalf("malformed saved selection: %+v", invalid)
	}
	if missing := f.api.secretSources(t.Context(), "owner", "Workflow/missing-settings"); missing.Status != "unavailable" || len(missing.Secrets) != 0 {
		t.Fatalf("missing settings: %+v", missing)
	}
}

func TestSecretSourcesCrewShareAndPrivateAccess(t *testing.T) {
	t.Run("Crew", func(t *testing.T) {
		f, _ := newExternalCrewCommandFixture(t)
		store, err := chathistory.NewFilesystemStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		f.api.chatStore = store
		f.write(t, externalCommandCrewState+"/workflow.json", `{"id":"team","label":"Test Team","created_by":"owner","capabilities":{"selected_global_secret_names":["VLT_A00001__TEAM_KEY"]}}`)
		before := f.read(t, externalCommandCrewState+"/workflow.json")
		w := f.call(t, "owner", "settings", map[string]any{"action": "secret_sources", "crew_id": "team"})
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"available"`) || f.read(t, externalCommandCrewState+"/workflow.json") != before {
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
	// Code routing retired in8106ccb9d. Protect the same own/private rule on
	// the current Crew entry point instead of restoring a code_id alias.
	t.Run("PrivateCrew", func(t *testing.T) {
		f, _ := newExternalCrewCommandFixture(t)
		store, err := chathistory.NewFilesystemStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		f.api.chatStore = store
		if err := defaultProjectOwners().SetPrivate("work", "test-team", true); err != nil {
			t.Fatal(err)
		}
		args := map[string]any{"action": "secret_sources", "crew_id": "team"}
		if w := f.call(t, "owner", "settings", args); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"available"`) {
			t.Fatalf("own private Crew source view: %d %s", w.Code, w.Body)
		}
		if w := f.call(t, "other", "settings", args); w.Code != 404 {
			t.Fatalf("another person's private Crew source view: %d %s", w.Code, w.Body)
		}
		w := codeReviewCall(f.api, codeReviewToken("owner", "workflows:read"), "settings", args)
		if w.Code != 403 {
			t.Fatalf("restricted machine connection: %d %s", w.Code, w.Body)
		}
	})
}
