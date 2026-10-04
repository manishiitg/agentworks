package step_based_workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// stepConfigToolRuntime separates the existing builder config executor from its
// live conversation while retaining the same validation and mutation behavior.
type stepConfigToolRuntime interface {
	ReadCurrentPlan(context.Context) (*PlanningResponse, error)
	ReadStepConfigsFromSubdir(context.Context, string) ([]StepConfig, error)
	WriteStepConfigsToSubdir(context.Context, string, []StepConfig) error
	GetSelectedServers() []string
	GetWorkspacePath() string
	ReadWorkspaceFile(context.Context, string) (string, error)
	WriteWorkspaceFile(context.Context, string, string) error
}

const updateStepConfigToolDescription = "Update planning/step_config.json for a specific workflow step. Changes take effect on the next execute_step or run_full_workflow call. To REMOVE a field (so the step falls back to preset/default behavior where that field has a fallback, or removes the explicit setting otherwise), list its name in clear_fields — sending null in a value field does NOT clear; it's ignored."

func getUpdateStepConfigParameters() map[string]interface{} {
	lockCodeDescription := "If true, the Builder's own execute_step also stops rewriting the saved main.py (no repair loop). Runs never rewrite a script whatever this says: a run executes the saved main.py and fails on its error, which Pulse reports. Only applies to scripted steps."
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"step_id": map[string]interface{}{
				"type":        "string",
				"description": "The step ID from planning/plan.json.",
			},
			"clear_fields": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Field names to CLEAR (remove from step_config.json) so the step uses preset/default behavior again. Clearing enabled_skills removes explicit step skills; step execution does not inherit workflow-selected skills, so set enabled_skills explicitly when the step needs installed skills. Only fields with a corresponding setter in this tool are clearable. Valid names: execution_llm, execution_llm_reason, execution_tier, execution_tier_reason, servers, tools, enabled_custom_tools, enabled_skills, additional_read_paths, learning_objective, lock_code, use_code_execution_mode, disable_parallel_tool_execution, coding_agent_tmux_lifecycle, description_reviewed, knowledgebase_access, knowledgebase_contribution, learnings_access, review_notes, validation_schema. Unknown names are reported as errors; nothing else in the same call is applied.",
			},
			"servers": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "MCP server names to use for this step",
			},
			"tools": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Tool names to enable for this step (format: 'server:tool' or 'server:*')",
			},
			"learning_objective": map[string]interface{}{
				"type":        "string",
				"description": "Extraction instruction for the step agent's direct post-completion learning turn. Use only for reusable execution HOW: browser selectors/timing/auth flows, API/MCP request and response quirks, CLI/SDK command patterns, parsing rules, retries, recovery, or file-format pitfalls. Do not use for facts/results, report data, routing decisions, validation-only steps, mechanical transforms, human approvals, pure db/KB readers, or mature scripted steps whose main.py already captures the method. Example: 'Capture the Buffer API create-update request shape, success fields, 401/429 handling, and output id parsing.' Required when learnings_access=\"read-write\" (the validator rejects write access with an empty objective).",
			},
			"lock_code": map[string]interface{}{
				"type":        "boolean",
				"description": lockCodeDescription,
			},
			"enabled_custom_tools": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Optional workspace/custom tools to add (format: 'category:tool' or 'category:*'). Every execution agent already receives workspace_advanced plus the narrow human baseline: human_feedback, notify_user, and create_human_input_request. Name any additional human tool explicitly. Legacy human_tools:* is normalized to the same three-tool baseline and does not grant the complete category. workspace_browser:agent_browser enables browser automation.",
			},
			"enabled_skills": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Skill folder names to enable for this step. Step execution only receives skills listed here; workflow-level selected skills are builder/workshop context and do not cascade into runtime steps. Use list_skills to see installed skills and get_workflow_config to see the workflow's currently selected skills for discovery/reference.",
			},
			"additional_read_paths": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Additional workflow-relative files or folders this step may READ, such as [\"variables\"] or [\"reports/reference.json\"]. Use only when the normal execution/db/KB/learnings paths do not cover a declared input. Paths are read-only and must remain inside the current workflow: absolute paths, '.', and '..' traversal are rejected. This never grants writes.",
			},
			"knowledgebase_access": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"read", "write", "read-write", "none"},
				"description": knowledgebaseAccessDescription,
			},
			"learnings_access": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"read", "read-write", "none"},
				"description": "Access mode for this step against learnings/_global/ (SKILL.md + references/). Defaults to 'read' — every step sees the workflow's accumulated how-to knowledge in its prompt. 'read-write' — step contributes reusable execution HOW and requires a concrete learning_objective; use for browser/API/CLI/SDK/MCP/parsing/retry discoveries. Keep routing, validation, mechanical transform, aggregation/report-shaping, human approval, pure db/KB reader, and mature scripted steps read-only. 'none' — step neither reads global skill nor contributes; use rarely, only when shared HOW would mislead the step or token isolation is important. Omit to keep the default.",
			},
			"knowledgebase_contribution": map[string]interface{}{
				"type":        "string",
				"description": "Natural-language contribution instruction. It becomes the step agent's contribution contract, injected into its post-completion self-review turn. KB writes only happen when this is non-empty AND knowledgebase_access grants write — an empty contribution means the step performs no KB writes regardless of access. Leave empty to skip KB updates for this step.",
			},
			"disable_parallel_tool_execution": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, force the LLM to emit only one tool call per turn for this step. Use when tool calls must run strictly sequentially (e.g., stateful browser sessions, file edits with ordering dependencies, or when the agent is making mistakes by racing parallel calls). Default (omit/false) = parallel tool calls allowed. For todo_task steps, child tasks inherit this setting from the parent.",
			},
			"coding_agent_tmux_lifecycle": map[string]interface{}{
				"type":        "string",
				"enum":        []interface{}{"close_on_completion", "keep_alive"},
				"description": "Lifecycle for tmux-backed coding providers on this step. Default/omit is close_on_completion: the step/sub-agent gets a bounded terminal that is closed when its turn completes. Use keep_alive only when a step intentionally needs its native coding-CLI session to survive after completion for later live steering or debugging; this can leave more tmux sessions open.",
			},
			"use_code_execution_mode": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, enable code execution mode — the agent writes and executes Python/shell code via mcpbridge to interact with MCP tools, rather than calling them directly. Useful for complex data processing or programmatic control over MCP tools. If false, explicitly disables code execution. Omit to inherit the preset default.",
			},
			"description_reviewed": map[string]interface{}{
				"type":        "boolean",
				"description": "True when the step description has been reviewed — covers BOTH clarity/optimization for execution AND confirmation that the description contains no secrets, hardcoded credentials, or user/run-specific values. Clear this (via clear_fields) if the description meaningfully changes.",
			},
			"review_notes": map[string]interface{}{
				"type":        "string",
				"description": "Free-form rationale covering why the config, locks, learning/KB choices, or description review state are justified. Cite concrete evidence — e.g., 'description is clear and secret-free; passed 3 groups with eval >= 9; learnings stable; pre-validation catches format regressions'. Persisted so later Pulse, plan-change, and review passes see the context.",
			},
			"execution_llm": map[string]interface{}{
				"type":        "object",
				"description": "Override the execution LLM for this step. Use get_llm_config to see available models.",
				"properties": map[string]interface{}{
					"published_llm_id": map[string]interface{}{"type": "string", "description": "Optional id from the published LLM set."},
					"provider":         map[string]interface{}{"type": "string", "description": "LLM provider (e.g., 'openai', 'anthropic', 'bedrock', 'openrouter', 'vertex', 'azure')"},
					"model_id":         map[string]interface{}{"type": "string", "description": "Model ID (e.g., 'gpt-4o', 'claude-sonnet-4-20250514')"},
					"options":          map[string]interface{}{"type": "object", "description": "Provider-specific runtime options copied from the published LLM, such as reasoning_effort.", "additionalProperties": true},
				},
			},
			"execution_tier": map[string]interface{}{
				"type":        "string",
				"enum":        []interface{}{"high", "medium", "low"},
				"description": "Persistent execution tier override for this workflow step in tiered mode. Use high for subjective/ambiguous judgment, medium for normal checks, low for deterministic/file-shape checks. execution_llm still takes precedence, and execute_step(..., tier=...) can still override step tier for a single run. REQUIRES execution_tier_reason. Pulse Architecture reviews model quality, cost, latency and retries to propose measured tier trials. Successful-run counts never change tiers. Unset execution steps default to high. Preserve user pins and apply changes through the existing approval contract.",
			},
			"execution_tier_reason": map[string]interface{}{
				"type":        "string",
				"description": "Why this step's tier is pinned. Required whenever execution_tier is set. Cite the owning architecture_review finding id, the current state, and the evidence (and the human_input_id if the change was user-approved). If the evidence does not settle it, do not pin: raise a decision with create_human_input_request and park the finding awaiting_user.",
			},
			"execution_llm_reason": map[string]interface{}{
				"type":        "string",
				"description": "Why this step is pinned to a specific model. Required whenever execution_llm is set. A pin outranks execution_tier entirely and will not follow provider-profile updates, so state the capability/cost comparison that justified it, citing the owning architecture_review finding id (and the human_input_id if approved).",
			},
			"validation_llm": map[string]interface{}{
				"type":        "object",
				"description": "Override the validation LLM for this step.",
				"properties": map[string]interface{}{
					"provider": map[string]interface{}{"type": "string"},
					"model_id": map[string]interface{}{"type": "string"},
				},
			},
			"validation_schema": map[string]interface{}{
				"type":        "object",
				"description": "Override the pre-validation schema for this step. Takes precedence over plan.json validation_schema. Defines file existence checks and JSON structure validation rules.",
				"properties": map[string]interface{}{
					"files": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"file_name":   map[string]interface{}{"type": "string", "description": "File name to validate (e.g., 'results.json')"},
								"must_exist":  map[string]interface{}{"type": "boolean", "description": "Whether the file must exist"},
								"json_checks": map[string]interface{}{"type": "array", "description": "JSON structure validation checks"},
							},
						},
					},
				},
			},
			"reason": map[string]interface{}{
				"type":        "string",
				"description": "REQUIRED: One-sentence rationale for why this step config is being updated. Captured into the plan changelog.",
			},
		},
		"required": []string{"step_id", "reason"},
	}
}

