package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PortableWorkflowContractVersion records the owning Crew's review of task,
// code and input semantics after the mechanical storage migration (PLAT-746).
const PortableWorkflowContractVersion = "1.0.52"

// ValidatePortableWorkflowRuntime always reads the current definitions. It
// constructs previews, never an agent, run, tool call or provider connection.
// Dynamic inputs/connectivity remain unresolved, just as in the normal preview.
func ValidatePortableWorkflowRuntime(ctx context.Context, workspace string, resources *CrewResources, read func(context.Context, string) (string, error)) ([]string, error) {
	if read == nil {
		return nil, fmt.Errorf("the owning caller's current definition reader is required")
	}
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	base.SetWorkspacePath(workspace)
	c := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, definitionRead: read}
	bindCrewResources(c, resources)
	plan, err := c.ReadCurrentPlan(ctx)
	if err != nil {
		return nil, err
	}
	if !c.usesCodeTree() {
		return nil, fmt.Errorf("code_layout_version must be 1; old source layouts are not upgraded automatically")
	}
	var toolErrors []error
	for _, name := range []string{"workflow.json", "planning/plan.json", "planning/step_config.json", "planning/step_override.json", "variables/variables.json"} {
		text, readErr := c.ReadWorkspaceFile(ctx, name)
		if readErr != nil {
			if name == "planning/plan.json" || !errors.Is(readErr, os.ErrNotExist) && !strings.Contains(strings.ToLower(readErr.Error()), "not found") && !strings.Contains(readErr.Error(), "404") {
				return nil, readErr
			}
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(text), &value); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if name != "workflow.json" && name != "planning/step_override.json" {
			if err := portableReferences(value, name, workspace); err != nil {
				return nil, err
			}
		}
		if name != "variables/variables.json" {
			if err := portableCustomToolSelections(value, name); err != nil {
				toolErrors = append(toolErrors, err)
			}
		}
		if name == "variables/variables.json" {
			var inputs VariablesManifest
			if err := json.Unmarshal([]byte(text), &inputs); err != nil {
				return nil, err
			}
			if err := ValidateWorkflowInputDefinitions(&inputs); err != nil {
				return nil, err
			}
			c.variablesManifest = &inputs
		}
	}
	if err := errors.Join(toolErrors...); err != nil {
		return nil, err
	}
	steps := append(collectAllSteps(plan.Steps), collectAllSteps(plan.OrphanSteps)...)
	rawPlan, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	root := workflowAbsPath(workspace)
	// Read the actual current plan, not runtime/planning (definitions and records
	// are siblings after a Crew move). Use the same graph policy as run preflight.
	graph, err := referenceMapForPlan(root, rawPlan, func(file string) ([]byte, error) {
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return nil, err
		}
		for _, info := range steps {
			if step, ok := info.Step.(*ScriptedPlanStep); ok && step.crewScript != nil && filepath.ToSlash(rel) == "code/"+step.ID+"/main.py" {
				return []byte(step.crewScript.Files["main.py"]), nil
			}
		}
		text, err := c.ReadWorkspaceFile(ctx, filepath.ToSlash(rel))
		return []byte(text), err
	}, c.codeRuntimeEnv(nil)["WORKFLOW_CODE_ROOT"])
	if err != nil {
		return nil, err
	}
	checked := make([]string, 0, len(steps))
	seen := map[string]bool{}
	for _, info := range steps {
		step := info.Step
		if seen[step.GetID()] {
			continue
		}
		seen[step.GetID()] = true
		issues := graphPreflightIssues(graph, step.GetID())
		if len(issues) > 0 {
			return nil, fmt.Errorf("step %s: %s", step.GetID(), GraphPreflightMessage(issues))
		}
		switch s := step.(type) {
		case *AgentPlanStep:
			if _, err := c.previewStepRuntime(ctx, s.GetID(), ""); err != nil {
				return nil, fmt.Errorf("step %s: %w", s.GetID(), err)
			}
			for _, item := range s.Items {
				if item.Type == "user_message" {
					if _, err := c.previewStepRuntime(ctx, s.GetID(), item.ID); err != nil {
						return nil, fmt.Errorf("step %s item %s: %w", s.GetID(), item.ID, err)
					}
				}
			}
		case *ScriptedPlanStep:
			if _, err := c.previewStepRuntime(ctx, s.GetID(), ""); err != nil {
				return nil, fmt.Errorf("step %s: %w", s.GetID(), err)
			}
			if s.Script == "" {
				text, err := c.ReadWorkspaceFile(ctx, c.scriptedSourceDir(s.GetID())+"/main.py")
				if err != nil {
					return nil, fmt.Errorf("step %s source: %w", s.GetID(), err)
				}
				if err := portableReferences(text, "code/"+s.ID+"/main.py", workspace); err != nil {
					return nil, err
				}
			} else if s.crewScript != nil {
				if _, found := s.crewScript.Files["main.py"]; !found {
					return nil, fmt.Errorf("step %s: saved script has no main.py", s.GetID())
				}
				for name, text := range s.crewScript.Files {
					if !utf8.Valid(text) {
						continue
					}
					if err := portableReferences(string(text), "script:"+s.Script+"/"+name, s.crewScript.Root); err != nil {
						return nil, err
					}
				}
			}
		}
		checked = append(checked, step.GetID())
	}
	code := c.codeRuntimeEnv(nil)["WORKFLOW_CODE_ROOT"]
	scan := func(root, label string) error {
		return filepath.WalkDir(root, func(file string, entry fs.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) && file == root {
				return nil // An agent-only workflow needs no code directory.
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || strings.HasPrefix(entry.Name(), "test_") || strings.HasSuffix(entry.Name(), "_test.py") {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("code contains a symlink: %s", entry.Name())
			}
			if ext := filepath.Ext(file); ext != ".py" && ext != ".js" && ext != ".ts" && ext != ".sh" && ext != ".md" {
				return nil
			}
			rel, _ := filepath.Rel(root, file)
			raw, err := c.ReadWorkspaceFile(ctx, label+"/"+filepath.ToSlash(rel))
			if err != nil {
				return err
			}
			return portableReferences(raw, label+"/"+filepath.ToSlash(rel), workspace)
		})
	}
	for _, location := range []struct{ root, label string }{{code, "code"}, {workflowAbsPath(workspace, "skills"), "skills"}} {
		if err := scan(location.root, location.label); err != nil {
			return nil, err
		}
	}
	return checked, nil
}

