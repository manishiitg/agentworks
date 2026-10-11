package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	pathpkg "path"
	"regexp"
	"strings"
	"sync"

	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// kebabCaseWorkflowName matches a kebab-case workflow folder name:
// lowercase letters and digits, separated by single hyphens, starting with a letter.
var kebabCaseWorkflowName = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// registerWorkflowCreatorTool registers the create_workflow tool on the multi-agent chat agent.
// This tool is the privileged path for creating new workflow folders — it bypasses the
// session's Chats/ folder guard by writing through the workspace API into the shared
// workspace root. The handler enforces:
//   - kebab-case name validation
//   - required-field validation for workflow.json and plan.json
//   - no-overwrite of existing workflows
//
// Registered only for multi-agent chat (not workflow phase).
func (api *StreamingAPI) registerWorkflowCreatorTool(underlyingAgent definitionToolRegistrar) error {
	if underlyingAgent == nil {
		return fmt.Errorf("underlying agent is nil")
	}

	params := workflowCreatorToolSchema()

	description := "Create a new workflow at Workflow/<folder_name>/ with the given workflow.json and planning/plan.json. Use one large message_sequence per shared-context span, with proof/provenance, evidence-based double-checking, repair, and final validation inside it; create another large sequence only when its context should be isolated. Put deterministic API/SDK/CLI/data-fetch/parse/persist work in coherent scripted-fetcher candidates with authoritative validated outputs. Never use one regular step per endpoint, routine action, or proof check. This tool writes structure only; after creation tell the user to open Workshop so deterministic steps are declared scripted and main.py is authored/tested before production. The tool validates the complete plan graph before writing anything and refuses dangling targets or overwrite."

	return underlyingAgent.RegisterCustomTool(
		"create_workflow",
		description,
		params,
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return api.handleWorkflowCreatorTool(ctx, args)
		},
		"workflow_creator",
	)
}

// workflowCreatorToolSchema is shared by chat and the global MCP catalog.
func workflowCreatorToolSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"folder_name": map[string]interface{}{
				"type":        "string",
				"description": "Shell-safe folder name under Workflow/ — kebab-case (lowercase letters, digits, hyphens only). No spaces, no underscores, no uppercase, no special characters. Examples: 'customer-onboarding', 'sales-report', 'api-health-check'. This is ONLY the on-disk folder name so shell commands like `ls Workflow/<folder_name>/` work without quoting. The human-readable display name goes in workflow_json.label and can be any string.",
			},
			"workflow_json": map[string]interface{}{
				"type":                 "object",
				"description":          "The full workflow.json manifest object. New workflows always use the current platform contract and code layout; supplied legacy version/layout markers are replaced. Required fields: schema_version (int, 1), id (string, e.g. 'wf_<folder_name>'), label (string, free-form human-readable name — can contain spaces, capitalization, anything). Should include objective, success_criteria, and a capabilities object with selected_servers/skills/etc picked smartly from the current chat context. If this workflow supports an org goal, name that goal from pulse/goals.html in the objective/success_criteria and design the workflow to produce measurable evidence for it. Set capabilities.selected_global_secret_names to [] unless specific global secrets are required.",
				"additionalProperties": true,
			},
			"plan_json": map[string]interface{}{
				"type":                 "object",
				"description":          "The full plan.json object. Required field: steps (array, at least 1 step). Start with one large message_sequence per coherent shared-context span; put run-specific proof/provenance, evidence-based double-check and repair turns, and the final validation_schema inside that step. Use multiple large sequences only when contexts should not be shared because of security/credentials, independent outputs/retries, clean-room independence, human/routing boundaries, or context contamination. Fixed API/SDK/CLI calls, deterministic fetching/pagination/parsing/normalization, and mechanical persistence belong in coherent regular fetcher steps batched by source/auth/retry/output contract. Do not create one step per endpoint/tool/checklist/proof item. Every output-producing step needs validation_schema. Each step needs type, id (kebab-case, unique), and title. Every route and explicit next_step_id must target a declared step or end; the complete graph is validated atomically before creation. The plan type IS the execution model (a regular step is scripted, a agent is conversational), so after creation explicitly hand off every deterministic (regular) step to Workshop for an authored/tested code/<step-id>/main.py before the first production run.",
				"additionalProperties": true,
			},
		},
		"required": []string{"folder_name", "workflow_json", "plan_json"},
	}

}

