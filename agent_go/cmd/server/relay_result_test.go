package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

func TestValidateRelayOutputStep(t *testing.T) {
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		"Workflow/relay/planning/plan.json": `{"steps":[{"type":"message_sequence","id":"answer","title":"Answer","description":"Return the answer","authored_prompt":true,"system_prompt":"Return JSON","next_step_id":"end","items":[{"id":"turn","type":"user_message","message":"{{input.question}}"}]}]}`,
	}})
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	if err := validateRelayOutputStep(context.Background(), "Workflow/relay", "answer"); err != nil {
		t.Fatalf("valid output rejected: %v", err)
	}
	for _, id := range []string{"ordinary", "missing"} {
		if err := validateRelayOutputStep(context.Background(), "Workflow/relay", id); err == nil || !strings.Contains(err.Error(), id) {
			t.Fatalf("invalid output %q accepted or unclear error: %v", id, err)
		}
	}
}

func TestApplyRelayResult(t *testing.T) {
	manifest := NewWorkflowManifest("Relay")
	manifest.Kind = "relay"
	manifest.RelayOutputStepID = "answer"
	result := webhookRunResult{Status: "completed", Terminal: true, Steps: []webhookStepOutput{
		{StepID: "prepare", Outputs: map[string]interface{}{"result.json": map[string]interface{}{"ignored": true}}},
		{StepID: "answer", Outputs: map[string]interface{}{"result.json": map[string]interface{}{"ok": true}}},
	}}
	applyRelayResult(manifest, &result, "", schedulerstate.Run{})
	var output map[string]interface{}
	if err := json.Unmarshal(result.Result, &output); err != nil || output["ok"] != true || result.Error != "" {
		t.Fatalf("Relay result = %s, error = %q, decode error = %v", result.Result, result.Error, err)
	}
	manifest.RelayOutputStepID = "missing"
	result.Result = nil
	applyRelayResult(manifest, &result, "", schedulerstate.Run{})
	if result.Status != "failed" || result.Error == "" {
		t.Fatalf("missing Relay result was accepted: %+v", result)
	}
}

func TestOrdinaryWorkflowResultUnchanged(t *testing.T) {
	manifest := NewWorkflowManifest("Ordinary workflow")
	result := webhookRunResult{Status: "completed", Terminal: true, Steps: []webhookStepOutput{{StepID: "answer", Outputs: map[string]interface{}{"result.json": map[string]interface{}{"ok": true}}}}}
	applyRelayResult(manifest, &result, "", schedulerstate.Run{})
	if result.Status != "completed" || result.Error != "" || len(result.Result) != 0 {
		t.Fatalf("ordinary workflow result changed: %+v", result)
	}
}