func createUpdateStepConfigExecutor(runtime stepConfigToolRuntime, logger loggerv2.Logger) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		stepIDRaw, ok := args["step_id"]
		if !ok || stepIDRaw == nil {
			return "step_id is required", nil
		}
		stepID, ok := stepIDRaw.(string)
		if !ok || stepID == "" {
			return "step_id must be a non-empty string", nil
		}
		reasonRaw, _ := args["reason"].(string)
		if strings.TrimSpace(reasonRaw) == "" {
			return "reason is required (one-sentence rationale captured into the plan changelog)", nil
		}

		resolvedStepID, resolveErr := resolveWorkshopStepConfigTarget(ctx, runtime, stepID)
		if resolveErr != nil {
			return resolveErr.Error(), nil
		}
		stepID = resolvedStepID
		configSubdir := "planning"

		// Read existing configs from the durable planning/step_config.json.
		configs, err := runtime.ReadStepConfigsFromSubdir(ctx, configSubdir)
		if err != nil {
			configs = []StepConfig{}
		}
		// Find or create entry for this step
		var targetConfig *StepConfig
		for i := range configs {
			if configs[i].ID == stepID {
				targetConfig = &configs[i]
				break
			}
		}
		if targetConfig == nil {
			configs = append(configs, StepConfig{ID: stepID})
			targetConfig = &configs[len(configs)-1]
		}
		if targetConfig.AgentConfigs == nil {
			targetConfig.AgentConfigs = &AgentConfigs{}
		}

		// Snapshot the step's config exactly as it stood before this call's
		// mutations, decoupled from *targetConfig via a JSON round trip so
		// later in-place field writes below cannot retroactively change it.
		// This is what makes the changelog's before_ref real instead of the
		// sha256("[]") placeholder every prior call recorded (PLAT-033) — the
		// entry below never set Changes at all, so before_ref/after_ref
		// always hashed the same empty list regardless of what changed.
		var beforeStepConfigSnapshot interface{}
		if beforeJSON, beforeErr := json.Marshal(targetConfig); beforeErr == nil {
			_ = json.Unmarshal(beforeJSON, &beforeStepConfigSnapshot)
		}

		// Apply provided fields
		if val, ok := args["servers"]; ok && val != nil {
			if arr, ok := val.([]interface{}); ok {
				servers := make([]string, 0, len(arr))
				for _, v := range arr {
					if s, ok := v.(string); ok {
						servers = append(servers, s)
					}
				}
				targetConfig.AgentConfigs.SelectedServers = servers
			}
		}
		if val, ok := args["tools"]; ok && val != nil {
			if arr, ok := val.([]interface{}); ok {
				tools := make([]string, 0, len(arr))
				for _, v := range arr {
					if s, ok := v.(string); ok {
						tools = append(tools, s)
					}
				}
				targetConfig.AgentConfigs.SelectedTools = tools
			}
		}
		if val, ok := args["learning_objective"]; ok && val != nil {
			if s, ok := val.(string); ok {
				targetConfig.AgentConfigs.LearningObjective = strings.TrimSpace(s)
			}
		}
		if val, ok := args["lock_code"]; ok && val != nil {
			if b, ok := val.(bool); ok {
				if b || targetConfig.AgentConfigs.LockCode == nil || !*targetConfig.AgentConfigs.LockCode {
					targetConfig.AgentConfigs.LockCode = &b
				}
			}
		}
		if val, ok := args["enabled_custom_tools"]; ok && val != nil {
			if arr, ok := val.([]interface{}); ok {
				customTools := make([]string, 0, len(arr))
				for _, v := range arr {
					if s, ok := v.(string); ok {
						customTools = append(customTools, s)
					}
				}
				targetConfig.AgentConfigs.EnabledCustomTools = customTools
			}
		}
		if val, ok := args["enabled_skills"]; ok && val != nil {
			if arr, ok := val.([]interface{}); ok {
				enabledSkills := make([]string, 0, len(arr))
				for _, v := range arr {
					if s, ok := v.(string); ok {
						enabledSkills = append(enabledSkills, s)
					}
				}
				targetConfig.AgentConfigs.EnabledSkills = enabledSkills
			}
		}
		if val, ok := args["additional_read_paths"]; ok && val != nil {
			if arr, ok := val.([]interface{}); ok {
				additionalReadPaths := make([]string, 0, len(arr))
				for _, v := range arr {
					if s, ok := v.(string); ok {
						additionalReadPaths = append(additionalReadPaths, s)
					}
				}
				targetConfig.AgentConfigs.AdditionalReadPaths = additionalReadPaths
			}
		}
		if val, ok := args["knowledgebase_access"]; ok && val != nil {
			if s, ok := val.(string); ok {
				targetConfig.AgentConfigs.KnowledgebaseAccess = s
			}
		}
		if val, ok := args["knowledgebase_contribution"]; ok && val != nil {
			if s, ok := val.(string); ok {
				targetConfig.AgentConfigs.KnowledgebaseContribution = s
			}
		}
		if val, ok := args["learnings_access"]; ok && val != nil {
			if s, ok := val.(string); ok {
				targetConfig.AgentConfigs.LearningsAccess = s
			}
		}
		if val, ok := args["disable_parallel_tool_execution"]; ok && val != nil {
			if b, ok := val.(bool); ok {
				targetConfig.AgentConfigs.DisableParallelToolExecution = &b
			}
		}
		if val, ok := args["coding_agent_tmux_lifecycle"]; ok && val != nil {
			if s, ok := val.(string); ok {
				lifecycle := strings.TrimSpace(s)
				if normalized := normalizeCodingAgentTmuxLifecycle(lifecycle); normalized != "" {
					lifecycle = normalized
				}
				targetConfig.AgentConfigs.CodingAgentTmuxLifecycle = lifecycle
			}
		}
		if val, ok := args["use_code_execution_mode"]; ok && val != nil {
			if b, ok := val.(bool); ok {
				targetConfig.AgentConfigs.UseCodeExecutionMode = &b
			}
		}
		// PLAT-287: declared_execution_mode is retired. A step's plan type
		// decides how it runs. Point at the real tools rather than silently
		// dropping the argument.
		for _, retired := range []string{"declared_execution_mode", "declared_execution_mode_reason"} {
			if val, ok := args[retired]; ok && val != nil {
				return "", fmt.Errorf("%s is retired (PLAT-287): a step's plan type decides how it runs (regular = scripted main.py, message_sequence = conversational) -- use change_step_type(step_id=%q, target_type=\"scripted\"|\"message_sequence\", reason=...)", retired, stepID)
			}
		}
		if val, ok := args["description_reviewed"]; ok && val != nil {
			if b, ok := val.(bool); ok {
				targetConfig.AgentConfigs.DescriptionReviewed = &b
			}
		}
		if val, ok := args["review_notes"]; ok && val != nil {
			if s, ok := val.(string); ok {
				targetConfig.AgentConfigs.ReviewNotes = strings.TrimSpace(s)
			}
		}
		if val, ok := args["execution_tier_reason"]; ok && val != nil {
			if s, ok := val.(string); ok {
				targetConfig.AgentConfigs.ExecutionTierReason = strings.TrimSpace(s)
			}
		}
		if val, ok := args["execution_llm_reason"]; ok && val != nil {
			if s, ok := val.(string); ok {
				targetConfig.AgentConfigs.ExecutionLLMReason = strings.TrimSpace(s)
			}
		}
		if val, ok := args["execution_tier"]; ok && val != nil {
			if s, ok := val.(string); ok {
				tier := strings.ToLower(strings.TrimSpace(s))
				if err := validateExecutionTierChange(tier, targetConfig.AgentConfigs.ExecutionTierReason); err != nil {
					return "", err
				}
				targetConfig.AgentConfigs.ExecutionTier = tier
			}
		}

		// Parse LLM override fields

		llmFields := []struct {
			key    string
			target **AgentLLMConfig
		}{
			{"execution_llm", &targetConfig.AgentConfigs.ExecutionLLM},
		}
		for _, f := range llmFields {
			if val, ok := args[f.key]; ok && val != nil {
				if llmMap, ok := val.(map[string]interface{}); ok {
					provider, _ := llmMap["provider"].(string)
					modelID, _ := llmMap["model_id"].(string)
					if provider != "" && modelID != "" {
						if err := validateExecutionLLMChange(true, targetConfig.AgentConfigs.ExecutionLLMReason); err != nil {
							return "", err
						}
						publishedLLMID, _ := llmMap["published_llm_id"].(string)
						options, _ := llmMap["options"].(map[string]interface{})
						*f.target = &AgentLLMConfig{
							PublishedLLMID: publishedLLMID,
							Provider:       provider,
							ModelID:        modelID,
							Options:        options,
						}
					}
				}
			}
		}

		// Parse validation_schema override
		if val, ok := args["validation_schema"]; ok && val != nil {
			// Marshal back to JSON and unmarshal into ValidationSchema struct
			vsJSON, jsonErr := json.Marshal(val)
			if jsonErr == nil {
				var vs ValidationSchema
				if jsonErr := json.Unmarshal(vsJSON, &vs); jsonErr == nil {
					targetConfig.ValidationSchema = &vs
					logger.Info(fmt.Sprintf("🔧 Step config for %q: validation_schema updated (%d file rules)", stepID, len(vs.Files)))
				} else {
					logger.Warn(fmt.Sprintf("⚠️ Failed to parse validation_schema for step %q: %v", stepID, jsonErr))
				}
			}
		}

		// Apply clear_fields LAST so explicit clears override any sets in the same call.
		// Writing null to a setter field is a no-op by design (LLMs send false/nil for
		// unrelated booleans); clear_fields is the explicit opt-in for removal.
		var clearedFields []string
		var unknownClearFields []string
		var retiredClearFields []string
		if rawClear, ok := args["clear_fields"]; ok && rawClear != nil {
			if arr, ok := rawClear.([]interface{}); ok {
				for _, v := range arr {
					name, ok := v.(string)
					if !ok || name == "" {
						continue
					}
					if clearStepConfigField(targetConfig, name) {
						clearedFields = append(clearedFields, name)
					} else if why, retired := isRetiredStepConfigClearField(name); retired {
						// PLAT-061: acknowledged no-op. Do not fail the call over a
						// field that is already ignored, and do not claim a change.
						retiredClearFields = append(retiredClearFields, fmt.Sprintf("%s (%s)", name, why))
					} else {
						unknownClearFields = append(unknownClearFields, name)
					}
				}
			}
		}
		if len(unknownClearFields) > 0 {
			return fmt.Sprintf("unknown clear_fields entries %v — no changes applied. Valid names are listed in the tool's clear_fields description.", unknownClearFields), nil
		}

		// --- Code-level validations ---
		// Collect errors (block save) and warnings (save but inform).
		var errors []string
		warnings := make([]string, 0)
		if len(retiredClearFields) > 0 {
			warnings = append(warnings, fmt.Sprintf("clear_fields named retired fields, which was a no-op: %s. Any stored value is already ignored by the runtime; nothing needed clearing.", strings.Join(retiredClearFields, "; ")))
		}

		// 1. Validate step ID exists in the plan.
		// Refresh from disk first so steps just added by other plan-mod tools in the
		// same turn (e.g. add_todo_task_route on a nested parent) are visible — the
		// query must not reuse a snapshot from a prior call.
		if _, resolveErr := resolveWorkshopStepConfigTarget(ctx, runtime, stepID); resolveErr != nil {
			errors = append(errors, fmt.Sprintf("Step ID %q not found in planning/plan.json.", stepID))
		}

		// 2. Validate servers exist in workflow-level selection
		workflowServers := runtime.GetSelectedServers()
		workflowServerSet := make(map[string]bool, len(workflowServers))
		for _, s := range workflowServers {
			workflowServerSet[s] = true
		}
		if len(targetConfig.AgentConfigs.SelectedServers) > 0 {
			var badServers []string
			for _, s := range targetConfig.AgentConfigs.SelectedServers {
				if !workflowServerSet[s] {
					badServers = append(badServers, s)
				}
			}
			if len(badServers) > 0 {
				errors = append(errors, fmt.Sprintf("Servers %v are NOT in the workflow-level selection %v and will be IGNORED at execution time. Remove them or add them to the workflow first.", badServers, workflowServers))
			}
		}

		// 3. Validate selected_tools format (should be "server:tool" or "server:*")
		if len(targetConfig.AgentConfigs.SelectedTools) > 0 {
			for _, t := range targetConfig.AgentConfigs.SelectedTools {
				if !strings.Contains(t, ":") {
					errors = append(errors, fmt.Sprintf("Tool %q is missing server prefix. Expected format: 'server:tool_name' or 'server:*'.", t))
				}
			}
		}

		// 4. Validate tools reference servers that are selected
		if len(targetConfig.AgentConfigs.SelectedTools) > 0 && len(targetConfig.AgentConfigs.SelectedServers) > 0 {
			stepServerSet := make(map[string]bool, len(targetConfig.AgentConfigs.SelectedServers))
			for _, s := range targetConfig.AgentConfigs.SelectedServers {
				stepServerSet[s] = true
			}
			for _, t := range targetConfig.AgentConfigs.SelectedTools {
				if idx := strings.Index(t, ":"); idx >= 0 {
					serverPart := t[:idx]
					if !stepServerSet[serverPart] {
						errors = append(errors, fmt.Sprintf("Tool %q references server %q which is not in selected_servers %v. Add the server or remove the tool.", t, serverPart, targetConfig.AgentConfigs.SelectedServers))
					}
				}
			}
		}

		// 5. Validate enabled_custom_tools format and categories
		validCustomCategories := map[string]bool{
			"workspace_advanced": true,
			"human_tools":        true,
			"workspace_browser":  true,
		}
		if len(targetConfig.AgentConfigs.EnabledCustomTools) > 0 {
			for _, t := range targetConfig.AgentConfigs.EnabledCustomTools {
				if idx := strings.Index(t, ":"); idx >= 0 {
					cat := t[:idx]
					if !validCustomCategories[cat] {
						errors = append(errors, fmt.Sprintf("Custom tool %q uses unknown category %q. Valid categories: workspace_advanced, human_tools, workspace_browser.", t, cat))
					}
				} else {
					errors = append(errors, fmt.Sprintf("Custom tool %q is missing category prefix. Expected format: 'category:tool_name' or 'category:*'.", t))
				}
			}

		}

		// 6. Validate learning config consistency.
		// Learnings access ↔ objective consistency. Mirror of the KB access ↔
		// contribution rule below: write-capable access is meaningless without an
		// extraction instruction for the direct post-completion learning turn.
		learningsAccessRaw := strings.TrimSpace(targetConfig.AgentConfigs.LearningsAccess)
		if learningsAccessRaw != "" {
			validLearningsModes := map[string]bool{
				LearningsAccessRead: true, LearningsAccessReadWrite: true, LearningsAccessNone: true,
			}
			if !validLearningsModes[learningsAccessRaw] {
				errors = append(errors, fmt.Sprintf("learnings_access %q is not recognized. Valid values: \"read\", \"read-write\", \"none\".", learningsAccessRaw))
			}
		}
		hasObjective := strings.TrimSpace(targetConfig.AgentConfigs.LearningObjective) != ""
		effectiveAccess := resolveLearningsAccess(targetConfig.AgentConfigs)
		if effectiveAccess == LearningsAccessReadWrite && !hasObjective {
			errors = append(errors, "learnings_access=\"read-write\" requires a non-empty learning_objective. The direct learnings turn needs an extraction instruction; set learning_objective or drop access to \"read\"/\"none\".")
		}
		// 6b. Validate execution_tier.
		if rawExecutionTier := strings.TrimSpace(targetConfig.AgentConfigs.ExecutionTier); rawExecutionTier != "" {
			if NormalizeTierOverride(rawExecutionTier) == "" {
				errors = append(errors, fmt.Sprintf("execution_tier %q is not recognized. Valid values: \"high\", \"medium\", \"low\".", rawExecutionTier))
			}
			if targetConfig.AgentConfigs.ExecutionLLM != nil {
				warnings = append(warnings, "execution_tier is set but execution_llm takes precedence, so the tier override will be ignored until execution_llm is cleared.")
			}
		}
		// 6c. Validate execution_llm shape.
		//
		// execution_tier and coding_agent_tmux_lifecycle were validated here but
		// execution_llm was not, so a malformed override reached the runtime
		// untouched and failed the step at turn 1 with "all LLMs failed (primary +
		// 0 fallbacks)". A live workflow had all four evaluation steps pinned to
		// provider "claude-code" with model_id "claude-code" — the provider name
		// repeated in the model slot — which the CLI rejects as a model that does
		// not exist. These are cheap structural checks; they cannot confirm a model
		// exists, only that the pairing is not self-evidently wrong.
		for _, llm := range collectStepLLMConfigsForValidation(targetConfig.AgentConfigs.ExecutionLLM) {
			if err := validateStepLLMConfig(llm.label, llm.publishedID, llm.provider, llm.modelID); err != "" {
				errors = append(errors, err)
			}
		}
		if rawLifecycle := strings.TrimSpace(targetConfig.AgentConfigs.CodingAgentTmuxLifecycle); rawLifecycle != "" {
			if normalizeCodingAgentTmuxLifecycle(rawLifecycle) == "" {
				errors = append(errors, fmt.Sprintf("coding_agent_tmux_lifecycle %q is not recognized. Valid values: \"close_on_completion\", \"keep_alive\".", rawLifecycle))
			}
		}
		if normalizedPaths, normalizeErr := normalizeAdditionalReadPaths(targetConfig.AgentConfigs.AdditionalReadPaths); normalizeErr != nil {
			errors = append(errors, normalizeErr.Error()+".")
		} else {
			targetConfig.AgentConfigs.AdditionalReadPaths = normalizedPaths
		}
		// 7. Validate KB access ↔ contribution consistency.
		// When knowledgebase_access grants write, knowledgebase_contribution MUST be
		// non-empty — otherwise the post-step KB update agent is silently skipped
		// (controller_kb_update.go:33 gates on a non-empty contribution). Mirror of
		// the learning rule: opting in to the write-capable access is meaningless
		// without an extraction instruction for the KB agent to act on.
		if kbAccessAllowsWrite(targetConfig.AgentConfigs.KnowledgebaseAccess) &&
			strings.TrimSpace(targetConfig.AgentConfigs.KnowledgebaseContribution) == "" {
			errors = append(errors, fmt.Sprintf("knowledgebase_access=%q requires a non-empty knowledgebase_contribution. Write access without an extraction instruction means the post-step KB update agent never runs; set knowledgebase_contribution or drop access to \"read\"/\"none\".", targetConfig.AgentConfigs.KnowledgebaseAccess))
		}

		// 8. Validate KB write-method enum + direct-mode pairing: direct write
		// needs access permitting writes AND a non-empty contribution string
		// (which becomes the self-review turn's contract).
		// KB writes always run as the step agent's own closing turn now, so the
		// access + contribution requirements apply whenever write access is granted
		// — not only when "direct" was set explicitly.
		if kbAccessAllowsWrite(targetConfig.AgentConfigs.KnowledgebaseAccess) &&
			strings.TrimSpace(targetConfig.AgentConfigs.KnowledgebaseContribution) == "" {
			warnings = append(warnings, "knowledgebase_access grants writes but knowledgebase_contribution is empty, so this step performs no KB writes. Set a contribution or drop the write access.")
		}
		if strings.TrimSpace(targetConfig.AgentConfigs.KnowledgebaseContribution) != "" &&
			!kbAccessAllowsWrite(targetConfig.AgentConfigs.KnowledgebaseAccess) {
			errors = append(errors, fmt.Sprintf("knowledgebase_contribution is set but knowledgebase_access %q does not permit writes. Use \"write\" or \"read-write\", or clear the contribution.", targetConfig.AgentConfigs.KnowledgebaseAccess))
		}

		// 9. Learnings write contract: access + objective. SKILL.md writes always
		// happen through the step agent's own post-completion turn; there is no
		// write-method choice to validate.
		if effectiveAccess == LearningsAccessReadWrite {
			if !hasObjective {
				errors = append(errors, "learnings_access=\"read-write\" requires a non-empty learning_objective. The objective is injected into the step's dedicated learnings turn as the SKILL.md contribution contract; without it the turn has nothing to instruct.")
			}
		}

		// If there are errors, reject the update and return feedback
		if len(errors) > 0 {
			result := fmt.Sprintf("❌ Step config for %q was NOT saved due to validation errors:\n", stepID)
			for i, e := range errors {
				result += fmt.Sprintf("\n%d. %s", i+1, e)
			}
			result += "\n\nFix the errors above and try again."
			if len(warnings) > 0 {
				result += "\n\nAlso note these warnings:"
				for i, w := range warnings {
					result += fmt.Sprintf("\n%d. %s", i+1, w)
				}
			}
			return result, nil
		}

		// Write updated configs back to the matching durable config file.
		if err := runtime.WriteStepConfigsToSubdir(ctx, configSubdir, configs); err != nil {
			return fmt.Sprintf("Failed to update step config: %v", err), nil
		}

		workspacePath := runtime.GetWorkspacePath()
		configPath := configSubdir + "/step_config.json"
		cleanupMessages := make([]string, 0, 1)

		// Re-read the persisted state rather than reusing the in-memory
		// targetConfig, so the after-snapshot reflects what
		// WriteStepConfigsToSubdir actually wrote — including any
		// normalization or silent field drop it applies — not what this
		// call asked for.
		var afterStepConfigSnapshot interface{}
		if persistedConfigs, readErr := runtime.ReadStepConfigsFromSubdir(ctx, configSubdir); readErr == nil {
			for i := range persistedConfigs {
				if persistedConfigs[i].ID == stepID {
					if afterJSON, afterErr := json.Marshal(persistedConfigs[i]); afterErr == nil {
						_ = json.Unmarshal(afterJSON, &afterStepConfigSnapshot)
					}
					break
				}
			}
		}
		if err := writePlanChangelogEntry(ctx, workspacePath, PlanChangelogEntry{
			Tool:           "update_step_config",
			Reason:         strings.TrimSpace(reasonRaw),
			StepIDs:        []string{stepID},
			Changes:        []PlanFieldChange{{StepID: stepID, Field: "agent_configs", OldValue: beforeStepConfigSnapshot, NewValue: afterStepConfigSnapshot}},
			Target:         configPath,
			BeforeSnapshot: beforeStepConfigSnapshot,
			AfterSnapshot:  afterStepConfigSnapshot,
		}, runtime.ReadWorkspaceFile, runtime.WriteWorkspaceFile, logger); err != nil {
			logger.Warn(fmt.Sprintf("⚠️ Plan changelog write failed (non-fatal): %v", err))
		}

		logger.Info(fmt.Sprintf("📝 Workshop: step config updated for step %q in %s/step_config.json", stepID, configSubdir))
		result := fmt.Sprintf("Step config for %q updated successfully in %s. Changes will take effect on the next execute_step or run_full_workflow call.", stepID, configPath)
		if len(cleanupMessages) > 0 {
			result += "\n\nCleanup:"
			for _, msg := range cleanupMessages {
				result += "\n- " + msg
			}
		}
		if len(warnings) > 0 {
			result += "\n\n⚠️ WARNINGS:"
			for i, w := range warnings {
				result += fmt.Sprintf("\n%d. %s", i+1, w)
			}
		}
		return result, nil
	}
}

