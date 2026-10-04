package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// CreateCrewRequest is the validated input for server-side Crew creation from
// Builder chat: identity, starter brief, capability selections, wiring, and
// the idempotency scope.
type CreateCrewRequest struct {
	UserID       string
	WorkflowPath string
	ProfileID    string
	Title        string
	Icon         string
	Role         string
	Purpose      string
	// Purpose is the Crew's whole identity statement: it is stored as the
	// top-level project description (the slot the Identity panel reads) and
	// seeded into the starter brief. There is intentionally no separate
	// description field — one concept, one value.
	Instructions string
	// TemplateID selects a trusted first-party Crew Agent Playbook.
	// Its local skill and checklist are copied after the Crew is created; no
	// external account or recurring work is enabled by this selection.
	TemplateID string
	Skills     []string
	Servers    []string
	// Secrets and GlobalSecrets are references to existing authorized secret
	// records by name. Values are never accepted: the schema has no
	// value-bearing fields, so plaintext cannot enter by construction.
	Secrets       []string
	GlobalSecrets []string
	// Alias defaults to the slugified crew title (suffixed when reserved).
	Alias string
	// TriggerName defaults to "<title> trigger"; TriggerMessage falls back to
	// Instructions, then Purpose, and is required after fallback.
	TriggerName    string
	TriggerMessage string
	// StepID defaults to "crew-<selected alias>"; StepTitle defaults to the
	// crew title; StepInstruction is required: the workflow-specific
	// instruction is the Builder's core input for the step it adds next.
	StepID              string
	StepTitle           string
	StepInstruction     string
	ContextDependencies []string
	// IdempotencyKey scopes one Builder proposal. Retrying with the same key
	// and an unchanged proposal returns the existing crew instead of minting
	// a duplicate; a changed proposal under a claimed key is a conflict and
	// must use a new key.
	IdempotencyKey string
}

// CreatedCrewStepConfig is the exact crew-step configuration the Builder
// passes to add_step(type="crew") as the immediate follow-up, adding only
// placement (insert_after_step_id) and reason. The step is deliberately not
// written here: plan mutations flow through the gated tool path so schedule
// collision and phase policies apply.
type CreatedCrewStepConfig struct {
	StepID              string   `json:"step_id"`
	Title               string   `json:"title"`
	Description         string   `json:"description"`
	CrewProfileID       string   `json:"crew_profile_id"`
	CrewProjectID       string   `json:"crew_project_id"`
	TriggerID           string   `json:"trigger_id"`
	Instruction         string   `json:"instruction"`
	ContextDependencies []string `json:"context_dependencies"`
}

// CreatedCrew is the durable result of CreateCrewProject. Duplicate reports
// an idempotent re-entry: the returned crew already existed under the key.
// Pending names integrations that were selected but still need connecting
// before the crew can use them.
type CreatedCrew struct {
	CrewID          string
	Title           string
	WorkspacePath   string
	ManifestPath    string
	SessionID       string
	TriggerID       string
	AttachmentAlias string
	Step            CreatedCrewStepConfig
	Pending         []PendingConnection
	Duplicate       bool
}

// PendingConnection is one selected integration the Builder must ask the
// user to connect: the reference is real, but the crew cannot use it yet.
type PendingConnection struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// crewCreationNamespace derives stable project IDs from idempotency keys so
// retried proposals resolve to the same crew without a stored receipt.
var crewCreationNamespace = uuid.MustParse("7c9e2f4a-1b3d-4e5f-8a6b-9c0d1e2f3a4b")

