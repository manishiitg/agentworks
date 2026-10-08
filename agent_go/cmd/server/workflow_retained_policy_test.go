package server

import (
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

func TestWorkflowRetainedPolicyChecksCurrentPermissions(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_edit":true}]}`)
	ws, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	docs.files["Workflow/test/workflow.json"] = `{"id":"wf_test","access":{"owners":["owner"]}}`
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	req := QueryRequest{SelectedFolder: "Workflow/test"}
	api := &StreamingAPI{lastChatPolicyBySession: map[string]string{}}
	keyFor := func(readOnly bool) string {
		return api.chatPolicySessionKey(resolveWorkflowChatPolicy("chat", req, nil, readOnly))
	}
	api.lastChatPolicyBySession["chat"] = keyFor(true)
	if compatible, err := api.workflowRetainedPolicyCompatible(ctx, "chat", req); err != nil || compatible {
		t.Fatal("promotion retained old Run definition", compatible, err)
	}
	if api.lastChatPolicyBySession["chat"] != keyFor(true) {
		t.Fatal("gate overwrote old policy")
	}
	api.lastChatPolicyBySession["chat"] = keyFor(false)
	if compatible, err := api.workflowRetainedPolicyCompatible(ctx, "chat", req); err != nil || !compatible {
		t.Fatal("unchanged Builder lost warm delivery", compatible, err)
	}
	docs.files["Workflow/test/workflow.json"] = `{"id":"wf_test","access":{"owners":["another"],"readers":["owner"]}}`
	if compatible, err := api.workflowRetainedPolicyCompatible(ctx, "chat", req); err != nil || compatible {
		t.Fatal("demotion retained Builder tools", compatible, err)
	}
	docs.files["Workflow/test/workflow.json"] = `{"id":"wf_test","access":{"owners":["another"]}}`
	if _, err := api.workflowRetainedPolicyCompatible(ctx, "chat", req); err == nil {
		t.Fatal("revoked access accepted")
	}
}

func TestWorkflowLiveInputRefreshesInsteadOfDeliveringToOldCLI(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_edit":true}]}`)
	ws, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	docs.files["Workflow/test/workflow.json"] = `{"id":"wf_test","access":{"owners":["owner"]}}`
	query := QueryRequest{SelectedFolder: "Workflow/test"}
	canceled := false
	next := make(chan QueryRequest, 1)
	api := &StreamingAPI{
		internalChatSubmissionStore: newTestChatSubmissionStore(),
		activeSessions:              map[string]*ActiveSessionInfo{"chat": {UserID: "owner"}},
		lastQueryRequests:           map[string]QueryRequest{"chat": query},
		lastChatPolicyBySession:     map[string]string{},
		agentCancelFuncs:            map[string]context.CancelFunc{"chat": func() { canceled = true }},
		internalQueryHandler: func(_ http.ResponseWriter, r *http.Request) {
			var req QueryRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			next <- req
		},
	}
	api.lastChatPolicyBySession["chat"] = api.chatPolicySessionKey(resolveWorkflowChatPolicy("chat", query, nil, true))
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/chat/live-input", strings.NewReader(`{"message":"remove the integration from this workflow"}`)).WithContext(ctx)
	req = mux.SetURLVars(req, map[string]string{"session_id": "chat"})
	response := httptest.NewRecorder()
	api.handleLiveInputMessage(response, req)
	if response.Code != http.StatusOK || !canceled {
		t.Fatalf("refresh not accepted: %d %s canceled=%v", response.Code, response.Body.String(), canceled)
	}
	select {
	case resumed := <-next:
		if !resumed.DisableLiveInputDelivery || resumed.SelectedFolder != query.SelectedFolder || resumed.Query != "remove the integration from this workflow" {
			t.Fatalf("unsafe continuation: %+v", resumed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no rebuilt turn dispatched")
	}
}

func TestWorkflowExplicitNewTurnDoesNotInterruptForegroundForRetainedAdmission(t *testing.T) {
	canceled := false
	api := &StreamingAPI{agentCancelFuncs: map[string]context.CancelFunc{"chat": func() { canceled = true }}}
	for _, req := range []QueryRequest{{SelectedFolder: "Workflow/test", IsAutoNotification: true}, {SelectedFolder: "Workflow/test", DisableLiveInputDelivery: true}} {
		compatible, _, err := api.prepareWorkflowRetainedDelivery(context.Background(), "chat", req, false)
		if err != nil || compatible || canceled {
			t.Fatal("explicit new turn attempted retained admission or canceled foreground", compatible, err, canceled)
		}
	}
}

func TestWorkflowRetainedPolicyBackfillsFolderFromSession(t *testing.T) {
	// Follow-ups often omit SelectedFolder and rely on session resumption.
	// The fingerprint check must still run against the session's last known
	// workspace: a manifest provider change (pi-cli -> muse-cli) must force
	// a fresh turn instead of silently reusing the stale retained runtime.
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_edit":true}]}`)
	ws, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	docs.files["Workflow/test/workflow.json"] = `{"id":"wf_test","access":{"owners":["owner"]},"capabilities":{"llm_config":{"mode":"explicit","builder_llm":{"provider":"muse-cli","model_id":"m","connection_id":""}}}}`
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	prev := QueryRequest{SelectedFolder: "Workflow/test", Provider: "pi-cli", ModelID: "google/gemini-3.8-flash"}
	api := &StreamingAPI{
		activeSessions:          map[string]*ActiveSessionInfo{"chat": {UserID: "owner", WorkspacePath: "Workflow/test"}},
		lastQueryRequests:       map[string]QueryRequest{"chat": prev},
		lastChatPolicyBySession: map[string]string{},
	}
	api.lastChatPolicyBySession["chat"] = api.chatPolicySessionKey(resolveWorkflowChatPolicy("chat", prev, nil, false))
	followup := QueryRequest{Provider: "pi-cli", ModelID: "google/gemini-3.8-flash"}
	if compatible, err := api.workflowRetainedPolicyCompatible(ctx, "chat", followup); err != nil || compatible {
		t.Fatalf("folder-less follow-up skipped the provider fingerprint check: compatible=%v err=%v", compatible, err)
	}
}

func TestWorkflowRetainedProviderAndAccountChangesRequireReconnect(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_edit":true}]}`)
	ws, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	req := QueryRequest{SelectedFolder: "Workflow/test", Provider: "claude-code", ModelID: "claude-sonnet-5-5", ConnectionID: "account-a"}
	api := &StreamingAPI{lastQueryRequests: map[string]QueryRequest{"chat": req}, lastChatPolicyBySession: map[string]string{}}
	api.lastChatPolicyBySession["chat"] = api.chatPolicySessionKey(resolveWorkflowChatPolicy("chat", req, nil, false))
	for _, tc := range []struct {
		name, provider, model, account string
		want                           bool
	}{
		{"unchanged", "claude-code", "claude-sonnet-5-5", "account-a", true},
		{"account switch", "claude-code", "claude-sonnet-5-5", "account-b", false},
		{"deleted private back to server", "claude-code", "claude-sonnet-5-5", "", false},
		{"back to gemini", "pi-cli", "google/gemini-3.8-flash", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs.files["Workflow/test/workflow.json"] = `{"id":"wf_test","access":{"owners":["owner"]},"capabilities":{"llm_config":{"mode":"explicit","builder_llm":{"provider":"` + tc.provider + `","model_id":"` + tc.model + `","connection_id":"` + tc.account + `"}}}}`
			got, err := api.workflowRetainedPolicyCompatible(ctx, "chat", req)
			if err != nil || got != tc.want {
				t.Fatalf("compatible=%v err=%v want=%v", got, err, tc.want)
			}
		})
	}
}

func TestWorkflowBrowserFollowupCannotInterruptActiveExternalBuilder(t *testing.T) {
	canceled := false
	api := &StreamingAPI{agentCancelFuncs: map[string]context.CancelFunc{"chat": func() { canceled = true }}}
	api.externalBuilderRuntime.sessions = map[string]*externalBuilderActive{"chat": {id: "external-operation"}}
	compatible, _, err := api.prepareWorkflowRetainedDelivery(context.Background(), "chat", QueryRequest{SelectedFolder: "Workflow/test"}, true)
	if err != nil || compatible || canceled {
		t.Fatal("browser request could reconfigure MCP operation", compatible, err, canceled)
	}
}

// An effort-only change must not send the next message to the old Medium CLI.
// Drive the actual live-input HTTP handler through its rebuild/dispatch route.
func TestWorkflowEffortChangeRefreshesRetainedLiveInput(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("LLM_CONFIG_LOCKED", "")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_edit":true}]}`)
	ws, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	docs.files["Workflow/test/workflow.json"] = `{"id":"wf_test","access":{"owners":["owner"]},"capabilities":{"llm_config":{"mode":"explicit","builder_llm":{"provider":"claude-code","model_id":"claude-sonnet-5-5","options":{"reasoning_effort":"high"}}}}}`
	query := QueryRequest{SelectedFolder: "Workflow/test", Provider: "claude-code", ModelID: "claude-sonnet-5-5", LLMConfig: &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "claude-code", ModelID: "claude-sonnet-5-5", Options: map[string]interface{}{"reasoning_effort": "medium"}}}}
	canceled := false
	next := make(chan QueryRequest, 1)
	api := &StreamingAPI{
		internalChatSubmissionStore:   newTestChatSubmissionStore(),
		conversationTurnQueueDraining: map[string]bool{"chat": true},
		activeSessions:                map[string]*ActiveSessionInfo{"chat": {UserID: "owner", WorkspacePath: "Workflow/test"}},
		lastQueryRequests:             map[string]QueryRequest{"chat": query},
		lastChatPolicyBySession:       map[string]string{},
		agentCancelFuncs:              map[string]context.CancelFunc{"chat": func() { canceled = true }},
		internalQueryHandler: func(_ http.ResponseWriter, r *http.Request) {
			var q QueryRequest
			_ = json.NewDecoder(r.Body).Decode(&q)
			next <- q
		},
	}
	api.lastChatPolicyBySession["chat"] = api.chatPolicySessionKey(resolveWorkflowChatPolicy("chat", query, nil, false))
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	r := httptest.NewRequest(http.MethodPost, "/api/sessions/chat/live-input", strings.NewReader(`{"message":"continue"}`)).WithContext(ctx)
	r = mux.SetURLVars(r, map[string]string{"session_id": "chat"})
	w := httptest.NewRecorder()
	api.handleLiveInputMessage(w, r)
	if w.Code != http.StatusOK || canceled || !strings.Contains(w.Body.String(), "queued_for_turn") {
		t.Fatalf("effort change must wait for current turn: status=%d canceled=%v body=%s", w.Code, canceled, w.Body.String())
	}
	// Use an idle receiver with no queued dispatch; the first API keeps its
	// deliberately paused durable queue, proving the active turn was untouched.
	api = &StreamingAPI{
		internalChatSubmissionStore: newTestChatSubmissionStore(),
		activeSessions:              api.activeSessions,
		lastQueryRequests:           api.lastQueryRequests,
		lastChatPolicyBySession:     api.lastChatPolicyBySession,
		internalQueryHandler:        api.internalQueryHandler,
	}
	r = httptest.NewRequest(http.MethodPost, "/api/sessions/chat/live-input", strings.NewReader(`{"message":"continue when idle"}`)).WithContext(ctx)
	r = mux.SetURLVars(r, map[string]string{"session_id": "chat"})
	w = httptest.NewRecorder()
	api.handleLiveInputMessage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("idle refresh failed: %d %s", w.Code, w.Body.String())
	}
	select {
	case q := <-next:
		if !q.DisableLiveInputDelivery {
			t.Fatal("continued on stale CLI")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no rebuilt turn dispatched")
	}
	query.LLMConfig.Primary.Options["reasoning_effort"] = "high"
	api.lastQueryRequests["chat"] = query
	if compatible, err := api.workflowRetainedPolicyCompatible(ctx, "chat", query); err != nil || !compatible {
		t.Fatalf("unchanged High effort should retain its CLI: compatible=%v err=%v", compatible, err)
	}
}