// ExternalPlanToolDefinition describes one explicitly supported plan operation.
// InputSchema contains only native arguments; transport workflow/revision fields
// are handled by the caller before execution.
type ExternalPlanToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type externalPlanExecutor func(context.Context, map[string]interface{}) (string, error)
type externalPlanTool struct {
	definition ExternalPlanToolDefinition
	schema     *jsonschema.Schema
	executor   func(*externalPlanRuntime, loggerv2.Logger) externalPlanExecutor
}

var externalPlanToolsOnce sync.Once
var externalPlanTools []externalPlanTool

func externalPlanToolRegistry() []externalPlanTool {
	externalPlanToolsOnce.Do(func() {
		add := func(name, description, schemaText string, factory func(*externalPlanRuntime, loggerv2.Logger) externalPlanExecutor) {
			var schema map[string]interface{}
			if err := json.Unmarshal([]byte(schemaText), &schema); err != nil {
				panic(fmt.Sprintf("invalid native schema for %s: %v", name, err))
			}
			closeExternalPlanSchema(schema)
			schema["additionalProperties"] = false
			encoded, err := json.Marshal(schema)
			if err != nil {
				panic(err)
			}
			compiler := jsonschema.NewCompiler()
			uri := "https://agentworks.invalid/plan-tools/" + name + ".json"
			if err := compiler.AddResource(uri, schema); err != nil {
				panic(err)
			}
			compiled, err := compiler.Compile(uri)
			if err != nil {
				panic(fmt.Sprintf("invalid native schema for %s: %v", name, err))
			}
			externalPlanTools = append(externalPlanTools, externalPlanTool{ExternalPlanToolDefinition{name, description, encoded}, compiled, factory})
		}
		add("update_scripted_step", "Update an existing scripted step contract.", getUpdateRegularStepSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createUpdateRegularStepExecutor(r.workspacePath, l, r.readFile, r.writeFile)
		})
		add("update_message_sequence_step", "Update an existing conversational message sequence step.", getUpdateMessageSequenceStepSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createUpdateMessageSequenceStepExecutor(r.workspacePath, l, r.readFile, r.writeFile)
		})
		add("update_routing_step", "Update an existing routing step.", getUpdateRoutingStepSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createUpdateRoutingStepExecutor(r.workspacePath, l, r.readFile, r.writeFile)
		})
		add("update_branch_step", "Update an existing branch step.", getUpdateBranchStepSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createUpdateBranchStepExecutor(r.workspacePath, l, r.readFile, r.writeFile)
		})
		add("update_human_input_step", "Update an existing human input step.", getUpdateHumanInputStepSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createUpdateHumanInputStepExecutor(r.workspacePath, l, r.readFile, r.writeFile)
		})
		add("update_orchestrator_step", "Update an existing legacy orchestrator compatibility record; new adaptive agents use message_sequence with predefined_routes.", getUpdateOrchestratorStepSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createUpdateOrchestratorStepExecutor(r.workspacePath, l, r.readFile, r.writeFile)
		})
		add("update_crew_step", "Update an existing crew step.", getUpdateCrewStepSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createUpdateCrewStepExecutor(r.workspacePath, l, r.readFile, r.writeFile)
		})
		add("add_scripted_step", "Add a scripted step to an existing plan.", getAddRegularStepSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createAddRegularStepExecutor(r.workspacePath, l, r.readFile, r.writeFile, r.moveFile)
		})
		add("add_message_sequence_step", "Add an agent step whose description is the system charter and whose items are user turns; optional predefined_routes enable adaptive specialist delegation.", getAddMessageSequenceStepSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createAddMessageSequenceStepExecutor(r.workspacePath, l, r.readFile, r.writeFile, r.moveFile)
		})
		add("add_routing_step", "Add a routing step.", getAddRoutingStepSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createAddRoutingStepExecutor(r.workspacePath, l, r.readFile, r.writeFile, r.moveFile)
		})
		add("add_branch_step", "Add a branch step.", getAddBranchStepSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createAddBranchStepExecutor(r.workspacePath, l, r.readFile, r.writeFile, r.moveFile)
		})
		add("add_human_input_step", "Add a human input step.", getAddHumanInputStepSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createAddHumanInputStepExecutor(r.workspacePath, l, r.readFile, r.writeFile, r.moveFile)
		})
		add("add_orchestrator_step", "Legacy compatibility action; new adaptive agents use add_message_sequence_step with predefined_routes.", getAddOrchestratorStepSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createAddOrchestratorStepExecutor(r.workspacePath, l, r.readFile, r.writeFile, r.moveFile)
		})
		add("add_crew_step", "Add a crew step.", getAddCrewStepSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createAddCrewStepExecutor(r.workspacePath, l, r.readFile, r.writeFile, r.moveFile)
		})
		add("delete_plan_steps", "Delete steps, rejecting deletion when remaining steps reference them.", getDeletePlanStepsSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createDeletePlanStepsExecutor(r.workspacePath, l, r.readFile, r.writeFile, r.moveFile)
		})
		add("update_validation_schema", "Update a step validation schema.", getUpdateValidationSchemaSchema(), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createUpdateValidationSchemaExecutor(r.workspacePath, l, r.readFile, r.writeFile)
		})

		configSchema, err := json.Marshal(getUpdateStepConfigParameters())
		if err != nil {
			panic(err)
		}
		add("update_step_config", updateStepConfigToolDescription, string(configSchema), func(r *externalPlanRuntime, l loggerv2.Logger) externalPlanExecutor {
			return createUpdateStepConfigExecutor(r, l)
		})
	})
	return externalPlanTools
}