var workflowCreationMutex sync.Mutex
var errWorkflowCreationConflict = errors.New("workflow already exists")

// handleWorkflowCreatorTool validates the arguments, creates the workflow folder, and writes
// workflow.json and planning/plan.json through the workspace API.
func (api *StreamingAPI) handleWorkflowCreatorTool(ctx context.Context, args map[string]interface{}) (string, error) {
	// 1. Validate folder_name (the on-disk path segment — must be shell-safe).
	// The human-readable display name lives in workflow_json.label and can be anything.
	folderName, _ := args["folder_name"].(string)
	folderName = strings.TrimSpace(folderName)
	if folderName == "" {
		return "", fmt.Errorf("folder_name is required")
	}
	if !kebabCaseWorkflowName.MatchString(folderName) {
		return "", fmt.Errorf("folder_name %q is not valid kebab-case — must be lowercase letters/digits separated by single hyphens, e.g. 'customer-onboarding' (the human-readable display name goes in workflow_json.label and can be any string)", folderName)
	}
	if len(folderName) > 64 {
		return "", fmt.Errorf("folder_name %q is too long (max 64 chars)", folderName)
	}

	// 2. Extract workflow_json and plan_json
	workflowRaw, hasWorkflow := args["workflow_json"]
	if !hasWorkflow {
		return "", fmt.Errorf("workflow_json is required")
	}
	planRaw, hasPlan := args["plan_json"]
	if !hasPlan {
		return "", fmt.Errorf("plan_json is required")
	}

	workflowMap, ok := workflowRaw.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("workflow_json must be a JSON object")
	}
	planMap, ok := planRaw.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("plan_json must be a JSON object")
	}

	// 3. Validate workflow.json required fields
	if err := validateWorkflowJSONRequiredFields(workflowMap); err != nil {
		return "", err
	}
	defaultWorkflowCreatorGlobalSecretsToNone(workflowMap)
	defaultWorkflowCreatorContract(workflowMap)

	// 4. Validate plan.json required fields
	if err := validatePlanJSONStructure(planMap); err != nil {
		return "", err
	}

	// 5. Resolve workspace paths (shared workspace root — Workflow/ is not per-user)
	workflowFolder := pathpkg.Join("Workflow", folderName)
	workflowJSONPath := pathpkg.Join(workflowFolder, "workflow.json")
	planJSONPath := pathpkg.Join(workflowFolder, "planning", "plan.json")

	// Serialize chat and external creations so competing calls cannot overwrite.
	workflowCreationMutex.Lock()
	defer workflowCreationMutex.Unlock()

	// 6. Refuse to overwrite existing workflows
	if _, exists, err := readFileFromWorkspace(ctx, workflowJSONPath); err != nil {
		return "", fmt.Errorf("failed to check workflow existence: %w", err)
	} else if exists {
		return "", fmt.Errorf("%w: Workflow/%s — pick a different folder_name or update the existing workflow via Builder", errWorkflowCreationConflict, folderName)
	}

	// IDs also identify workflows for MCP, bindings and executions.
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to check workflow IDs: %w", err)
	}
	for _, existing := range discovered {
		if existing.Manifest != nil && existing.Manifest.ID == workflowMap["id"] {
			return "", fmt.Errorf("%w: choose a different workflow_json.id", errWorkflowCreationConflict)
		}
	}

	// 7. Marshal and write workflow.json
	workflowBytes, err := json.MarshalIndent(workflowMap, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal workflow.json: %w", err)
	}
	if err := writeRawFileToWorkspace(ctx, workflowJSONPath, string(workflowBytes)); err != nil {
		return "", fmt.Errorf("failed to write workflow.json: %w", err)
	}

	// 8. Marshal and write plan.json
	planBytes, err := json.MarshalIndent(planMap, "", "  ")
	if err != nil {
		_ = deleteWorkspaceFolder(ctx, workflowFolder)
		return "", fmt.Errorf("failed to marshal plan.json: %w", err)
	}
	if err := writeRawFileToWorkspace(ctx, planJSONPath, string(planBytes)); err != nil {
		_ = deleteWorkspaceFolder(ctx, workflowFolder)
		return "", fmt.Errorf("failed to write plan.json: %w", err)
	}

	// Scaffold soul/soul.md with a TODO-marked template. soul.md is the canonical
	// source for workflow objective + success criteria (plan.json no longer holds
	// these fields), so seeding the structure at creation time gives the builder
	// an obvious place to fill in and prevents "dir not found" on first edit.
	soulLabel, _ := workflowMap["label"].(string)
	soulPath := pathpkg.Join(workflowFolder, "soul", "soul.md")
	if err := writeRawFileToWorkspace(ctx, soulPath, todo_creation_human.SoulScaffold(soulLabel)); err != nil {
		// Non-fatal: log and continue. Builder can create it manually if the API
		// happens to reject, and ResolveWorkflowObjective falls back to workflow.json.
		log.Printf("[WORKFLOW CREATOR] Warning: failed to scaffold soul/soul.md for Workflow/%s: %v", folderName, err)
	}

	// Scaffold db/reports/.gitkeep and db/.gitkeep so the builder can write
	// HTML report pages on day one without hitting "no such file or directory"
	// from the workspace write API (which doesn't auto-create parent dirs for
	// heredocs). db/ is created lazily on first run by createRunFolderStructure —
	// eager-creation just unblocks pre-run edits. knowledgebase/ is intentionally NOT scaffolded here — that folder
	// needs seeded graph.json/index.json/notes/_index.json (InitKBGraphFiles)
	// which createRunFolderStructure handles on first run.
	reportKeepPath := pathpkg.Join(workflowFolder, "db", "reports", ".gitkeep")
	if err := writeRawFileToWorkspace(ctx, reportKeepPath, ""); err != nil {
		log.Printf("[WORKFLOW CREATOR] Warning: failed to scaffold db/reports/ for Workflow/%s: %v", folderName, err)
	}
	dbKeepPath := pathpkg.Join(workflowFolder, "db", ".gitkeep")
	if err := writeRawFileToWorkspace(ctx, dbKeepPath, ""); err != nil {
		log.Printf("[WORKFLOW CREATOR] Warning: failed to scaffold db/ for Workflow/%s: %v", folderName, err)
	}

	log.Printf("[WORKFLOW CREATOR] Created new workflow: Workflow/%s (workflow.json=%d bytes, plan.json=%d bytes)", folderName, len(workflowBytes), len(planBytes))

	// 9. Collect step summary for the response
	stepSummary := summarizePlanSteps(planMap)

	result := map[string]interface{}{
		"workflow_id":   workflowMap["id"],
		"folder_path":   fmt.Sprintf("Workflow/%s", folderName),
		"workflow_json": fmt.Sprintf("Workflow/%s/workflow.json", folderName),
		"plan_json":     fmt.Sprintf("Workflow/%s/planning/plan.json", folderName),
		"label":         workflowMap["label"],
		"objective":     workflowMap["objective"],
		"step_count":    stepSummary.count,
		"steps":         stepSummary.items,
		"next_action":   fmt.Sprintf("Open Workflow/%s/ in Workshop before the first production run. Declare deterministic API/SDK/CLI/fetch/parse/persist steps scripted, then author and test each code/<step-id>/main.py; keep judgment, agent, and browser work agentic.", folderName),
		"message":       fmt.Sprintf("Workflow Workflow/%s/ created. The user can activate it from the workflow picker; scripted-step setup must be completed in Workshop before the first production run.", folderName),
	}

	resultJSON, marshalErr := json.MarshalIndent(result, "", "  ")
	if marshalErr != nil {
		return fmt.Sprintf("%v", result), nil
	}
	return string(resultJSON), nil
}

