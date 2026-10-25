package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

const codeReviewDirectory = `{"users":[
	{"id":"owner","username":"owner","can_create":true},
	{"id":"other","username":"other","can_create":true},
	{"id":"boss","username":"boss","admin":true,"can_create":true},
	{"id":"rev","username":"rev","role":"viewer","crew_reviewer":true}]}`

func codeReviewCall(api *StreamingAPI, claims *UserClaims, name string, args map[string]any) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	w := httptest.NewRecorder()
	api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), claims, nil))
	return w
}

func codeReviewToken(user string, scopes ...string) *UserClaims {
	return &UserClaims{UserID: user, Username: user, AccessToken: &accesstokens.Token{ID: "tok-" + user, Scopes: scopes}}
}

// Every Code review tool only reads.
func TestCodeReviewToolsAreReadOnly(t *testing.T) {
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, tool := range catalog {
		if !isExternalCodeReviewTool(tool.Name) {
			continue
		}
		found++
		if tool.mutates || tool.executes || tool.plan {
			t.Fatalf("%s is not read-only", tool.Name)
		}
	}
	if found != len(externalCodeReviewTools) {
		t.Fatalf("catalog has %d of %d Code review tools", found, len(externalCodeReviewTools))
	}
}

// Only an admin sets the reviewer flag: a reviewer cannot grant it, not even
// to themself.
func TestOnlyAnAdminMakesCrewReviewers(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, codeReviewDirectory)
	api := &StreamingAPI{}
	for _, caller := range []string{"rev", "other"} {
		for _, target := range []string{"rev", "other"} {
			w := httptest.NewRecorder()
			requireAdmin(api.handleAdminUpdateUser)(w, adminRequest(http.MethodPut, "/api/admin/users/"+target, `{"crew_reviewer":true}`, &UserClaims{UserID: caller, Username: caller}, map[string]string{"id": target}))
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s set crew_reviewer on %s: %d", caller, target, w.Code)
			}
		}
	}
	w := httptest.NewRecorder()
	requireAdmin(api.handleAdminUpdateUser)(w, adminRequest(http.MethodPut, "/api/admin/users/other", `{"crew_reviewer":true}`, &UserClaims{UserID: "boss", Username: "boss"}, map[string]string{"id": "other"}))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"crew_reviewer":true`) {
		t.Fatalf("admin could not make a reviewer: %d %s", w.Code, w.Body.String())
	}
}