// Close declared object shapes while retaining native explicit dictionaries and
// unconstrained JSON values (such as a parameter's default).
func closeExternalPlanSchema(schema map[string]interface{}) {
	if props, ok := schema["properties"].(map[string]interface{}); ok {
		if _, explicit := schema["additionalProperties"]; !explicit {
			schema["additionalProperties"] = false
		}
		for _, raw := range props {
			if child, ok := raw.(map[string]interface{}); ok {
				closeExternalPlanSchema(child)
			}
		}
	}
	for _, key := range []string{"items", "additionalProperties"} {
		if child, ok := schema[key].(map[string]interface{}); ok {
			closeExternalPlanSchema(child)
		}
	}
	for _, key := range []string{"anyOf", "allOf", "oneOf"} {
		if children, ok := schema[key].([]interface{}); ok {
			for _, raw := range children {
				if child, ok := raw.(map[string]interface{}); ok {
					closeExternalPlanSchema(child)
				}
			}
		}
	}
}

// ExternalPlanToolDefinitions returns a detached copy of the allowlisted native
// schemas. Callers cannot alter the registry used to validate future requests.
func ExternalPlanToolDefinitions() []ExternalPlanToolDefinition {
	registry := externalPlanToolRegistry()
	definitions := make([]ExternalPlanToolDefinition, len(registry))
	for i, tool := range registry {
		definitions[i] = tool.definition
		definitions[i].InputSchema = append(json.RawMessage(nil), tool.definition.InputSchema...)
	}
	return definitions
}