// CreateCrewProject creates a Crew project server-side, mirroring the UI
// creation layout byte-for-byte (product.json identity, workflow.json runtime
// manifest, code/ folder) so UI-created and Builder-created crews are
// indistinguishable. The creating workflow is pre-seeded in the crew's
// workflow_context_paths (always mounted read-only). First chat open binds
// the session and lazily initializes the database, exactly as for UI crews.
func (s *ProductScheduleService) CreateCrewProject(ctx context.Context, req CreateCrewRequest) (CreatedCrew, error) {
	userID := strings.TrimSpace(req.UserID)
	workflowPath := strings.Trim(strings.TrimSpace(req.WorkflowPath), "/")
	profileID := strings.TrimSpace(req.ProfileID)
	if profileID == "" {
		profileID = "work"
	}
	title := strings.TrimSpace(req.Title)
	key := strings.TrimSpace(req.IdempotencyKey)
	if userID == "" {
		return CreatedCrew{}, fmt.Errorf("crew creation requires a user")
	}
	claims := GetUserFromContext(ctx)
	if claims == nil || strings.TrimSpace(claims.UserID) != userID || userAccessForClaims(claims).Disabled {
		return CreatedCrew{}, fmt.Errorf("crew creation requires the current authorized user")
	}
	if !userAllowedProduct(claims, "work") {
		return CreatedCrew{}, fmt.Errorf("Crew product is unavailable or access denied")
	}
	if err := validateCrewCreationWorkflowPath(workflowPath); err != nil {
		return CreatedCrew{}, err
	}
	if title == "" || len([]rune(title)) > 60 {
		return CreatedCrew{}, fmt.Errorf("crew title must be 1-60 characters")
	}
	if len([]rune(strings.TrimSpace(req.Icon))) > 8 {
		return CreatedCrew{}, fmt.Errorf("crew icon must be at most 8 characters")
	}
	if strings.TrimSpace(req.Role) == "" {
		return CreatedCrew{}, fmt.Errorf("crew role is required")
	}
	if len([]rune(strings.TrimSpace(req.Role))) > 120 {
		return CreatedCrew{}, fmt.Errorf("crew role must be at most 120 characters")
	}
	if strings.TrimSpace(req.Purpose) == "" {
		return CreatedCrew{}, fmt.Errorf("crew purpose is required")
	}
	if len([]rune(strings.TrimSpace(req.Purpose))) > 2000 {
		return CreatedCrew{}, fmt.Errorf("crew purpose must be at most 2000 characters")
	}
	if len([]rune(strings.TrimSpace(req.Instructions))) > 8000 {
		return CreatedCrew{}, fmt.Errorf("crew instructions must be at most 8000 characters")
	}
	if key == "" || len(key) > 128 {
		return CreatedCrew{}, fmt.Errorf("crew creation requires an idempotency key of 1-128 characters")
	}
	if profileID != "work" {
		return CreatedCrew{}, fmt.Errorf("crew creation currently supports only the work profile")
	}
	template, err := loadCrewAgentTemplate(strings.TrimSpace(req.TemplateID))
	if err != nil {
		return CreatedCrew{}, err
	}
	if _, err := authorizeWorkflowContextPaths(ctx, []string{workflowPath}); err != nil {
		return CreatedCrew{}, fmt.Errorf("creating workflow is unavailable or access denied: %w", err)
	}
	creatingManifest, exists, err := ReadWorkflowManifest(ctx, workflowPath)
	if err != nil || !exists || creatingManifest == nil {
		return CreatedCrew{}, fmt.Errorf("creating workflow is unavailable or access denied")
	}
	if access := workflowAccessForManifest(claims, creatingManifest); access != WorkflowAccessWrite && access != WorkflowAccessOwner {
		return CreatedCrew{}, fmt.Errorf("creating a Crew for this workflow requires write access")
	}
	if s == nil || s.registry == nil {
		return CreatedCrew{}, fmt.Errorf("product profiles are unavailable")
	}
	profile, err := s.registry.Resolve(profileID, 0, userID)
	if err != nil {
		return CreatedCrew{}, err
	}
	inheritedLLM := inheritCrewCreationLLM(creatingManifest)
	if !strings.EqualFold(strings.TrimSpace(profile.Runtime.Conversation.Mode), agentprofiles.ConversationModeKeyed) {
		return CreatedCrew{}, fmt.Errorf("profile %q does not support crew projects", profileID)
	}
	projectsRoot, err := cleanAgentProfileWorkspace(profile.Runtime.Workspace.ProjectsRoot, userID)
	if err != nil {
		return CreatedCrew{}, fmt.Errorf("invalid product projects root: %w", err)
	}
	runtimeRoot := agentProfileRuntimeWorkspace(userID, projectsRoot)
	// Every reference is validated before anything is created: unknown
	// names fail here, never as half-written crews.
	skills, err := validateCrewCreationSkills(req.Skills)
	if err != nil {
		return CreatedCrew{}, err
	}
	servers, pending, err := s.validateCrewCreationServers(req.Servers)
	if err != nil {
		return CreatedCrew{}, err
	}
	secrets, err := s.validateCrewCreationSecrets(ctx, userID, workflowPath, req.Secrets)
	if err != nil {
		return CreatedCrew{}, err
	}
	globalSecrets, err := s.validateCrewCreationGlobalSecrets(req.GlobalSecrets)
	if err != nil {
		return CreatedCrew{}, err
	}
	wiring, err := validateCrewCreationWiring(req, title)
	if err != nil {
		return CreatedCrew{}, err
	}

	slug := slugifyCrewTitle(title)
	receipt, adopted, err := s.claimCrewCreationReceipt(ctx, userID, workflowPath, profileID, key, crewCreationFingerprint(userID, workflowPath, profileID, req), title, slug, runtimeRoot, wiring)
	if err != nil {
		return CreatedCrew{}, err
	}
	wiring.alias = receipt.Alias
	wiring.stepID = receipt.StepID
	created := CreatedCrew{Pending: pending}
	if adopted {
		existing, ok, err := readCreatedCrew(ctx, receipt.WorkspacePath, receipt.CrewID)
		if err != nil {
			return CreatedCrew{}, err
		}
		if ok {
			// Idempotent re-entry adopts the existing crew and continues
			// through the phases below, so a retry after a partial failure
			// still converges instead of stranding the crew half-built.
			existing.Duplicate = true
			existing.Pending = pending
			created = existing
		} else if projectExistsAtPath(ctx, receipt.WorkspacePath) {
			return CreatedCrew{}, fmt.Errorf("crew path %q is now owned by another project; use a new idempotency key", receipt.WorkspacePath)
		} else {
			// The receipt outlived its manifests (a crash between the
			// receipt write and the manifest writes, or deleted
			// manifests): rebuild under the frozen identity.
			created, err = writeCrewCreationManifests(ctx, userID, profile, profileID, workflowPath, title, req.Purpose, req.Role, req.Icon, receipt.CrewID, receipt.WorkspacePath, inheritedLLM)
			if err != nil {
				return CreatedCrew{}, err
			}
			created.Duplicate = true
			created.Pending = pending
		}
	} else {
		created, err = writeCrewCreationManifests(ctx, userID, profile, profileID, workflowPath, title, req.Purpose, req.Role, req.Icon, receipt.CrewID, receipt.WorkspacePath, inheritedLLM)
		if err != nil {
			return CreatedCrew{}, err
		}
		created.Pending = pending
	}

	if err := applyCrewCreationStarter(ctx, created.WorkspacePath, title, workflowPath, req.Purpose, req.Instructions); err != nil {
		return CreatedCrew{}, err
	}
	if err := applyCrewAgentTemplate(ctx, created.WorkspacePath, template); err != nil {
		return CreatedCrew{}, err
	}
	if err := applyCrewCreationSelections(ctx, profileID, created.WorkspacePath, skills, servers, secrets, globalSecrets); err != nil {
		return CreatedCrew{}, err
	}
	if err := applyCrewCreationWiring(ctx, s, userID, profileID, workflowPath, created.CrewID, wiring, &created); err != nil {
		return CreatedCrew{}, err
	}
	return created, nil
}