// Local 2026-10-08: a turn stored its policy key with the Builder's Pulse part,
// but the follow-up check rebuilt it without, so every follow-up to a Pulse
// workflow's Builder chat counted as a policy change and cancelled the running
// turn. Both must use the same key.
func TestPulseWorkflowBuilderFollowUpKeepsTheRunningTurn(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_edit":true}]}`)
	ws, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	docs.files["Workflow/test/workflow.json"] = `{"id":"wf_test","access":{"owners":["owner"]},"pulse":{"enabled":true,"autonomy":{"level":3}}}`
	docs.files["Workflow/test/soul/soul.md"] = "Grow subscribers to 100 by December."
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	req := QueryRequest{SelectedFolder: "Workflow/test", PhaseID: "workflow-builder"}
	api := &StreamingAPI{lastChatPolicyBySession: map[string]string{}}
	suffix := pulsePolicyKeySuffix("chat", req.PhaseID, req.SelectedFolder)
	if suffix == "" {
		t.Fatal("a Pulse workflow's Builder chat must carry the Pulse part of the key")
	}
	api.lastChatPolicyBySession["chat"] = api.chatPolicySessionKey(resolveWorkflowChatPolicy("chat", req, nil, false)) + suffix
	if compatible, err := api.workflowRetainedPolicyCompatible(ctx, "chat", req); err != nil || !compatible {
		t.Fatal("an unchanged Pulse workflow's follow-up was treated as a policy change", compatible, err)
	}
}
