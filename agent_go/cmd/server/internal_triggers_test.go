package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

func TestNormalizeTriggerKind(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want string
	}{
		{"", ""},
		{"internal", "internal"},
		{" Internal ", "internal"},
		{"public", ""},
		{"secret", ""},
	} {
		if got := normalizeTriggerKind(tt.in); got != tt.want {
			t.Fatalf("normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if !isInternalTriggerKind("internal") || isInternalTriggerKind("") || isInternalTriggerKind("public") {
		t.Fatal("isInternalTriggerKind mismatch")
	}
}

func TestValidateTriggerCaller(t *testing.T) {
	if err := validateTriggerCaller(nil, triggerCallerWorkflow); err == nil {
		t.Fatal("nil caller must be rejected")
	}
	if err := validateTriggerCaller(&triggerCaller{Type: "crew", ID: "proj"}, triggerCallerWorkflow); err == nil {
		t.Fatal("wrong caller type must be rejected")
	}
	if err := validateTriggerCaller(&triggerCaller{Type: "workflow", ID: "  "}, triggerCallerWorkflow); err == nil {
		t.Fatal("empty caller id must be rejected")
	}
	if err := validateTriggerCaller(&triggerCaller{Type: "workflow", ID: "release-pipeline"}, triggerCallerWorkflow); err != nil {
		t.Fatalf("valid caller rejected: %v", err)
	}
	if err := validateTriggerCaller(&triggerCaller{Type: "crew", ID: "rts", ProfileID: "work"}, triggerCallerCrew); err != nil {
		t.Fatalf("valid crew caller rejected: %v", err)
	}
}

func TestTriggerCallerFromArgs(t *testing.T) {
	if got := triggerCallerFromArgs(map[string]interface{}{}); got != nil {
		t.Fatalf("absent caller = %+v, want nil", got)
	}
	got := triggerCallerFromArgs(map[string]interface{}{"caller": map[string]interface{}{"type": "crew", "id": "rts", "profile_id": "work"}})
	if got == nil || got.Type != "crew" || got.ID != "rts" || got.ProfileID != "work" {
		t.Fatalf("caller = %+v", got)
	}
	if got := triggerCallerFromArgs(map[string]interface{}{"caller": map[string]interface{}{"type": "", "id": ""}}); got != nil {
		t.Fatalf("blank caller = %+v, want nil", got)
	}
}

func TestValidateProductWebhookInternal(t *testing.T) {
	base := productWebhookTrigger{
		ID: "4c98bba9-b433-4bf8-b1de-68eebd143a6c", Name: "Release reviewer",
		Enabled: true, Message: "Review the delivery",
		Kind:   triggerKindInternal,
		Caller: &triggerCaller{Type: "workflow", ID: "release-pipeline"},
	}
	if err := validateProductWebhook(base); err != nil {
		t.Fatalf("internal trigger rejected: %v", err)
	}
	withSecret := base
	withSecret.Webhook = &WorkflowWebhookConfig{AuthMode: "bearer", EncryptedSecret: "ciphertext"}
	if err := validateProductWebhook(withSecret); err == nil {
		t.Fatal("internal trigger with a secret must be rejected")
	}
	// Crews may call Crews (crew→crew); unknown caller types still fail.
	crewCaller := base
	crewCaller.Caller = &triggerCaller{Type: "crew", ID: "other", ProfileID: "work"}
	if err := validateProductWebhook(crewCaller); err != nil {
		t.Fatalf("internal crew trigger with a crew caller rejected: %v", err)
	}
	wrongCaller := base
	wrongCaller.Caller = &triggerCaller{Type: "slack", ID: "other"}
	if err := validateProductWebhook(wrongCaller); err == nil {
		t.Fatal("internal crew trigger with an unknown caller type must be rejected")
	}
	noCaller := base
	noCaller.Caller = nil
	if err := validateProductWebhook(noCaller); err == nil {
		t.Fatal("internal trigger without a caller must be rejected")
	}
	badKind := base
	badKind.Kind = "public"
	if err := validateProductWebhook(badKind); err == nil {
		t.Fatal("unknown trigger kind must be rejected")
	}
}

func TestProductWebhookDTOInternalHasNoPath(t *testing.T) {
	dto := productWebhookDTO(productWebhookTrigger{
		ID: "4c98bba9-b433-4bf8-b1de-68eebd143a6c", Name: "Release reviewer",
		Enabled: true, Message: "Review the delivery",
		Kind:   triggerKindInternal,
		Caller: &triggerCaller{Type: "workflow", ID: "release-pipeline"},
	})
	if dto.Path != "" || dto.AuthMode != "" || dto.Secret != "" {
		t.Fatalf("internal dto exposed endpoint material: %+v", dto)
	}
	if dto.Kind != triggerKindInternal || dto.Caller == nil || dto.Caller.ID != "release-pipeline" {
		t.Fatalf("internal dto dropped binding: %+v", dto)
	}
}

func TestValidateWebhookScheduleInternal(t *testing.T) {
	base := WorkflowSchedule{
		ID: "trigger-1", Name: "Crew smoke", ScheduleType: "webhook",
		Enabled: true, WorkshopMode: "run",
		Webhook: &WorkflowWebhookConfig{StepID: "work"},
		Kind:    triggerKindInternal,
		Caller:  &triggerCaller{Type: "crew", ID: "rts", ProfileID: "work"},
	}
	if err := validateWebhookSchedule(base); err != nil {
		t.Fatalf("internal schedule rejected: %v", err)
	}
	withSecret := base
	withSecret.Webhook = &WorkflowWebhookConfig{AuthMode: "bearer", EncryptedSecret: "ciphertext"}
	if err := validateWebhookSchedule(withSecret); err == nil {
		t.Fatal("internal schedule with a secret must be rejected")
	}
	// Workflows may call workflows (workflow→workflow); unknown types fail.
	workflowCaller := base
	workflowCaller.Caller = &triggerCaller{Type: "workflow", ID: "other"}
	if err := validateWebhookSchedule(workflowCaller); err != nil {
		t.Fatalf("internal workflow schedule with a workflow caller rejected: %v", err)
	}
	wrongCaller := base
	wrongCaller.Caller = &triggerCaller{Type: "slack", ID: "other"}
	if err := validateWebhookSchedule(wrongCaller); err == nil {
		t.Fatal("internal workflow schedule with an unknown caller type must be rejected")
	}
	noCaller := base
	noCaller.Caller = nil
	if err := validateWebhookSchedule(noCaller); err == nil {
		t.Fatal("internal schedule without a caller must be rejected")
	}
	public := base
	public.Kind = ""
	public.Caller = nil
	public.Webhook = &WorkflowWebhookConfig{AuthMode: "bearer", EncryptedSecret: "ciphertext"}
	if err := validateWebhookSchedule(public); err != nil {
		t.Fatalf("public schedule rejected: %v", err)
	}
	publicNoSecret := public
	publicNoSecret.Webhook = &WorkflowWebhookConfig{}
	if err := validateWebhookSchedule(publicNoSecret); err == nil {
		t.Fatal("public schedule without a secret must be rejected")
	}
}

func TestWorkflowWebhookDTOInternalHasNoPath(t *testing.T) {
	dto := workflowWebhookDTO(WorkflowSchedule{
		ID: "trigger-1", Name: "Crew smoke", ScheduleType: "webhook",
		Enabled: true, Webhook: &WorkflowWebhookConfig{StepID: "work"},
		Kind:   triggerKindInternal,
		Caller: &triggerCaller{Type: "crew", ID: "rts"},
	})
	if dto.Path != "" || dto.AuthMode != "" || dto.Secret != "" {
		t.Fatalf("internal dto exposed endpoint material: %+v", dto)
	}
	if dto.Kind != triggerKindInternal || dto.Caller == nil || dto.StepID != "work" {
		t.Fatalf("internal dto dropped binding or target: %+v", dto)
	}
}

// newInternalTriggerTestCrew registers a keyed crew profile with triggers for binding tests.
func newInternalTriggerTestCrew(t *testing.T) (*ProductScheduleService, map[string]string) {
	t.Helper()
	registry := agentprofiles.NewRegistry()
	profile := agentprofiles.Profile{
		ID: "crewx", Name: "CrewX", Version: 1, SystemPromptTemplate: "hi", BuiltIn: true, Product: "crewx",
		Runtime: agentprofiles.RuntimePolicy{
			Transport:    "auto",
			Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
			Workspace:    agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: "Chats/Work/projects"},
		},
		Features: []agentprofiles.FeatureBinding{{ID: "triggers"}},
		UIPanels: agentprofiles.UIPanels{Schedules: true},
	}
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.Resolve("crewx", 0, "owner")
	if err != nil || !agentprofiles.HasFeature(resolved, "triggers") {
		t.Fatalf("test profile lacks triggers: %+v %v", resolved, err)
	}
	files := map[string]string{}
	svc := NewProductScheduleService(nil, registry)
	svc.readFile = func(_ context.Context, path string) (string, bool, error) {
		c, ok := files[path]
		return c, ok, nil
	}
	svc.writeFile = func(_ context.Context, path, content string) error {
		files[path] = content
		return nil
	}
	return svc, files
}

