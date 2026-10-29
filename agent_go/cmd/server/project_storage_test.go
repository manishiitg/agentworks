package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Canonical routing and private-state identity are different: preserving an
// encrypted namespace must never reopen the old filesystem route or share it.
func TestCanonicalCrewKeepsPrivateBindingsAndAudience(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	root := "Crew/example-12345678"
	old := workspaceref.PhysicalPathOf("owner", workspaceref.CodeProjectsRoot, "example-12345678")
	rec := projectOwnerRecord{Product: crewProfileID, Folder: "example-12345678", ProjectID: "stable-project-id", OwnerID: "owner", Shared: true, CanonicalRoot: root, BindingRoot: old, Private: true, AudienceConfigured: true}
	if err := defaultProjectOwners().Register(rec); err != nil {
		t.Fatal(err)
	}
	if got := projectBindingRoot(root); got != old {
		t.Fatalf("private namespace changed: %q", got)
	}
	connection := services.GmailConnection{Personal: true, Workspaces: []string{old}}
	if !connection.UsableFrom(services.GmailUseScope{Workspace: root}) || connection.UsableFrom(services.GmailUseScope{Workspace: "Crew/other"}) {
		t.Fatal("saved private connection lost its exact project binding")
	}
	if _, ok := resolveCrewPath(context.Background(), "owner", old); ok {
		t.Fatal("retired filesystem path accepted")
	}
	if _, _, ok := projectProductForPath(old); ok {
		t.Fatal("retired path became a product")
	}
	if crewAdminInspectionAllowed(root) {
		t.Fatal("merge created an admin inspection grant")
	}
	if crewSharedWith(root, "other") {
		t.Fatal("private migrated Crew became shared")
	}
	if !crewProjectOwnedByCallerStrict("owner", root) || crewProjectOwnedByCallerStrict("other", root) {
		t.Fatal("registered owner changed")
	}
}

// The unified public path creates one private Crew, keeps owner discovery, and
// refuses a second person's discovery/call plus retired public tool names.
func TestCrewWorkspaceCanonicalLifecycle(t *testing.T) {
	ensureProjectStateRoot(t)
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"other","username":"other","can_create":true,"products":["work"]}]}`)
	call := func(user, name string, args map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
		w := httptest.NewRecorder()
		env.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/v1/call", string(raw), crewWriter(user), nil))
		return w
	}
	w := call("owner", "workspace", map[string]any{"action": "create", "name": "Private example"})
	var created map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &created) != nil || created["profile_id"] != "work" {
		t.Fatalf("canonical create: %d %s", w.Code, w.Body.String())
	}
	id, _ := created["workspace_id"].(string)
	if id == "" {
		t.Fatal("missing stable workspace identity")
	}
	for _, user := range []string{"owner", "other"} {
		w = call(user, "workspace", map[string]any{"action": "list"})
		if w.Code != 200 {
			t.Fatalf("%s list: %d %s", user, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), id) != (user == "owner") {
			t.Fatalf("private discovery for %s: %s", user, w.Body.String())
		}
	}
	w = call("other", "workspace", map[string]any{"action": "ask", "workspace_id": id, "message": "must not run"})
	if w.Code != 403 && w.Code != 404 {
		t.Fatalf("private call: %d %s", w.Code, w.Body.String())
	}
	for _, name := range []string{"create_crew", "list_crews", "ask_my_code", "code"} {
		w = call("owner", name, map[string]any{})
		if w.Code != 404 {
			t.Fatalf("retired public name %s: %d %s", name, w.Code, w.Body.String())
		}
	}
}
