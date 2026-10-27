package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Exercise the published action family, never a retired raw tool name.
func accountActionCall(api *StreamingAPI, claims *UserClaims, action string, args map[string]any) *httptest.ResponseRecorder {
	payload := map[string]any{"action": action}
	for key, value := range args {
		payload[key] = value
	}
	return codeReviewCall(api, claims, "account", payload)
}

// The screen and MCP save through one handler, with the same current audience
// on the next run. Neither an admin without scope nor a member can change it.
func TestAccountAvailabilityMCPUsesAdminHandler(t *testing.T) {
	api, mock := newCodeAdminFixture(t, true)
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "codex-cli")
	t.Setenv("AGENTWORKS_PROVIDER_POLICY", "")
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"admin","admin":true,"can_create":true},{"id":"bob","username":"bob","can_create":true},{"id":"alice","username":"alice","can_create":true}]}`)
	admin := codeReviewToken("admin", "users:manage")
	args := map[string]any{"account": "codex-cli", "available_to": map[string]any{"users": []any{"bob"}}}
	for _, token := range []*UserClaims{codeReviewToken("bob", "users:manage"), codeReviewToken("admin", "crews:review")} {
		if w := accountActionCall(api, token, "set_availability", args); w.Code != http.StatusForbidden {
			t.Fatalf("unauthorized account change: %d %s", w.Code, w.Body.String())
		}
	}
	if w := accountActionCall(api, admin, "set_availability", args); w.Code != http.StatusOK {
		t.Fatalf("admin account change: %d %s", w.Code, w.Body.String())
	}
	if w := accountActionCall(api, admin, "availability", map[string]any{"account": "codex-cli"}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"users":["bob"]`) {
		t.Fatalf("read audience: %d %s", w.Code, w.Body.String())
	}
	mock.mu.Lock()
	audit := mock.files[codeAdminAuditPath(time.Now())]
	mock.mu.Unlock()
	if !strings.Contains(audit, `"action":"set_account_availability"`) || !strings.Contains(audit, `"action":"get_account_availability"`) {
		t.Fatalf("account audience reads/writes not audited: %s", audit)
	}
	current, err := effectiveServerAccountAvailability(context.Background(), "codex-cli")
	if err != nil || !current.AvailableTo.admits("bob") || !current.AvailableTo.admits("admin") || current.AvailableTo.admits("alice") {
		t.Fatalf("saved audience not applied: %+v %v", current, err)
	}
	args["available_to"] = map[string]any{"products": []any{"work"}}
	if w := accountActionCall(api, admin, "set_availability", args); w.Code != http.StatusBadRequest {
		t.Fatalf("retired product audience accepted: %d %s", w.Code, w.Body.String())
	}
	t.Setenv("AGENTWORKS_PROVIDER_POLICY", `{"codex-cli":{"available_to":"admins","pinned":true}}`)
	args["available_to"] = "all"
	if w := accountActionCall(api, admin, "set_availability", args); w.Code != http.StatusConflict {
		t.Fatalf("pinned policy changed: %d %s", w.Code, w.Body.String())
	}
}