// crewCreationWiring is the validated wiring resolved from a creation request.
// Alias is the requested value or the title-derived base; step ID is the
// requested value or a provisional base that the receipt claim re-derives
// from the selected alias. The claim resolves both to free values before
// anything is created.
type crewCreationWiring struct {
	alias           string
	autoAlias       bool
	triggerName     string
	triggerMessage  string
	stepID          string
	autoStepID      bool
	stepTitle       string
	stepInstruction string
	contextDeps     []string
}

func validateCrewCreationWiring(req CreateCrewRequest, title string) (crewCreationWiring, error) {
	out := crewCreationWiring{}
	alias := strings.TrimSpace(req.Alias)
	if alias == "" {
		alias = slugifyCrewTitle(title)
		if err := workflowtypes.ValidateCrewAttachmentAlias(alias); err != nil {
			alias += "-crew"
		}
		out.autoAlias = true
	}
	if err := workflowtypes.ValidateCrewAttachmentAlias(alias); err != nil {
		return out, err
	}
	out.alias = alias
	out.triggerName = strings.TrimSpace(req.TriggerName)
	if out.triggerName == "" {
		out.triggerName = title + " trigger"
	}
	if len([]rune(out.triggerName)) > 120 {
		return out, fmt.Errorf("trigger name must be at most 120 characters")
	}
	out.triggerMessage = strings.TrimSpace(req.TriggerMessage)
	if out.triggerMessage == "" {
		out.triggerMessage = strings.TrimSpace(req.Instructions)
	}
	if out.triggerMessage == "" {
		out.triggerMessage = strings.TrimSpace(req.Purpose)
	}
	if out.triggerMessage == "" {
		return out, fmt.Errorf("trigger message, instructions, or purpose is required")
	}
	if len([]rune(out.triggerMessage)) > 8000 {
		return out, fmt.Errorf("trigger message must be at most 8000 characters")
	}
	out.stepID = strings.TrimSpace(req.StepID)
	if out.stepID == "" {
		out.stepID = "crew-" + slugifyCrewTitle(title)
		out.autoStepID = true
	}
	if len(out.stepID) > 120 {
		return out, fmt.Errorf("step id must be at most 120 characters")
	}
	out.stepTitle = strings.TrimSpace(req.StepTitle)
	if out.stepTitle == "" {
		out.stepTitle = title
	}
	if len([]rune(out.stepTitle)) > 120 {
		return out, fmt.Errorf("step title must be at most 120 characters")
	}
	out.stepInstruction = strings.TrimSpace(req.StepInstruction)
	if out.stepInstruction == "" {
		return out, fmt.Errorf("step instruction is required")
	}
	if len([]rune(out.stepInstruction)) > 8000 {
		return out, fmt.Errorf("step instruction must be at most 8000 characters")
	}
	deps, err := validateCrewCreationNames("context dependency", req.ContextDependencies)
	if err != nil {
		return out, err
	}
	out.contextDeps = deps
	return out, nil
}

// applyCrewCreationWiring creates the crew's workflow-bound trigger, attaches
// the crew read-only, and builds the step configuration for the Builder's
// follow-up add_step call. Trigger and attachment adoption make re-entry
// converge: matching records are reused, never duplicated.
func applyCrewCreationWiring(ctx context.Context, svc *ProductScheduleService, userID, profileID, workflowPath, crewID string, wiring crewCreationWiring, created *CreatedCrew) error {
	workflowID, err := boundWorkflowID(ctx, workflowPath)
	if err != nil {
		return err
	}
	triggerID, err := ensureCrewCreationTrigger(ctx, svc, userID, profileID, crewID, workflowID, wiring.triggerName, wiring.triggerMessage)
	if err != nil {
		return err
	}
	if _, _, err := attachCrewProjectToWorkflow(ctx, svc, userID, workflowPath, profileID, crewID, wiring.alias); err != nil {
		return err
	}
	created.TriggerID = triggerID
	created.AttachmentAlias = wiring.alias
	created.Step = CreatedCrewStepConfig{
		StepID:              wiring.stepID,
		Title:               wiring.stepTitle,
		Description:         strings.TrimSpace(created.Title) + " via the " + wiring.alias + " crew attachment",
		CrewProfileID:       profileID,
		CrewProjectID:       crewID,
		TriggerID:           triggerID,
		Instruction:         wiring.stepInstruction,
		ContextDependencies: wiring.contextDeps,
	}
	return nil
}

// ensureCrewCreationTrigger returns the crew's internal trigger for the
// creating workflow, creating one (isolated, enabled, caller-bound) when the
// same-named binding does not already exist.
func ensureCrewCreationTrigger(ctx context.Context, svc *ProductScheduleService, userID, profileID, projectID, workflowID, name, message string) (string, error) {
	if svc == nil {
		return "", fmt.Errorf("product schedules are unavailable")
	}
	triggers, err := svc.projectWebhookConfigs(ctx, userID, profileID, projectID)
	if err != nil {
		return "", err
	}
	for _, trigger := range triggers {
		if !trigger.IsInternal() || trigger.Caller == nil {
			continue
		}
		if strings.TrimSpace(trigger.Caller.Type) != triggerCallerWorkflow || strings.TrimSpace(trigger.Caller.ID) != workflowID {
			continue
		}
		if strings.TrimSpace(trigger.Name) == name {
			return trigger.ID, nil
		}
	}
	response, _, err := svc.saveProductWebhookConfig(ctx, userID, productWebhookRequest{
		ProfileID: profileID, ProjectID: projectID, Name: name, Message: message,
		Enabled: true, RunDestination: runDestinationIsolated,
		Kind:   triggerKindInternal,
		Caller: &triggerCaller{Type: triggerCallerWorkflow, ID: workflowID},
	}, "")
	if err != nil {
		return "", err
	}
	return response.ID, nil
}