// Newly authored plans use the current layout and contract, just like UI creation.
// This is only called by creation; existing workflows still require migrations.
func defaultWorkflowCreatorContract(workflowMap map[string]interface{}) {
	defaults := NewWorkflowManifest("")
	workflowMap["version"] = defaults.Version
	workflowMap["code_layout_version"] = defaults.CodeLayoutVersion
}

func defaultWorkflowCreatorGlobalSecretsToNone(workflowMap map[string]interface{}) {
	caps, _ := workflowMap["capabilities"].(map[string]interface{})
	if caps == nil {
		caps = make(map[string]interface{})
		workflowMap["capabilities"] = caps
	}
	if _, exists := caps["selected_global_secret_names"]; !exists || caps["selected_global_secret_names"] == nil {
		caps["selected_global_secret_names"] = []interface{}{}
	}
}

// validateWorkflowJSONRequiredFields checks that workflow.json has the bare minimum
// fields that the WorkflowManifest struct requires on unmarshal.
func validateWorkflowJSONRequiredFields(m map[string]interface{}) error {
	if _, ok := m["schema_version"]; !ok {
		return fmt.Errorf("workflow_json missing required field: schema_version (int, must be 1)")
	}
	id, ok := m["id"].(string)
	if !ok || strings.TrimSpace(id) == "" {
		return fmt.Errorf("workflow_json missing required field: id (non-empty string, e.g. 'wf_<name>')")
	}
	label, ok := m["label"].(string)
	if !ok || strings.TrimSpace(label) == "" {
		return fmt.Errorf("workflow_json missing required field: label (non-empty string, human-readable name)")
	}
	return nil
}