func TestSaveWorkflowWebhookInternalBindsCrewWithoutSecret(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true}]}`)
	manifest := NewWorkflowManifest("Binding test")
	manifest.CreatedBy = "owner"
	manifest.Access = &WorkflowAccess{Owners: []string{"owner"}}
	raw, _ := json.Marshal(manifest)
	crewProduct := `{"schema_version":1,"product":"crewx","id":"rts","title":"RTS","session_id":"sess-1"}`
	mock := &mockWorkspaceAPI{files: map[string]string{
		manifestPath("Workflow/test"):                       string(raw),
		"Workflow/test/planning/plan.json":                  `{"steps":[{"type":"regular","id":"work","title":"Work","description":"Work"}]}`,
		"Workflow/test/variables/variables.json":            `{"variables":[],"groups":[{"name":"default"}]}`,
		"_users/owner/Chats/Work/projects/rts/product.json": crewProduct,
	}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	crewSvc, crewFiles := newInternalTriggerTestCrew(t)
	crewFiles["_users/owner/Chats/Work/projects/rts/product.json"] = crewProduct

	api := &StreamingAPI{productSchedules: crewSvc}
	svc := NewSchedulerService(api)
	api.scheduler = svc
	policy := workflowChatPolicy{Mode: "builder", Origin: "interactive", Capabilities: map[string]bool{"plan_authoring": true}}
	reg := &recordingRegistrar{}
	if err := api.registerWebhookTools(reg, "owner", "Workflow/test", policy); err != nil {
		t.Fatal(err)
	}
	tool := reg.tools["manage_workflow_webhook"]
	output, err := tool.exec(context.Background(), map[string]interface{}{
		"action": "create", "name": "Crew smoke", "enabled": true,
		"kind": "internal", "caller": map[string]interface{}{"type": "crew", "id": "rts", "profile_id": "crewx"},
		"route_selections": map[string]string{}, "group_names": []string{"default"},
	})
	if err != nil {
		t.Fatalf("internal create failed: %v", err)
	}
	var created workflowWebhookResponse
	if err := json.Unmarshal([]byte(output), &created); err != nil {
		t.Fatal(err)
	}
	if created.Secret != "" || created.Path != "" || created.AuthMode != "" {
		t.Fatalf("internal trigger exposed endpoint material: %+v", created)
	}
	if created.Kind != triggerKindInternal || created.Caller == nil || created.Caller.ID != "rts" {
		t.Fatalf("internal binding lost: %+v", created)
	}
	if _, err := tool.exec(context.Background(), map[string]interface{}{
		"action": "create", "name": "Ghost", "enabled": true,
		"kind": "internal", "caller": map[string]interface{}{"type": "crew", "id": "ghost", "profile_id": "crewx"},
		"route_selections": map[string]string{}, "group_names": []string{"default"},
	}); err == nil {
		t.Fatal("binding to an unknown crew project must fail")
	}
}

func TestSaveProductWebhookConfigInternalBindsWorkflowWithoutSecret(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	callerManifest, _ := json.Marshal(WorkflowManifest{ID: "release-pipeline", Label: "Release"})
	mock := &mockWorkspaceAPI{files: map[string]string{
		manifestPath("Workflow/release-pipeline"): string(callerManifest),
	}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	svc, files := newInternalTriggerTestCrew(t)
	productJSON := `{"schema_version":1,"product":"crewx","id":"rts","title":"RTS","session_id":"sess-1","triggers":[]}`
	// The binding resolves the project workspace through the shared store, then
	// reads the manifest through the service file funcs.
	files["_users/owner/Chats/Work/projects/rts/product.json"] = productJSON
	mock.files["_users/owner/Chats/Work/projects/rts/product.json"] = productJSON

	response, created, err := svc.saveProductWebhookConfig(context.Background(), "owner", productWebhookRequest{
		ProfileID: "crewx", ProjectID: "rts", Name: "Release reviewer", Enabled: true,
		Message: "Review the delivery", Kind: triggerKindInternal,
		Caller: &triggerCaller{Type: "workflow", ID: "release-pipeline"},
	}, "")
	if err != nil || !created {
		t.Fatalf("internal create = created=%v err=%v", created, err)
	}
	if response.Secret != "" || response.Path != "" || response.AuthMode != "" {
		t.Fatalf("internal trigger exposed endpoint material: %+v", response)
	}
	if response.Kind != triggerKindInternal || response.Caller == nil || response.Caller.ID != "release-pipeline" {
		t.Fatalf("internal binding lost: %+v", response)
	}
	persisted, err := svc.projectWebhookConfigs(context.Background(), "owner", "crewx", "rts")
	if err != nil || len(persisted) != 1 || !persisted[0].IsInternal() || persisted[0].Webhook != nil {
		t.Fatalf("persisted triggers = %+v err=%v", persisted, err)
	}
	if _, _, err := svc.saveProductWebhookConfig(context.Background(), "owner", productWebhookRequest{
		ProfileID: "crewx", ProjectID: "rts", Name: "Ghost", Enabled: true,
		Message: "Review", Kind: triggerKindInternal,
		Caller: &triggerCaller{Type: "workflow", ID: "ghost-pipeline"},
	}, ""); err == nil {
		t.Fatal("binding to an unknown workflow must fail")
	}
}

func TestTriggerCallerMatchesPresented(t *testing.T) {
	binding := &triggerCaller{Type: "crew", ID: "rts", ProfileID: "work"}
	for _, tt := range []struct {
		name      string
		binding   *triggerCaller
		wantType  string
		presented triggerCaller
		match     bool
	}{
		{"crew match", binding, triggerCallerCrew, triggerCaller{Type: "crew", ID: "rts", ProfileID: "work"}, true},
		{"type case-insensitive", binding, triggerCallerCrew, triggerCaller{Type: "Crew", ID: "rts", ProfileID: "work"}, true},
		{"id trims", binding, triggerCallerCrew, triggerCaller{Type: "crew", ID: " rts ", ProfileID: "work"}, true},
		{"wrong id", binding, triggerCallerCrew, triggerCaller{Type: "crew", ID: "other", ProfileID: "work"}, false},
		{"wrong profile", binding, triggerCallerCrew, triggerCaller{Type: "crew", ID: "rts", ProfileID: "other"}, false},
		{"wrong presented type", binding, triggerCallerCrew, triggerCaller{Type: "workflow", ID: "rts", ProfileID: "work"}, false},
		{"nil binding", nil, triggerCallerCrew, triggerCaller{Type: "crew", ID: "rts", ProfileID: "work"}, false},
		{"workflow ignores profile", &triggerCaller{Type: "workflow", ID: "wf"}, triggerCallerWorkflow, triggerCaller{Type: "workflow", ID: "wf", ProfileID: "anything"}, true},
		{"binding type must agree", &triggerCaller{Type: "crew", ID: "wf"}, triggerCallerWorkflow, triggerCaller{Type: "workflow", ID: "wf"}, false},
	} {
		if got := tt.binding.matchesPresented(tt.wantType, tt.presented); got != tt.match {
			t.Fatalf("%s: match = %v, want %v", tt.name, got, tt.match)
		}
	}
}

func TestCheckInternalPayload(t *testing.T) {
	if err := checkInternalPayload([]byte(`{"a":1}`)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	if err := checkInternalPayload([]byte(`not json`)); !errors.Is(err, ErrInternalTriggerPayload) {
		t.Fatalf("invalid JSON err = %v, want ErrInternalTriggerPayload", err)
	}
	big := []byte(`"` + strings.Repeat("x", maxWebhookBodyBytes) + `"`)
	if err := checkInternalPayload(big); !errors.Is(err, ErrInternalTriggerPayload) {
		t.Fatalf("oversize err = %v, want ErrInternalTriggerPayload", err)
	}
}

func TestSelectInternalProductTrigger(t *testing.T) {
	triggers := []productWebhookTrigger{
		{ID: "t-internal", Enabled: true, Kind: "internal", Caller: &triggerCaller{Type: "workflow", ID: "wf"}},
		{ID: "t-disabled", Enabled: false, Kind: "internal", Caller: &triggerCaller{Type: "workflow", ID: "wf"}},
		{ID: "t-public", Enabled: true, Webhook: &WorkflowWebhookConfig{AuthMode: "Bearer [REDACTED]"}},
	}
	if _, err := selectInternalProductTrigger(triggers, "t-internal"); err != nil {
		t.Fatalf("internal trigger rejected: %v", err)
	}
	if _, err := selectInternalProductTrigger(triggers, "missing"); !errors.Is(err, ErrInternalTriggerNotFound) {
		t.Fatalf("missing err = %v, want ErrInternalTriggerNotFound", err)
	}
	if _, err := selectInternalProductTrigger(triggers, "t-public"); !errors.Is(err, ErrInternalTriggerNotBound) {
		t.Fatalf("public err = %v, want ErrInternalTriggerNotBound", err)
	}
	if _, err := selectInternalProductTrigger(triggers, "t-disabled"); !errors.Is(err, ErrInternalTriggerDisabled) {
		t.Fatalf("disabled err = %v, want ErrInternalTriggerDisabled", err)
	}
}

func TestFindInternalWorkflowTrigger(t *testing.T) {
	manifest := &WorkflowManifest{ID: "wf-1", Schedules: []WorkflowSchedule{
		{ID: "t-internal", ScheduleType: "webhook", Enabled: true, Kind: "internal", Caller: &triggerCaller{Type: "crew", ID: "rts", ProfileID: "work"}},
		{ID: "t-disabled", ScheduleType: "webhook", Enabled: false, Kind: "internal", Caller: &triggerCaller{Type: "crew", ID: "rts"}},
		{ID: "t-public", ScheduleType: "webhook", Enabled: true, Webhook: &WorkflowWebhookConfig{}},
		{ID: "t-cron", ScheduleType: "cron", Enabled: true},
	}}
	if _, err := findInternalWorkflowTrigger(manifest, "t-internal"); err != nil {
		t.Fatalf("internal trigger rejected: %v", err)
	}
	if _, err := findInternalWorkflowTrigger(nil, "t-internal"); !errors.Is(err, ErrInternalTriggerNotFound) {
		t.Fatalf("nil manifest err = %v, want ErrInternalTriggerNotFound", err)
	}
	if _, err := findInternalWorkflowTrigger(manifest, "missing"); !errors.Is(err, ErrInternalTriggerNotFound) {
		t.Fatalf("missing err = %v, want ErrInternalTriggerNotFound", err)
	}
	if _, err := findInternalWorkflowTrigger(manifest, "t-cron"); !errors.Is(err, ErrInternalTriggerNotFound) {
		t.Fatalf("non-webhook err = %v, want ErrInternalTriggerNotFound", err)
	}
	if _, err := findInternalWorkflowTrigger(manifest, "t-public"); !errors.Is(err, ErrInternalTriggerNotBound) {
		t.Fatalf("public err = %v, want ErrInternalTriggerNotBound", err)
	}
	if _, err := findInternalWorkflowTrigger(manifest, "t-disabled"); !errors.Is(err, ErrInternalTriggerDisabled) {
		t.Fatalf("disabled err = %v, want ErrInternalTriggerDisabled", err)
	}
}

// newInternalDispatchCrew wires a crew project with the given triggers JSON
// through both the service file stubs and the workspace mock, so internal
// dispatch tests exercise the real scoped lookup.
func newInternalDispatchCrew(t *testing.T, triggersJSON string) (*ProductScheduleService, map[string]string) {
	t.Helper()
	svc, files := newInternalTriggerTestCrew(t)
	svc.api = &StreamingAPI{}
	manifestPath := "_users/owner/Chats/Work/projects/rts/product.json"
	crewProduct := `{"schema_version":1,"product":"crewx","id":"rts","title":"RTS","session_id":"sess-1","triggers":` + triggersJSON + `}`
	mock := &mockWorkspaceAPI{files: map[string]string{manifestPath: crewProduct}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	files[manifestPath] = crewProduct
	return svc, files
}

const internalDispatchCrewTriggers = `[
	{"id":"trig-1","name":"Review","enabled":true,"message":"Review it","kind":"internal","caller":{"type":"workflow","id":"wf-1"}},
	{"id":"trig-off","name":"Off","enabled":false,"message":"Off","kind":"internal","caller":{"type":"workflow","id":"wf-1"}},
	{"id":"trig-pub","name":"Pub","enabled":true,"message":"Pub","webhook":{"auth_mode":"bearer","encrypted_secret":"x"}}
]`

func TestDispatchInternalProductTriggerQueuedAndDuplicate(t *testing.T) {
	svc, files := newInternalDispatchCrew(t, internalDispatchCrewTriggers)
	// Hold the conversation so dispatch queues instead of starting a live
	// agent turn; the claim, payload, and queue wiring is what this pins.
	convKey := "owner\x1fproduct-project:crewx:rts:trig-1"
	svc.conversations = map[string]bool{convKey: true}
	ctx := context.Background()
	call := internalCrewTriggerCall{
		UserID: "owner", ProfileID: "crewx", ProjectID: "rts", TriggerID: "trig-1",
		Caller:     triggerCaller{Type: "workflow", ID: "wf-1"},
		DeliveryID: "d1", Payload: []byte(`{"a":1}`),
	}
	result, err := svc.dispatchInternalProductTrigger(ctx, call)
	if err != nil {
		t.Fatalf("dispatch err = %v", err)
	}
	wantRun := webhookDeliveryRunID("rts", "trig-1", "d1")
	if result.RunID != wantRun || result.Status != "queued" || result.Duplicate {
		t.Fatalf("result = %+v, want queued run %s", result, wantRun)
	}
	payloadPath := "_users/owner/Chats/Work/projects/rts/triggers/deliveries/" + wantRun + ".json"
	if files[payloadPath] != "{\"a\":1}\n" {
		t.Fatalf("payload file = %q", files[payloadPath])
	}
	svc.mu.Lock()
	depth := len(svc.queued[convKey])
	svc.mu.Unlock()
	if depth != 1 {
		t.Fatalf("queued depth = %d, want 1", depth)
	}
	dup, err := svc.dispatchInternalProductTrigger(ctx, call)
	if err != nil {
		t.Fatalf("duplicate err = %v", err)
	}
	if !dup.Duplicate || dup.RunID != wantRun || dup.Status != "queued" {
		t.Fatalf("duplicate = %+v, want duplicate queued run %s", dup, wantRun)
	}
}

func TestDispatchInternalProductTriggerRejects(t *testing.T) {
	svc, _ := newInternalDispatchCrew(t, internalDispatchCrewTriggers)
	svc.conversations = map[string]bool{"owner\x1fproduct-project:crewx:rts:trig-1": true}
	ctx := context.Background()
	base := internalCrewTriggerCall{
		UserID: "owner", ProfileID: "crewx", ProjectID: "rts", TriggerID: "trig-1",
		Caller:     triggerCaller{Type: "workflow", ID: "wf-1"},
		DeliveryID: "d-reject", Payload: []byte(`{}`),
	}
	for _, tt := range []struct {
		name   string
		mutate func(*internalCrewTriggerCall)
		want   error
	}{
		{"bad payload", func(c *internalCrewTriggerCall) { c.Payload = []byte(`nope`) }, ErrInternalTriggerPayload},
		{"unknown trigger", func(c *internalCrewTriggerCall) { c.TriggerID = "missing" }, ErrInternalTriggerNotFound},
		{"unknown project", func(c *internalCrewTriggerCall) { c.ProjectID = "ghost" }, ErrInternalTriggerNotFound},
		{"public trigger", func(c *internalCrewTriggerCall) { c.TriggerID = "trig-pub" }, ErrInternalTriggerNotBound},
		{"disabled trigger", func(c *internalCrewTriggerCall) { c.TriggerID = "trig-off" }, ErrInternalTriggerDisabled},
		{"wrong caller id", func(c *internalCrewTriggerCall) { c.Caller.ID = "intruder" }, ErrInternalCallerMismatch},
		{"wrong caller type", func(c *internalCrewTriggerCall) { c.Caller.Type = "crew" }, ErrInternalCallerMismatch},
	} {
		call := base
		tt.mutate(&call)
		if _, err := svc.dispatchInternalProductTrigger(ctx, call); !errors.Is(err, tt.want) {
			t.Fatalf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
	}
}

func TestGetInternalProductTriggerRun(t *testing.T) {
	svc, _ := newInternalDispatchCrew(t, internalDispatchCrewTriggers)
	svc.conversations = map[string]bool{"owner\x1fproduct-project:crewx:rts:trig-1": true}
	ctx := context.Background()
	call := internalCrewTriggerCall{
		UserID: "owner", ProfileID: "crewx", ProjectID: "rts", TriggerID: "trig-1",
		Caller:     triggerCaller{Type: "workflow", ID: "wf-1"},
		DeliveryID: "d-poll", Payload: []byte(`{}`),
	}
	result, err := svc.dispatchInternalProductTrigger(ctx, call)
	if err != nil {
		t.Fatalf("dispatch err = %v", err)
	}
	caller := triggerCaller{Type: "workflow", ID: "wf-1"}
	got, err := svc.getInternalProductTriggerRun(ctx, "owner", "crewx", "rts", "trig-1", result.RunID, caller)
	if err != nil {
		t.Fatalf("get run err = %v", err)
	}
	if got.RunID != result.RunID || got.Status != "queued" || got.Terminal {
		t.Fatalf("run status = %+v", got)
	}
	if _, err := svc.getInternalProductTriggerRun(ctx, "owner", "crewx", "rts", "trig-1", "missing", caller); !errors.Is(err, ErrInternalTriggerRunGone) {
		t.Fatalf("missing run err = %v, want ErrInternalTriggerRunGone", err)
	}
	if _, err := svc.getInternalProductTriggerRun(ctx, "owner", "crewx", "rts", "trig-1", result.RunID, triggerCaller{Type: "workflow", ID: "intruder"}); !errors.Is(err, ErrInternalCallerMismatch) {
		t.Fatalf("wrong caller err = %v, want ErrInternalCallerMismatch", err)
	}
	if _, err := svc.getInternalProductTriggerRun(ctx, "owner", "crewx", "rts", "trig-off", result.RunID, caller); !errors.Is(err, ErrInternalTriggerDisabled) {
		t.Fatalf("disabled trigger err = %v, want ErrInternalTriggerDisabled", err)
	}
}

func TestDeliverProductTriggerPayloadFailure(t *testing.T) {
	svc, _ := newInternalTriggerTestCrew(t)
	svc.api = &StreamingAPI{}
	svc.writeFile = func(context.Context, string, string) error { return errors.New("disk gone") }
	_, _ = newScheduleRunWorkspaceStub(t)
	ctx := context.Background()
	match := &productWebhookMatch{
		UserID: "owner", Profile: agentprofiles.Profile{ID: "crewx"},
		Binding:  productConversationBinding{WorkspacePath: "Chats/owner/rts", ManifestPath: "Chats/owner/rts/product.json"},
		Manifest: productProjectManifest{ID: "rts", Title: "RTS"},
		Trigger:  productWebhookTrigger{ID: "trig-1", Name: "Review", Enabled: true, Kind: "internal"},
	}
	_, err := svc.deliverProductTrigger(ctx, match, "d-fail", "", []byte(`{}`), "note", nil)
	if !errors.Is(err, ErrProductTriggerNotPersist) {
		t.Fatalf("err = %v, want ErrProductTriggerNotPersist", err)
	}
	entry, err := FindScheduleRun(ctx, agentProfileRuntimeWorkspace("owner", match.Binding.WorkspacePath), webhookDeliveryRunID("rts", "trig-1", "d-fail"))
	if err != nil || entry.Status != "error" {
		t.Fatalf("record = %+v err=%v, want error status", entry, err)
	}
}

func testInternalWorkflowManifest() *WorkflowManifest {
	return &WorkflowManifest{ID: "wf-1", Schedules: []WorkflowSchedule{
		{ID: "trig-1", ScheduleType: "webhook", Enabled: true, Kind: "internal", Caller: &triggerCaller{Type: "crew", ID: "rts", ProfileID: "work"}},
		{ID: "trig-off", ScheduleType: "webhook", Enabled: false, Kind: "internal", Caller: &triggerCaller{Type: "crew", ID: "rts", ProfileID: "work"}},
		{ID: "trig-pub", ScheduleType: "webhook", Enabled: true, Webhook: &WorkflowWebhookConfig{}},
	}}
}

func TestDispatchInternalWorkflowTriggerAccepted(t *testing.T) {
	ctx := context.Background()
	var got *WorkflowWebhookDelivery
	receiver := webhookReceiver{
		existing: func(context.Context, string) (schedulerstate.Run, error) {
			return schedulerstate.Run{}, schedulerstate.ErrRunNotFound
		},
		start: func(_ string, _ string, _ string, input *WorkflowWebhookDelivery) (string, error) {
			got = input
			return input.RunID, nil
		},
	}
	call := internalWorkflowTriggerCall{
		WorkflowID: "wf-1", TriggerID: "trig-1",
		Caller:     triggerCaller{Type: "crew", ID: "rts", ProfileID: "work"},
		DeliveryID: "d1", Payload: []byte(`{"k":"v"}`),
	}
	result, err := receiver.dispatchInternal(ctx, "Workflow/test", testInternalWorkflowManifest(), "trig-1", call)
	if err != nil {
		t.Fatalf("dispatch err = %v", err)
	}
	wantRun := webhookDeliveryRunID("wf-1", "trig-1", "d1")
	if result.RunID != wantRun || result.Status != "accepted" || result.Duplicate {
		t.Fatalf("result = %+v, want accepted run %s", result, wantRun)
	}
	if got == nil || got.RunID != wantRun || string(got.Payload) != `{"k":"v"}` || got.Group != "" {
		t.Fatalf("delivery = %+v, want raw passthrough", got)
	}
}

func TestDispatchInternalWorkflowTriggerDuplicateAndStoreError(t *testing.T) {
	ctx := context.Background()
	manifest := testInternalWorkflowManifest()
	call := internalWorkflowTriggerCall{
		WorkflowID: "wf-1", TriggerID: "trig-1",
		Caller:     triggerCaller{Type: "crew", ID: "rts", ProfileID: "work"},
		DeliveryID: "d1", Payload: []byte(`{}`),
	}
	wantRun := webhookDeliveryRunID("wf-1", "trig-1", "d1")
	dupReceiver := webhookReceiver{
		existing: func(context.Context, string) (schedulerstate.Run, error) {
			return schedulerstate.Run{RunID: wantRun, State: schedulerstate.State("running")}, nil
		},
		start: func(string, string, string, *WorkflowWebhookDelivery) (string, error) {
			t.Fatal("start must not run on duplicate")
			return "", nil
		},
	}
	dup, err := dupReceiver.dispatchInternal(ctx, "Workflow/test", manifest, "trig-1", call)
	if err != nil {
		t.Fatalf("duplicate err = %v", err)
	}
	if !dup.Duplicate || dup.RunID != wantRun || dup.Status != "running" {
		t.Fatalf("duplicate = %+v", dup)
	}
	brokenReceiver := webhookReceiver{
		existing: func(context.Context, string) (schedulerstate.Run, error) {
			return schedulerstate.Run{}, errors.New("boom")
		},
		start: func(string, string, string, *WorkflowWebhookDelivery) (string, error) {
			t.Fatal("start must not run without storage")
			return "", nil
		},
	}
	if _, err := brokenReceiver.dispatchInternal(ctx, "Workflow/test", manifest, "trig-1", call); !errors.Is(err, ErrWebhookRunStoreMissing) {
		t.Fatalf("store err = %v, want ErrWebhookRunStoreMissing", err)
	}
}

func TestDispatchInternalWorkflowTriggerRejects(t *testing.T) {
	ctx := context.Background()
	manifest := testInternalWorkflowManifest()
	receiver := webhookReceiver{
		existing: func(context.Context, string) (schedulerstate.Run, error) {
			return schedulerstate.Run{}, schedulerstate.ErrRunNotFound
		},
		start: func(string, string, string, *WorkflowWebhookDelivery) (string, error) {
			t.Fatal("rejected dispatch must not start")
			return "", nil
		},
	}
	base := internalWorkflowTriggerCall{
		WorkflowID: "wf-1", TriggerID: "trig-1",
		Caller:     triggerCaller{Type: "crew", ID: "rts", ProfileID: "work"},
		DeliveryID: "d-reject", Payload: []byte(`{}`),
	}
	for _, tt := range []struct {
		name   string
		id     string
		mutate func(*internalWorkflowTriggerCall)
		want   error
	}{
		{"unknown trigger", "missing", func(c *internalWorkflowTriggerCall) {}, ErrInternalTriggerNotFound},
		{"public trigger", "trig-pub", func(c *internalWorkflowTriggerCall) {}, ErrInternalTriggerNotBound},
		{"disabled trigger", "trig-off", func(c *internalWorkflowTriggerCall) {}, ErrInternalTriggerDisabled},
		{"wrong caller id", "trig-1", func(c *internalWorkflowTriggerCall) { c.Caller.ID = "intruder" }, ErrInternalCallerMismatch},
		{"wrong caller profile", "trig-1", func(c *internalWorkflowTriggerCall) { c.Caller.ProfileID = "other" }, ErrInternalCallerMismatch},
		{"wrong caller type", "trig-1", func(c *internalWorkflowTriggerCall) { c.Caller.Type = "workflow" }, ErrInternalCallerMismatch},
		{"bad payload", "trig-1", func(c *internalWorkflowTriggerCall) { c.Payload = []byte(`nope`) }, ErrInternalTriggerPayload},
	} {
		call := base
		tt.mutate(&call)
		if _, err := receiver.dispatchInternal(ctx, "Workflow/test", manifest, tt.id, call); !errors.Is(err, tt.want) {
			t.Fatalf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
	}
	tooLong := base
	tooLong.DeliveryID = strings.Repeat("d", 257)
	if _, err := receiver.dispatchInternal(ctx, "Workflow/test", manifest, "trig-1", tooLong); err == nil {
		t.Fatal("oversize delivery id must be rejected")
	} else {
		var invalid *invalidWebhookDeliveryError
		if !errors.As(err, &invalid) {
			t.Fatalf("oversize delivery err = %v, want invalidWebhookDeliveryError", err)
		}
	}
}

func TestDispatchInternalWorkflowTriggerBusyPassthrough(t *testing.T) {
	ctx := context.Background()
	receiver := webhookReceiver{
		existing: func(context.Context, string) (schedulerstate.Run, error) {
			return schedulerstate.Run{}, schedulerstate.ErrRunNotFound
		},
		start: func(string, string, string, *WorkflowWebhookDelivery) (string, error) {
			return "", errors.New("webhook concurrency limit reached (4 active deliveries)")
		},
	}
	call := internalWorkflowTriggerCall{
		WorkflowID: "wf-1", TriggerID: "trig-1",
		Caller:     triggerCaller{Type: "crew", ID: "rts", ProfileID: "work"},
		DeliveryID: "d-busy", Payload: []byte(`{}`),
	}
	_, err := receiver.dispatchInternal(ctx, "Workflow/test", testInternalWorkflowManifest(), "trig-1", call)
	if err == nil || !strings.Contains(err.Error(), "webhook concurrency limit reached") {
		t.Fatalf("busy err = %v, want concurrency failure surfaced", err)
	}
}

func TestReadInternalWorkflowTriggerRun(t *testing.T) {
	ctx := context.Background()
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := &SchedulerService{stateStore: store}
	sched := WorkflowSchedule{ID: "trig-1", ScheduleType: "webhook", Enabled: true, Kind: "internal", Caller: &triggerCaller{Type: "crew", ID: "rts", ProfileID: "work"}}
	manifest := &WorkflowManifest{ID: "wf-1", Schedules: []WorkflowSchedule{sched}}
	scope, scopeID, _ := scheduleStateScope(buildScheduleContext("Workflow/test", manifest, sched))
	if err := store.BeginRun(ctx, schedulerstate.Run{RunID: "run-1", LockKey: "k1", ScheduleID: "trig-1", ScopeType: scope, ScopeID: scopeID, TriggerSource: "webhook", State: schedulerstate.State("running")}); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginRun(ctx, schedulerstate.Run{RunID: "run-2", LockKey: "k2", ScheduleID: "other", ScopeType: scope, ScopeID: scopeID, TriggerSource: "webhook"}); err != nil {
		t.Fatal(err)
	}
	caller := triggerCaller{Type: "crew", ID: "rts", ProfileID: "work"}
	got, err := svc.readInternalWorkflowTriggerRun(ctx, "Workflow/test", manifest, "trig-1", "run-1", caller)
	if err != nil {
		t.Fatalf("read run err = %v", err)
	}
	if got.RunID != "run-1" || got.Status != "running" || got.Terminal {
		t.Fatalf("run result = %+v", got)
	}
	if _, err := svc.readInternalWorkflowTriggerRun(ctx, "Workflow/test", manifest, "trig-1", "run-2", caller); !errors.Is(err, ErrInternalTriggerRunGone) {
		t.Fatalf("foreign run err = %v, want ErrInternalTriggerRunGone", err)
	}
	if _, err := svc.readInternalWorkflowTriggerRun(ctx, "Workflow/test", manifest, "trig-1", "missing", caller); !errors.Is(err, ErrInternalTriggerRunGone) {
		t.Fatalf("missing run err = %v, want ErrInternalTriggerRunGone", err)
	}
	off := &WorkflowManifest{ID: "wf-1", Schedules: []WorkflowSchedule{{ID: "trig-1", ScheduleType: "webhook", Enabled: false, Kind: "internal", Caller: &triggerCaller{Type: "crew", ID: "rts", ProfileID: "work"}}}}
	if _, err := svc.readInternalWorkflowTriggerRun(ctx, "Workflow/test", off, "trig-1", "run-1", caller); !errors.Is(err, ErrInternalTriggerDisabled) {
		t.Fatalf("disabled trigger err = %v, want ErrInternalTriggerDisabled", err)
	}
	if _, err := svc.readInternalWorkflowTriggerRun(ctx, "Workflow/test", manifest, "trig-1", "run-1", triggerCaller{Type: "crew", ID: "intruder", ProfileID: "work"}); !errors.Is(err, ErrInternalCallerMismatch) {
		t.Fatalf("wrong caller err = %v, want ErrInternalCallerMismatch", err)
	}
}

func TestReadPublishedRelayFunctionRunUsesReleaseScopeAndLiveCaller(t *testing.T) {
	ctx := context.Background()
	draft := "Workflow/relay-crew-poll"
	release := relayReleaseWorkspace(draft, "v1")
	caller := triggerCaller{Type: triggerCallerCrew, ID: "reports", ProfileID: "work"}
	sched := WorkflowSchedule{ID: "function-1", ScheduleType: "webhook", Kind: triggerKindFunction, Enabled: true, Function: &WorkflowFunctionSpec{Name: "process", AllowedCallers: []triggerCaller{caller}}}
	live := NewWorkflowManifest("Crew poll")
	live.Kind = "relay"
	live.Schedules = []WorkflowSchedule{sched}
	published := *live
	published.Schedules = []WorkflowSchedule{sched}
	liveRaw, _ := json.Marshal(live)
	publishedRaw, _ := json.Marshal(&published)
	releaseRaw, _ := json.Marshal(relayRelease{Version: "v1", Hash: "test", FileCount: 1, Files: []string{"workflow.json"}})
	deliveryRaw, _ := json.Marshal(WorkflowWebhookDelivery{RunID: "run-1", Payload: json.RawMessage(`{"relay_version":"v1"}`)})
	mock := &mockWorkspaceAPI{files: map[string]string{
		manifestPath(draft):                string(liveRaw),
		manifestPath(release):              string(publishedRaw),
		release + "/release.json":          string(releaseRaw),
		webhookInputPath(release, "run-1"): string(deliveryRaw),
	}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	scope, scopeID, lockKey := scheduleStateScope(buildScheduleContext(release, &published, sched))
	if err := store.BeginRun(ctx, schedulerstate.Run{RunID: "run-1", LockKey: lockKey, ScheduleID: sched.ID, ScopeType: scope, ScopeID: scopeID, TriggerSource: "webhook", State: schedulerstate.State("running")}); err != nil {
		t.Fatal(err)
	}
	svc := &SchedulerService{stateStore: store}
	got, err := svc.readInternalWorkflowTriggerRun(ctx, draft, live, sched.ID, "run-1", caller)
	if err != nil || got.RunID != "run-1" || got.Version != "v1" {
		t.Fatalf("published Crew poll = %+v, %v", got, err)
	}
	live.Schedules[0].Function.AllowedCallers = []triggerCaller{{Type: triggerCallerCrew, ID: "other", ProfileID: "work"}}
	if _, err := svc.readInternalWorkflowTriggerRun(ctx, draft, live, sched.ID, "run-1", caller); !errors.Is(err, ErrInternalCallerMismatch) {
		t.Fatalf("revoked caller poll = %v, want mismatch", err)
	}
}

func TestSaveProductWebhookRejectsInaccessibleCallerWorkflow(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true}]}`)
	svc, files := newInternalTriggerTestCrew(t)
	crewPath := "_users/owner/Chats/Work/projects/rts/product.json"
	files[crewPath] = `{"schema_version":1,"product":"crewx","id":"rts","title":"RTS","session_id":"sess-1","triggers":[]}`
	own := NewWorkflowManifest("Own pipeline")
	own.CreatedBy = "owner"
	own.Access = &WorkflowAccess{Owners: []string{"owner"}}
	ownRaw, _ := json.Marshal(own)
	foreign := NewWorkflowManifest("Foreign pipeline")
	foreign.ID = "wf-foreign"
	foreign.CreatedBy = "stranger"
	foreign.Access = &WorkflowAccess{Owners: []string{"stranger"}}
	foreignRaw, _ := json.Marshal(foreign)
	mock := &mockWorkspaceAPI{files: map[string]string{
		manifestPath("Workflow/own"):     string(ownRaw),
		manifestPath("Workflow/foreign"): string(foreignRaw),
		crewPath:                         files[crewPath],
	}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	post := func(callerID string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]interface{}{
			"profile_id": "crewx", "project_id": "rts",
			"name": "Bound", "message": "hi", "enabled": true,
			"kind":   triggerKindInternal,
			"caller": map[string]interface{}{"type": "workflow", "id": callerID},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/product-webhooks", bytes.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "owner"}))
		rec := httptest.NewRecorder()
		svc.saveProductWebhook(rec, req)
		return rec
	}
	if rec := post("wf-foreign"); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "access denied") {
		t.Fatalf("foreign caller: code=%d body=%q, want access denial", rec.Code, rec.Body.String())
	}
	var saved productProjectManifest
	if err := json.Unmarshal([]byte(files[crewPath]), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Triggers) != 0 {
		t.Fatalf("denied binding persisted %d triggers", len(saved.Triggers))
	}
	if rec := post(own.ID); rec.Code != http.StatusCreated {
		t.Fatalf("own caller: code=%d body=%q, want 201", rec.Code, rec.Body.String())
	}
}

