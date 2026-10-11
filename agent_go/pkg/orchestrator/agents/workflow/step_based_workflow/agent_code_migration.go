package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

type legacyAgentCodeItem struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type"`
	Title       string                 `json:"title,omitempty"`
	Runtime     string                 `json:"runtime,omitempty"`
	ScriptPath  string                 `json:"script_path,omitempty"`
	InputFiles  []string               `json:"input_files,omitempty"`
	InputJSON   map[string]interface{} `json:"input_json,omitempty"`
	OutputFiles []string               `json:"output_files,omitempty"`
	WriteAccess AgentWriteAccess       `json:"write_access,omitempty"`
	OnFailure   struct {
		Action     string `json:"action,omitempty"`
		MaxRetries int    `json:"max_retries,omitempty"`
	} `json:"on_failure,omitempty"`
}

type legacyAgentMigration struct {
	stepIndex        int
	stepID           string
	stepTitle        string
	stepDescription  string
	contextInputs    []string
	parentValidation *ValidationSchema
	codeItems        []legacyAgentCodeItem
	validations      []*ValidationSchema
	rawStep          json.RawMessage
}

type agentSequenceMigrationResult struct {
	SequenceIDs   []string `json:"sequence_ids"`
	ScriptedIDs   []string `json:"scripted_step_ids"`
	CopiedScripts []string `json:"copied_scripts"`
}

// ValidateAgentCodeMigrationComplete verifies the v1.0.10 plan
// postcondition without trusting an agent-authored success message. It accepts
// both migrated plans and genuine no-op plans, and rejects every remaining
// legacy code item, including ambiguous nested and orphan usages.
func ValidateAgentCodeMigrationComplete(planContent string) error {
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(planContent), &document); err != nil {
		return fmt.Errorf("parse planning/plan.json: %w", err)
	}

	var rawSteps []json.RawMessage
	if err := json.Unmarshal(document["steps"], &rawSteps); err != nil {
		return fmt.Errorf("parse planning/plan.json steps: %w", err)
	}

	migrations, blockers, err := planAgentCodeMigrations(rawSteps, document["orphan_steps"])
	if err != nil {
		return err
	}
	if len(blockers) > 0 {
		sort.Strings(blockers)
		return fmt.Errorf("legacy message_sequence code migration is incomplete: %s", strings.Join(blockers, "; "))
	}
	if len(migrations) > 0 {
		sequenceIDs := make([]string, 0, len(migrations))
		for _, migration := range migrations {
			sequenceIDs = append(sequenceIDs, migration.stepID)
		}
		sort.Strings(sequenceIDs)
		return fmt.Errorf("legacy message_sequence code migration is incomplete for sequence(s): %s", strings.Join(sequenceIDs, ", "))
	}

	return nil
}