// writeCrewCreationManifests writes a fresh crew's runtime manifest, identity
// manifest, and code folder, mirroring the UI creation layout.
func writeCrewCreationManifests(ctx context.Context, userID string, profile agentprofiles.Profile, profileID, workflowPath, title, purpose, role, icon, crewID, workspacePath string, inheritedLLM *workflowtypes.AgentLLMConfig) (CreatedCrew, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	sessionID := profileID + ":project:" + crewID
	purpose = strings.TrimSpace(purpose)
	role = strings.TrimSpace(role)
	icon = strings.TrimSpace(icon)
	if icon == "" {
		icon = strings.ToUpper(string([]rune(title)[:1]))
		if icon == "" {
			icon = "C"
		}
	}
	capabilities := map[string]interface{}{
		"selected_servers":             []string{},
		"selected_tools":               []string{},
		"selected_skills":              []string{},
		"selected_secrets":             []string{},
		"selected_global_secret_names": []string{},
		"browser_mode":                 "auto",
		"use_code_execution_mode":      false,
	}
	if llmConfig := resolveCrewCreationLLMConfig(profile, inheritedLLM); llmConfig != nil {
		capabilities["llm_config"] = llmConfig
	}
	// A Crew created outside a workflow (external authoring) references none.
	contextPaths := []string{}
	if workflowPath != "" {
		contextPaths = append(contextPaths, workflowPath)
	}
	runtimeManifest := map[string]interface{}{
		"schema_version":         1,
		"id":                     crewID,
		"label":                  title,
		"capabilities":           capabilities,
		"workflow_context_paths": contextPaths,
		"schedules":              []string{},
		"triggers":               []string{},
		"created_at":             now,
		"updated_at":             now,
	}
	manifest := map[string]interface{}{
		"schema_version": 1,
		"product":        profileID,
		"owner_id":       sanitizeUserIDForPath(userID),
		"id":             crewID,
		"title":          title,
		"description":    purpose,
		"session_id":     sessionID,
		"created_at":     now,
		"updated_at":     now,
		"identity":       map[string]interface{}{"name": title, "icon": icon, "role": role},
	}
	// The server records who owns the project before anything about it exists (PLAT-449): the registry is the
	// authority, the owner_id above is information for whoever reads the folder.
	if err := defaultProjectOwners().Register(projectOwnerRecord{
		Product: profileID, Folder: filepath.Base(filepath.ToSlash(workspacePath)), OwnerID: userID, ProjectID: crewID,
		Shared: workspaceref.MustParse(filepath.ToSlash(workspacePath)).IsShared(),
	}); err != nil {
		// A project in its owner's tree still resolves by its path; one at the shared root would belong to
		// nobody, so only that creation fails.
		if workspaceref.MustParse(filepath.ToSlash(workspacePath)).IsShared() || errors.Is(err, errProjectOwnerConflict) {
			return CreatedCrew{}, fmt.Errorf("register crew owner: %w", err)
		}
		log.Printf("[OWNER_REGISTRY] %s: %v", workspacePath, err)
	}
	if err := writeCrewCreationManifest(ctx, filepath.ToSlash(filepath.Join(workspacePath, "workflow.json")), runtimeManifest); err != nil {
		return CreatedCrew{}, err
	}
	manifestPath := filepath.ToSlash(filepath.Join(workspacePath, "product.json"))
	if err := writeCrewCreationManifest(ctx, manifestPath, manifest); err != nil {
		return CreatedCrew{}, err
	}
	// Raw folder create, not the guarded workspace client: this is a
	// server-side privileged write outside the Builder session's sandbox
	// (the session guard confines the model's direct file access, not the
	// server's own authorized tool implementations). The guarded client
	// denies the crew path with ACCESS DENIED in every Builder session.
	if err := createWorkspaceFolder(ctx, filepath.ToSlash(filepath.Join(workspacePath, "code"))); err != nil {
		return CreatedCrew{}, fmt.Errorf("initialize crew code folder: %w", err)
	}
	return CreatedCrew{CrewID: crewID, Title: title, WorkspacePath: workspacePath, ManifestPath: manifestPath, SessionID: sessionID}, nil
}

// inheritCrewCreationLLM resolves the creating workflow's model choice into
// a concrete builder LLM for the crew: a provider_profile resolves through
// the same provider defaults the workflow engine uses, and an explicit
// builder_llm carries over verbatim. Nil means no usable choice, and the
// crew falls back to the profile default.
func inheritCrewCreationLLM(manifest *WorkflowManifest) *workflowtypes.AgentLLMConfig {
	if manifest == nil {
		return nil
	}
	config := manifest.Capabilities.LLMConfig
	if config == nil {
		return nil
	}
	if config.Mode == workflowtypes.LLMConfigModeExplicit && config.BuilderLLM != nil {
		return cloneCrewCreationLLM(config.BuilderLLM)
	}
	if config.Mode == workflowtypes.LLMConfigModeProviderProfile && strings.TrimSpace(config.Provider) != "" {
		builder, _, ok := workflowtypes.ResolveProviderProfileConfig(config)
		if !ok || builder == nil {
			return nil
		}
		return cloneCrewCreationLLM(builder)
	}
	return nil
}

func cloneCrewCreationLLM(config *workflowtypes.AgentLLMConfig) *workflowtypes.AgentLLMConfig {
	if config == nil {
		return nil
	}
	out := *config
	if config.Options != nil {
		options := make(map[string]interface{}, len(config.Options))
		for key, value := range config.Options {
			options[key] = value
		}
		out.Options = options
	}
	if strings.TrimSpace(out.Provider) == "" || strings.TrimSpace(out.ModelID) == "" {
		return nil
	}
	return &out
}

// resolveCrewCreationLLMConfig prefers the creating workflow's model so a
// Builder-created crew runs where its workflow runs; otherwise it mirrors
// the UI creation default from the profile.
func resolveCrewCreationLLMConfig(profile agentprofiles.Profile, inherited *workflowtypes.AgentLLMConfig) map[string]interface{} {
	if inherited != nil {
		builder := map[string]interface{}{"provider": inherited.Provider, "model_id": inherited.ModelID}
		if len(inherited.Options) > 0 {
			builder["options"] = inherited.Options
		}
		if strings.TrimSpace(inherited.ConnectionID) != "" {
			builder["connection_id"] = inherited.ConnectionID
		}
		if strings.TrimSpace(inherited.PublishedLLMID) != "" {
			builder["published_llm_id"] = inherited.PublishedLLMID
		}
		return map[string]interface{}{"schema_version": 2, "mode": "explicit", "builder_llm": builder}
	}
	return defaultCrewLLMConfig(profile)
}