type externalPlanSelectedServersKey struct{}

// WithExternalPlanSelectedServers supplies the workflow's server selection from
// trusted server metadata. It must never be populated from tool arguments.
func WithExternalPlanSelectedServers(ctx context.Context, servers []string) context.Context {
	return context.WithValue(ctx, externalPlanSelectedServersKey{}, append([]string(nil), servers...))
}

// ExecuteExternalPlanTool invokes the same executor as the builder, without an
// LLM session. The caller must authorize workspace access, provide confined
// callbacks, enforce revision/busy checks, and coordinate multi-file commits.
// Managed write capability is scoped to executor callbacks and never leaks to
// generic file tools. Native nonfatal warnings are included in the result.
func ExecuteExternalPlanTool(ctx context.Context, name string, args map[string]interface{}, workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, moveFile func(context.Context, string, string) error) (string, error) {
	var tool *externalPlanTool
	for _, candidate := range externalPlanToolRegistry() {
		if candidate.definition.Name == name {
			selected := candidate
			tool = &selected
			break
		}
	}
	if tool == nil {
		return "", fmt.Errorf("unsupported external plan tool %q", name)
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("invalid arguments for %s: %w", name, err)
	}
	normalized, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		return "", fmt.Errorf("invalid arguments for %s: %w", name, err)
	}
	if err := tool.schema.Validate(normalized); err != nil {
		return "", fmt.Errorf("invalid arguments for %s: %w", name, err)
	}
	// Native executors expect the standard encoding/json float64/[]interface{}
	// representation, including when an in-process client supplied typed slices.
	var nativeArgs map[string]interface{}
	if err := json.Unmarshal(encoded, &nativeArgs); err != nil {
		return "", err
	}
	if _, err := requireReason(nativeArgs); err != nil {
		return "", err
	}
	if readFile == nil || writeFile == nil {
		return "", fmt.Errorf("external plan tools require read and write callbacks")
	}
	if moveFile == nil {
		moveFile = func(context.Context, string, string) error {
			return fmt.Errorf("workspace move operation is unavailable")
		}
	}
	if logger == nil {
		logger = loggerv2.NewNoop()
	}
	capture := &externalPlanWarningLogger{Logger: logger, warnings: &externalPlanWarnings{}}
	servers, _ := ctx.Value(externalPlanSelectedServersKey{}).([]string)
	nativeRead := nativePlanReadFile(readFile)
	runtime := &externalPlanRuntime{workspacePath: workspacePath, readFile: nativeRead, writeFile: withPlanMutationWriteAccess(workspacePath, writeFile), moveFile: moveFile, servers: servers}
	result, err := tool.executor(runtime, capture)(withPlanChangeOrigin(ctx, "agentworks-api"), nativeArgs)
	// The legacy config tool reports rejected updates as text with nil error.
	// External transports need an actual failure so scripts do not claim success.
	if err == nil && name == "update_step_config" && runtime.configWrites == 0 {
		err = fmt.Errorf("%s", result)
	}
	capture.warnings.mu.Lock()
	warnings := append([]string(nil), capture.warnings.messages...)
	capture.warnings.mu.Unlock()
	if len(warnings) > 0 {
		result += "\n\nWarnings:\n- " + strings.Join(warnings, "\n- ")
	}
	return result, err
}

