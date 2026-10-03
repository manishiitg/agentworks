package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtrigger"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

const (
	maxWebhookBodyBytes   = 1024 * 1024
	maxWebhookConcurrency = 4
)

// The plaintext secret is returned only on creation/rotation. Ciphertext uses
// the existing server secrets key, bound to this workflow and trigger.
type WorkflowWebhookConfig struct {
	StepID           string                          `json:"step_id,omitempty"`
	InputMode        string                          `json:"input_mode,omitempty"`
	AllowedVariables []string                        `json:"allowed_variables,omitempty"`
	PayloadMappings  *WorkflowWebhookPayloadMappings `json:"payload_mappings,omitempty"`
	AuthMode         string                          `json:"auth_mode"`
	EncryptedSecret  string                          `json:"encrypted_secret"`
}

type WorkflowWebhookValueMapping = workflowtrigger.ValueMapping

// Reused by webhooks and channel-trigger sources.
type WorkflowWebhookPayloadMappings = workflowtrigger.PayloadMappings

type WorkflowWebhookDelivery struct {
	gmailRuleID     string            // Internal admission metadata, never decoded from a request.
	Group           string            `json:"group,omitempty"`
	Variables       map[string]string `json:"variables,omitempty"`
	RunID           string            `json:"run_id"`
	DeliveryID      string            `json:"delivery_id"`
	Event           string            `json:"event,omitempty"`
	ReceivedAt      time.Time         `json:"received_at"`
	Payload         json.RawMessage   `json:"payload"`
	RouteSelections map[string]string `json:"route_selections,omitempty"`
	StepID          string            `json:"step_id,omitempty"`
}

// WebhookRunMetadata is bounded delivery context for history; it never includes
// the full payload or authentication secrets.
type WebhookRunMetadata struct {
	TriggerName string    `json:"trigger_name"`
	DeliveryID  string    `json:"delivery_id"`
	Event       string    `json:"event,omitempty"`
	ReceivedAt  time.Time `json:"received_at"`
	CommitSHA   string    `json:"commit_sha,omitempty"`
	Component   string    `json:"component,omitempty"`
	Env         string    `json:"env,omitempty"`
	DeployedAt  string    `json:"deployed_at,omitempty"`
}

func webhookRunMetadata(sctx *ScheduleContext) *WebhookRunMetadata {
	if sctx == nil || sctx.WebhookInput == nil {
		return nil
	}
	metadata := &WebhookRunMetadata{TriggerName: sctx.Schedule.Name, DeliveryID: sctx.WebhookInput.DeliveryID, Event: sctx.WebhookInput.Event, ReceivedAt: sctx.WebhookInput.ReceivedAt}
	var deploy struct {
		CommitSHA  string `json:"commit_sha"`
		Component  string `json:"component"`
		Env        string `json:"env"`
		DeployedAt string `json:"deployed_at"`
	}
	if json.Unmarshal(sctx.WebhookInput.Payload, &deploy) != nil {
		return metadata
	}
	sha := strings.TrimSpace(deploy.CommitSHA)
	if len(sha) >= 7 && len(sha) <= 64 {
		validSHA := true
		for _, digit := range sha {
			if !((digit >= '0' && digit <= '9') || (digit >= 'A' && digit <= 'F') || (digit >= 'a' && digit <= 'f')) {
				validSHA = false
				break
			}
		}
		if validSHA {
			metadata.CommitSHA = sha
		}
	}
	if component := strings.TrimSpace(deploy.Component); len(component) <= 80 && !strings.ContainsAny(component, "\r\n") {
		metadata.Component = component
	}
	if env := strings.TrimSpace(deploy.Env); len(env) <= 80 && !strings.ContainsAny(env, "\r\n") {
		metadata.Env = env
	}
	if deployedAt := strings.TrimSpace(deploy.DeployedAt); len(deployedAt) <= 64 {
		if _, err := time.Parse(time.RFC3339Nano, deployedAt); err == nil {
			metadata.DeployedAt = deployedAt
		}
	}
	return metadata
}

type webhookRouteOption struct {
	StepID    string `json:"step_id"`
	StepTitle string `json:"step_title"`
	RouteID   string `json:"route_id"`
	RouteName string `json:"route_name"`
}

type workflowWebhookResponse struct {
	StepID           string                          `json:"step_id,omitempty"`
	InputMode        string                          `json:"input_mode,omitempty"`
	AllowedVariables []string                        `json:"allowed_variables,omitempty"`
	PayloadMappings  *WorkflowWebhookPayloadMappings `json:"payload_mappings,omitempty"`
	ID               string                          `json:"id"`
	Name             string                          `json:"name"`
	Enabled          bool                            `json:"enabled"`
	AuthMode         string                          `json:"auth_mode"`
	Path             string                          `json:"path"`
	RouteSelections  map[string]string               `json:"route_selections"`
	GroupNames       []string                        `json:"group_names"`
	MaxConcurrency   int                             `json:"max_concurrency"`
	Secret           string                          `json:"secret,omitempty"`
	Kind             string                          `json:"kind,omitempty"`
	Caller           *triggerCaller                  `json:"caller,omitempty"`
	Function         *WorkflowFunctionSpec           `json:"function,omitempty"`
	Gmail            *WorkflowGmailTriggerConfig     `json:"gmail,omitempty"`
}