// defaultCrewLLMConfig mirrors the UI creation default: the profile's
// default provider option, else its first option. A nil return omits
// llm_config and the profile default applies at chat time.
func defaultCrewLLMConfig(profile agentprofiles.Profile) map[string]interface{} {
	profile = profileWithProductDefault(context.Background(), profile)
	options := profile.Runtime.ProviderOptions
	if len(options) == 0 {
		return nil
	}
	selected := options[0]
	for _, option := range options {
		if option.Default {
			selected = option
			break
		}
	}
	provider := strings.TrimSpace(selected.Provider)
	modelID := strings.TrimSpace(selected.ModelID)
	if provider == "" || modelID == "" {
		return nil
	}
	builder := map[string]interface{}{"provider": provider, "model_id": modelID}
	effort := ""
	if raw, ok := selected.Options["reasoning_effort"].(string); ok && strings.TrimSpace(raw) != "" {
		effort = strings.TrimSpace(raw)
	} else if len(selected.ReasoningEfforts) > 0 {
		effort = strings.TrimSpace(selected.ReasoningEfforts[0])
	}
	if effort != "" {
		builder["options"] = map[string]interface{}{"reasoning_effort": effort}
	}
	return map[string]interface{}{"schema_version": 2, "mode": "explicit", "builder_llm": builder}
}

// applyCrewCreationStarter seeds the crew's project-root MEMORY.md with the
// Builder's brief when one was supplied. Crews are instructed to read
// MEMORY.md before reporting project information unknown, so this is the
// channel that actually reaches the specialist; overwriting keeps retries
// idempotent.
func applyCrewCreationStarter(ctx context.Context, workspacePath, title, workflowPath, purpose, instructions string) error {
	purpose = strings.TrimSpace(purpose)
	instructions = strings.TrimSpace(instructions)
	if purpose == "" && instructions == "" {
		return nil
	}
	var b strings.Builder
	b.WriteString("# " + strings.TrimSpace(title) + "\n\n")
	b.WriteString("## " + time.Now().UTC().Format("2006-01-02") + " — Crew brief\n\n")
	b.WriteString("Created by Workflow Builder for workflow `" + workflowPath + "`.\n\n")
	if purpose != "" {
		b.WriteString("Purpose: " + purpose + "\n\n")
	}
	if instructions != "" {
		b.WriteString("Starter instructions from the Builder:\n\n" + instructions + "\n")
	}
	if err := writeRawFileToWorkspace(ctx, filepath.ToSlash(filepath.Join(workspacePath, "MEMORY.md")), b.String()); err != nil {
		return fmt.Errorf("write crew starter brief: %w", err)
	}
	return nil
}

// applyCrewCreationSelections records capability selections through the
// existing product writers with union merges, so re-entry converges without
// duplicating entries. Empty lists leave the manifest defaults untouched.
func applyCrewCreationSelections(ctx context.Context, profileID, workspacePath string, skills, servers, secrets, globalSecrets []string) error {
	merge := func(add []string) func([]string) []string {
		return func(current []string) []string {
			seen := make(map[string]struct{}, len(current)+len(add))
			out := make([]string, 0, len(current)+len(add))
			for _, name := range append(append([]string{}, current...), add...) {
				if _, dup := seen[name]; dup {
					continue
				}
				seen[name] = struct{}{}
				out = append(out, name)
			}
			return out
		}
	}
	if len(skills) > 0 {
		if err := updateProductSelectedSkills(ctx, profileID, workspacePath, merge(skills)); err != nil {
			return fmt.Errorf("select crew skills: %w", err)
		}
	}
	if len(servers) > 0 {
		if err := updateProductSelectedServers(ctx, profileID, workspacePath, merge(servers)); err != nil {
			return fmt.Errorf("select crew servers: %w", err)
		}
	}
	if len(secrets) > 0 {
		if err := updateProductSelectedSecrets(ctx, profileID, workspacePath, merge(secrets)); err != nil {
			return fmt.Errorf("select crew secrets: %w", err)
		}
	}
	if len(globalSecrets) > 0 {
		if err := updateProductSelectedGlobalSecrets(ctx, profileID, workspacePath, merge(globalSecrets)); err != nil {
			return fmt.Errorf("select crew global secrets: %w", err)
		}
	}
	return nil
}

// validateCrewCreationSkills trims, dedupes, and verifies each skill is
// installed. Builders must list or install skills before proposing them;
// hallucinating a skill name fails fast here instead of recording a broken
// reference. A transient read failure also rejects, safely: creation is
// retryable under the same key.
func validateCrewCreationSkills(names []string) ([]string, error) {
	checked, err := validateCrewCreationNames("skill", names)
	if err != nil {
		return nil, err
	}
	for _, name := range checked {
		// Built-in skills (agent-browser and product skills) are served from the
		// in-memory registry, exactly as attachment resolves them, not skills/.
		if skills.IsBuiltinSkill(name) {
			continue
		}
		if _, err := skills.GetSkill(getWorkspaceAPIURL(), name); err != nil {
			return nil, fmt.Errorf("crew skill %q is not installed; install it first or drop it: %w", name, err)
		}
	}
	return checked, nil
}

