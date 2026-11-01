package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
)

// Project entry points share a file contract. Product executors receive views
// of these records; no entry point is stored as a clock or a fake HTTP hook.
type projectFunctionDefinition struct {
	ID             string                      `json:"id"`
	Name           string                      `json:"name"`
	Description    string                      `json:"description,omitempty"`
	Enabled        bool                        `json:"enabled"`
	Action         projectactions.Action       `json:"action"`
	Input          json.RawMessage             `json:"input"`
	InputSchema    map[string]interface{}      `json:"input_schema,omitempty"`
	ResultSchema   map[string]interface{}      `json:"result_schema,omitempty"`
	InputMapping   *projectWebhookInputMapping `json:"input_mapping,omitempty"`
	AllowedCallers []triggerCaller             `json:"allowed_callers,omitempty"`
	CreatedBy      string                      `json:"created_by,omitempty"`
	CreatedAt      time.Time                   `json:"created_at,omitempty"`
	UpdatedAt      time.Time                   `json:"updated_at,omitempty"`
}

type projectFunctionsDocument struct {
	Functions []projectFunctionDefinition `json:"functions"`
}

type projectWebhookAuth struct {
	Mode            string `json:"mode"`
	EncryptedSecret string `json:"encrypted_secret,omitempty"`
}

type projectWebhookInputMapping struct {
	Mode      string                          `json:"mode,omitempty"`
	Variables []string                        `json:"variables,omitempty"`
	Payload   *WorkflowWebhookPayloadMappings `json:"payload,omitempty"`
}

type projectWebhookDefinition struct {
	ID           string                     `json:"id"`
	Name         string                     `json:"name"`
	Project      projectactions.Project     `json:"project"`
	Enabled      bool                       `json:"enabled"`
	Path         string                     `json:"path"`
	Action       projectactions.Action      `json:"action"`
	Input        json.RawMessage            `json:"input"`
	RunAs        string                     `json:"run_as"`
	Auth         projectWebhookAuth         `json:"auth"`
	InputMapping projectWebhookInputMapping `json:"input_mapping,omitempty"`
}

type projectWebhooksDocument struct {
	Webhooks []projectWebhookDefinition `json:"webhooks"`
}

func readProjectEntryFile(ctx context.Context, root, name string, document interface{}) error {
	raw, found, err := readFileFromWorkspace(ctx, path.Join(root, name))
	if err != nil || !found {
		return err
	}
	if err := json.Unmarshal([]byte(raw), document); err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	return nil
}

func writeProjectEntryFile(ctx context.Context, root, name string, document interface{}) error {
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	file := path.Join(root, name)
	previous, found, err := readFileFromWorkspace(ctx, file)
	if err != nil {
		return err
	}
	if found && strings.TrimSpace(previous) == strings.TrimSpace(string(raw)) {
		return nil
	}
	return writeFileToWorkspace(ctx, file, string(raw)+"\n")
}

func readProjectFunctionDefinitions(ctx context.Context, root string) ([]projectFunctionDefinition, error) {
	var doc projectFunctionsDocument
	if err := readProjectEntryFile(ctx, root, "functions.json", &doc); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, fn := range doc.Functions {
		if fn.ID == "" || fn.Name == "" || seen[fn.ID] {
			return nil, fmt.Errorf("functions need unique identities and names")
		}
		if fn.Action.Type != "run" && fn.Action.Type != "message" {
			return nil, fmt.Errorf("function %s needs a run or message action", fn.Name)
		}
		seen[fn.ID] = true
	}
	return doc.Functions, nil
}

func writeProjectFunctionDefinitions(ctx context.Context, root string, functions []projectFunctionDefinition) error {
	if functions == nil {
		functions = []projectFunctionDefinition{}
	}
	sort.Slice(functions, func(i, j int) bool { return functions[i].Name < functions[j].Name })
	return writeProjectEntryFile(ctx, root, "functions.json", projectFunctionsDocument{Functions: functions})
}