type workflowWebhookRequest struct {
	StepID           *string                         `json:"step_id,omitempty"`
	InputMode        string                          `json:"input_mode,omitempty"`
	AllowedVariables []string                        `json:"allowed_variables,omitempty"`
	PayloadMappings  *WorkflowWebhookPayloadMappings `json:"payload_mappings,omitempty"`
	WorkspacePath    string                          `json:"workspace_path"`
	Name             string                          `json:"name"`
	Enabled          bool                            `json:"enabled"`
	AuthMode         string                          `json:"auth_mode"`
	RouteSelections  map[string]string               `json:"route_selections"`
	GroupNames       []string                        `json:"group_names"`
	RotateSecret     bool                            `json:"rotate_secret"`
	Kind             string                          `json:"kind,omitempty"`
	Caller           *triggerCaller                  `json:"caller,omitempty"`
	Function         *WorkflowFunctionSpec           `json:"function,omitempty"`
}

// IsInternalTrigger reports whether the schedule is invokable only through
// internal dispatch (no public URL): a caller binding, function, or Gmail source.
func (s WorkflowSchedule) IsInternalTrigger() bool {
	return s.ScheduleType == "webhook" && (isInternalTriggerKind(s.Kind) || isFunctionTriggerKind(s.Kind) || s.IsGmailTrigger())
}

func validateWebhookSchedule(s WorkflowSchedule) error {
	if s.ScheduleType != "webhook" {
		if s.Webhook != nil {
			return errors.New("webhook configuration requires schedule_type=webhook")
		}
		return nil
	}
	if kind := strings.TrimSpace(s.Kind); kind != "" && !isInternalTriggerKind(kind) && !isFunctionTriggerKind(kind) && !s.IsGmailTrigger() {
		return errors.New("webhook kind must be internal, function or gmail")
	}
	if s.Gmail != nil && !s.IsGmailTrigger() {
		return errors.New("Gmail settings require kind=gmail")
	}
	if s.IsGmailTrigger() {
		if s.Gmail == nil || s.Gmail.ConnectionID == "" || s.Gmail.Address == "" || s.Caller != nil || s.Function != nil || s.Webhook == nil || s.Webhook.EncryptedSecret != "" || s.Webhook.AuthMode != "" {
			return errors.New("Gmail triggers require a mailbox binding and issue no webhook secret")
		}
		if _, err := gmailinbound.NormalizeFilters(s.Gmail.Filters); err != nil {
			return err
		}
		if _, err := gmailinbound.NormalizeRules(s.Gmail.Rules, true); err != nil {
			return err
		}
		if len(s.Gmail.Rules) > 0 && (s.Webhook.InputMode != "raw" || s.Webhook.PayloadMappings != nil || len(s.Webhook.AllowedVariables) > 0) {
			return errors.New("Gmail rules use saved bindings and raw email input, never payload-selected routing")
		}
	} else if isFunctionTriggerKind(s.Kind) {
		if err := validateWorkflowFunctionSpec(s.Function); err != nil {
			return err
		}
		if s.Caller != nil {
			return errors.New("a function trigger has no caller stamp; limit callers with function.allowed_callers")
		}
		if s.Webhook != nil && strings.TrimSpace(s.Webhook.EncryptedSecret) != "" {
			return errors.New("function triggers issue no secret")
		}
	} else if s.Function != nil {
		return errors.New("function settings require kind=function")
	} else if isInternalTriggerKind(s.Kind) {
		if err := validateAnyTriggerCaller(s.Caller); err != nil {
			return err
		}
		if s.Webhook != nil && strings.TrimSpace(s.Webhook.EncryptedSecret) != "" {
			return errors.New("internal triggers issue no secret")
		}
	} else {
		if s.Webhook == nil || s.Webhook.EncryptedSecret == "" {
			return errors.New("Create API triggers with manage_workflow_webhook in Builder chat to generate a secret")
		}
		if s.Webhook.AuthMode != "bearer" && s.Webhook.AuthMode != "github" {
			return errors.New("webhook auth_mode must be bearer or github")
		}
	}
	if s.CronExpression != "" || len(s.CalendarItems) > 0 || len(s.TriggerPayload) > 0 || len(s.Messages) > 0 || s.Query != "" || s.PulseReviewOnly || s.ShouldResumePrevious() {
		return errors.New("API triggers accept delivery input only; clock settings, request overrides, messages, and session resume are not supported")
	}
	if (s.CollisionPolicy != "" && s.CollisionPolicy != "skip") || len(scheduleDependencyIDs(s)) > 0 {
		return errors.New("API triggers return a retryable busy response; schedule queues and dependencies are not supported")
	}
	if s.WorkshopMode != "run" {
		return errors.New("API triggers require workshop_mode=run")
	}
	return nil
}

