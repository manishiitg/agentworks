package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

const externalPlanTestWorkspace = "Workflow/external-plan-test"

type externalPlanTestFiles struct {
	files  map[string]string
	writes int
}

func newExternalPlanTestFiles(t *testing.T, steps ...PlanStepInterface) *externalPlanTestFiles {
	t.Helper()
	data, err := json.Marshal(PlanningResponse{Steps: steps})
	if err != nil {
		t.Fatal(err)
	}
	return &externalPlanTestFiles{files: map[string]string{externalPlanTestWorkspace + "/planning/plan.json": string(data), externalPlanTestWorkspace + "/planning/step_config.json": `{"steps":[]}`}}
}
func (f *externalPlanTestFiles) read(_ context.Context, path string) (string, error) {
	if data, ok := f.files[path]; ok {
		return data, nil
	}
	return "", os.ErrNotExist
}
func (f *externalPlanTestFiles) write(_ context.Context, path, data string) error {
	f.files[path] = data
	f.writes++
	return nil
}
func (f *externalPlanTestFiles) execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	return ExecuteExternalPlanTool(ctx, name, args, externalPlanTestWorkspace, loggerv2.NewNoop(), f.read, f.write, nil)
}

func TestExternalPlanToolsExposeStrictDetachedAllowlist(t *testing.T) {
	definitions := ExternalPlanToolDefinitions()
	if len(definitions) != 15 {
		t.Fatalf("got %d tool definitions", len(definitions))
	}
	names := map[string]bool{}
	for _, definition := range definitions {
		if names[definition.Name] {
			t.Fatalf("duplicate tool %s", definition.Name)
		}
		names[definition.Name] = true
		var schema map[string]interface{}
		if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if schema["additionalProperties"] != false {
			t.Fatalf("%s permits unknown fields", definition.Name)
		}
	}
	for _, forbidden := range []string{"execute_step", "run_full_workflow", "migrate_declared_execution_mode", "create_plan", "write_workspace_file"} {
		if names[forbidden] {
			t.Fatalf("unexpected external tool %s", forbidden)
		}
	}
	definitions[0].InputSchema[0] = 'x'
	if !json.Valid(ExternalPlanToolDefinitions()[0].InputSchema) {
		t.Fatal("caller altered internal schema")
	}
}

func TestExternalPlanToolsUpdateUsesNativeMutationAndChangelog(t *testing.T) {
	files := newExternalPlanTestFiles(t, regularStep("fetch-invoices"))
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, "external-user-123")
	result, err := files.execute(ctx, "update_scripted_step", map[string]interface{}{"existing_step_id": "fetch-invoices", "title": "Fetch pending invoices", "reason": "Clarify pending invoice scope"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Successfully updated scripted step") {
		t.Fatalf("unexpected result %s", result)
	}
	var plan PlanningResponse
	if err := json.Unmarshal([]byte(files.files[externalPlanTestWorkspace+"/planning/plan.json"]), &plan); err != nil {
		t.Fatal(err)
	}
	if got := plan.Steps[0].GetTitle(); got != "Fetch pending invoices" {
		t.Fatalf("title = %q", got)
	}
	found := false
	for path, data := range files.files {
		if !strings.Contains(path, "/planning/changelog/") {
			continue
		}
		var changelog PlanChangelog
		if err := json.Unmarshal([]byte(data), &changelog); err != nil {
			t.Fatal(err)
		}
		if len(changelog.Entries) != 1 {
			t.Fatalf("entries = %d", len(changelog.Entries))
		}
		entry := changelog.Entries[0]
		if entry.Tool != "update_scripted_step" || entry.Reason != "Clarify pending invoice scope" || entry.Origin.SessionID != "external-user-123" || entry.Origin.AgentName != "agentworks-api" {
			t.Fatalf("unexpected changelog: %+v", entry)
		}
		found = true
	}
	if !found {
		t.Fatal("no changelog written")
	}
}

