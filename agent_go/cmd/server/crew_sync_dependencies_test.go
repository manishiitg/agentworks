package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856-480: use the existing paired-Crew HTTP fixture. Only the external
// Vault metadata boundary is replaced; owner checks, encrypted value storage,
// canonical settings, selected-secret loading and the new HTTP route are real.
func checkCrewDependencySecretBoundary(t *testing.T, api *StreamingAPI, ctx context.Context, owner *UserClaims, local, remote crewroot.Roots) {
	t.Helper()
	previousStore, previousGlobals := api.chatStore, managedGlobals
	store, e := chathistory.NewFilesystemStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	api.chatStore = store
	managedGlobals = map[string]string{}
	t.Cleanup(func() {
		api.chatStore = previousStore
		managedGlobals = previousGlobals
		crewDependencyChecks.Delete(local.ID)
	})
	var mu sync.Mutex
	roots := map[string]string{}
	names := map[string][]string{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/api/project-vaults":
			var in struct {
				Root string `json:"workspace_root"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			id := ""
			for key, value := range roots {
				if value == in.Root {
					id = key
				}
			}
			if id == "" {
				id = fmt.Sprintf("v-%06d", len(roots)+1)
				roots[id] = in.Root
				names[id] = []string{}
			}
			json.NewEncoder(w).Encode(map[string]string{"id": id})
		case r.URL.Path == "/api/vaults":
			groups := []any{}
			for id, root := range roots {
				groups = append(groups, map[string]any{"group": map[string]any{"ID": id, "Name": "Project", "ProjectRoot": root, "Owners": []string{owner.UserID}}, "members": []string{owner.UserID}, "connections": []any{}, "secret_names": names[id]})
			}
			json.NewEncoder(w).Encode(map[string]any{"vaults": groups})
		case r.URL.Path == "/api/admin/runtime/servers":
			w.Write([]byte(`{"servers":[],"groups":[],"secrets":[]}`))
		case strings.HasPrefix(r.URL.Path, "/api/vaults/"):
			pieces := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/vaults/"), "/")
			id := pieces[0]
			if len(pieces) == 2 && pieces[1] == "secrets" && r.Method == "POST" {
				var in struct{ Name string }
				json.NewDecoder(r.Body).Decode(&in)
				if !slices.Contains(names[id], in.Name) {
					names[id] = append(names[id], in.Name)
				}
				w.Write([]byte(`{}`))
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"role": "owner", "secret_names": names[id]})
		default:
			w.WriteHeader(404)
		}
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "1")
	const value = "selected-crew-value-480"
	const ignored = "unselected-crew-value-480"
	for name, v := range map[string]string{"TRANSFER_ME": value, "IGNORE_ME": ignored} {
		if e := api.upsertSharedWorkflowSecret(ctx, local.Virtual, name, v); e != nil {
			t.Fatal(e)
		}
	}
	setSelection := func(selected []string) {
		t.Helper()
		raw, found, e := readProjectRuntimeManifest(ctx, "work", local.Virtual)
		if e != nil || !found {
			t.Fatal("source settings unavailable")
		}
		var doc map[string]any
		json.Unmarshal([]byte(raw), &doc)
		caps, _ := doc["capabilities"].(map[string]any)
		if caps == nil {
			caps = map[string]any{}
			doc["capabilities"] = caps
		}
		caps["selected_secrets"] = selected
		caps["selected_servers"] = []string{}
		caps["chosen_vault_apps_only"] = true
		body, _ := json.Marshal(doc)
		if e := writeFileToWorkspace(ctx, local.Virtual+"/workflow.json", string(body)); e != nil {
			t.Fatal(e)
		}
	}
	setSelection([]string{"TRANSFER_ME"})
	binding, e := crewroot.ReadSync(local.ID)
	if e != nil || binding == nil {
		t.Fatal("missing pairing")
	}
	binding.Editing = "local"
	var sent []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer paired-owner" {
			w.WriteHeader(401)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var input crewDependencyRequest
		if json.Unmarshal(raw, &input) != nil {
			t.Error("invalid dependency request")
			return
		}
		sent = sent[:0]
		for name := range input.Values {
			sent = append(sent, name)
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		r = r.WithContext(ctx)
		r = mux.SetURLVars(r, map[string]string{"project": remote.ID})
		api.apiRequestLogMiddleware(http.HandlerFunc(api.handleCrewDependencies)).ServeHTTP(w, r)
	}))
	defer server.Close()
	connection := desktopConnection{Server: server.URL, Token: "paired-owner"}
	var captured bytes.Buffer
	previousLog := log.Writer()
	log.SetOutput(&captured)
	defer log.SetOutput(previousLog)
	request := httptest.NewRequest("POST", "/", nil).WithContext(ctx)
	report, e := api.prepareCrewDependencies(request, local, binding, connection, true)
	if e != nil {
		t.Fatal(e)
	}
	if len(sent) != 1 || sent[0] != "TRANSFER_ME" {
		t.Fatal("unselected secret entered the transfer")
	}
	if report == nil || !report.Ready {
		t.Fatalf("destination not ready: %+v", report)
	}
	loaded := api.loadSelectedSecrets(ctx, owner.UserID, remote.Virtual, []string{"TRANSFER_ME"})
	if len(loaded) != 1 || loaded[0].Value != value {
		t.Fatal("destination runtime cannot use transferred secret")
	}
	rows, e := store.ListUserSecrets(ctx, managedGlobalSecretsUserID)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, row := range rows {
		if strings.Contains(row.EncryptedValue, value) {
			t.Fatal("plaintext persisted")
		}
		if row.Name == "TRANSFER_ME" {
			found = true
		}
	}
	if found {
		t.Fatal("project secret promoted into an unscoped key")
	}
	encoded, _ := json.Marshal(report)
	cached, _ := os.ReadFile(mustDependencyPath(t, local.ID))
	if strings.Contains(string(encoded)+string(cached)+captured.String(), value) || strings.Contains(string(encoded), ignored) {
		t.Fatal("secret value escaped to response, metadata or logs")
	}
	// PLAT-856-480: a deleted/revoked auto-added connection must not wedge
	// cleanup and therefore all later file syncs.
	previous := readCrewDependencies(remote.ID)
	previous.configured = append(previous.configured, crewConfiguredDependency{"mcp", "deleted-server-connection", "removed-source-connection"})
	if e := saveCrewDependencies(remote.ID, previous); e != nil {
		t.Fatal(e)
	}
	if _, e := api.prepareCrewDependencies(request, local, binding, connection, true); e != nil {
		t.Fatal("deleted destination connection blocked sync", e)
	}
	for _, item := range readCrewDependencies(remote.ID).configured {
		if item.ID == "deleted-server-connection" {
			t.Fatal("stale mapping retained")
		}
	}
	// Retrying after a local rotation must keep the destination's existing value.
	if e := api.upsertSharedWorkflowSecret(ctx, local.Virtual, "TRANSFER_ME", "new-source-value-480"); e != nil {
		t.Fatal(e)
	}
	report, e = api.prepareCrewDependencies(request, local, binding, connection, true)
	if e != nil || len(report.Items) == 0 || report.Items[0].State != "kept" {
		t.Fatal("existing destination value not preserved")
	}
	loaded = api.loadSelectedSecrets(ctx, owner.UserID, remote.Virtual, []string{"TRANSFER_ME"})
	if len(loaded) != 1 || loaded[0].Value != value {
		t.Fatal("retry overwrote destination value")
	}
	// Other owners and wrong pair IDs must be refused before imports.
	for _, who := range []string{"other", owner.UserID} {
		input := crewDependencyRequest{PairID: "wrong", Names: []string{"DENIED"}, Values: map[string]string{"DENIED": "must-not-store"}}
		if who == "other" {
			input.PairID = binding.PairID
		}
		raw, _ := json.Marshal(input)
		req := httptest.NewRequest("POST", "/", bytes.NewReader(raw)).WithContext(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: who}))
		req = mux.SetURLVars(req, map[string]string{"project": remote.ID})
		w := httptest.NewRecorder()
		api.handleCrewDependencies(w, req)
		if w.Code < 400 {
			t.Fatal("unadmitted dependency import")
		}
	}
	setSelection([]string{})
	if _, e = api.prepareCrewDependencies(request, local, binding, connection, true); e != nil {
		t.Fatal(e)
	}
	settings := httptest.NewRecorder()
	api.externalCrewSettingsCall(settings, request, "get_settings", map[string]any{"crew_id": remote.ID})
	if settings.Code != 200 {
		t.Fatal("cannot check destination selection")
	}
	var view struct {
		Secrets struct {
			Stored []externalSettingsSecret `json:"stored"`
		} `json:"secrets"`
	}
	json.Unmarshal(settings.Body.Bytes(), &view)
	for _, row := range view.Secrets.Stored {
		if row.Name == "TRANSFER_ME" && row.Selected {
			t.Fatalf("source unselection left the copied value enabled; report=%+v mapped=%+v", readCrewDependencies(remote.ID), readCrewDependencies(remote.ID).configured)
		}
	}
	// An existing but unselected destination value must become usable again,
	// without replacing it with the changed source value.
	setSelection([]string{"TRANSFER_ME"})
	if _, e = api.prepareCrewDependencies(request, local, binding, connection, true); e != nil {
		t.Fatal(e)
	}
	settings = httptest.NewRecorder()
	api.externalCrewSettingsCall(settings, request, "get_settings", map[string]any{"crew_id": remote.ID})
	json.Unmarshal(settings.Body.Bytes(), &view)
	selected := false
	for _, row := range view.Secrets.Stored {
		if row.Name == "TRANSFER_ME" && row.Selected {
			selected = true
		}
	}
	if !selected {
		t.Fatal("existing destination value was not selected on re-enable")
	}

}
func mustDependencyPath(t *testing.T, id string) string {
	t.Helper()
	p, e := crewDependencyPath(id)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

// The same provider is not an identity: never auto-attach its alternative
// account. A previously selected server account remains the owner's choice.
func TestCrewDependencyConnectionMatchNeedsExactIdentity(t *testing.T) {
	// PR621's real finding: an unavailable Vault list does not prevent
	// cleanup of independently listed personal Google accounts.
	partial := projectVaultInventory{Notices: []string{"Vaults could not be loaded. Ask in chat to check the connection."}}
	if crewDependencyInventoryUnavailable(partial, "google") || !crewDependencyInventoryUnavailable(partial, "mcp") {
		t.Fatal("unrelated inventory failure blocks personal account cleanup")
	}

	inventory := projectVaultInventory{Vaults: []projectVaultGroup{{Items: []projectVaultItem{{ID: "server", Kind: "mcp", Name: "Other account", Provider: "example", CanSelect: true}, {ID: "private", Kind: "mcp", Name: "Local account", Provider: "example"}}}}}
	want := crewDependencyConnection{Kind: "mcp", Name: "Local account", Provider: "example"}
	if item, exact := matchCrewConnection(want, inventory); item == nil || exact {
		t.Fatal("provider-only match treated as exact")
	}
	inventory.Vaults[0].Items[0].Name = want.Name
	if item, exact := matchCrewConnection(want, inventory); item == nil || !exact {
		t.Fatal("explicit admitted account not matched")
	}
	want.AccountEmail = "owner@example.com"
	inventory.Vaults[0].Items[0].AccountEmail = "other@example.com"
	if item, _ := matchCrewConnection(want, inventory); item != nil {
		t.Fatal("another account email matched")
	}
}