func createMigrateAgentCodeItemsExecutor(
	workspacePath string,
	logger loggerv2.Logger,
	readFile func(context.Context, string) (string, error),
	writeFile func(context.Context, string, string) error,
) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, _ map[string]interface{}) (string, error) {
		planPath := normalizePathForWorkspaceAPI(filepath.Join("planning", "plan.json"), workspacePath)
		planContent, err := readFile(ctx, planPath)
		if err != nil {
			return "", fmt.Errorf("read planning/plan.json: %w", err)
		}

		var document map[string]json.RawMessage
		if err := json.Unmarshal([]byte(planContent), &document); err != nil {
			return "", fmt.Errorf("parse planning/plan.json: %w", err)
		}
		var rawSteps []json.RawMessage
		if err := json.Unmarshal(document["steps"], &rawSteps); err != nil {
			return "", fmt.Errorf("parse planning/plan.json steps: %w", err)
		}

		migrations, blockers, err := planAgentCodeMigrations(rawSteps, document["orphan_steps"])
		if err != nil {
			return "", err
		}
		if len(blockers) > 0 {
			sort.Strings(blockers)
			return "", fmt.Errorf(
				"MESSAGE_SEQUENCE_CODE_MIGRATION_BLOCKED: no files were changed. Explicitly split these ambiguous usages into message_sequence -> standalone scripted regular step -> agent with durable context: %s",
				strings.Join(blockers, "; "),
			)
		}
		if len(migrations) == 0 {
			return `{"status":"no_op","message":"No message_sequence code items found."}`, nil
		}

		stepConfigs, err := readMigrationStepConfigs(ctx, workspacePath, readFile)
		if err != nil {
			return "", err
		}
		newRawSteps := make([]json.RawMessage, 0, len(rawSteps)+8)
		migrationByIndex := make(map[int]legacyAgentMigration, len(migrations))
		for _, migration := range migrations {
			migrationByIndex[migration.stepIndex] = migration
		}

		result := agentSequenceMigrationResult{}
		for index, rawStep := range rawSteps {
			migration, migrate := migrationByIndex[index]
			if !migrate {
				newRawSteps = append(newRawSteps, rawStep)
				continue
			}

			converted, copied, configs, convertErr := convertLegacyCodeSequence(ctx, workspacePath, migration, stepConfigs, readFile, writeFile)
			if convertErr != nil {
				return "", convertErr
			}
			newRawSteps = append(newRawSteps, converted...)
			stepConfigs = configs
			result.SequenceIDs = append(result.SequenceIDs, migration.stepID)
			result.CopiedScripts = append(result.CopiedScripts, copied...)
			for _, item := range migration.codeItems {
				result.ScriptedIDs = append(result.ScriptedIDs, item.ID)
			}
		}

		stepsJSON, err := json.Marshal(newRawSteps)
		if err != nil {
			return "", fmt.Errorf("marshal migrated plan steps: %w", err)
		}
		document["steps"] = stepsJSON
		migratedPlan, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal migrated plan: %w", err)
		}
		var typedPlan PlanningResponse
		if err := json.Unmarshal(migratedPlan, &typedPlan); err != nil {
			return "", fmt.Errorf("validate migrated plan parse: %w", err)
		}
		if err := ValidatePlanStructure(&typedPlan); err != nil {
			return "", fmt.Errorf("validate migrated plan: %w", err)
		}
		if err := validateStepConfigs(stepConfigs); err != nil {
			return "", fmt.Errorf("validate migrated step config: %w", err)
		}

		configJSON, err := json.MarshalIndent(StepConfigFile{Steps: stepConfigs}, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal migrated step config: %w", err)
		}
		configPath := normalizePathForWorkspaceAPI(filepath.Join("planning", "step_config.json"), workspacePath)
		managedCtx := workspacepkg.WithSystemManagedWritePaths(ctx, planPath, configPath)
		if err := writeFile(managedCtx, planPath, string(migratedPlan)); err != nil {
			return "", fmt.Errorf("write migrated planning/plan.json: %w", err)
		}
		if err := writeFile(managedCtx, configPath, string(configJSON)); err != nil {
			if rollbackErr := writeFile(managedCtx, planPath, planContent); rollbackErr != nil {
				return "", fmt.Errorf("write migrated planning/step_config.json: %w; rollback planning/plan.json also failed: %w", err, rollbackErr)
			}
			return "", fmt.Errorf("write migrated planning/step_config.json: %w; planning/plan.json was rolled back", err)
		}

		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:    "migrate_agent_code_items",
			Reason:  "Move deterministic code out of hidden agent items into visible standalone scripted steps for workflow contract v1.0.10.",
			StepIDs: append(append([]string{}, result.SequenceIDs...), result.ScriptedIDs...),
			// planContent/migratedPlan are the real pre/post planning/plan.json
			// content already in scope from the rewrite above — wire them in so
			// the changelog records a real diff instead of the empty-Changes
			// placeholder (PLAT-074).
			BeforeSnapshot: json.RawMessage(planContent),
			AfterSnapshot:  json.RawMessage(migratedPlan),
		}, readFile, withPlanMutationWriteAccess(workspacePath, writeFile), logger)

		payload, _ := json.Marshal(result)
		return fmt.Sprintf(`{"status":"migrated","result":%s}`, payload), nil
	}
}