func TestExternalPlanToolsRejectInvalidArgumentsBeforeIO(t *testing.T) {
	for name, args := range map[string]map[string]interface{}{
		"missing reason":       {"existing_step_id": "fetch", "title": "New"},
		"blank reason":         {"existing_step_id": "fetch", "reason": "  "},
		"wrong type":           {"existing_step_id": "fetch", "reason": "Rename", "title": false},
		"unknown field":        {"existing_step_id": "fetch", "reason": "Rename", "workflow_id": "other"},
		"unknown nested field": {"existing_step_id": "fetch", "reason": "Define input", "script_parameters": map[string]interface{}{"limit": map[string]interface{}{"type": "integer", "description": "Limit", "requried": true}}},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			read := func(context.Context, string) (string, error) { calls++; return "", os.ErrNotExist }
			write := func(context.Context, string, string) error { calls++; return nil }
			_, err := ExecuteExternalPlanTool(context.Background(), "update_scripted_step", args, externalPlanTestWorkspace, nil, read, write, nil)
			if err == nil {
				t.Fatal("invalid arguments accepted")
			}
			if calls != 0 {
				t.Fatalf("invalid input performed %d IO calls", calls)
			}
		})
	}
}

func TestExternalPlanToolsRejectWrongStepTypeAndDanglingDelete(t *testing.T) {
	t.Run("wrong step type", func(t *testing.T) {
		files := newExternalPlanTestFiles(t, routingStepWithTargets("target", "target"), regularStep("target"))
		_, err := files.execute(context.Background(), "update_scripted_step", map[string]interface{}{"existing_step_id": "router", "title": "Rename", "reason": "Clarify name"})
		if err == nil || !strings.Contains(err.Error(), "update_routing_step") {
			t.Fatalf("expected native type error, got %v", err)
		}
		if files.writes != 0 {
			t.Fatal("wrong type wrote files")
		}
	})
	t.Run("referenced delete", func(t *testing.T) {
		files := newExternalPlanTestFiles(t, routingStepWithTargets("target", "target"), regularStep("target"))
		_, err := files.execute(context.Background(), "delete_plan_steps", map[string]interface{}{"deleted_step_ids": []string{"target"}, "reason": "Remove obsolete target"})
		if err == nil || !strings.Contains(err.Error(), "step deletion rejected") {
			t.Fatalf("expected graph error, got %v", err)
		}
		if files.writes != 0 {
			t.Fatal("invalid deletion wrote files")
		}
	})
}

func TestExternalPlanToolsSurfaceChangelogWarnings(t *testing.T) {
	files := newExternalPlanTestFiles(t, regularStep("fetch"))
	write := func(ctx context.Context, path, content string) error {
		if strings.Contains(path, "/planning/changelog/") {
			return errors.New("simulated changelog write failure")
		}
		return files.write(ctx, path, content)
	}
	result, err := ExecuteExternalPlanTool(context.Background(), "update_scripted_step", map[string]interface{}{"existing_step_id": "fetch", "title": "Fetch invoices", "reason": "Clarify invoice scope"}, externalPlanTestWorkspace, nil, files.read, write, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Warnings:") || !strings.Contains(result, "simulated changelog write failure") {
		t.Fatalf("changelog failure hidden: %s", result)
	}
}

