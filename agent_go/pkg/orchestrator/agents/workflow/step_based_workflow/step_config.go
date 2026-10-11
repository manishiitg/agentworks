package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// StepConfig represents a single step's configuration in step_config.json
type StepConfig struct {
	ID               string            `json:"id"`              // Stable step ID (from plan.json) - required identifier
	Title            string            `json:"title,omitempty"` // Step title (optional, for reference/display only)
	AgentConfigs     *AgentConfigs     `json:"agent_configs,omitempty"`
	ValidationSchema *ValidationSchema `json:"validation_schema,omitempty"` // Override pre-validation schema (takes precedence over plan.json)
}

// StepConfigFile represents the step_config.json file format
// Format: { "steps": [{ "id": "...", "agent_configs": {...} }] }
type StepConfigFile struct {
	Steps []StepConfig `json:"steps"`
}

// ParseStepConfigContent parses step_config.json content in object format:
// Format: { "steps": [{ "id": "...", "agent_configs": {...} }] }
func ParseStepConfigContent(content string) ([]StepConfig, error) {
	// Parse as object format with "steps" field
	var configFile StepConfigFile
	if err := json.Unmarshal([]byte(content), &configFile); err != nil {
		return nil, fmt.Errorf("failed to parse step_config.json: expected format { \"steps\": [...] }, got error: %w", err)
	}
	normalizeLegacyLearningLocks(configFile.Steps)

	if err := validateStepConfigs(configFile.Steps); err != nil {
		return nil, err
	}
	return configFile.Steps, nil
}

// legacyDeclared reports the canonical value of the retired
// declared_execution_mode key ("scripted", "agentic" or ""), nil-safe. Read
// only by the transitional shim (isScriptedStep) and the PLAT-287 migrations.
func (ac *AgentConfigs) legacyDeclared() string {
	if ac == nil {
		return ""
	}
	return canonicalDeclaredExecutionMode(ac.LegacyDeclaredExecutionMode)
}

func validateStepConfigs(configs []StepConfig) error {
	seen := make(map[string]int, len(configs))
	for i, config := range configs {
		id := strings.TrimSpace(config.ID)
		if id == "" {
			return fmt.Errorf("step_config.json step at index %d has an empty id", i)
		}
		if previous, exists := seen[id]; exists {
			return fmt.Errorf("step_config.json has duplicate id %q at indices %d and %d", id, previous, i)
		}
		seen[id] = i
	}
	return nil
}

// repairStepConfigs migrates legacy ambiguous files deterministically. The last
// occurrence wins, matching the historical map-based matcher, and empty IDs are
// removed because they cannot address a plan step.
func repairStepConfigs(configs []StepConfig) ([]StepConfig, bool) {
	last := make(map[string]int, len(configs))
	for i, config := range configs {
		if id := strings.TrimSpace(config.ID); id != "" {
			last[id] = i
		}
	}
	repaired := make([]StepConfig, 0, len(last))
	changed := len(last) != len(configs)
	for i, config := range configs {
		id := strings.TrimSpace(config.ID)
		if id == "" || last[id] != i {
			changed = true
			continue
		}
		if config.ID != id {
			config.ID = id
			changed = true
		}
		repaired = append(repaired, config)
	}
	if normalizeLegacyLearningLocks(repaired) {
		changed = true
	}
	return repaired, changed
}

// normalizeLegacyLearningLocks retires the redundant lock_learnings fields.
// A true legacy lock meant "keep reading but stop writing", which is exactly
// learnings_access="read". Preserve an explicit "none" because migration must
// never widen access. False locks and their reasons are simply removed.
func normalizeLegacyLearningLocks(configs []StepConfig) bool {
	changed := false
	for i := range configs {
		ac := configs[i].AgentConfigs
		if ac == nil || (ac.LegacyLockLearnings == nil && ac.LegacyLockLearningsReason == "") {
			continue
		}
		if ac.LegacyLockLearnings != nil && *ac.LegacyLockLearnings && resolveLearningsAccess(ac) != LearningsAccessNone {
			ac.LearningsAccess = LearningsAccessRead
		}
		ac.LegacyLockLearnings = nil
		ac.LegacyLockLearningsReason = ""
		changed = true
	}
	return changed
}