// validateCrewCreationNames trims, dedupes, and shape-checks capability
// references. Names are references only: non-empty, bounded, and free of
// path separators and control characters. Availability is checked separately
// by the server/secret/skill validators before anything is created.
func validateCrewCreationNames(kind string, names []string) ([]string, error) {
	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" || len(name) > 128 || strings.ContainsAny(name, "/\\\x00") || strings.Contains(name, "..") {
			return nil, fmt.Errorf("invalid crew %s reference %q", kind, raw)
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

// validateCrewCreationWorkflowPath mirrors the workflow_context_paths rule:
// the creating workflow must be addressable as Workflow/<folder>, since its
// path is seeded into the crew manifest verbatim.
func validateCrewCreationWorkflowPath(workflowPath string) error {
	parts := strings.Split(workflowPath, "/")
	if len(parts) != 2 || parts[0] != "Workflow" || parts[1] == "" || parts[1] == "." || parts[1] == ".." || strings.ContainsAny(workflowPath, "\\\x00") {
		return fmt.Errorf("creating workflow must be a Workflow/<folder> path")
	}
	return nil
}

// slugifyCrewTitle mirrors the UI slugifyTitle for ASCII titles: lowercase,
// non-alphanumeric runs become dashes, trimmed and capped at 48 characters.
// It deliberately skips Unicode NFKD folding (no new dependency for a
// cosmetic segment; the id suffix carries uniqueness).
func slugifyCrewTitle(title string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !ok {
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
			continue
		}
		b.WriteRune(r)
		lastDash = false
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	if slug == "" {
		return "workspace"
	}
	return slug
}

// crewCreationSuffix derives the stable 8-hex directory suffix from the
// idempotency key.
func crewCreationSuffix(key string) string {
	sum := sha256.Sum256([]byte("crew-creation-path:" + key))
	return hex.EncodeToString(sum[:])[:8]
}

// readCreatedCrew returns the existing crew when the candidate path already
// holds the crew minted under this key (verified by project ID, never by
// path alone).
func readCreatedCrew(ctx context.Context, workspacePath, crewID string) (CreatedCrew, bool, error) {
	raw, found, err := readFileFromWorkspace(ctx, filepath.ToSlash(filepath.Join(workspacePath, "product.json")))
	if err != nil || !found {
		return CreatedCrew{}, false, err
	}
	var manifest productProjectManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return CreatedCrew{}, false, nil
	}
	if strings.TrimSpace(manifest.ID) != crewID {
		return CreatedCrew{}, false, nil
	}
	return CreatedCrew{
		CrewID:        strings.TrimSpace(manifest.ID),
		Title:         strings.TrimSpace(manifest.Title),
		WorkspacePath: workspacePath,
		ManifestPath:  filepath.ToSlash(filepath.Join(workspacePath, "product.json")),
		SessionID:     strings.TrimSpace(manifest.SessionID),
	}, true, nil
}

// projectExistsAtPath reports whether any crew already occupies a path,
// regardless of which key minted it.
func projectExistsAtPath(ctx context.Context, workspacePath string) bool {
	_, found, err := readFileFromWorkspace(ctx, filepath.ToSlash(filepath.Join(workspacePath, "product.json")))
	return err == nil && found
}

func writeCrewCreationManifest(ctx context.Context, path string, manifest map[string]interface{}) error {
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeRawFileToWorkspace(ctx, path, string(encoded)+"\n"); err != nil {
		return fmt.Errorf("write crew manifest %s: %w", path, err)
	}
	return nil
}

// crewCreationAvailability bundles the live lookups behind creation-time
// availability checks. Each probe is overrideable so tests run hermetic.
type crewCreationAvailability struct {
	MCPServer     func(name string) (canonical string, connected bool, err error)
	ScopedSecrets func(ctx context.Context, path, userID string) (map[string]bool, error)
	GlobalSecrets func() map[string]bool
}

// validateCrewCreationServers shape-checks requested MCP servers, resolves
// each against the platform catalog, and records the canonical name.
// Unknown servers fail the call; configured-but-disconnected servers are
// selected and reported as pending so the Builder can ask the user to
// connect exactly those.
func (s *ProductScheduleService) validateCrewCreationServers(names []string) ([]string, []PendingConnection, error) {
	checked, err := validateCrewCreationNames("server", names)
	if err != nil {
		return nil, nil, err
	}
	servers := make([]string, 0, len(checked))
	var pending []PendingConnection
	for _, name := range checked {
		canonical, connected, err := s.probeCrewCreationMCPServer(name)
		if err != nil {
			return nil, nil, err
		}
		servers = append(servers, canonical)
		if !connected {
			pending = append(pending, PendingConnection{Kind: "mcp_server", Name: canonical, Reason: "configured but not connected; connect it in the Crew UI before the crew can use its tools"})
		}
	}
	return servers, pending, nil
}

func (s *ProductScheduleService) probeCrewCreationMCPServer(name string) (string, bool, error) {
	if s != nil && s.crewAvailability != nil && s.crewAvailability.MCPServer != nil {
		return s.crewAvailability.MCPServer(name)
	}
	if s == nil || s.api == nil {
		return "", false, fmt.Errorf("MCP configuration is unavailable")
	}
	catalog, err := s.api.loadMergedConfig()
	if err != nil {
		return "", false, fmt.Errorf("load MCP configuration: %w", err)
	}
	canonical, config, err := resolveMCPServerName(catalog, name)
	if err != nil {
		return "", false, err
	}
	return canonical, s.api.isPlatformMCPServerConnected(catalog, canonical, config), nil
}

// validateCrewCreationSecrets shape-checks requested project secrets and
// verifies each resolves in a scope the new crew actually reads: the global
// store. A fresh crew has no scoped secrets of its own yet, and the creating
// workflow's scoped secrets do not carry over, so a name found only there
// fails with a targeted hint instead of recording a reference that would
// resolve empty at runtime.
func (s *ProductScheduleService) validateCrewCreationSecrets(ctx context.Context, userID, workflowPath string, names []string) ([]string, error) {
	checked, err := validateCrewCreationNames("secret", names)
	if err != nil {
		return nil, err
	}
	if len(checked) == 0 {
		return nil, nil
	}
	globals := s.probeCrewCreationGlobalSecrets()
	var creatingScope map[string]bool
	for _, name := range checked {
		if globals[name] {
			continue
		}
		// Runs at most once: the first non-global name below always returns.
		creatingScope, err = s.probeCrewCreationScopedSecrets(ctx, workflowPath, userID)
		if err != nil {
			return nil, err
		}
		if creatingScope[name] {
			return nil, fmt.Errorf("crew secret %q exists in workflow %q, but workflow secrets do not carry over to a new crew; add it in the Crew UI after creation or drop it", name, workflowPath)
		}
		return nil, fmt.Errorf("crew secret %q has no stored value; use a global secret name or add it in the Crew UI after creation, or drop it", name)
	}
	return checked, nil
}