// The Goal execution adapter still owns variable, group and route validation.
// Its fixed inputs are separate from the caller's schema-checked function input.
func workflowFunctionDefinition(schedule WorkflowSchedule) (projectFunctionDefinition, error) {
	input, err := json.Marshal(scheduleExecutionInput{Messages: schedule.Messages, Groups: schedule.GroupNames, Routes: schedule.RouteSelections, Payload: schedule.TriggerPayload, TeamAuthorizedBy: schedule.TeamAuthorizedBy, TeamPolicyRevision: schedule.TeamPolicyRevision})
	if err != nil {
		return projectFunctionDefinition{}, err
	}
	if schedule.Function == nil {
		return projectFunctionDefinition{}, fmt.Errorf("function %s has no input contract", schedule.ID)
	}
	step := ""
	if schedule.Webhook != nil {
		step = schedule.Webhook.StepID
	}
	return projectFunctionDefinition{ID: schedule.ID, Name: schedule.Function.Name, Description: firstNonEmptyTrimmed(schedule.Function.Description, schedule.Name), Enabled: schedule.Enabled, Action: projectactions.Action{Type: "run", Step: step, Tab: schedule.ChatID}, Input: input, InputSchema: workflowFunctionInputSchema(schedule), ResultSchema: workflowFunctionResultSchema(), AllowedCallers: schedule.Function.AllowedCallers}, nil
}

func workflowFunctionExecutionView(fn projectFunctionDefinition) (WorkflowSchedule, error) {
	var input scheduleExecutionInput
	if err := json.Unmarshal(fn.Input, &input); err != nil {
		return WorkflowSchedule{}, err
	}
	spec := &WorkflowFunctionSpec{Name: fn.Name, Description: fn.Description, AllowedCallers: fn.AllowedCallers, InputSchema: fn.InputSchema}
	required := map[string]bool{}
	// JSON decoding and native authoring may use either of these slice types.
	switch values := fn.InputSchema["required"].(type) {
	case []interface{}:
		for _, value := range values {
			if name, ok := value.(string); ok {
				required[name] = true
			}
		}
	case []string:
		for _, name := range values {
			required[name] = true
		}
	}
	properties, _ := fn.InputSchema["properties"].(map[string]interface{})
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "group" && len(input.Groups) > 1 {
			continue
		}
		property, _ := properties[name].(map[string]interface{})
		kind, _ := property["type"].(string)
		description, _ := property["description"].(string)
		parameter := WorkflowFunctionInput{Name: name, Type: kind, Description: description, Required: required[name]}
		switch values := property["enum"].(type) {
		case []interface{}:
			for _, value := range values {
				if value, ok := value.(string); ok {
					parameter.Enum = append(parameter.Enum, value)
				}
			}
		case []string:
			parameter.Enum = append(parameter.Enum, values...)
		}
		spec.Inputs = append(spec.Inputs, parameter)
	}
	return WorkflowSchedule{ChatID: fn.Action.Tab, ID: fn.ID, Name: firstNonEmptyTrimmed(fn.Description, fn.Name), Enabled: fn.Enabled, ScheduleType: "webhook", Kind: triggerKindFunction, Function: spec, Webhook: &WorkflowWebhookConfig{StepID: fn.Action.Step}, Mode: "workshop", WorkshopMode: "run", GroupNames: input.Groups, RouteSelections: input.Routes, TriggerPayload: input.Payload, Messages: input.Messages, TeamAuthorizedBy: input.TeamAuthorizedBy, TeamPolicyRevision: input.TeamPolicyRevision}, nil
}

func crewFunctionDefinition(root string, fn crewFunction) (projectFunctionDefinition, error) {
	input, err := json.Marshal(scheduleExecutionInput{Messages: []string{fn.Instructions}})
	if err != nil {
		return projectFunctionDefinition{}, err
	}
	id := fn.TriggerID
	if id == "" {
		id = uuid.NewSHA1(uuid.NameSpaceOID, []byte(root+"\x00"+fn.Name)).String()
	}
	action := projectactions.Action{Type: "message", Tab: fn.tabName()}
	if fn.tabName() == "" {
		action.Conversation = "fresh"
	}
	return projectFunctionDefinition{ID: id, Name: fn.Name, Description: fn.Description, Enabled: true, Action: action, Input: input, InputSchema: fn.InputSchema, ResultSchema: fn.ResultSchema, CreatedBy: fn.CreatedBy, CreatedAt: fn.CreatedAt, UpdatedAt: fn.UpdatedAt}, nil
}