// validatePlanJSONRequiredFields checks that plan.json has a non-empty steps array,
// each step has type/id/title, and every step id is unique and kebab-case.
func validatePlanJSONRequiredFields(m map[string]interface{}) error {
	stepsRaw, ok := m["steps"]
	if !ok {
		return fmt.Errorf("plan_json missing required field: steps (array of at least 1 step)")
	}
	steps, ok := stepsRaw.([]interface{})
	if !ok {
		return fmt.Errorf("plan_json.steps must be an array")
	}
	if len(steps) == 0 {
		return fmt.Errorf("plan_json.steps must contain at least 1 step")
	}
	seenIDs := make(map[string]bool, len(steps))
	for i, stepRaw := range steps {
		step, ok := stepRaw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("plan_json.steps[%d] must be an object", i)
		}
		stepType, _ := step["type"].(string)
		if strings.TrimSpace(stepType) == "" {
			return fmt.Errorf("plan_json.steps[%d].type is required (e.g. 'regular', 'routing', 'human_input', 'todo_task')", i)
		}
		stepID, _ := step["id"].(string)
		stepID = strings.TrimSpace(stepID)
		if stepID == "" {
			return fmt.Errorf("plan_json.steps[%d].id is required (kebab-case)", i)
		}
		if !kebabCaseWorkflowName.MatchString(stepID) {
			return fmt.Errorf("plan_json.steps[%d].id %q must be kebab-case (lowercase letters/digits separated by hyphens)", i, stepID)
		}
		if seenIDs[stepID] {
			return fmt.Errorf("plan_json.steps[%d].id %q is duplicated — each step id must be unique", i, stepID)
		}
		seenIDs[stepID] = true
		title, _ := step["title"].(string)
		if strings.TrimSpace(title) == "" {
			return fmt.Errorf("plan_json.steps[%d].title is required (human-readable title)", i)
		}
	}
	return nil
}

func validatePlanJSONStructure(m map[string]interface{}) error {
	if err := validatePlanJSONRequiredFields(m); err != nil {
		return err
	}
	planJSON, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("failed to marshal plan_json for validation: %w", err)
	}
	var plan todo_creation_human.PlanningResponse
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		return fmt.Errorf("plan_json is invalid: %w", err)
	}
	if err := todo_creation_human.ValidatePlanStructure(&plan); err != nil {
		return fmt.Errorf("plan_json is invalid: %w", err)
	}
	return nil
}

// planStepSummary captures a compact view of the plan's steps for the tool response.
type planStepSummary struct {
	count int
	items []map[string]string
}

// summarizePlanSteps returns an id+title list for the steps in the plan.
// Used in the create_workflow result so the manager can report back to the user
// without re-parsing its own plan_json argument.
func summarizePlanSteps(planMap map[string]interface{}) planStepSummary {
	steps, ok := planMap["steps"].([]interface{})
	if !ok {
		return planStepSummary{}
	}
	items := make([]map[string]string, 0, len(steps))
	for _, raw := range steps {
		step, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := step["id"].(string)
		title, _ := step["title"].(string)
		items = append(items, map[string]string{
			"id":    id,
			"title": title,
		})
	}
	return planStepSummary{
		count: len(items),
		items: items,
	}
}