func (s *ProductScheduleService) probeCrewCreationScopedSecrets(ctx context.Context, path, userID string) (map[string]bool, error) {
	if s != nil && s.crewAvailability != nil && s.crewAvailability.ScopedSecrets != nil {
		return s.crewAvailability.ScopedSecrets(ctx, path, userID)
	}
	if s == nil || s.api == nil {
		return nil, fmt.Errorf("workflow secret store is unavailable")
	}
	stored, err := s.api.ensureSharedWorkflowSecrets(ctx, path, userID)
	if err != nil {
		return nil, fmt.Errorf("list workflow secrets: %w", err)
	}
	out := make(map[string]bool, len(stored))
	for _, secret := range stored {
		out[secret.Name] = true
	}
	return out, nil
}

// validateCrewCreationGlobalSecrets shape-checks requested global secrets
// and verifies each names an existing global record.
func (s *ProductScheduleService) validateCrewCreationGlobalSecrets(names []string) ([]string, error) {
	checked, err := validateCrewCreationNames("global secret", names)
	if err != nil {
		return nil, err
	}
	if len(checked) == 0 {
		return nil, nil
	}
	globals := s.probeCrewCreationGlobalSecrets()
	for _, name := range checked {
		if !globals[name] {
			return nil, fmt.Errorf("global secret %q does not exist; use list_secrets to discover available globals or drop it", name)
		}
	}
	return checked, nil
}

func (s *ProductScheduleService) probeCrewCreationGlobalSecrets() map[string]bool {
	if s != nil && s.crewAvailability != nil && s.crewAvailability.GlobalSecrets != nil {
		return s.crewAvailability.GlobalSecrets()
	}
	out := map[string]bool{}
	for _, secret := range getGlobalSecrets() {
		out[secret.Name] = true
	}
	return out
}

// crewCreationReceipt freezes one proposal's identity the first time its key
// is claimed: crew ID, workspace path, attachment alias, and step ID. A
// retry under the same key adopts the receipt instead of re-deriving
// anything from mutable proposal fields, so a changed title cannot mint a
// second directory and the occupied-path fallback persists across retries.
// A changed payload under a claimed key is a conflict, never a new crew.
type crewCreationReceipt struct {
	Key           string `json:"key"`
	UserID        string `json:"user_id"`
	WorkflowPath  string `json:"workflow_path"`
	ProfileID     string `json:"profile_id"`
	PayloadHash   string `json:"payload_hash"`
	Title         string `json:"title"`
	CrewID        string `json:"crew_id"`
	WorkspacePath string `json:"workspace_path"`
	Alias         string `json:"alias"`
	StepID        string `json:"step_id"`
	CreatedAt     string `json:"created_at"`
}

func crewCreationReceiptPath(userID, key string) string {
	return filepath.ToSlash(filepath.Join(chatHistoryRoot(userID), "crew-creation", submissionDigest("crew-creation:"+key)+".json"))
}