func webhookAAD(workflowID, id string) []byte {
	return []byte("workflow-webhook:" + workflowID + ":" + id)
}
func webhookInputPath(workspacePath, runID string) string {
	return workspacePath + "/webhooks/deliveries/" + runID + ".json"
}
func webhookRunInputInstruction(path string) string {
	return stepworkflow.WebhookInputInstruction(path)
}

func workflowWebhookDTO(s WorkflowSchedule) workflowWebhookResponse {
	authMode := ""
	if s.Webhook != nil {
		authMode = s.Webhook.AuthMode
	}
	path := "/api/hooks/workflow/" + s.ID
	if s.IsInternalTrigger() {
		path = ""
	}
	out := workflowWebhookResponse{ID: s.ID, Name: s.Name, Enabled: s.Enabled, AuthMode: authMode, Path: path, RouteSelections: s.RouteSelections, GroupNames: s.GroupNames, MaxConcurrency: maxWebhookConcurrency, Kind: normalizeTriggerKind(s.Kind), Caller: s.Caller, Function: s.Function}
	out.Gmail = s.Gmail
	if s.Webhook != nil {
		out.StepID = s.Webhook.StepID
		out.InputMode = s.Webhook.InputMode
		out.AllowedVariables = s.Webhook.AllowedVariables
		out.PayloadMappings = s.Webhook.PayloadMappings
	}
	return out
}

func workflowWebhookRoutes(ctx context.Context, workspacePath string) ([]webhookRouteOption, error) {
	plan, err := readPlanFromWorkspace(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	options := []webhookRouteOption{}
	// Only top-level deterministic switches can be selected by run_full_workflow.
	for _, step := range plan.Steps {
		if step == nil {
			continue
		}
		if sw, ok := step.(interface {
			GetRoutes() []stepworkflow.RoutingRoute
		}); ok {
			for _, route := range sw.GetRoutes() {
				options = append(options, webhookRouteOption{StepID: step.GetID(), StepTitle: step.GetTitle(), RouteID: route.RouteID, RouteName: route.RouteName})
			}
		}
	}
	return options, nil
}

func validateWebhookRoutes(ctx context.Context, workspacePath string, selections map[string]string) error {
	options, err := workflowWebhookRoutes(ctx, workspacePath)
	if err != nil {
		return fmt.Errorf("load API trigger routes: %w", err)
	}
	for stepID, routeID := range selections {
		found := false
		for _, option := range options {
			if option.StepID == stepID && option.RouteID == routeID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("API trigger route %q on step %q no longer exists", routeID, stepID)
		}
	}
	return nil
}

func webhookMappingTargets(mapping WorkflowWebhookValueMapping) ([]string, error) {
	source := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(mapping.Source), "$."), ".")
	if source == "" {
		return nil, errors.New("payload mapping source is required")
	}
	for _, segment := range strings.Split(source, ".") {
		if strings.TrimSpace(segment) == "" {
			return nil, fmt.Errorf("invalid payload mapping source %q", mapping.Source)
		}
	}
	if len(mapping.Values) == 0 {
		return nil, fmt.Errorf("payload mapping %q requires at least one value", mapping.Source)
	}
	targets := make([]string, 0, len(mapping.Values)+1)
	for sourceValue, target := range mapping.Values {
		if sourceValue == "" || strings.TrimSpace(target) == "" {
			return nil, fmt.Errorf("payload mapping %q contains an empty source value or target", mapping.Source)
		}
		targets = append(targets, strings.TrimSpace(target))
	}
	if fallback := strings.TrimSpace(mapping.Default); fallback != "" {
		targets = append(targets, fallback)
	}
	return targets, nil
}