// Inspect authored definitions, including nested step overrides, before typed
// loading can discard an unknown field or merge away a stale selection.
func portableCustomToolSelections(value any, location string) error {
	var failures []error
	switch v := value.(type) {
	case []any:
		for index, child := range v {
			if err := portableCustomToolSelections(child, fmt.Sprintf("%s/%d", location, index)); err != nil {
				failures = append(failures, err)
			}
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child, path := v[key], location+"/"+key
			if key == "enabled_custom_tools" && child != nil {
				entries, ok := child.([]any)
				if !ok {
					failures = append(failures, fmt.Errorf("%s must be a list of category:tool selections", path))
					continue
				}
				for index, entry := range entries {
					selection, ok := entry.(string)
					if !ok {
						failures = append(failures, fmt.Errorf("%s/%d must be a tool name", path, index))
						continue
					}
					if err := orchestrator.ValidateCustomToolSelection(selection); err != nil {
						failures = append(failures, fmt.Errorf("%s/%d: %w", path, index, err))
					}
				}
			} else if key != "drift_review" && key != "review_notes" {
				if err := portableCustomToolSelections(child, path); err != nil {
					failures = append(failures, err)
				}
			}
		}
	}
	return errors.Join(failures...)
}

// Historical evidence is not a live instruction and must not be rewritten.
// Typed grants and output patterns are checked as values, never shell-expanded.
func portableReferences(value any, location, workspace string) error {
	switch v := value.(type) {
	case string:
		if strings.Contains(v, "learnings/") || v == workspace || strings.Contains(v, workspace+"/") {
			return fmt.Errorf("%s still names retired storage; review it in this Crew's chat and use the appropriate runtime anchor or workflow-relative value", location)
		}
	case []any:
		for index, child := range v {
			if err := portableReferences(child, fmt.Sprintf("%s/%d", location, index), workspace); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, child := range v {
			switch key {
			case "drift_review", "review_notes", "goal_agent_migration", "lock_learnings_reason", "lock_code_reason":
				continue
			}
			if key == "additional_read_paths" {
				raw, err := json.Marshal(child)
				if err != nil {
					return err
				}
				var paths []string
				if err := json.Unmarshal(raw, &paths); err != nil {
					return fmt.Errorf("%s: read grants must be workflow-relative paths", location)
				}
				if _, err := normalizeAdditionalReadPaths(paths); err != nil {
					return err
				}
				for _, path := range paths {
					if strings.Contains(path, "$") {
						return fmt.Errorf("%s: read grants do not expand environment variables; use workflow-relative paths", location)
					}
				}
			}
			if err := portableReferences(child, location+"/"+key, workspace); err != nil {
				return err
			}
		}
	}
	return nil
}

// RecordWorkflowContractUpgrade records a saved version, never an attempted
// write. This uses the plan's existing change log, not a migration ledger.
func RecordWorkflowContractUpgrade(ctx context.Context, workspace, from, to, reason string, steps []string, read func(context.Context, string) (string, error), write func(context.Context, string, string) error) error {
	return writePlanChangelogEntry(ctx, workspace, PlanChangelogEntry{
		Tool: "workflow_upgrade", Reason: reason, StepIDs: steps, Target: "workflow.json.version",
		Changes:        []PlanFieldChange{{Field: "version", OldValue: from, NewValue: to}},
		BeforeSnapshot: map[string]string{"version": from}, AfterSnapshot: map[string]string{"version": to},
	}, read, write, loggerv2.NewNoop())
}

func RecordWorkflowManifestRepair(ctx context.Context, workspace, field string, before, after any, reason string, read func(context.Context, string) (string, error), write func(context.Context, string, string) error) error {
	return writePlanChangelogEntry(ctx, workspace, PlanChangelogEntry{
		Tool: "workflow_upgrade_repair", Reason: reason, Target: "workflow.json." + field,
		Changes:        []PlanFieldChange{{Field: field, OldValue: before, NewValue: after}},
		BeforeSnapshot: before, AfterSnapshot: after,
	}, read, write, loggerv2.NewNoop())
}

// RepairPortableWorkflowStep unblocks a named legacy step that the strict run
// loader cannot parse. Other raw steps, unknown fields and history stay intact;
// no relaxed plan is ever admitted for execution. The final stamp checks all.
func RepairPortableWorkflowStep(ctx context.Context, workspace string, repair map[string]interface{}, reason string, read func(context.Context, string) (string, error), write func(context.Context, string, string) error) (string, error) {
	id, _ := repair["step_id"].(string)
	changes, _ := repair["changes"].(map[string]interface{})
	if id == "" || len(changes) == 0 || strings.TrimSpace(reason) == "" {
		return "", fmt.Errorf("provide step_id, changes and a reason describing the retained task and mapping")
	}
	if changedID, present := changes["id"]; present && changedID != id {
		return "", fmt.Errorf("a repair cannot change the step identity")
	}
	file := workspace + "/planning/plan.json"
	before, err := read(ctx, file)
	if err != nil {
		return "", err
	}
	var plan map[string]interface{}
	if err := json.Unmarshal([]byte(before), &plan); err != nil {
		return "", err
	}
	var matches []map[string]interface{}
	var visit func(any)
	visit = func(value any) {
		if list, ok := value.([]any); ok {
			for _, child := range list {
				visit(child)
			}
			return
		}
		step, ok := value.(map[string]any)
		if !ok {
			return
		}
		if step["id"] == id {
			matches = append(matches, step)
		}
		if routes, ok := step["predefined_routes"].([]any); ok {
			for _, route := range routes {
				if route, ok := route.(map[string]any); ok {
					visit(route["sub_agent_step"])
				}
			}
		}
	}
	visit(plan["steps"])
	visit(plan["orphan_steps"])
	if len(matches) != 1 {
		return "", fmt.Errorf("choose one exact existing step ID; no step was changed")
	}
	step := matches[0]
	for field, value := range changes {
		step[field] = value
	}
	if fields, ok := repair["remove_fields"].([]any); ok {
		for _, field := range fields {
			name, ok := field.(string)
			if !ok || name == "id" || name == "type" {
				return "", fmt.Errorf("remove_fields must name removable step fields")
			}
			delete(step, name)
		}
	}
	typed, err := convertMapToStep(step)
	if err != nil {
		return "", err
	}
	if err := validateLoadedPlanStep(typed, 0); err != nil {
		return "", err
	}
	after, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return "", err
	}
	if err := write(ctx, file, string(after)); err != nil {
		return "", err
	}
	if err := writePlanChangelogEntry(ctx, workspace, PlanChangelogEntry{
		Tool: "update_step", Reason: reason, StepIDs: []string{id}, Target: "planning/plan.json",
		BeforeSnapshot: before, AfterSnapshot: string(after),
	}, read, write, loggerv2.NewNoop()); err != nil {
		return "", fmt.Errorf("step saved but its change log failed; workflow remains unstamped: %w", err)
	}
	return "Step repaired. Check status and complete every remaining failure before stamping; no run was started.", nil
}