// crewCreationFingerprint hashes the proposal verbatim: any changed field is
// a different proposal and must use a new key.
func crewCreationFingerprint(userID, workflowPath, profileID string, req CreateCrewRequest) string {
	trimmed := func(value string) string { return strings.TrimSpace(value) }
	lists := func(values []string) []string {
		out := make([]string, 0, len(values))
		for _, value := range values {
			out = append(out, strings.TrimSpace(value))
		}
		return out
	}
	payload := struct {
		UserID, WorkflowPath, ProfileID                      string
		Title, Icon, Role, Purpose, Instructions, TemplateID string
		Skills, Servers, Secrets, GlobalSecrets              []string
		Alias, TriggerName, TriggerMessage                   string
		StepID, StepTitle, StepInstruction                   string
		ContextDependencies                                  []string
	}{
		UserID: userID, WorkflowPath: workflowPath, ProfileID: profileID,
		Title: trimmed(req.Title), Icon: trimmed(req.Icon), Role: trimmed(req.Role),
		Purpose: trimmed(req.Purpose), Instructions: trimmed(req.Instructions), TemplateID: trimmed(req.TemplateID),
		Skills: lists(req.Skills), Servers: lists(req.Servers), Secrets: lists(req.Secrets), GlobalSecrets: lists(req.GlobalSecrets),
		Alias: trimmed(req.Alias), TriggerName: trimmed(req.TriggerName), TriggerMessage: trimmed(req.TriggerMessage),
		StepID: trimmed(req.StepID), StepTitle: trimmed(req.StepTitle), StepInstruction: trimmed(req.StepInstruction),
		ContextDependencies: lists(req.ContextDependencies),
	}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// claimCrewCreationReceipt adopts the frozen identity for a claimed key or
// claims the key by freezing a fresh one. Identity selection (alias and
// step availability, occupied-path fallback) happens under the receipt lock
// before any crew resource exists.
func (s *ProductScheduleService) claimCrewCreationReceipt(ctx context.Context, userID, workflowPath, profileID, key, fingerprint, title, slug, runtimeRoot string, wiring crewCreationWiring) (*crewCreationReceipt, bool, error) {
	path := crewCreationReceiptPath(userID, key)
	lock := productConversationRegistryMutex(path)
	lock.Lock()
	defer lock.Unlock()
	raw, found, err := readFileFromWorkspace(ctx, path)
	if err != nil {
		return nil, false, fmt.Errorf("read crew creation receipt: %w", err)
	}
	if found {
		var receipt crewCreationReceipt
		if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
			return nil, false, fmt.Errorf("unreadable crew creation receipt; use a new idempotency key")
		}
		if receipt.Key != key || receipt.UserID != userID || receipt.WorkflowPath != workflowPath || receipt.ProfileID != profileID {
			return nil, false, fmt.Errorf("idempotency key is already in use by another crew proposal; use a new key")
		}
		if receipt.PayloadHash != fingerprint {
			return nil, false, fmt.Errorf("idempotency key %q already created crew %q (%s); a changed proposal must use a new key", key, receipt.Title, receipt.CrewID)
		}
		return &receipt, true, nil
	}
	crewID := uuid.NewSHA1(crewCreationNamespace, []byte("crew-creation:"+key)).String()
	workspacePath := filepath.ToSlash(filepath.Join(runtimeRoot, slug+"-"+crewCreationSuffix(key)))
	if projectExistsAtPath(ctx, workspacePath) {
		// Path collision with an unrelated crew: fall back to random
		// identity. The receipt freezes the fallback, so re-entry adopts
		// the same crew instead of minting another.
		crewID = uuid.NewString()
		workspacePath = filepath.ToSlash(filepath.Join(runtimeRoot, slug+"-"+uuid.NewString()[:8]))
		for attempts := 0; projectExistsAtPath(ctx, workspacePath) && attempts < 5; attempts++ {
			workspacePath = filepath.ToSlash(filepath.Join(runtimeRoot, slug+"-"+uuid.NewString()[:8]))
		}
		if projectExistsAtPath(ctx, workspacePath) {
			return nil, false, fmt.Errorf("crew workspace path is unavailable; use a new idempotency key")
		}
	}
	alias, err := selectCrewCreationAlias(ctx, workflowPath, wiring)
	if err != nil {
		return nil, false, err
	}
	stepWiring := wiring
	if wiring.autoStepID {
		// Pair the default step with the selected alias, not the title:
		// the Builder adds steps after creation, so the plan cannot yet
		// show the first crew's step when the second crew is created.
		// Pairing keeps duplicate titles usable (slug-2 with crew-slug-2)
		// even against an empty plan.
		stepWiring.stepID = "crew-" + alias
	}
	stepID, err := selectCrewCreationStepID(ctx, workflowPath, stepWiring)
	if err != nil {
		return nil, false, err
	}
	receipt := &crewCreationReceipt{
		Key: key, UserID: userID, WorkflowPath: workflowPath, ProfileID: profileID,
		PayloadHash: fingerprint, Title: title, CrewID: crewID, WorkspacePath: workspacePath,
		Alias: alias, StepID: stepID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, false, err
	}
	if err := writeRawFileToWorkspace(ctx, path, string(encoded)+"\n"); err != nil {
		return nil, false, fmt.Errorf("write crew creation receipt: %w", err)
	}
	return receipt, false, nil
}

// selectCrewCreationAlias resolves the attachment alias before any resource
// exists. An explicit alias must be free; a default alias takes the first
// free deterministic suffix (slug, slug-2, ...) so duplicate titles never
// fail after their project and trigger are already written.
func selectCrewCreationAlias(ctx context.Context, workflowPath string, wiring crewCreationWiring) (string, error) {
	manifest, exists, err := ReadWorkflowManifest(ctx, workflowPath)
	if err != nil || !exists || manifest == nil {
		return "", fmt.Errorf("creating workflow is unavailable or access denied")
	}
	taken := make(map[string]bool, len(manifest.CrewAttachments))
	for _, attachment := range manifest.CrewAttachments {
		taken[attachment.Alias] = true
	}
	if !wiring.autoAlias {
		if taken[wiring.alias] {
			return "", fmt.Errorf("attachment alias %q is already used on this workflow; choose another alias", wiring.alias)
		}
		return wiring.alias, nil
	}
	for attempt := 0; attempt <= 100; attempt++ {
		candidate := wiring.alias
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", wiring.alias, attempt+1)
		}
		if err := workflowtypes.ValidateCrewAttachmentAlias(candidate); err != nil {
			return "", err
		}
		if !taken[candidate] {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free attachment alias for %q; pass an explicit alias", wiring.alias)
}

// selectCrewCreationStepID resolves the step ID against the workflow plan
// before any resource exists, with the same explicit-or-suffixed rule as
// aliases. The plan read is best-effort: a workflow still being built may
// have no plan yet, and add_step enforces uniqueness when the step lands.
func selectCrewCreationStepID(ctx context.Context, workflowPath string, wiring crewCreationWiring) (string, error) {
	taken := crewCreationPlanStepIDs(ctx, workflowPath)
	if !wiring.autoStepID {
		if taken[wiring.stepID] {
			return "", fmt.Errorf("step id %q is already used in this workflow; choose another step id", wiring.stepID)
		}
		return wiring.stepID, nil
	}
	for attempt := 0; attempt <= 100; attempt++ {
		candidate := wiring.stepID
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", wiring.stepID, attempt+1)
		}
		if len(candidate) > 120 {
			return "", fmt.Errorf("step id must be at most 120 characters")
		}
		if !taken[candidate] {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free step id for %q; pass an explicit step id", wiring.stepID)
}

// crewCreationPlanStepIDs collects the step IDs declared in the workflow
// plan. Any read problem yields an empty set: plan enforcement stays with
// add_step.
func crewCreationPlanStepIDs(ctx context.Context, workflowPath string) map[string]bool {
	taken := map[string]bool{}
	raw, found, err := readFileFromWorkspace(ctx, filepath.ToSlash(filepath.Join(workflowPath, "planning/plan.json")))
	if err != nil || !found {
		return taken
	}
	var plan struct {
		Steps []struct {
			ID string `json:"id"`
		} `json:"steps"`
	}
	if json.Unmarshal([]byte(raw), &plan) != nil {
		return taken
	}
	for _, step := range plan.Steps {
		if id := strings.TrimSpace(step.ID); id != "" {
			taken[id] = true
		}
	}
	return taken
}