func TestRelayResultReadsSavedRunOutput(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	workspace := "Workflow/relay"
	runFolder := "iteration-1-hook"
	runRoot := filepath.Join(docs, workspace, "runs", runFolder)
	if err := os.MkdirAll(filepath.Join(runRoot, "default", "execution", "answer"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runRoot, ".webhook-run-id"), []byte("run-1"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 18; i++ {
		name := fmt.Sprintf("a%02d.txt", i)
		if err := os.WriteFile(filepath.Join(runRoot, "default", "execution", "answer", name), []byte(strings.Repeat("x", 116508)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(runRoot, "default", "execution", "answer", "result.json"), []byte(`{"ok":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	finished := time.Now()
	run := schedulerstate.Run{RunID: "run-1", RunFolder: runFolder, State: schedulerstate.StateCompleted, CompletedAt: &finished}
	result, err := readWebhookRunResult(workspace, run)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Steps) == 0 || result.Steps[0].Outputs["result.json"] != nil {
		t.Fatal("test fixture should exceed the generic inline output limit")
	}
	manifest := NewWorkflowManifest("Relay")
	manifest.Kind = "relay"
	manifest.RelayOutputStepID = "answer"
	applyRelayResult(manifest, &result, workspace, run)
	if string(result.Result) != `{"ok":true}` || result.Error != "" {
		t.Fatalf("saved Relay output = %s, error = %q", result.Result, result.Error)
	}
}

func TestRelayManifestKindValidation(t *testing.T) {
	manifest := NewWorkflowManifest("Relay")
	manifest.Kind = "relay"
	manifest.RelayOutputStepID = "answer"
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("relay manifest rejected: %v", err)
	}
	if manifest.PulseEnabled() || manifest.EffectivePulseMode(WorkflowSchedule{}) != schedulePulseModeOff {
		t.Fatal("Relay enabled Pulse")
	}
	manifest.Capabilities.SlackConnectionID = "slack-app"
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("Relay accepted a Slack connection")
	}
	manifest.Capabilities.SlackConnectionID = ""
	manifest.Capabilities.Notifications = &WorkflowNotificationConfig{RunSummaryChannels: []string{"slack"}, SlackWebhookSecretName: "slack-webhook"}
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("Relay accepted Slack notifications")
	}
	manifest.Capabilities.Notifications = &WorkflowNotificationConfig{RunSummaryChannels: []string{"whatsapp"}}
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("Relay accepted WhatsApp notifications")
	}
	manifest.Capabilities.Notifications = &WorkflowNotificationConfig{RunSummaryChannels: []string{"gmail"}}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("Relay rejected Gmail notifications: %v", err)
	}
	for _, timer := range []WorkflowSchedule{
		{ID: "timer", ScheduleType: "cron", CronExpression: "0 * * * *", GroupNames: []string{"prod"}, PulseMode: "off"},
		{ID: "calendar", ScheduleType: "calendar", CalendarItems: []CalendarScheduleItem{{Date: "2030-01-01", Time: "12:00"}}, GroupNames: []string{"prod"}, PulseMode: "off"},
	} {
		manifest.Schedules = []WorkflowSchedule{timer}
		if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "API function triggers only") {
			t.Fatalf("Relay timer accepted: %v", err)
		}
	}
	manifest.Schedules = nil
	function := reviewPRTrigger()
	manifest.Schedules = []WorkflowSchedule{function}
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("Relay accepted multiple variable groups")
	}
	function.GroupNames = []string{"prod"}
	manifest.Schedules = []WorkflowSchedule{function}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("single-group Relay rejected: %v", err)
	}
	manifest.Schedules = nil
	manifest.Kind = "other"
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("unknown workflow kind accepted")
	}
}

func TestRelayFunctionContextSuppressesLegacyChannels(t *testing.T) {
	manifest := NewWorkflowManifest("Relay")
	manifest.Kind = "relay"
	// Existing Relays may still have the previous Slack configuration on disk.
	manifest.Capabilities.SlackConnectionID = "legacy-slack"
	manifest.Capabilities.Notifications = &WorkflowNotificationConfig{
		GmailConnectionID:                   "google-account",
		RunSummaryChannels:                  []string{"gmail", "slack"},
		SlackWebhookSecretName:              "legacy-webhook",
		RunSummarySlackWebhookSecretNames:   []string{"legacy-run-webhook"},
		PulseSummarySlackWebhookSecretNames: []string{"legacy-pulse-webhook"},
	}
	sctx := buildScheduleContext("Workflow/relay", manifest, WorkflowSchedule{GroupNames: []string{"prod"}, TriggerPayload: json.RawMessage(`{"question":"status"}`)})
	if sctx.WorkflowKind != "relay" || sctx.Capabilities.Notifications == nil ||
		!strings.Contains(strings.Join(sctx.Capabilities.Notifications.ExcludeChannels, ","), "slack") ||
		!strings.Contains(strings.Join(sctx.Capabilities.Notifications.ExcludeChannels, ","), "whatsapp") {
		t.Fatalf("Relay schedule context = %+v", sctx)
	}
	if sctx.Capabilities.SlackConnectionID != "" || sctx.Capabilities.Notifications.SlackWebhookSecretName != "" ||
		len(sctx.Capabilities.Notifications.RunSummarySlackWebhookSecretNames) != 0 || len(sctx.Capabilities.Notifications.PulseSummarySlackWebhookSecretNames) != 0 {
		t.Fatalf("Relay retained legacy Slack credentials: %+v", sctx.Capabilities)
	}
	if sctx.Capabilities.Notifications.GmailConnectionID != "google-account" || manifest.Capabilities.SlackConnectionID != "legacy-slack" || manifest.Capabilities.Notifications.SlackWebhookSecretName != "legacy-webhook" {
		t.Fatal("schedule sanitization changed Google access or mutated the saved manifest")
	}

}

func TestRelayRejectsBotChannelRoute(t *testing.T) {
	manifest := NewWorkflowManifest("Relay")
	manifest.Kind = "relay"
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{"Workflow/relay/workflow.json": string(raw)}})
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	api := &StreamingAPI{}
	userID, allowed, err := api.checkBotWorkflowAccess(context.Background(), "owner", "", services.ChannelRoute{WorkflowID: manifest.ID, WorkspacePath: "Workflow/relay"})
	if err != nil || allowed || userID != "owner" {
		t.Fatalf("Relay bot route access = (%q, %v, %v)", userID, allowed, err)
	}
}

func TestRelayManifestUpdateRejectsInvalidGroupsAsBadRequest(t *testing.T) {
	manifest := NewWorkflowManifest("Relay")
	manifest.Kind = "relay"
	manifest.CreatedBy = "owner"
	manifest.Access = &WorkflowAccess{Owners: []string{"owner"}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		"Workflow/relay/workflow.json": string(raw),
	}})
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	function := reviewPRTrigger()
	requestBody, err := json.Marshal(UpdateWorkflowManifestRequest{
		WorkspacePath: "Workflow/relay",
		Schedules:     &[]WorkflowSchedule{function},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/workflows/manifest", strings.NewReader(string(requestBody)))
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "owner"}))
	response := httptest.NewRecorder()
	(&StreamingAPI{}).handleUpdateWorkflowManifest(response, req)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "exactly one variable group") {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}
