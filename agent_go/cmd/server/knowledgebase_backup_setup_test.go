package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestKnowledgebaseBackupButtonFlowUsesFrozenAdminSetup(t *testing.T) {
	api, _ := knowledgebaseServerTest(t)
	claims := &UserClaims{UserID: "admin", Username: "admin"}
	ctx := context.WithValue(t.Context(), UserContextKey, claims)
	bootstrap := func() bool {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/knowledgebase/bootstrap", nil).WithContext(ctx)
		w := httptest.NewRecorder()
		api.handleKnowledgebaseViewer(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		var response struct {
			Configured bool `json:"backup_configured"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Configured
	}
	if bootstrap() {
		t.Fatal("backup configured before setup")
	}
	args := map[string]any{"action": "configure_backup", "remote_url": "git@github.com:org/knowledge.git", "branch": "main", "request_id": "setup"}
	result, err := knowledgebaseAccessExecutor(ctx, agentprofiles.ToolRuntimeContext{Product: "knowledgebase", UserID: "admin"}, args)
	if err != nil {
		t.Fatal(err)
	}
	var proposal struct {
		ID string `json:"proposal_id"`
	}
	if json.Unmarshal([]byte(result), &proposal) != nil || proposal.ID == "" {
		t.Fatal(result)
	}
	if bootstrap() {
		t.Fatal("proposal applied before confirmation")
	}
	r := httptest.NewRequest(http.MethodPost, "/api/knowledgebase/access-proposals", strings.NewReader(`{"id":"`+proposal.ID+`","approve":true}`)).WithContext(ctx)
	w := httptest.NewRecorder()
	api.handleKnowledgebaseAccessProposals(w, r)
	if w.Code != 200 || !bootstrap() {
		t.Fatal("setup did not update bootstrap", w.Code, w.Body)
	}
}