func planAgentCodeMigrations(rawSteps []json.RawMessage, rawOrphans json.RawMessage) ([]legacyAgentMigration, []string, error) {
	var migrations []legacyAgentMigration
	var blockers []string
	allIDs := map[string]bool{}
	for _, raw := range rawSteps {
		collectRawPlanStepIDs(raw, allIDs)
	}
	if len(rawOrphans) > 0 && string(rawOrphans) != "null" {
		var orphans []json.RawMessage
		if err := json.Unmarshal(rawOrphans, &orphans); err != nil {
			return nil, nil, fmt.Errorf("parse orphan_steps during agent migration: %w", err)
		}
		for _, raw := range orphans {
			collectRawPlanStepIDs(raw, allIDs)
			if ids := rawAgentCodeItemIDs(raw); len(ids) > 0 {
				blockers = append(blockers, fmt.Sprintf("orphan step contains code items %v", ids))
			}
		}
	}

	for index, raw := range rawSteps {
		var header struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			return nil, nil, fmt.Errorf("parse step %d during agent migration: %w", index, err)
		}
		if header.Type != "agent" {
			if ids := rawAgentCodeItemIDs(raw); len(ids) > 0 {
				blockers = append(blockers, fmt.Sprintf("nested code items under step %q: %v", header.ID, ids))
			}
			continue
		}

		var seq struct {
			ID                  string            `json:"id"`
			Title               string            `json:"title"`
			Description         string            `json:"description"`
			ContextDependencies []string          `json:"context_dependencies"`
			ValidationSchema    *ValidationSchema `json:"validation_schema,omitempty"`
			NextStepID          string            `json:"next_step_id,omitempty"`
			Items               []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(raw, &seq); err != nil {
			return nil, nil, fmt.Errorf("parse message_sequence %q: %w", header.ID, err)
		}
		codeIDs := rawAgentCodeItemIDs(raw)
		if len(codeIDs) == 0 {
			continue
		}
		if strings.TrimSpace(seq.NextStepID) != "" {
			blockers = append(blockers, fmt.Sprintf("sequence %q has code items and next_step_id=%q", seq.ID, seq.NextStepID))
			continue
		}

		migration := legacyAgentMigration{
			stepIndex:        index,
			stepID:           seq.ID,
			stepTitle:        seq.Title,
			stepDescription:  seq.Description,
			contextInputs:    seq.ContextDependencies,
			parentValidation: seq.ValidationSchema,
			rawStep:          raw,
		}
		for itemIndex, rawItem := range seq.Items {
			var kind struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			}
			if err := json.Unmarshal(rawItem, &kind); err != nil {
				return nil, nil, fmt.Errorf("parse sequence %q item %d: %w", seq.ID, itemIndex, err)
			}
			switch strings.TrimSpace(kind.Type) {
			case "code":
				var item legacyAgentCodeItem
				if err := json.Unmarshal(rawItem, &item); err != nil {
					return nil, nil, fmt.Errorf("parse sequence %q code item %q: %w", seq.ID, kind.ID, err)
				}
				if err := validateLegacyCodeItemForMigration(seq.ID, item); err != nil {
					blockers = append(blockers, err.Error())
					continue
				}
				if allIDs[item.ID] {
					blockers = append(blockers, fmt.Sprintf("sequence %q code item %q collides with an existing plan step id", seq.ID, item.ID))
					continue
				}
				allIDs[item.ID] = true
				migration.codeItems = append(migration.codeItems, item)
				migration.validations = append(migration.validations, nil)
			case "prevalidation":
				if len(migration.codeItems) == 0 || migration.validations[len(migration.validations)-1] != nil {
					blockers = append(blockers, fmt.Sprintf("sequence %q prevalidation item %q is not immediately paired with one code item", seq.ID, kind.ID))
					continue
				}
				var gate struct {
					ValidationSchema *ValidationSchema `json:"validation_schema,omitempty"`
					Prevalidation    *ValidationSchema `json:"prevalidation,omitempty"`
				}
				if err := json.Unmarshal(rawItem, &gate); err != nil {
					return nil, nil, fmt.Errorf("parse sequence %q prevalidation %q: %w", seq.ID, kind.ID, err)
				}
				if gate.ValidationSchema == nil {
					gate.ValidationSchema = gate.Prevalidation
				}
				if gate.ValidationSchema == nil {
					blockers = append(blockers, fmt.Sprintf("sequence %q prevalidation item %q has no schema", seq.ID, kind.ID))
					continue
				}
				migration.validations[len(migration.validations)-1] = gate.ValidationSchema
			default:
				blockers = append(blockers, fmt.Sprintf("sequence %q mixes code item(s) with conversational item %q of type %q", seq.ID, kind.ID, kind.Type))
			}
		}
		if len(migration.codeItems) > 0 {
			migrations = append(migrations, migration)
		}
	}
	return migrations, blockers, nil
}

