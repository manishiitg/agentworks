package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

func TestKnowledgebaseExternalAccessAppliesWithOwnerChecks(t *testing.T) {
	tokenTestSetup(t)
	api, service := knowledgebaseServerTest(t)
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record, _, err := store.Issue(t.Context(), accesstokens.Token{Name: "Owner client", UserID: "priya", Username: "priya", Scopes: []string{"knowledgebase:read", "knowledgebase:write"}, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claims := &UserClaims{UserID: "priya", Username: "priya", AccessToken: &record}
	admin := knowledgebase.Principal{IdentityID: "admin", IsAdmin: true}
	acl := func(folder string) string {
		t.Helper()
		result, err := service.Call(t.Context(), admin, "get_knowledgebase_access", map[string]any{"folder_path": folder})
		if err != nil {
			t.Fatal(err)
		}
		return knowledgeMap(result)["acl_version"].(string)
	}
	grantPriya := func(role string) {
		t.Helper()
		_, err := service.Call(t.Context(), admin, "manage_knowledgebase_access", map[string]any{"action": "grant", "folder_path": "Payments/Checkout", "identity_id": "priya", "role": role, "expected_acl_version": acl("Payments/Checkout"), "request_id": "priya-" + role})
		if err != nil {
			t.Fatal(err)
		}
	}
	request := func(c *UserClaims, args map[string]any) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, c))
		w := httptest.NewRecorder()
		api.externalKnowledgebaseCall(w, r, "manage_knowledgebase_access", args)
		return w
	}
	args := func(id, folder string) map[string]any {
		return map[string]any{"action": "grant", "folder_path": folder, "identity_id": "outsider", "role": "Reader", "expected_acl_version": acl(folder), "request_id": id}
	}
	for _, role := range []string{"Reader", "Editor"} {
		if role == "Editor" {
			grantPriya(role)
		}
		if w := request(claims, args("denied-"+role, "Payments/Checkout")); w.Code == 200 {
			t.Fatalf("%s changed access", role)
		}
	}
	grantPriya("Owner")
	input := args("direct-owner", "Payments/Checkout")
	if w := request(claims, input); w.Code != 200 {
		t.Fatalf("owner grant: %d %s", w.Code, w.Body.String())
	}
	if _, err := service.Call(t.Context(), knowledgebase.Principal{IdentityID: "outsider"}, "read_knowledgebase", map[string]any{"path": "Payments/Checkout/retries.md"}); err != nil {
		t.Fatal("direct grant did not take effect", err)
	}
	paths, _ := filepath.Glob(filepath.Join(knowledgebaseIntegrationTestRoot(t), "approval_*.json"))
	if len(paths) > 0 {
		t.Fatal("external access change unexpectedly created app approval")
	}
	stale := map[string]any{}
	for key, value := range input {
		stale[key] = value
	}
	stale["request_id"] = "stale-owner"
	if w := request(claims, stale); w.Code != 409 {
		t.Fatalf("stale ACL accepted: %d %s", w.Code, w.Body.String())
	}
	if w := request(claims, args("outside-owner", "Payments/Billing")); w.Code == 200 {
		t.Fatal("Owner granted outside owned subtree")
	}
	if w := request(claims, map[string]any{"action": "create_service_account", "name": "Agent", "request_id": "non-admin-service"}); w.Code == 200 {
		t.Fatal("non-admin created service account")
	}
	reader := *claims
	readRecord := record
	readRecord.Scopes = []string{"knowledgebase:read"}
	reader.AccessToken = &readRecord
	if w := request(&reader, args("read-token", "Payments/Checkout")); w.Code != 403 {
		t.Fatalf("read-only token bypassed action gate: %d", w.Code)
	}
	capped := *claims
	capRecord := record
	caps := []knowledgebase.Cap{{FolderPath: "Payments/Checkout", Role: "editor"}}
	capRecord.KnowledgebaseFolders = &caps
	capped.AccessToken = &capRecord
	if w := request(&capped, args("capped-token", "Payments/Checkout")); w.Code != 403 {
		t.Fatalf("content caps bypassed action gate: %d", w.Code)
	}
	managed := *claims
	managed.ExecutionPrincipal = &ExecutionPrincipal{Kind: "scheduled"}
	if w := request(&managed, args("managed-token", "Payments/Checkout")); w.Code != 403 {
		t.Fatalf("managed execution changed grants: %d", w.Code)
	}
	if w := request(claims, map[string]any{"action": "revoke", "folder_path": "Payments/Checkout", "identity_id": "outsider", "expected_acl_version": acl("Payments/Checkout"), "request_id": "direct-revoke"}); w.Code != 200 {
		t.Fatalf("owner revoke: %d %s", w.Code, w.Body.String())
	}
	if _, err := service.Call(t.Context(), knowledgebase.Principal{IdentityID: "outsider"}, "read_knowledgebase", map[string]any{"path": "Payments/Checkout/retries.md"}); err == nil {
		t.Fatal("revoked reader still admitted")
	}
	if err := store.Revoke(t.Context(), record.ID, "priya", time.Now()); err != nil {
		t.Fatal(err)
	}
	if w := request(claims, args("revoked-token", "Payments/Checkout")); w.Code == 200 {
		t.Fatal("revoked owner token still admitted")
	}
}

func knowledgebaseIntegrationTestRoot(t *testing.T) string {
	t.Helper()
	root, err := knowledgeIntegrationRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestKnowledgebaseExternalAccessDiscovery(t *testing.T) {
	tokenTestSetup(t)
	for _, tc := range []struct {
		name    string
		scopes  []string
		caps    *[]knowledgebase.Cap
		want    bool
		project bool
	}{
		{"reader", []string{"knowledgebase:read"}, nil, false, false},
		{"writer", []string{"knowledgebase:read", "knowledgebase:write"}, nil, true, false},
		{"builder writer", []string{"knowledgebase:read", "knowledgebase:write", "builder:chat", "workflows:read", "files:read", "runs:execute"}, nil, true, true},
		{"crew writer", []string{"knowledgebase:read", "knowledgebase:write", "crews:read", "crews:write"}, nil, true, true},
		{"crew write without read", []string{"knowledgebase:read", "knowledgebase:write", "crews:write"}, nil, true, false},
		{"capped writer", []string{"knowledgebase:read", "knowledgebase:write"}, &[]knowledgebase.Cap{{FolderPath: "Payments", Role: "editor"}}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := &UserClaims{UserID: GetDefaultUserID(), AccessToken: &accesstokens.Token{Scopes: tc.scopes, KnowledgebaseFolders: tc.caps, AllWorkflows: true, AllCrews: true}}
			tool := knowledgebaseToolForClaims(claims, externalTool{Name: "manage_knowledgebase_access"})
			raw, _ := json.Marshal(tool.InputSchema)
			var schema struct {
				Properties struct {
					Action struct {
						Enum []string `json:"enum"`
					} `json:"action"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(raw, &schema); err != nil {
				t.Fatal(err)
			}
			contains := func(value string) bool {
				for _, action := range schema.Properties.Action.Enum {
					if action == value {
						return true
					}
				}
				return false
			}
			if contains("grant") != tc.want || contains("revoke") != tc.want {
				t.Fatalf("wrong discovery actions: %s", raw)
			}
			for _, action := range []string{"inspect_project", "bind_project", "unbind_project"} {
				if contains(action) != tc.project {
					t.Fatalf("wrong project discovery: %s", raw)
				}
			}
		})
	}
}
