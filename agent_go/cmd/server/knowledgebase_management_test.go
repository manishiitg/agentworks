package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func TestKnowledgebaseManagementFormsKeepInteractiveAndRoleBoundaries(t *testing.T) {
	tokenTestSetup(t)
	api, service := knowledgebaseServerTest(t)
	request := func(claims *UserClaims, body string) *httptest.ResponseRecorder {
		ctx := context.WithValue(t.Context(), UserContextKey, claims)
		w := httptest.NewRecorder()
		api.handleKnowledgebaseManagement(w, httptest.NewRequest(http.MethodPost, "/api/knowledgebase/manage", strings.NewReader(body)).WithContext(ctx))
		return w
	}
	backup := `{"action":"configure_backup","remote_url":"https://github.com/org/brain.git","username":"git","request_id":"form-backup"}`
	for _, claims := range []*UserClaims{nil, {UserID: "priya"}, {UserID: "admin", AccessToken: &accesstokens.Token{ID: "external"}}} {
		if w := request(claims, backup); w.Code != 403 {
			t.Fatalf("unauthorized prepare admitted: %d %s", w.Code, w.Body)
		}
	}
	admin := &UserClaims{UserID: "admin", Username: "admin"}
	if w := request(admin, `{"action":"configure_backup","pat":"must-not-echo"}`); w.Code != 400 || strings.Contains(w.Body.String(), "must-not-echo") {
		t.Fatalf("credential input admitted or echoed: %d %s", w.Code, w.Body)
	}
	if w := request(admin, `{"action":"set_project_access"}`); w.Code != 400 {
		t.Fatal("unrelated management action admitted")
	}
	if w := request(admin, `{"action":"configure_backup","remote_url":"https://git:private-key@github.com/org/brain.git","username":"git","request_id":"unsafe"}`); w.Code != 400 || strings.Contains(w.Body.String(), "private-key") {
		t.Fatal("credential-bearing URL admitted or echoed")
	}
	// A prepared change must not apply before explicit confirmation.
	w := request(admin, backup)
	if w.Code != 200 {
		t.Fatalf("prepare: %d %s", w.Code, w.Body)
	}
	configured, err := service.BackupConfigured()
	if err != nil || configured {
		t.Fatal("prepare applied backup", err)
	}
	var proposal struct {
		ID string `json:"proposal_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &proposal); err != nil || len(proposal.ID) != 64 {
		t.Fatal("missing approval", w.Body)
	}
	ctx := context.WithValue(t.Context(), UserContextKey, admin)
	approved := httptest.NewRecorder()
	api.handleKnowledgebaseAccessProposals(approved, httptest.NewRequest(http.MethodPost, "/api/knowledgebase/access-proposals", strings.NewReader(`{"id":"`+proposal.ID+`","approve":true}`)).WithContext(ctx))
	if approved.Code != 200 {
		t.Fatalf("confirm: %d %s", approved.Code, approved.Body)
	}
	configured, err = service.BackupConfigured()
	if err != nil || !configured {
		t.Fatal("approved backup missing", err)
	}
	// A reader cannot prepare a grant even with a well-formed ACL version.
	if w := request(&UserClaims{UserID: "priya"}, `{"action":"grant","folder_path":"Payments/Checkout","identity_id":"outsider","role":"Reader","expected_acl_version":"stale","request_id":"reader-grant"}`); w.Code != 403 {
		t.Fatalf("reader grant admitted: %d %s", w.Code, w.Body)
	}
}