func TestExecuteAutomationRunSetupFailureBecomesTerminal(t *testing.T) {
	svc, _ := newInternalDispatchCrew(t, internalDispatchCrewTriggers)
	ctx := context.Background()
	crewWS := "_users/owner/Chats/Work/projects/rts"
	runsWorkspace := agentProfileRuntimeWorkspace("owner", crewWS)
	runID := "run-setup-fail"
	if _, claimed, err := ClaimScheduleRun(ctx, runsWorkspace, &ScheduleRunEntry{
		ID: runID, ScheduleID: "crewx:rts:trig-1", TriggerSource: "webhook",
		Status: "queued", StartedAt: time.Now().UTC(),
	}); err != nil || !claimed {
		t.Fatalf("pre-claim = claimed=%v err=%v", claimed, err)
	}
	// A profile declaring no conversation policy fails binding resolution
	// before the worker touches the conversation registry or run store.
	job := productScheduleJob{
		UserID: "owner", Profile: agentprofiles.Profile{ID: "crewx"},
		ProjectID: "rts", ProjectTitle: "RTS", WorkspacePath: crewWS,
		Schedule: productschedule.Schedule{ID: "trig-1", Name: "Review", Enabled: true},
	}
	runCtx, cancel := context.WithCancel(ctx)
	_, err := svc.executeAutomationRun(runCtx, cancel, job, "webhook", time.Time{},
		productScheduleRunOptions{RunID: runID}, &productScheduleRun{}, "job-key", "conv-key", nil)
	if err == nil || !strings.Contains(err.Error(), "resolve product conversation") {
		t.Fatalf("setup err = %v, want conversation resolution failure", err)
	}
	entry, err := FindScheduleRun(ctx, runsWorkspace, runID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Status != "error" || !isTerminalScheduleRunStatus(entry.Status) || entry.CompletedAt == nil {
		t.Fatalf("entry = %+v, want terminal error", entry)
	}
	if !strings.Contains(entry.Error, "resolve product conversation") {
		t.Fatalf("entry error = %q, want setup failure detail", entry.Error)
	}
	// Redelivery adopts the terminal error instead of a stranded queue record.
	existing, claimed, err := ClaimScheduleRun(ctx, runsWorkspace, &ScheduleRunEntry{ID: runID, Status: "queued"})
	if err != nil || claimed {
		t.Fatalf("redelivery claim = claimed=%v err=%v", claimed, err)
	}
	if existing.Status != "error" {
		t.Fatalf("redelivery status = %q, want terminal error", existing.Status)
	}
	// Schedules claim nothing up front: setup failure reports without writing.
	runCtx2, cancel2 := context.WithCancel(ctx)
	if _, err := svc.executeAutomationRun(runCtx2, cancel2, job, "cron", time.Time{},
		productScheduleRunOptions{}, &productScheduleRun{}, "job-key-2", "conv-key-2", nil); err == nil {
		t.Fatal("cron setup failure must still report its error")
	}
	runs, err := ReadScheduleRuns(ctx, runsWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (no record for the unclaimed cron run)", len(runs))
	}
}

func TestTriggerTurnMessageReferencesDeliveryFile(t *testing.T) {
	msg := triggerTurnMessage("Brief me", "Started by webhook.", "triggers/deliveries/r.json")
	if !strings.Contains(msg, "triggers/deliveries/r.json") {
		t.Fatalf("trigger turn lost the delivery file reference: %q", msg)
	}
	if strings.Contains(msg, "```json") {
		t.Fatal("trigger turn inlined its payload into crew chat")
	}
	if !strings.Contains(msg, "Brief me") || !strings.Contains(msg, "Started by webhook.") {
		t.Fatalf("trigger turn lost its message or source note: %q", msg)
	}
}

func TestTriggerTurnMessageCarriesAutonomyNote(t *testing.T) {
	msg := triggerTurnMessage("Brief me", "Started by webhook.", "triggers/deliveries/r.json")
	if !strings.Contains(msg, "Do not ask clarifying questions") {
		t.Fatalf("trigger turn lost the autonomy note: %q", msg)
	}
	if !strings.Contains(msg, "not an interactive conversation") {
		t.Fatalf("trigger turn lost the one-way framing: %q", msg)
	}
}
