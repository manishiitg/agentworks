package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

// PLAT-825-82: hiding the picker did not stop a typed tag reaching Builder.
// Drive real query admission and stop allowed requests before model assembly.
func TestWorkflowPulseOffTagRefusedBeforeBuilderDelivery(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	for _, tc := range []struct {
		name, user, message string
		session             string
		enabled             bool
		want                int
	}{
		{"off", "alice", "#pulse Are you available?", "", false, http.StatusConflict},
		{"whitespace and case", "alice", " \n#Pulse\tAre you available?", "", false, http.StatusConflict},
		{"colon", "alice", "#pulse: Are you available?", "", false, http.StatusConflict},
		{"comma", "alice", "#pulse, Are you available?", "", false, http.StatusConflict},
		{"period", "alice", "#pulse. Are you available?", "", false, http.StatusConflict},
		{"on", "alice", "#pulse Are you available?", "", true, http.StatusNoContent},
		{"ordinary Builder request", "alice", "Improve the plan", "", false, http.StatusNoContent},
		{"discussing the tag", "alice", "What does #pulse mean?", "", false, http.StatusNoContent},
		{"different tag", "alice", "#pulseboard report", "", false, http.StatusNoContent},
		{"no workflow access", "carol", "#pulse Are you available?", "", false, http.StatusForbidden},
		{"direct Pulse off", "alice", "How are we doing?", "schedule-goallead--example-g1", false, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enabled := "false"
			if tc.enabled {
				enabled = "true"
			}
			env.mock.mu.Lock()
			env.mock.files["Workflow/w/workflow.json"] = `{"id":"wf-w","label":"Example","created_by":"alice","access":{"owners":["alice"]},"pulse":{"enabled":` + enabled + `,"outward_actions":"ask"}}`
			env.mock.mu.Unlock()
			reachedBuilder := false
			env.api.internalAgentToolsModeDecided = func(_, _ string) bool { reachedBuilder = true; return true }
			req := QueryRequest{Query: tc.message, AgentMode: "workflow_phase", PhaseID: "workflow-builder", SelectedFolder: "Workflow/w",
				LLMConfig: &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "claude-code", ModelID: "sonnet"}}}
			r := sharedSecretsRequest(http.MethodPost, "/api/query", tc.user, req)
			session := tc.session
			if session == "" {
				session = "pulse-tag-" + strings.ReplaceAll(tc.name, " ", "-")
			}
			r.Header.Set("X-Session-ID", session)
			w := httptest.NewRecorder()
			env.api.handleQuery(w, r)
			if w.Code != tc.want || reachedBuilder != (tc.want == http.StatusNoContent) {
				t.Fatalf("status=%d reachedBuilder=%t body=%s", w.Code, reachedBuilder, w.Body)
			}
			if tc.want == http.StatusConflict && !strings.Contains(w.Body.String(), errPulseOff.Error()) {
				t.Fatalf("missing plain next step: %s", w.Body)
			}
		})
	}
}