func TestExternalPlanToolsManagedAccessDoesNotLeak(t *testing.T) {
	const sessionID = "external-plan-access-test"
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	workspacepkg.SetSessionFolderGuard(sessionID, []string{externalPlanTestWorkspace}, []string{externalPlanTestWorkspace})
	workspacepkg.SetSessionFolderGuardBlockedWritePaths(sessionID, []string{externalPlanTestWorkspace + "/planning"})
	defer workspacepkg.ClearSessionShellConfig(sessionID)
	client := workspacepkg.NewClient("http://unused")
	files := newExternalPlanTestFiles(t, regularStep("fetch"))
	write := func(callCtx context.Context, path, content string) error {
		if err := client.ValidatePathWithContext(callCtx, path, true); err != nil {
			return err
		}
		return files.write(callCtx, path, content)
	}
	_, err := ExecuteExternalPlanTool(ctx, "update_scripted_step", map[string]interface{}{"existing_step_id": "fetch", "title": "Fetch invoices", "reason": "Clarify scope"}, externalPlanTestWorkspace, nil, files.read, write, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ValidatePathWithContext(ctx, externalPlanTestWorkspace+"/planning/plan.json", true); err == nil {
		t.Fatal("typed mutation capability leaked to generic writes")
	}
}

func TestExternalPlanToolsStepConfigSharesValidationAndPersists(t *testing.T) {
	files := newExternalPlanTestFiles(t, regularStep("fetch"))
	_, err := files.execute(context.Background(), "update_step_config", map[string]interface{}{"step_id": "fetch", "enabled_skills": []string{"invoice-review"}, "reason": "Use invoice review skill"})
	if err != nil {
		t.Fatal(err)
	}
	configs, err := ParseStepConfigContent(files.files[externalPlanTestWorkspace+"/planning/step_config.json"])
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || len(configs[0].AgentConfigs.EnabledSkills) != 1 || configs[0].AgentConfigs.EnabledSkills[0] != "invoice-review" {
		t.Fatalf("unexpected configs: %+v", configs)
	}
	before := files.writes
	_, err = files.execute(context.Background(), "update_step_config", map[string]interface{}{"step_id": "fetch", "clear_fields": []string{"made_up"}, "reason": "Attempt invalid clear"})
	if err == nil || !strings.Contains(err.Error(), "unknown clear_fields") {
		t.Fatalf("legacy text rejection must be an error, got %v", err)
	}
	if files.writes != before {
		t.Fatal("rejected config mutation wrote files")
	}
	_, err = files.execute(context.Background(), "update_step_config", map[string]interface{}{"step_id": "fetch", "servers": []string{"unselected-server"}, "reason": "Try unavailable server"})
	if err == nil || !strings.Contains(err.Error(), "NOT in the workflow-level selection") {
		t.Fatalf("expected server selection validation: %v", err)
	}
	ctx := WithExternalPlanSelectedServers(context.Background(), []string{"selected-server"})
	_, err = files.execute(ctx, "update_step_config", map[string]interface{}{"step_id": "fetch", "servers": []string{"selected-server"}, "reason": "Enable authorized server"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExternalPlanToolsConfigReadFailureCannotOverwrite(t *testing.T) {
	for _, invalid := range []string{`not json`, `{"steps":[{"id":"fetch"},{"id":"fetch"}]}`} {
		t.Run(invalid, func(t *testing.T) {
			files := newExternalPlanTestFiles(t, regularStep("fetch"))
			path := externalPlanTestWorkspace + "/planning/step_config.json"
			files.files[path] = invalid
			_, err := files.execute(context.Background(), "update_step_config", map[string]interface{}{"step_id": "fetch", "enabled_skills": []string{"invoices"}, "reason": "Enable invoice skill"})
			if err == nil || !strings.Contains(err.Error(), "refusing to overwrite unreadable step config") {
				t.Fatalf("expected fail-closed read guard, got %v", err)
			}
			if files.writes != 0 || files.files[path] != invalid {
				t.Fatal("invalid existing config overwritten")
			}
		})
	}
}

func TestExternalPlanToolsAddScriptedStepCreatesNativeConfig(t *testing.T) {
	files := newExternalPlanTestFiles(t, regularStep("fetch"))
	_, err := files.execute(context.Background(), "add_scripted_step", map[string]interface{}{
		"id": "validate", "title": "Validate invoices", "description": "Validate invoice totals and write validated.json.",
		"context_dependencies": []string{}, "context_output": "validated.json", "insert_after_step_id": "fetch",
		"validation_schema": map[string]interface{}{"files": []interface{}{map[string]interface{}{"file_name": "validated.json", "must_exist": true}}},
		"reason":            "Validate invoice totals before processing",
	})
	if err != nil {
		t.Fatal(err)
	}
	var plan PlanningResponse
	if err := json.Unmarshal([]byte(files.files[externalPlanTestWorkspace+"/planning/plan.json"]), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 2 || plan.Steps[1].GetID() != "validate" || plan.Steps[1].StepType() != StepTypeRegular {
		t.Fatalf("unexpected added plan %+v", plan)
	}
	configs, err := ParseStepConfigContent(files.files[externalPlanTestWorkspace+"/planning/step_config.json"])
	if err != nil {
		t.Fatal(err)
	}
	config := MatchStepConfigByID("validate", configs)
	if config == nil || config.UseCodeExecutionMode == nil || !*config.UseCodeExecutionMode {
		t.Fatalf("native scripted configuration missing: %+v", config)
	}
}
