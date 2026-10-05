package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	args := map[string]any{"action": "configure_backup", "username": "git", "remote_url": "git@github.com:org/knowledge.git", "branch": "main", "request_id": "setup"}
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

func TestKnowledgebaseBackupPATNeverAppearsInProposalResponses(t *testing.T) {
	for _, source := range []string{"tool", "secure-field"} {
		t.Run(source, func(t *testing.T) {
			tokenTestSetup(t)
			api, _ := knowledgebaseServerTest(t)
			ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "admin", Username: "admin"})
			pat := "setup-test-only-secret"
			args := map[string]any{"action": "configure_backup", "username": "kb-user", "remote_url": "https://github.com/org/knowledge.git", "request_id": "pat-setup"}
			if source == "tool" {
				args["pat"] = pat
			}
			result, err := knowledgebaseAccessExecutor(ctx, agentprofiles.ToolRuntimeContext{Product: "knowledgebase", UserID: "admin"}, args)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(result, pat) || strings.Contains(result, "encrypted_pat") {
				t.Fatal("proposal response leaked PAT")
			}
			var proposal struct {
				ID string `json:"proposal_id"`
			}
			if err := json.Unmarshal([]byte(result), &proposal); err != nil {
				t.Fatal(err)
			}
			root := knowledgebaseIntegrationTestRoot(t)
			raw, err := os.ReadFile(filepath.Join(root, "approval_"+proposal.ID+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), pat) {
				t.Fatal("proposal stored plaintext PAT")
			}
			w := httptest.NewRecorder()
			api.handleKnowledgebaseAccessProposals(w, httptest.NewRequest(http.MethodGet, "/api/knowledgebase/access-proposals", nil).WithContext(ctx))
			if w.Code != 200 || strings.Contains(w.Body.String(), pat) || strings.Contains(w.Body.String(), "encrypted_pat") {
				t.Fatal("proposal list leaked PAT", w.Code)
			}
			body := map[string]any{"id": proposal.ID, "approve": true}
			if source == "secure-field" {
				body["pat"] = pat
			}
			encoded, _ := json.Marshal(body)
			w = httptest.NewRecorder()
			api.handleKnowledgebaseAccessProposals(w, httptest.NewRequest(http.MethodPost, "/api/knowledgebase/access-proposals", strings.NewReader(string(encoded))).WithContext(ctx))
			if w.Code != 200 || strings.Contains(w.Body.String(), pat) || !strings.Contains(w.Body.String(), `"pat_configured":true`) {
				t.Fatal("PAT setup failed or leaked", w.Code, w.Body)
			}
		})
	}
}