func readAndRepairStepConfigs(ctx context.Context, bo *orchestrator.BaseOrchestrator, path, content string) ([]StepConfig, error) {
	var configFile StepConfigFile
	if err := json.Unmarshal([]byte(content), &configFile); err != nil {
		return nil, fmt.Errorf("failed to parse step_config.json: expected format { \"steps\": [...] }, got error: %w", err)
	}
	configs, changed := repairStepConfigs(configFile.Steps)
	if !changed {
		return configs, validateStepConfigs(configs)
	}
	data, err := json.MarshalIndent(StepConfigFile{Steps: configs}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal repaired step_config.json: %w", err)
	}
	if err := bo.WriteWorkspaceFile(ctx, path, string(data)); err != nil {
		return nil, fmt.Errorf("failed to persist repaired step_config.json at %s: %w", path, err)
	}
	bo.GetLogger().Warn(fmt.Sprintf("Repaired duplicate or empty step_config IDs in %s using deterministic last-entry-wins migration", path))
	return configs, nil
}

// ReadStepConfigs reads step_config.json from the workspace
// Public method that accepts BaseOrchestrator, workspacePath, and runWorkspacePath as parameters
// NOTE: workspacePath and runWorkspacePath are kept for API compatibility but paths are constructed as relative
// ReadWorkspaceFile auto-prepends the workspace path, so we only pass relative paths
func ReadStepConfigs(ctx context.Context, bo *orchestrator.BaseOrchestrator, workspacePath, runWorkspacePath, configSubdir string) ([]StepConfig, error) {
	// Extract the run folder relative path from runWorkspacePath
	// runWorkspacePath is typically "{workspace}/runs/{selectedRunFolder}" or just "{workspace}"
	// We need to construct relative paths for ReadWorkspaceFile

	// Default configSubdir to "planning" if empty
	if configSubdir == "" {
		configSubdir = "planning"
	}

	// First, try to read from run folder (run-specific config)
	// Use relative path - ReadWorkspaceFile auto-prepends workspacePath
	var runConfigRelativePath string
	if runWorkspacePath != workspacePath && runWorkspacePath != "" {
		// Extract relative run path from runWorkspacePath
		// runWorkspacePath = "{workspace}/runs/{runFolder}" -> relative = "runs/{runFolder}"
		relativePart := runWorkspacePath
		if len(workspacePath) > 0 && len(runWorkspacePath) > len(workspacePath) {
			relativePart = runWorkspacePath[len(workspacePath):]
			relativePart = filepath.Clean(relativePart)
			relativePart = filepath.ToSlash(relativePart)
			if len(relativePart) > 0 && relativePart[0] == '/' {
				relativePart = relativePart[1:]
			}
		}
		runConfigRelativePath = filepath.Join(relativePart, configSubdir, "step_config.json")
	} else {
		runConfigRelativePath = filepath.Join(configSubdir, "step_config.json")
	}

	content, err := bo.ReadWorkspaceFile(ctx, runConfigRelativePath)
	if err == nil {
		// Run folder config exists - use it
		configs, err := readAndRepairStepConfigs(ctx, bo, runConfigRelativePath, content)
		if err != nil {
			return nil, fmt.Errorf("failed to parse run folder step_config.json: %w", err)
		}
		bo.GetLogger().Info(fmt.Sprintf("📁 Using run-specific step_config.json from: %s", runConfigRelativePath))
		return configs, nil
	}

	// Fallback to workspace default config
	// Use relative path only - ReadWorkspaceFile auto-prepends workspacePath
	configPath := filepath.Join(configSubdir, "step_config.json")
	content, err = bo.ReadWorkspaceFile(ctx, configPath)
	if err != nil {
		// File doesn't exist yet - return empty array
		if os.IsNotExist(err) {
			bo.GetLogger().Info(fmt.Sprintf("📁 No step_config.json found (neither run-specific nor default) - using defaults"))
			return []StepConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read step_config.json: %w", err)
	}

	configs, err := readAndRepairStepConfigs(ctx, bo, configPath, content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse step_config.json: %w", err)
	}

	bo.GetLogger().Info(fmt.Sprintf("📁 Using default step_config.json from: %s", configPath))
	return configs, nil
}

// ReadStepConfigs is a private wrapper that uses receiver fields (for backward compatibility)
// Uses run folder path if available, otherwise falls back to base workspace path
func (hcpo *StepBasedWorkflowOrchestrator) ReadStepConfigs(ctx context.Context) ([]StepConfig, error) {
	workspacePath := hcpo.GetWorkspacePath()
	// Build run folder path if selectedRunFolder is set
	var runWorkspacePath string

	configSubdir := "planning"

	if hcpo.selectedRunFolder != "" {
		runWorkspacePath = filepath.Join(workspacePath, "runs", hcpo.selectedRunFolder)
		hcpo.GetLogger().Info(fmt.Sprintf("📁 Reading step_config.json - will try run folder first: %s/%s/step_config.json", runWorkspacePath, configSubdir))
	} else {
		// No run folder selected yet - use base workspace path
		runWorkspacePath = workspacePath
		hcpo.GetLogger().Info(fmt.Sprintf("📁 Reading step_config.json - no run folder selected, using base workspace: %s/%s/step_config.json", workspacePath, configSubdir))
	}
	return ReadStepConfigs(ctx, hcpo.BaseOrchestrator, workspacePath, runWorkspacePath, configSubdir)
}

// ReadStepConfigsFromSubdir reads the workspace-default step_config.json from
// a specific subdirectory such as "planning" or "evaluation". It intentionally
// bypasses run-specific configs because workshop config edits should update the
// durable workspace config, not a selected run's snapshot.
func (hcpo *StepBasedWorkflowOrchestrator) ReadStepConfigsFromSubdir(ctx context.Context, configSubdir string) ([]StepConfig, error) {
	workspacePath := hcpo.GetWorkspacePath()
	if configSubdir == "" {
		configSubdir = "planning"
	}
	return ReadStepConfigs(ctx, hcpo.BaseOrchestrator, workspacePath, workspacePath, configSubdir)
}

// WriteStepConfigs writes step_config.json to the workspace in object format
// Format: { "steps": [{ "id": "...", "agent_configs": {...} }] }
// Uses the orchestrator's WriteWorkspaceFile method
// Note: Directory creation is handled automatically by the workspace API
// WriteWorkspaceFile auto-prepends the workspace path, so we only pass the relative path
func (hcpo *StepBasedWorkflowOrchestrator) WriteStepConfigs(ctx context.Context, configs []StepConfig) error {
	configSubdir := "planning"
	return hcpo.WriteStepConfigsToSubdir(ctx, configSubdir, configs)
}

// WriteStepConfigsToSubdir writes step_config.json into a specific config
// subdirectory such as "planning" or "evaluation".
func (hcpo *StepBasedWorkflowOrchestrator) WriteStepConfigsToSubdir(ctx context.Context, configSubdir string, configs []StepConfig) error {
	if configSubdir == "" {
		configSubdir = "planning"
	}
	// Never persist the retired learning lock. This also protects typed callers
	// that constructed AgentConfigs from a legacy payload without reading first.
	normalizeLegacyLearningLocks(configs)

	// Use relative path only - WriteWorkspaceFile auto-prepends workspacePath
	configPath := filepath.Join(configSubdir, "step_config.json")
	if err := validateStepConfigs(configs); err != nil {
		return err
	}

	// Write in object format with "steps" field
	configFile := StepConfigFile{
		Steps: configs,
	}
	jsonData, err := json.MarshalIndent(configFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal step_config.json: %w", err)
	}

	// planning/ is protected from raw file writes. This method is the typed,
	// validated step-config writer, so grant it access to this exact managed file
	// without exposing the rest of planning/ to agents or general file tools.
	writeCtx := withStepConfigMutationWriteAccess(ctx, hcpo.GetWorkspacePath(), configSubdir)

	// WriteWorkspaceFile will automatically create the directory structure via the workspace API
	if err := hcpo.WriteWorkspaceFile(writeCtx, configPath, string(jsonData)); err != nil {
		return fmt.Errorf("failed to write step_config.json: %w", err)
	}

	return nil
}

func withStepConfigMutationWriteAccess(ctx context.Context, workspacePath, configSubdir string) context.Context {
	if filepath.Clean(configSubdir) != "planning" {
		return ctx
	}
	return withPlanningFileMutationWriteAccess(ctx, workspacePath, "step_config.json")
}

// ReadStepOverrides reads step overrides from workflow.json execution_defaults.
// Returns nil if no overrides are configured.
func (hcpo *StepBasedWorkflowOrchestrator) ReadStepOverrides(ctx context.Context) (*AgentConfigs, error) {
	manifestContent, err := hcpo.ReadWorkspaceFile(ctx, "workflow.json")
	if err != nil {
		return nil, nil
	}

	var manifest struct {
		ExecutionDefaults struct {
			DisableParallelToolExecution *bool    `json:"disable_parallel_tool_execution"`
			EnabledCustomTools           []string `json:"enabled_custom_tools"`
		} `json:"execution_defaults"`
	}
	if err := json.Unmarshal([]byte(manifestContent), &manifest); err != nil {
		return nil, nil
	}

	ed := manifest.ExecutionDefaults
	if ed.DisableParallelToolExecution == nil && len(ed.EnabledCustomTools) == 0 {
		return nil, nil
	}

	hcpo.GetLogger().Info("📁 Using step overrides from workflow.json execution_defaults")
	return &AgentConfigs{
		DisableParallelToolExecution: ed.DisableParallelToolExecution,
		EnabledCustomTools:           ed.EnabledCustomTools,
	}, nil
}

// MatchStepConfigs matches new plan steps with existing configs by ID only
// Returns a map of step index -> matched AgentConfigs
// Returns an error if any step is missing a required ID field
func MatchStepConfigs(newSteps []PlanStepInterface, oldConfigs []StepConfig) (map[int]*AgentConfigs, error) {
	result := make(map[int]*AgentConfigs)

	// Create lookup map: ID -> config
	idConfigMap := make(map[string]*AgentConfigs)

	for i := range oldConfigs {
		if oldConfigs[i].AgentConfigs != nil && oldConfigs[i].ID != "" {
			idConfigMap[oldConfigs[i].ID] = oldConfigs[i].AgentConfigs
		}
	}

	// Match new steps to old configs by ID only
	// Steps always have IDs from backend - throw error if missing
	for i, step := range newSteps {
		// Use existing step ID (required) - steps always have IDs from plan.json
		stepID := step.GetID()
		if stepID == "" {
			// This should never happen - steps always have IDs from backend
			// Throw error to match frontend behavior and catch bugs early
			stepTitle := "unknown"
			if step.GetTitle() != "" {
				stepTitle = step.GetTitle()
			}
			return nil, fmt.Errorf("step at index %d is missing required ID field. Step title: %q", i, stepTitle)
		}

		// Match config by ID
		config := idConfigMap[stepID]
		if config != nil {
			result[i] = config
		} else {
			// Log when step ID doesn't match - helps debug matching issues
			// Only log if there are configs available (to avoid noise when no configs exist)
			if len(idConfigMap) > 0 {
				// Get available IDs for debugging
				availableIDs := make([]string, 0, len(idConfigMap))
				for id := range idConfigMap {
					availableIDs = append(availableIDs, id)
				}
				// Note: Can't use logger here as this is a pure function
				// Logging will be done in the caller
			}
		}
		// If not found, result[i] will be nil (no config for this step)
	}

	return result, nil
}

// MatchStepConfigByID matches a nested step config by its stable ID.
// stepID: the step ID to match (from plan.json)
// Returns the matched AgentConfigs or nil if not found
func MatchStepConfigByID(stepID string, oldConfigs []StepConfig) *AgentConfigs {
	if stepID == "" {
		return nil
	}

	// Look up by ID
	for i := range oldConfigs {
		if oldConfigs[i].ID == stepID {
			return oldConfigs[i].AgentConfigs
		}
	}

	return nil
}

// MergeAgentConfigFields merges all fields from source config into target config.
// Only non-nil fields from source are copied to target.
// This ensures step-specific configs from step_config.json override defaults.
func MergeAgentConfigFields(target *AgentConfigs, source *AgentConfigs, stepID string, logger loggerv2.Logger) {
	if source == nil {
		return
	}

	if target == nil {
		logger.Warn(fmt.Sprintf("⚠️ Cannot merge config for step %s: target is nil", stepID))
		return
	}

	if source.UseCodeExecutionMode != nil {
		target.UseCodeExecutionMode = source.UseCodeExecutionMode
		logger.Info(fmt.Sprintf("🔧 Using step config (ID: %s) - use_code_execution_mode: %v", stepID, *source.UseCodeExecutionMode))
	}
	if source.LearningObjective != "" {
		target.LearningObjective = source.LearningObjective
	}
	if source.LearningsAccess != "" {
		target.LearningsAccess = source.LearningsAccess
	}
	if source.ExecutionLLM != nil {
		target.ExecutionLLM = source.ExecutionLLM
	}
	if source.ExecutionTier != "" {
		target.ExecutionTier = source.ExecutionTier
	}
	if source.ExecutionTierReason != "" {
		target.ExecutionTierReason = source.ExecutionTierReason
	}
	if source.ExecutionLLMReason != "" {
		target.ExecutionLLMReason = source.ExecutionLLMReason
	}
	if source.SelectedServers != nil {
		target.SelectedServers = source.SelectedServers
	}
	if source.SelectedTools != nil {
		target.SelectedTools = source.SelectedTools
	}
	if source.EnabledCustomTools != nil {
		target.EnabledCustomTools = source.EnabledCustomTools
		logger.Info(fmt.Sprintf("🔧 Using step config (ID: %s) - enabled_custom_tools: %v", stepID, source.EnabledCustomTools))
	}
	if source.EnabledSkills != nil {
		target.EnabledSkills = source.EnabledSkills
	}
	if source.AdditionalReadPaths != nil {
		target.AdditionalReadPaths = source.AdditionalReadPaths
	}
	if source.DisableParallelToolExecution != nil {
		target.DisableParallelToolExecution = source.DisableParallelToolExecution
		logger.Info(fmt.Sprintf("🔧 Using step config (ID: %s) - disable_parallel_tool_execution: %v", stepID, *source.DisableParallelToolExecution))
	}
	if source.CodingAgentTmuxLifecycle != "" {
		target.CodingAgentTmuxLifecycle = source.CodingAgentTmuxLifecycle
		logger.Info(fmt.Sprintf("🔧 Using step config (ID: %s) - coding_agent_tmux_lifecycle: %s", stepID, source.CodingAgentTmuxLifecycle))
	}
	if source.LegacyDeclaredExecutionMode != "" {
		target.LegacyDeclaredExecutionMode = source.LegacyDeclaredExecutionMode
	}
	if source.LegacyDeclaredExecutionModeReason != "" {
		target.LegacyDeclaredExecutionModeReason = source.LegacyDeclaredExecutionModeReason
	}
	if source.DescriptionReviewed != nil {
		target.DescriptionReviewed = source.DescriptionReviewed
	}
	if source.DriftReview != nil {
		target.DriftReview = source.DriftReview
	}
	if source.ReviewNotes != "" {
		target.ReviewNotes = source.ReviewNotes
	}

	// PLAT-061/E4. These were missing, so on the merge path — taken whenever the
	// step already carries in-memory AgentConfigs — a saved lock_code:true or
	// knowledgebase_access never reached the runtime, silently. Three of them
	// gate writes, which is why an incomplete merge here is a correctness bug
	// rather than untidiness. TestMergeAgentConfigFieldsCoversEveryField fails
	// when a new field is added without a case.
	if source.LockCode != nil {
		target.LockCode = source.LockCode
	}
	if source.KnowledgebaseAccess != "" {
		target.KnowledgebaseAccess = source.KnowledgebaseAccess
	}
	if source.KnowledgebaseContribution != "" {
		target.KnowledgebaseContribution = source.KnowledgebaseContribution
	}
	if source.SuccessfulRuns != nil {
		target.SuccessfulRuns = source.SuccessfulRuns
	}
}

// normalizeAdditionalReadPaths validates and canonicalizes read-only grants from
// step_config.json. Entries are relative to the current workflow root. Absolute
// paths and traversal are rejected so this escape hatch cannot expose another
// workflow or the host filesystem.
func normalizeAdditionalReadPaths(paths []string) ([]string, error) {
	normalized := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, raw := range paths {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("additional_read_paths entry contains a NUL byte")
		}
		value = strings.ReplaceAll(value, `\`, "/")
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
		if filepath.IsAbs(value) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("additional_read_paths entry %q must be a workflow-relative path that stays inside the current workflow", raw)
		}
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}
		normalized = append(normalized, clean)
	}
	return normalized, nil
}

// ApplyStepConfigFromFile loads step_config.json and applies matched config to the step.
// If step has no AgentConfigs, it creates one and copies all matched fields.
// If step already has AgentConfigs, it merges only the fields from matched config.
// Returns error if config file cannot be read.
func ApplyStepConfigFromFile(
	ctx context.Context,
	step PlanStepInterface,
	orchestrator *StepBasedWorkflowOrchestrator,
) error {
	if step.GetID() == "" {
		return nil // No ID, skip config matching
	}

	stepConfigs, err := orchestrator.ReadStepConfigs(ctx)
	if err != nil {
		return fmt.Errorf("failed to read step configs: %w", err)
	}

	matchedConfig := MatchStepConfigByID(step.GetID(), stepConfigs)
	if matchedConfig != nil {
		// Initialize AgentConfigs if not present
		agentConfigs := getAgentConfigs(step)
		if agentConfigs == nil {
			// Need to set AgentConfigs on the step - this requires type assertion
			switch s := step.(type) {
			case *RegularPlanStep:
				s.AgentConfigs = matchedConfig
			case *HumanInputPlanStep:
				s.AgentConfigs = matchedConfig
			case *RoutingPlanStep:
				s.AgentConfigs = matchedConfig
			case *BranchPlanStep:
				s.AgentConfigs = matchedConfig
			case *AgentPlanStep:
				s.AgentConfigs = matchedConfig
			default:
				return fmt.Errorf("unknown step type: %T", step)
			}
			orchestrator.GetLogger().Info(fmt.Sprintf("✅ Applied full config for step %s (ID: %s)", step.GetTitle(), step.GetID()))
		} else {
			// Merge matched config into existing config
			MergeAgentConfigFields(agentConfigs, matchedConfig, step.GetID(), orchestrator.GetLogger())
		}

	}

	// Apply global overrides from workflow.json execution_defaults (highest priority)
	// This must run even when no step_config.json match exists, since execution_defaults
	// (e.g., disable_parallel_tool_execution) apply to ALL steps regardless of per-step config.
	overrides, err := orchestrator.ReadStepOverrides(ctx)
	if err != nil {
		orchestrator.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to read step_override.json in ApplyStepConfigFromFile: %v", err))
	} else if overrides != nil {
		currentConfigs := getAgentConfigs(step)
		if currentConfigs == nil {
			// Set overrides as the config
			switch s := step.(type) {
			case *RegularPlanStep:
				s.AgentConfigs = overrides
			case *HumanInputPlanStep:
				s.AgentConfigs = overrides
			case *RoutingPlanStep:
				s.AgentConfigs = overrides
			case *BranchPlanStep:
				s.AgentConfigs = overrides
			case *AgentPlanStep:
				s.AgentConfigs = overrides
			}
		} else {
			MergeAgentConfigFields(currentConfigs, overrides, step.GetID(), orchestrator.GetLogger())
		}
		orchestrator.GetLogger().Info(fmt.Sprintf("🔧 Applied global overrides for step %s (ID: %s)", step.GetTitle(), step.GetID()))
	}

	return nil
}

// readStepConfigViaFileCallback reads planning/step_config.json using the same
// readFile callback shape that plan-mod tool executors get. Returns an empty
// slice if the file doesn't exist (no configs declared yet) — that's a normal
// state, not an error.
func readStepConfigViaFileCallback(ctx context.Context, workspacePath string, readFile func(context.Context, string) (string, error)) ([]StepConfig, error) {
	configPath := normalizePathForWorkspaceAPI(filepath.Join("planning", "step_config.json"), workspacePath)
	content, err := readFile(ctx, configPath)
	if err != nil {
		// File-not-found is the common case for fresh plans; treat as empty.
		return []StepConfig{}, nil
	}
	if content == "" {
		return []StepConfig{}, nil
	}
	return ParseStepConfigContent(content)
}

// writeStepConfigViaFileCallback persists the given configs back to
// planning/step_config.json using the same writeFile callback shape that
// plan-mod tool executors get.
func writeStepConfigViaFileCallback(ctx context.Context, workspacePath string, configs []StepConfig, writeFile func(context.Context, string, string) error) error {
	configPath := normalizePathForWorkspaceAPI(filepath.Join("planning", "step_config.json"), workspacePath)
	file := StepConfigFile{Steps: configs}
	out, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal step_config.json: %w", err)
	}
	return writeFile(ctx, configPath, string(out))
}

// pruneStepConfigsByID removes entries whose IDs are in deletedSet and returns
// (newConfigs, deletedIDsActuallyRemoved). The second slice is empty when
// nothing matched, letting callers skip the write.
func pruneStepConfigsByID(configs []StepConfig, deletedSet map[string]bool) ([]StepConfig, []string) {
	if len(configs) == 0 || len(deletedSet) == 0 {
		return configs, nil
	}
	kept := make([]StepConfig, 0, len(configs))
	var removed []string
	for _, cfg := range configs {
		if deletedSet[cfg.ID] {
			removed = append(removed, cfg.ID)
			continue
		}
		kept = append(kept, cfg)
	}
	return kept, removed
}