func validateLegacyCodeItemForMigration(sequenceID string, item legacyAgentCodeItem) error {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.ScriptPath) == "" {
		return fmt.Errorf("sequence %q has a code item missing id or script_path", sequenceID)
	}
	if item.Runtime != "" && item.Runtime != "python" && item.Runtime != "python3" {
		return fmt.Errorf("sequence %q code item %q uses unsupported runtime %q", sequenceID, item.ID, item.Runtime)
	}
	if len(item.InputJSON) > 0 {
		return fmt.Errorf("sequence %q code item %q uses input_json and requires an explicit durable-input redesign", sequenceID, item.ID)
	}
	if item.WriteAccess.Knowledgebase || item.WriteAccess.Learnings {
		return fmt.Errorf("sequence %q code item %q writes KB/learnings and requires an explicit store-ownership redesign", sequenceID, item.ID)
	}
	if len(item.OutputFiles) == 0 {
		return fmt.Errorf("sequence %q code item %q has no declared output_files", sequenceID, item.ID)
	}
	return nil
}

func convertLegacyCodeSequence(
	ctx context.Context,
	workspacePath string,
	migration legacyAgentMigration,
	configs []StepConfig,
	readFile func(context.Context, string) (string, error),
	writeFile func(context.Context, string, string) error,
) ([]json.RawMessage, []string, []StepConfig, error) {
	parentConfig, remainingConfigs := removeStepConfigByID(configs, migration.stepID)
	dependencies := append([]string{}, migration.contextInputs...)
	converted := make([]json.RawMessage, 0, len(migration.codeItems))
	copied := make([]string, 0, len(migration.codeItems))

	for index, item := range migration.codeItems {
		sourcePath := normalizePathForWorkspaceAPI(filepath.FromSlash(item.ScriptPath), workspacePath)
		source, err := readFile(ctx, sourcePath)
		if err != nil {
			return nil, nil, configs, fmt.Errorf("read script for sequence %q item %q at %s: %w", migration.stepID, item.ID, item.ScriptPath, err)
		}
		destinationRel := filepath.Join("learnings", item.ID, "main.py")
		destinationPath := normalizePathForWorkspaceAPI(destinationRel, workspacePath)
		writeCtx := workspacepkg.WithSystemManagedWritePaths(ctx, destinationPath)
		if err := writeFile(writeCtx, destinationPath, source); err != nil {
			return nil, nil, configs, fmt.Errorf("copy script for sequence %q item %q to %s: %w", migration.stepID, item.ID, destinationRel, err)
		}
		copied = append(copied, filepath.ToSlash(destinationRel))

		stepDependencies := dedupeMigrationStrings(append(append([]string{}, dependencies...), item.InputFiles...))
		validation := migration.validations[index]
		if index == len(migration.codeItems)-1 {
			validation = mergeMigrationValidationSchemas(validation, migration.parentValidation)
		}
		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = humanizeMigrationID(item.ID)
		}
		description := fmt.Sprintf(
			"Execute the deterministic Python script at learnings/%s/main.py as a standalone scripted step. This step was migrated from message_sequence %q item %q; preserve the declared durable inputs, outputs, validation, and read-only/side-effect contract.\n\nOriginal sequence contract:\n%s",
			item.ID,
			migration.stepID,
			item.ID,
			migration.stepDescription,
		)
		regular := &RegularPlanStep{
			Type: StepTypeRegular,
			CommonStepFields: CommonStepFields{
				ID:                  item.ID,
				Title:               title,
				Description:         description,
				ContextDependencies: stepDependencies,
				ContextOutput:       FlexibleContextOutput(strings.Join(item.OutputFiles, ", ")),
				ValidationSchema:    validation,
			},
			HasLoop: false,
		}
		raw, err := json.Marshal(regular)
		if err != nil {
			return nil, nil, configs, fmt.Errorf("marshal migrated scripted step %q: %w", item.ID, err)
		}
		converted = append(converted, raw)

		cfg := cloneMigrationStepConfig(parentConfig, item, title)
		if index == len(migration.codeItems)-1 && parentConfig != nil {
			cfg.ValidationSchema = parentConfig.ValidationSchema
		}
		remainingConfigs = append(remainingConfigs, cfg)
		dependencies = dedupeMigrationStrings(append(dependencies, item.OutputFiles...))
	}
	return converted, copied, remainingConfigs, nil
}