type externalPlanWarnings struct {
	mu       sync.Mutex
	messages []string
}
type externalPlanWarningLogger struct {
	loggerv2.Logger
	warnings *externalPlanWarnings
}

func (l *externalPlanWarningLogger) Warn(msg string, fields ...loggerv2.Field) {
	l.warnings.mu.Lock()
	l.warnings.messages = append(l.warnings.messages, msg)
	l.warnings.mu.Unlock()
	l.Logger.Warn(msg, fields...)
}
func (l *externalPlanWarningLogger) With(fields ...loggerv2.Field) loggerv2.Logger {
	return &externalPlanWarningLogger{l.Logger.With(fields...), l.warnings}
}

type externalPlanRuntime struct {
	workspacePath string
	readFile      func(context.Context, string) (string, error)
	writeFile     func(context.Context, string, string) error
	moveFile      func(context.Context, string, string) error
	servers       []string
	configWrites  int
	configReadErr error
}

func (r *externalPlanRuntime) GetWorkspacePath() string { return r.workspacePath }
func (r *externalPlanRuntime) GetSelectedServers() []string {
	return append([]string(nil), r.servers...)
}
func (r *externalPlanRuntime) ReadWorkspaceFile(ctx context.Context, path string) (string, error) {
	return r.readFile(ctx, normalizePathForWorkspaceAPI(path, r.workspacePath))
}
func (r *externalPlanRuntime) WriteWorkspaceFile(ctx context.Context, path, content string) error {
	return r.writeFile(ctx, normalizePathForWorkspaceAPI(path, r.workspacePath), content)
}
func (r *externalPlanRuntime) ReadCurrentPlan(ctx context.Context) (*PlanningResponse, error) {
	return readPlanFromFile(ctx, r.workspacePath, r.readFile)
}
func (r *externalPlanRuntime) ReadStepConfigsFromSubdir(ctx context.Context, subdir string) ([]StepConfig, error) {
	content, err := r.ReadWorkspaceFile(ctx, filepath.Join(subdir, "step_config.json"))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			r.configReadErr = err
		}
		return nil, err
	}
	configs, err := ParseStepConfigContent(content)
	if err != nil {
		r.configReadErr = err
	}
	return configs, err
}
func (r *externalPlanRuntime) WriteStepConfigsToSubdir(ctx context.Context, subdir string, configs []StepConfig) error {
	if r.configReadErr != nil {
		return fmt.Errorf("refusing to overwrite unreadable step config: %w", r.configReadErr)
	}
	normalizeLegacyLearningLocks(configs)
	if err := validateStepConfigs(configs); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(StepConfigFile{Steps: configs}, "", "  ")
	if err != nil {
		return err
	}
	err = r.WriteWorkspaceFile(withStepConfigMutationWriteAccess(ctx, r.workspacePath, subdir), filepath.Join(subdir, "step_config.json"), string(encoded))
	if err == nil {
		r.configWrites++
	}
	return err
}

func nativePlanReadFile(readFile func(context.Context, string) (string, error)) func(context.Context, string) (string, error) {
	// The native workspace helpers recognize "not found" text, while hosted
	// callbacks use the typed filesystem sentinel. Preserve both conventions.
	return func(callCtx context.Context, path string) (string, error) {
		content, readErr := readFile(callCtx, path)
		if errors.Is(readErr, os.ErrNotExist) {
			return "", fmt.Errorf("not found: %w", readErr)
		}
		return content, readErr
	}
}
