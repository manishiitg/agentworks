package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// A refused call names the scope that would allow it.
func TestRefusedCallNamesTheMissingScope(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true}]}`)
	api := &StreamingAPI{}
	claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	raw, _ := json.Marshal(map[string]any{"name": "create_crew", "arguments": map[string]any{}})
	w := httptest.NewRecorder()
	api.handleExternalCall(w, adminRequest("POST", "/api/external/v1/call", string(raw), claims, nil))
	if w.Code != 403 || !strings.Contains(w.Body.String(), "crews:write") {
		t.Fatalf("refusal should name crews:write: %d %s", w.Code, w.Body)
	}
}