func validateWebhookPayloadMappings(ctx context.Context, workspacePath string, cfg *WorkflowWebhookConfig, groups []string, staticRoutes map[string]string) error {
	if cfg == nil || cfg.PayloadMappings == nil {
		return nil
	}
	mappings := cfg.PayloadMappings
	if cfg.InputMode == "envelope" {
		return errors.New("payload_mappings require input_mode=raw")
	}
	if mappings.Step != nil && (cfg.StepID != "" || len(staticRoutes) > 0 || len(mappings.Routes) > 0) {
		return errors.New("a payload step mapping cannot be combined with a fixed step or route selections")
	}
	if cfg.StepID != "" && len(mappings.Routes) > 0 {
		return errors.New("payload route mappings cannot be combined with a fixed step")
	}
	if mappings.Group != nil {
		targets, err := webhookMappingTargets(*mappings.Group)
		if err != nil {
			return fmt.Errorf("group mapping: %w", err)
		}
		for _, group := range targets {
			if !slices.Contains(groups, group) {
				return fmt.Errorf("group mapping target %q is not allowed by this trigger", group)
			}
		}
	}
	for stepID, mapping := range mappings.Routes {
		stepID = strings.TrimSpace(stepID)
		if stepID == "" {
			return errors.New("payload route mapping requires a routing or branch step ID")
		}
		if _, exists := staticRoutes[stepID]; exists {
			return fmt.Errorf("payload route mapping for step %q conflicts with its fixed route selection", stepID)
		}
		targets, err := webhookMappingTargets(mapping)
		if err != nil {
			return fmt.Errorf("route mapping for step %q: %w", stepID, err)
		}
		for _, routeID := range targets {
			if err := validateWebhookRoutes(ctx, workspacePath, map[string]string{stepID: routeID}); err != nil {
				return err
			}
		}
	}
	if mappings.Step != nil {
		targets, err := webhookMappingTargets(*mappings.Step)
		if err != nil {
			return fmt.Errorf("step mapping: %w", err)
		}
		for _, stepID := range targets {
			if err := validateWebhookTarget(ctx, workspacePath, stepID, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

var workflowWebhookConfigMu sync.Mutex

func WorkflowWebhookRoutes(router *mux.Router, svc *SchedulerService) {
	router.HandleFunc("/api/workflow-webhooks", svc.listWorkflowWebhooks).Methods("GET")
	router.HandleFunc("/api/workflow-webhooks", requireWorkflowWriteAccess(svc.saveWorkflowWebhook)).Methods("POST")
	router.HandleFunc("/api/workflow-webhooks/{id}", requireWorkflowWriteAccess(svc.saveWorkflowWebhook)).Methods("PUT")
	router.HandleFunc("/api/workflow-webhooks/{id}", requireWorkflowWriteAccess(svc.deleteWorkflowWebhook)).Methods("DELETE")
	router.HandleFunc("/api/workflow-webhooks/{id}/runs/{run}/payload", svc.getWorkflowWebhookPayload).Methods("GET")
	receiver := webhookReceiver{find: findScheduleByIDAny, start: svc.triggerSavedSchedule, existing: svc.existingWebhookRun}
	router.HandleFunc("/api/hooks/workflow/{id}", receiver.receive).Methods("POST")
	router.HandleFunc("/api/hooks/workflow/{id}/runs/{run}", svc.pollWebhookRun).Methods("GET")
	router.HandleFunc("/api/hooks/workflow/{id}/runs/{run}/artifact", svc.downloadWebhookArtifact).Methods("GET", "HEAD")
}

func (s *SchedulerService) getWorkflowWebhookPayload(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	runID := mux.Vars(r)["run"]
	result, err := findScheduleByIDAny(r.Context(), id)
	if err != nil || result.Manifest == nil || result.Index < 0 || result.Index >= len(result.Manifest.Schedules) {
		http.Error(w, "webhook not found", http.StatusNotFound)
		return
	}
	if !requireWorkflowVisible(w, r, result.WorkspacePath) {
		return
	}
	schedule := result.Manifest.Schedules[result.Index]
	if schedule.ScheduleType != "webhook" {
		http.Error(w, "webhook not found", http.StatusNotFound)
		return
	}

	runs, err := ReadScheduleRuns(r.Context(), result.WorkspacePath)
	if err != nil {
		http.Error(w, "could not read webhook history", http.StatusInternalServerError)
		return
	}
	found := false
	for _, run := range runs {
		if run.ID == runID && run.ScheduleID == id && run.Webhook != nil {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "webhook run not found", http.StatusNotFound)
		return
	}

	content, exists, err := readFileFromWorkspace(r.Context(), webhookInputPath(result.WorkspacePath, runID))
	if err != nil {
		http.Error(w, "could not read webhook payload", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "webhook payload is no longer available", http.StatusNotFound)
		return
	}
	var delivery WorkflowWebhookDelivery
	if err := json.Unmarshal([]byte(content), &delivery); err != nil || !json.Valid(delivery.Payload) {
		http.Error(w, "stored webhook payload is invalid", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"raw_payload": string(delivery.Payload)})
}

func (s *SchedulerService) listWorkflowWebhooks(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("workspace_path")
	if path == "" {
		http.Error(w, "workspace_path is required", 400)
		return
	}
	if currentUserWorkflowAccess(r, path) == WorkflowAccessNone {
		writeWorkflowPermissionDenied(w, "read")
		return
	}
	manifest, found, err := ReadWorkflowManifest(r.Context(), path)
	if err != nil || !found {
		http.Error(w, "workflow not found", 404)
		return
	}
	hooks := []workflowWebhookResponse{}
	for _, sched := range manifest.Schedules {
		if sched.ScheduleType == "webhook" {
			hooks = append(hooks, workflowWebhookDTO(sched))
		}
	}
	routes, routeErr := workflowWebhookRoutes(r.Context(), path)
	routeError := ""
	if routeErr != nil {
		routeError = routeErr.Error()
		routes = []webhookRouteOption{}
	}
	steps, _ := workflowWebhookSteps(r.Context(), path)
	groups := []string{}
	variableNames := []string{}
	content, exists, err := readFileFromWorkspace(r.Context(), path+"/variables/variables.json")
	if err == nil && exists {
		var vars VariablesManifest
		if json.Unmarshal([]byte(content), &vars) == nil {
			for _, v := range vars.Variables {
				variableNames = append(variableNames, v.Name)
			}
			for _, g := range vars.Groups {
				groups = append(groups, g.Name)
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"triggers": hooks, "routes": routes, "groups": groups, "declared_variables": variableNames, "route_error": routeError, "steps": steps})
}

func (s *SchedulerService) saveWorkflowWebhook(w http.ResponseWriter, r *http.Request) {
	var req workflowWebhookRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req) != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if req.WorkspacePath == "" {
		http.Error(w, "workspace_path is required", 400)
		return
	}
	if strings.EqualFold(strings.TrimSpace(req.Kind), "gmail") {
		http.Error(w, "Use manage_gmail_trigger in Builder chat to configure Gmail", 400)
		return
	}
	if !requireWorkflowOwner(w, r, req.WorkspacePath) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "name is required", 400)
		return
	}
	if !isInternalTriggerKind(req.Kind) && !isFunctionTriggerKind(req.Kind) && req.AuthMode != "bearer" && req.AuthMode != "github" {
		http.Error(w, "auth_mode must be bearer or github", 400)
		return
	}
	groups := normalizeScheduleGroupNames(req.GroupNames)
	var err error
	if req.Enabled {
		groups, err = validateScheduleGroupNamesForWorkspace(r.Context(), req.WorkspacePath, groups)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	workflowWebhookConfigMu.Lock()
	defer workflowWebhookConfigMu.Unlock()
	manifest, found, err := ReadWorkflowManifest(r.Context(), req.WorkspacePath)
	if err != nil || !found {
		http.Error(w, "workflow not found", 404)
		return
	}
	id := mux.Vars(r)["id"]
	index := -1
	for i, sched := range manifest.Schedules {
		if sched.ID == id && sched.ScheduleType == "webhook" {
			index = i
			break
		}
	}
	if id != "" && index < 0 {
		http.Error(w, "API trigger not found", 404)
		return
	}
	if index >= 0 && manifest.Schedules[index].IsGmailTrigger() {
		http.Error(w, "Use manage_gmail_trigger in Builder chat to configure Gmail", 400)
		return
	}
	cfg := &WorkflowWebhookConfig{AuthMode: req.AuthMode}
	secret := ""
	effectiveKind := req.Kind
	caller := req.Caller
	if index >= 0 {
		if manifest.Schedules[index].Webhook != nil {
			*cfg = *manifest.Schedules[index].Webhook
		}
		if strings.TrimSpace(req.Kind) == "" {
			effectiveKind = manifest.Schedules[index].Kind
		}
		if caller == nil {
			caller = manifest.Schedules[index].Caller
		}
	}
	function := isFunctionTriggerKind(effectiveKind)
	spec := req.Function
	if function && spec == nil && index >= 0 {
		spec = manifest.Schedules[index].Function
	}
	internal := isInternalTriggerKind(effectiveKind) || function
	if internal {
		cfg.AuthMode = ""
		cfg.EncryptedSecret = ""
	}
	if index < 0 {
		id = uuid.NewString()
	}
	if !internal && (index < 0 || req.RotateSecret || cfg.AuthMode != req.AuthMode || cfg.EncryptedSecret == "") {
		random := make([]byte, 32)
		if _, err = rand.Read(random); err == nil {
			secret = hex.EncodeToString(random)
			cfg.EncryptedSecret, err = encryptSecretValueWithAAD(secret, webhookAAD(manifest.ID, id))
		}
		if err != nil {
			http.Error(w, "cannot generate trigger secret", 500)
			return
		}
	}
	if req.StepID != nil {
		cfg.StepID = strings.TrimSpace(*req.StepID)
	}
	if cfg.StepID != "" && len(req.RouteSelections) != 0 {
		http.Error(w, "choose a step or route selections, not both", 400)
		return
	}
	if req.Enabled {
		if err := validateWebhookTarget(r.Context(), req.WorkspacePath, cfg.StepID, req.RouteSelections); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	if !internal {
		cfg.AuthMode = req.AuthMode
	}
	if req.InputMode != "" && req.InputMode != "raw" && req.InputMode != "envelope" {
		http.Error(w, "input_mode must be raw or envelope", 400)
		return
	}
	if req.InputMode != "" {
		cfg.InputMode = req.InputMode
	}
	if req.AllowedVariables != nil {
		cfg.AllowedVariables = req.AllowedVariables
	}
	if req.PayloadMappings != nil {
		if req.PayloadMappings.Group == nil && len(req.PayloadMappings.Routes) == 0 && req.PayloadMappings.Step == nil {
			cfg.PayloadMappings = nil
		} else {
			cfg.PayloadMappings = req.PayloadMappings
		}
	}
	if err := validateWebhookVariableNames(r.Context(), req.WorkspacePath, cfg.AllowedVariables); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := validateWebhookPayloadMappings(r.Context(), req.WorkspacePath, cfg, groups, req.RouteSelections); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if function {
		if err := validateWorkflowFunctionSpec(spec); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := validateWebhookVariableNames(r.Context(), req.WorkspacePath, workflowFunctionInputNames(spec)); err != nil {
			http.Error(w, "function inputs must be declared, non-secret workflow variables: "+err.Error(), 400)
			return
		}
		for _, other := range manifest.Schedules {
			if other.ID != id && other.IsFunctionTrigger() && other.Function.Name == spec.Name {
				http.Error(w, fmt.Sprintf("this workflow already has a function named %q", spec.Name), 400)
				return
			}
		}
		caller = nil
	} else if internal {
		if err := validateAnyTriggerCaller(caller); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if strings.EqualFold(strings.TrimSpace(caller.Type), triggerCallerWorkflow) {
			// workflow→workflow: the caller must be a workflow the requester
			// can open, and never this workflow itself.
			callerPath, _, err := findWorkflowManifestByID(r.Context(), caller.ID)
			if err != nil {
				http.Error(w, "caller workflow not found", 400)
				return
			}
			if manifest != nil && strings.TrimSpace(caller.ID) == strings.TrimSpace(manifest.ID) {
				http.Error(w, "a workflow cannot bind a trigger to itself", 400)
				return
			}
			if _, err := authorizeWorkflowContextPaths(r.Context(), []string{callerPath}); err != nil {
				http.Error(w, "caller workflow is unavailable or access denied", 400)
				return
			}
		} else if s.api == nil || s.api.productSchedules == nil || !s.api.productSchedules.crewProjectExists(r.Context(), productWorkspaceUserID(r.Context()), caller.ProfileID, caller.ID) {
			http.Error(w, "caller crew project not found", 400)
			return
		}
	} else {
		caller = nil
	}
	sched := WorkflowSchedule{ID: id, Name: strings.TrimSpace(req.Name), ScheduleType: "webhook", Timezone: "UTC", Enabled: req.Enabled, RouteSelections: req.RouteSelections, GroupNames: groups, Mode: "workshop", WorkshopMode: "run", CollisionPolicy: "skip", Webhook: cfg, Kind: normalizeTriggerKind(effectiveKind), Caller: caller, PulseMode: "off", PulseModeReason: "Webhook deliveries skip Pulse, backup and publish."}
	if function {
		sched.Function = spec
	}
	if index >= 0 {
		sched.Description = manifest.Schedules[index].Description
		sched.ExecutionMode = manifest.Schedules[index].ExecutionMode
		manifest.Schedules[index] = sched
	} else {
		manifest.Schedules = append(manifest.Schedules, sched)
	}
	if err = ValidateManifest(manifest); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err = WriteWorkflowManifest(r.Context(), req.WorkspacePath, manifest); err != nil {
		http.Error(w, "cannot save API trigger", 500)
		return
	}
	s.InvalidateWorkflowManifestCache()
	if err = s.LoadSchedule(buildScheduleContext(req.WorkspacePath, manifest, sched)); err != nil {
		scheduleLogf("[WEBHOOK] Failed to register trigger %s: %v", id, err)
	}
	response := workflowWebhookDTO(sched)
	response.Secret = secret
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if index < 0 {
		w.WriteHeader(http.StatusCreated)
	}
	_ = json.NewEncoder(w).Encode(response)
}

func (s *SchedulerService) deleteWorkflowWebhook(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("workspace_path")
	if path == "" {
		http.Error(w, "workspace_path is required", 400)
		return
	}
	if !requireWorkflowOwner(w, r, path) {
		return
	}
	workflowWebhookConfigMu.Lock()
	defer workflowWebhookConfigMu.Unlock()
	manifest, found, err := ReadWorkflowManifest(r.Context(), path)
	if err != nil || !found {
		http.Error(w, "workflow not found", 404)
		return
	}
	id := mux.Vars(r)["id"]
	for i, sched := range manifest.Schedules {
		if sched.ID != id || sched.ScheduleType != "webhook" {
			continue
		}
		if sched.IsGmailTrigger() {
			http.Error(w, "Use manage_gmail_trigger in Builder chat to disable Gmail", 400)
			return
		}
		manifest.Schedules = append(manifest.Schedules[:i], manifest.Schedules[i+1:]...)
		if err = WriteWorkflowManifest(r.Context(), path, manifest); err != nil {
			http.Error(w, "cannot delete API trigger", 500)
			return
		}
		s.InvalidateWorkflowManifestCache()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Error(w, "API trigger not found", 404)
}

func verifyWebhookRequest(cfg *WorkflowWebhookConfig, secret string, r *http.Request, body []byte) bool {
	if cfg == nil || secret == "" {
		return false
	}
	switch cfg.AuthMode {
	case "bearer":
		supplied := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if supplied == r.Header.Get("Authorization") {
			return false
		}
		return subtle.ConstantTimeCompare([]byte(supplied), []byte(secret)) == 1
	case "github":
		signature := r.Header.Get("X-Hub-Signature-256")
		if !strings.HasPrefix(signature, "sha256=") {
			return false
		}
		supplied, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
		if err != nil {
			return false
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		return hmac.Equal(supplied, mac.Sum(nil))
	}
	return false
}

type webhookReceiver struct {
	find     func(context.Context, string) (*ScheduleSearchResult, error)
	start    func(string, string, string, *WorkflowWebhookDelivery) (string, error)
	existing func(context.Context, string) (schedulerstate.Run, error)
}

func webhookDeliveryRunID(workflowID, id, deliveryID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(workflowID+"\x00"+id+"\x00"+deliveryID)).String()
}

func (s *SchedulerService) existingWebhookRun(ctx context.Context, runID string) (schedulerstate.Run, error) {
	s.stateStoreMu.RLock()
	defer s.stateStoreMu.RUnlock()
	if s.stateStore == nil {
		return schedulerstate.Run{}, errors.New("API trigger run store is unavailable")
	}
	return s.stateStore.GetRun(ctx, runID)
}

func (receiver webhookReceiver) receive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id := mux.Vars(r)["id"]
	if _, err := uuid.Parse(id); err != nil {
		http.Error(w, "API trigger not found", 404)
		return
	}
	result, err := receiver.find(r.Context(), id)
	if err != nil {
		http.Error(w, "API trigger not found", 404)
		return
	}
	sched := result.Manifest.Schedules[result.Index]
	if sched.ScheduleType != "webhook" || sched.Webhook == nil || sched.IsInternalTrigger() {
		http.Error(w, "API trigger not found", 404)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes))
	if err != nil {
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) {
			http.Error(w, "payload exceeds 1 MiB", 413)
		} else {
			http.Error(w, "cannot read payload", 400)
		}
		return
	}
	secret, err := decryptSecretValueWithAAD(sched.Webhook.EncryptedSecret, webhookAAD(result.Manifest.ID, id))
	if err != nil || !verifyWebhookRequest(sched.Webhook, secret, r, body) {
		http.Error(w, "invalid webhook credentials", 401)
		return
	}
	if !sched.Enabled {
		http.Error(w, "API trigger is disabled", 410)
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (contentType != "application/json" && !strings.HasSuffix(contentType, "+json")) {
		http.Error(w, "Content-Type must be application/json", 415)
		return
	}
	if !json.Valid(body) {
		http.Error(w, "invalid JSON payload", 400)
		return
	}
	if sched.Webhook.AuthMode == "github" && r.Header.Get("X-GitHub-Event") == "ping" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"pong"}`))
		return
	}
	deliveryID := r.Header.Get("Idempotency-Key")
	event := r.Header.Get("X-Webhook-Event")
	if sched.Webhook.AuthMode == "github" {
		deliveryID = r.Header.Get("X-GitHub-Delivery")
		event = r.Header.Get("X-GitHub-Event")
	}
	delivery, err := receiver.deliver(r.Context(), result.Manifest.ID, result.WorkspacePath, sched, deliveryID, event, body)
	if err != nil {
		var invalid *invalidWebhookDeliveryError
		switch {
		case errors.As(err, &invalid):
			http.Error(w, invalid.msg, 400)
		case errors.Is(err, ErrWebhookRunStoreMissing):
			http.Error(w, "run storage is unavailable", 503)
		default:
			w.Header().Set("Retry-After", "30")
			http.Error(w, "workflow could not start; retry the delivery or check API trigger configuration", http.StatusServiceUnavailable)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if delivery.Duplicate {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": delivery.Status, "run_id": delivery.RunID, "duplicate": true, "status_url": webhookStatusPath(id, delivery.RunID)})
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "accepted", "run_id": delivery.RunID, "delivery_id": delivery.DeliveryID, "status_url": webhookStatusPath(id, delivery.RunID)})
}

// invalidWebhookDeliveryError carries a delivery validation failure with the
// exact message the public endpoint has always returned.
type invalidWebhookDeliveryError struct{ msg string }

func (e *invalidWebhookDeliveryError) Error() string { return e.msg }

// deliver runs the shared duplicate-check→validate→start pipeline for one
// workflow trigger delivery, including the concurrent-retry recheck. Both the
// public HTTP endpoint and internal dispatch funnel through here.
func (receiver webhookReceiver) deliver(ctx context.Context, manifestID, workspacePath string, sched WorkflowSchedule, deliveryID, event string, body []byte) (internalTriggerDeliveryResult, error) {
	if len(deliveryID) > 256 || len(event) > 256 {
		return internalTriggerDeliveryResult{}, &invalidWebhookDeliveryError{"delivery ID and event must be at most 256 bytes"}
	}
	if strings.TrimSpace(deliveryID) == "" {
		deliveryID = uuid.NewString()
	}
	runID := webhookDeliveryRunID(manifestID, sched.ID, deliveryID)
	lookupExisting := func() (internalTriggerDeliveryResult, bool, error) {
		run, lookupErr := receiver.existing(ctx, runID)
		if lookupErr == nil {
			return internalTriggerDeliveryResult{RunID: run.RunID, DeliveryID: deliveryID, Duplicate: true, Status: string(run.State)}, true, nil
		}
		if !errors.Is(lookupErr, schedulerstate.ErrRunNotFound) {
			return internalTriggerDeliveryResult{}, true, fmt.Errorf("%w: %w", ErrWebhookRunStoreMissing, lookupErr)
		}
		return internalTriggerDeliveryResult{}, false, nil
	}
	if result, done, err := lookupExisting(); done || err != nil {
		return result, err
	}
	input := &WorkflowWebhookDelivery{RunID: runID, DeliveryID: deliveryID, Event: event, ReceivedAt: time.Now().UTC(), Payload: append(json.RawMessage(nil), body...)}
	if err := resolveWebhookDeliveryOptions(sched, input); err != nil {
		return internalTriggerDeliveryResult{}, &invalidWebhookDeliveryError{err.Error()}
	}
	acceptedID, err := receiver.start(workspacePath, sched.ID, "", input)
	if err != nil {
		// A concurrent retry may have claimed this delivery since the first lookup.
		if result, done, lookupErr := lookupExisting(); done || lookupErr != nil {
			return result, lookupErr
		}
		return internalTriggerDeliveryResult{}, err
	}
	return internalTriggerDeliveryResult{RunID: acceptedID, DeliveryID: deliveryID, Status: "accepted"}, nil
}

// dispatchInternal invokes a workflow trigger from a Crew run without a
// secret or loopback HTTP. The manifest is passed in so the discovery step
// stays injectable; public triggers are rejected so this path can never
// bypass secret auth.
func (receiver webhookReceiver) dispatchInternal(ctx context.Context, workspacePath string, manifest *WorkflowManifest, triggerID string, call internalWorkflowTriggerCall) (internalTriggerDeliveryResult, error) {
	if err := checkInternalPayload(call.Payload); err != nil {
		return internalTriggerDeliveryResult{}, err
	}
	sched, err := findInternalWorkflowTrigger(manifest, triggerID)
	if err != nil {
		return internalTriggerDeliveryResult{}, err
	}
	if !sched.Caller.matchesAnyPresented(call.Caller) {
		return internalTriggerDeliveryResult{}, ErrInternalCallerMismatch
	}
	deliveryID := strings.TrimSpace(call.DeliveryID)
	if deliveryID == "" {
		deliveryID = uuid.NewString()
	}
	return receiver.deliver(ctx, manifest.ID, workspacePath, *sched, deliveryID, strings.TrimSpace(call.Event), call.Payload)
}

// findInternalWorkflowTrigger resolves a workflow trigger for internal
// callers: it must exist, be a webhook schedule, be internal, and be enabled.
// Caller authorization is left to the caller-facing method.
func findInternalWorkflowTrigger(manifest *WorkflowManifest, triggerID string) (*WorkflowSchedule, error) {
	if manifest == nil {
		return nil, ErrInternalTriggerNotFound
	}
	want := strings.TrimSpace(triggerID)
	for i := range manifest.Schedules {
		if strings.TrimSpace(manifest.Schedules[i].ID) != want {
			continue
		}
		sched := &manifest.Schedules[i]
		if sched.ScheduleType != "webhook" {
			return nil, ErrInternalTriggerNotFound
		}
		if !sched.IsInternalTrigger() {
			return nil, ErrInternalTriggerNotBound
		}
		if sched.IsGmailTrigger() {
			return nil, ErrInternalTriggerNotBound
		}
		if !sched.Enabled {
			return nil, ErrInternalTriggerDisabled
		}
		return sched, nil
	}
	return nil, ErrInternalTriggerNotFound
}

// dispatchInternalWorkflowTrigger discovers the workflow manifest and
// dispatches an internal trigger delivery from a Crew run.
func (s *SchedulerService) dispatchInternalWorkflowTrigger(ctx context.Context, call internalWorkflowTriggerCall) (internalTriggerDeliveryResult, error) {
	workspacePath, manifest, err := findWorkflowManifestByID(ctx, call.WorkflowID)
	if err != nil {
		return internalTriggerDeliveryResult{}, fmt.Errorf("%w: %w", ErrInternalTriggerNotFound, err)
	}
	receiver := webhookReceiver{start: s.triggerSavedSchedule, existing: s.existingWebhookRun}
	return receiver.dispatchInternal(ctx, workspacePath, manifest, call.TriggerID, call)
}