func readMigrationStepConfigs(ctx context.Context, workspacePath string, readFile func(context.Context, string) (string, error)) ([]StepConfig, error) {
	path := normalizePathForWorkspaceAPI(filepath.Join("planning", "step_config.json"), workspacePath)
	content, err := readFile(ctx, path)
	if err != nil {
		if isMissingOrEmptyWorkspaceError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read planning/step_config.json: %w", err)
	}
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}
	var file StepConfigFile
	if err := json.Unmarshal([]byte(content), &file); err != nil {
		return nil, fmt.Errorf("parse planning/step_config.json: %w", err)
	}
	if err := validateStepConfigs(file.Steps); err != nil {
		return nil, err
	}
	return file.Steps, nil
}

func removeStepConfigByID(configs []StepConfig, stepID string) (*StepConfig, []StepConfig) {
	var parent *StepConfig
	remaining := make([]StepConfig, 0, len(configs))
	for _, cfg := range configs {
		if cfg.ID == stepID {
			copy := cfg
			parent = &copy
			continue
		}
		remaining = append(remaining, cfg)
	}
	return parent, remaining
}

func cloneMigrationStepConfig(parent *StepConfig, item legacyAgentCodeItem, title string) StepConfig {
	var agent AgentConfigs
	if parent != nil && parent.AgentConfigs != nil {
		data, _ := json.Marshal(parent.AgentConfigs)
		_ = json.Unmarshal(data, &agent)
	}
	// The new standalone step's plan type (regular) is what makes it scripted
	// (PLAT-287); only the code-execution flag is carried on the config.
	useCode := true
	agent.UseCodeExecutionMode = &useCode
	if agent.LockCode == nil {
		unlocked := false
		agent.LockCode = &unlocked
	}
	// db_access was removed in PLAT-061: it never narrowed anything — every
	// workflow step receives managed read-write access — so deriving it from
	// whether the migrated item touched the DB was writing a field the runtime
	// discarded.
	// PLAT-061: the migration no longer derives a fix-iteration count. It wrote 0
	// for every item without a legacy repair action, which read as a deliberate
	// "never repair" but was only the absence of a legacy field. Scripted steps
	// now use the uniform default; lock_code is the explicit way to skip repair.
	return StepConfig{ID: item.ID, Title: title, AgentConfigs: &agent}
}

func mergeMigrationValidationSchemas(a, b *ValidationSchema) *ValidationSchema {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &ValidationSchema{
		Files: append(append([]FileValidationRule{}, a.Files...), b.Files...),
		DB:    append(append([]DBValidationRule{}, a.DB...), b.DB...),
	}
}

func collectRawPlanStepIDs(raw json.RawMessage, ids map[string]bool) {
	var step map[string]json.RawMessage
	if json.Unmarshal(raw, &step) != nil {
		return
	}
	var id string
	_ = json.Unmarshal(step["id"], &id)
	if id != "" {
		ids[id] = true
	}
	var routes []struct {
		SubAgentStep json.RawMessage `json:"sub_agent_step"`
	}
	if json.Unmarshal(step["predefined_routes"], &routes) == nil {
		for _, route := range routes {
			if len(route.SubAgentStep) > 0 {
				collectRawPlanStepIDs(route.SubAgentStep, ids)
			}
		}
	}
}

func rawAgentCodeItemIDs(raw json.RawMessage) []string {
	var node interface{}
	if json.Unmarshal(raw, &node) != nil {
		return nil
	}
	var ids []string
	var walk func(interface{})
	walk = func(value interface{}) {
		switch typed := value.(type) {
		case map[string]interface{}:
			if typed["type"] == "agent" {
				if items, ok := typed["items"].([]interface{}); ok {
					for _, rawItem := range items {
						if item, ok := rawItem.(map[string]interface{}); ok && item["type"] == "code" {
							ids = append(ids, fmt.Sprint(item["id"]))
						}
					}
				}
			}
			for _, child := range typed {
				walk(child)
			}
		case []interface{}:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(node)
	return ids
}

func dedupeMigrationStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func humanizeMigrationID(id string) string {
	parts := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(id))
	for i := range parts {
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, " ")
}