func crewFunctionExecutionView(record projectFunctionDefinition) (crewFunction, error) {
	var input scheduleExecutionInput
	if err := json.Unmarshal(record.Input, &input); err != nil {
		return crewFunction{}, err
	}
	execution := "tab"
	if record.Action.Conversation == "fresh" {
		execution = "fresh"
	}
	return crewFunction{Name: record.Name, Description: record.Description, Instructions: strings.Join(input.Messages, "\n\n"), InputSchema: record.InputSchema, ResultSchema: record.ResultSchema, Execution: execution, Agent: record.Action.Tab, TriggerID: record.ID, CreatedBy: record.CreatedBy, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, nil
}

func webhookExecutionConfig(record projectWebhookDefinition) *WorkflowWebhookConfig {
	return &WorkflowWebhookConfig{StepID: record.Action.Step, InputMode: record.InputMapping.Mode, AllowedVariables: record.InputMapping.Variables, PayloadMappings: record.InputMapping.Payload, AuthMode: record.Auth.Mode, EncryptedSecret: record.Auth.EncryptedSecret}
}

func workflowWebhookDefinition(project projectactions.Project, caller string, schedule WorkflowSchedule) (projectWebhookDefinition, error) {
	input, err := json.Marshal(scheduleExecutionInput{Messages: schedule.Messages, Groups: schedule.GroupNames, Routes: schedule.RouteSelections, Payload: schedule.TriggerPayload, TeamAuthorizedBy: schedule.TeamAuthorizedBy, TeamPolicyRevision: schedule.TeamPolicyRevision})
	if err != nil {
		return projectWebhookDefinition{}, err
	}
	if schedule.Webhook == nil {
		return projectWebhookDefinition{}, fmt.Errorf("webhook %s has no authentication settings", schedule.ID)
	}
	cfg := schedule.Webhook
	action := projectactions.Action{Type: "run", Step: cfg.StepID, Tab: schedule.ChatID}
	if len(schedule.Messages) > 0 {
		action.Type = "message"
	}
	return projectWebhookDefinition{ID: schedule.ID, Name: schedule.Name, Project: project, Enabled: schedule.Enabled, Path: "/api/hooks/" + schedule.ID, Action: action, Input: input, RunAs: caller, Auth: projectWebhookAuth{Mode: cfg.AuthMode, EncryptedSecret: cfg.EncryptedSecret}, InputMapping: projectWebhookInputMapping{Mode: cfg.InputMode, Variables: cfg.AllowedVariables, Payload: cfg.PayloadMappings}}, nil
}

func workflowWebhookExecutionView(record projectWebhookDefinition) (WorkflowSchedule, error) {
	var input scheduleExecutionInput
	if err := json.Unmarshal(record.Input, &input); err != nil {
		return WorkflowSchedule{}, err
	}
	mode := "run"
	if record.Action.Type == "message" {
		mode = "workshop"
	}
	return WorkflowSchedule{ChatID: record.Action.Tab, ID: record.ID, Name: record.Name, Enabled: record.Enabled, ScheduleType: "webhook", Mode: "workshop", WorkshopMode: mode, Webhook: webhookExecutionConfig(record), GroupNames: input.Groups, RouteSelections: input.Routes, TriggerPayload: input.Payload, Messages: input.Messages, RunAs: record.RunAs, TeamAuthorizedBy: input.TeamAuthorizedBy, TeamPolicyRevision: input.TeamPolicyRevision}, nil
}

func crewWebhookDefinition(project projectactions.Project, caller string, trigger productWebhookTrigger) (projectWebhookDefinition, error) {
	input, err := json.Marshal(scheduleExecutionInput{Messages: []string{trigger.Message}})
	if err != nil {
		return projectWebhookDefinition{}, err
	}
	if trigger.Webhook == nil {
		return projectWebhookDefinition{}, fmt.Errorf("webhook %s has no authentication settings", trigger.ID)
	}
	action := projectactions.Action{Type: "message"}
	if trigger.ownConversation() {
		action.Conversation = "isolated"
	}
	return projectWebhookDefinition{ID: trigger.ID, Name: trigger.Name, Project: project, Enabled: trigger.Enabled, Path: "/api/hooks/" + trigger.ID, Action: action, Input: input, RunAs: caller, Auth: projectWebhookAuth{Mode: trigger.Webhook.AuthMode, EncryptedSecret: trigger.Webhook.EncryptedSecret}}, nil
}

func crewWebhookExecutionView(record projectWebhookDefinition) (productWebhookTrigger, error) {
	var input scheduleExecutionInput
	if err := json.Unmarshal(record.Input, &input); err != nil {
		return productWebhookTrigger{}, err
	}
	return productWebhookTrigger{ID: record.ID, Name: record.Name, Enabled: record.Enabled, Message: strings.Join(input.Messages, "\n\n"), RunDestination: runDestination(record.Action.Conversation == "isolated"), Webhook: webhookExecutionConfig(record)}, nil
}

func internalFunctionName(id string) string { return "binding_" + strings.ReplaceAll(id, "-", "_") }

// Explicit bindings referenced by saved steps become callable records with the
// same ID and caller list. Their input mapping belongs to the target adapter.
func workflowBindingDefinition(schedule WorkflowSchedule) (projectFunctionDefinition, error) {
	input, err := json.Marshal(scheduleExecutionInput{Messages: schedule.Messages, Groups: schedule.GroupNames, Routes: schedule.RouteSelections, Payload: schedule.TriggerPayload, TeamAuthorizedBy: schedule.TeamAuthorizedBy, TeamPolicyRevision: schedule.TeamPolicyRevision})
	if err != nil {
		return projectFunctionDefinition{}, err
	}
	record := projectFunctionDefinition{ID: schedule.ID, Name: internalFunctionName(schedule.ID), Description: schedule.Name, Enabled: schedule.Enabled, Action: projectactions.Action{Type: "run", Tab: schedule.ChatID}, Input: input, InputSchema: map[string]interface{}{"type": "object"}, InputMapping: &projectWebhookInputMapping{}}
	if schedule.Caller != nil {
		record.AllowedCallers = []triggerCaller{*schedule.Caller}
	}
	if cfg := schedule.Webhook; cfg != nil {
		record.Action.Step = cfg.StepID
		record.InputMapping = &projectWebhookInputMapping{Mode: cfg.InputMode, Variables: cfg.AllowedVariables, Payload: cfg.PayloadMappings}
	}
	return record, nil
}

func crewBindingDefinition(trigger productWebhookTrigger) (projectFunctionDefinition, error) {
	input, err := json.Marshal(scheduleExecutionInput{Messages: []string{trigger.Message}})
	if err != nil {
		return projectFunctionDefinition{}, err
	}
	record := projectFunctionDefinition{ID: trigger.ID, Name: internalFunctionName(trigger.ID), Description: trigger.Name, Enabled: trigger.Enabled, Action: projectactions.Action{Type: "message"}, Input: input, InputSchema: map[string]interface{}{"type": "object"}, InputMapping: &projectWebhookInputMapping{}}
	if trigger.ownConversation() {
		record.Action.Conversation = "isolated"
	}
	if trigger.Caller != nil {
		record.AllowedCallers = []triggerCaller{*trigger.Caller}
	}
	return record, nil
}

func readProjectWebhookDefinitions(ctx context.Context, root string) ([]projectWebhookDefinition, error) {
	var doc projectWebhooksDocument
	if err := readProjectEntryFile(ctx, root, "webhooks.json", &doc); err != nil {
		return nil, err
	}
	return doc.Webhooks, nil
}

func writeProjectWebhookDefinitions(ctx context.Context, root string, records []projectWebhookDefinition) error {
	if records == nil {
		records = []projectWebhookDefinition{}
	}
	return writeProjectEntryFile(ctx, root, "webhooks.json", projectWebhooksDocument{Webhooks: records})
}

func loadWorkflowEntryActions(ctx context.Context, root string, manifest *WorkflowManifest) error {
	if isProductRuntimeManifestWorkspace(root) {
		return nil
	}
	hooks, err := readProjectWebhookDefinitions(ctx, root)
	if err != nil {
		return err
	}
	functions, err := readProjectFunctionDefinitions(ctx, root)
	if err != nil {
		return err
	}
	manifest.Schedules = nil
	for _, record := range hooks {
		kind := "goal"
		if manifest.Kind == "relay" {
			kind = "relay"
		}
		if record.Project.ID != manifest.ID || record.Project.Kind != kind {
			return fmt.Errorf("webhook project does not match its folder")
		}
		if manifest.crewExecutable != nil && (workflowExecutionOwnerUserID(manifest) == "" || record.RunAs != workflowExecutionOwnerUserID(manifest)) {
			return fmt.Errorf("webhook must run as its Crew owner")
		}
		owners := manifest.effectiveOwners()
		if (len(owners) == 0 && (record.RunAs == "" || record.RunAs != workflowExecutionOwnerUserID(manifest))) || (len(owners) > 0 && !containsID(owners, record.RunAs)) {
			return fmt.Errorf("webhook caller no longer owns this project")
		}
		view, err := workflowWebhookExecutionView(record)
		if err != nil {
			return err
		}
		manifest.Schedules = append(manifest.Schedules, view)
	}
	for _, record := range functions {
		view, err := workflowFunctionExecutionView(record)
		if err != nil {
			return err
		}
		if record.InputMapping != nil {
			view.Kind = triggerKindInternal
			view.Function = nil
			if len(record.AllowedCallers) != 1 {
				return fmt.Errorf("saved step function %s needs its bound caller", record.ID)
			}
			caller := record.AllowedCallers[0]
			view.Caller = &caller
			view.Webhook = &WorkflowWebhookConfig{StepID: record.Action.Step, InputMode: record.InputMapping.Mode, AllowedVariables: record.InputMapping.Variables, PayloadMappings: record.InputMapping.Payload}
		}
		manifest.Schedules = append(manifest.Schedules, view)
	}
	return nil
}

func saveWorkflowEntryActions(ctx context.Context, root string, manifest *WorkflowManifest) error {
	if isProductRuntimeManifestWorkspace(root) {
		return nil
	}
	kind := "goal"
	if manifest.Kind == "relay" {
		kind = "relay"
	}
	project := projectactions.Project{Kind: kind, ID: manifest.ID}
	var hooks []projectWebhookDefinition
	var functions []projectFunctionDefinition
	for _, schedule := range manifest.Schedules {
		if schedule.ScheduleType != "webhook" {
			continue
		}
		switch {
		case schedule.IsFunctionTrigger():
			record, err := workflowFunctionDefinition(schedule)
			if err != nil {
				return err
			}
			functions = append(functions, record)
		case schedule.IsInternalTrigger():
			record, err := workflowBindingDefinition(schedule)
			if err != nil {
				return err
			}
			functions = append(functions, record)
		default:
			record, err := workflowWebhookDefinition(project, workflowExecutionOwnerUserID(manifest), schedule)
			if err != nil {
				return err
			}
			hooks = append(hooks, record)
		}
	}
	if err := writeProjectFunctionDefinitions(ctx, root, functions); err != nil {
		return err
	}
	return writeProjectWebhookDefinitions(ctx, root, hooks)
}

func loadCrewEntryActions(ctx context.Context, root string, manifest *productProjectManifest) error {
	hooks, err := readProjectWebhookDefinitions(ctx, root)
	if err != nil {
		return err
	}
	functions, err := readProjectFunctionDefinitions(ctx, root)
	if err != nil {
		return err
	}
	manifest.Triggers = nil
	for _, record := range hooks {
		if record.Project.Kind != "crew" || record.Project.ID != manifest.ID {
			return fmt.Errorf("webhook project does not match its Crew")
		}
		// The registry decides who owns a Crew, never its manifest (PLAT-449).
		owner, registered := crewProjectOwnerID(root)
		if !registered {
			owner = manifest.OwnerID
		}
		if owner == "" && !IsMultiUserMode() {
			owner = GetDefaultUserID()
		}
		if owner == "" || record.RunAs != owner {
			return fmt.Errorf("webhook caller does not match its Crew owner")
		}
		view, err := crewWebhookExecutionView(record)
		if err != nil {
			return err
		}
		manifest.Triggers = append(manifest.Triggers, view)
	}
	for _, record := range functions {
		var input scheduleExecutionInput
		if err := json.Unmarshal(record.Input, &input); err != nil {
			return err
		}
		view := productWebhookTrigger{ID: record.ID, Name: firstNonEmptyTrimmed(record.Description, record.Name), Enabled: record.Enabled, Message: strings.Join(input.Messages, "\n\n"), Kind: triggerKindInternal, RunDestination: runDestinationIsolated, WorkspaceFunction: record.InputMapping == nil}
		if record.InputMapping != nil {
			if len(record.AllowedCallers) != 1 {
				return fmt.Errorf("saved step function %s needs its bound caller", record.ID)
			}
			caller := record.AllowedCallers[0]
			view.Caller = &caller
		}
		manifest.Triggers = append(manifest.Triggers, view)
	}
	return nil
}

func saveCrewEntryActions(ctx context.Context, root string, manifest productProjectManifest) error {
	existing, err := readProjectFunctionDefinitions(ctx, root)
	if err != nil {
		return err
	}
	var functions []projectFunctionDefinition
	for _, record := range existing {
		if record.InputMapping == nil {
			functions = append(functions, record)
		}
	}
	var hooks []projectWebhookDefinition
	owner, registered := crewProjectOwnerID(root)
	if !registered {
		owner = manifest.OwnerID
	}
	if owner == "" && !IsMultiUserMode() {
		owner = GetDefaultUserID()
	}
	for _, view := range manifest.Triggers {
		if view.WorkspaceFunction {
			continue
		} // generated views are never written as bindings
		if view.IsInternal() {
			record, err := crewBindingDefinition(view)
			if err != nil {
				return err
			}
			functions = append(functions, record)
			continue
		}
		record, err := crewWebhookDefinition(projectactions.Project{Kind: "crew", ID: manifest.ID}, owner, view)
		if err != nil {
			return err
		}
		hooks = append(hooks, record)
	}
	if err := writeProjectFunctionDefinitions(ctx, root, functions); err != nil {
		return err
	}
	return writeProjectWebhookDefinitions(ctx, root, hooks)
}
