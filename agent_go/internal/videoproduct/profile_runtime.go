package videoproduct

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/mediapresentation"
	sbw "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

const presentationsMigration = `CREATE TABLE IF NOT EXISTS ui_presentations (id TEXT PRIMARY KEY, kind TEXT NOT NULL, schema_version INTEGER NOT NULL, session_id TEXT, title TEXT NOT NULL, payload_json TEXT NOT NULL, resources_json TEXT NOT NULL, actions_json TEXT NOT NULL, status TEXT NOT NULL, revision INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`
const presentationsKindIndexMigration = `CREATE INDEX IF NOT EXISTS idx_ui_presentations_kind ON ui_presentations(kind)`

type integratedProductManifest struct {
	SchemaVersion int    `json:"schema_version"`
	Product       string `json:"product"`
	ID            string `json:"id"`
	Title         string `json:"title"`
	Description   string `json:"description,omitempty"`
	SessionID     string `json:"session_id"`
}

func marshalWorkspaceJSON(value interface{}) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

func updateWorkspaceJSON(ctx context.Context, client *workspace.Client, path string, value interface{}) error {
	content, err := marshalWorkspaceJSON(value)
	if err != nil {
		return err
	}
	_, err = client.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: path, Content: content})
	return err
}

func workspaceFileExists(ctx context.Context, client *workspace.Client, path string) bool {
	_, err := client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: path})
	return err == nil
}

func videoStudioDefaultVariablesManifest() sbw.VariablesManifest {
	return sbw.VariablesManifest{
		Variables:      []sbw.Variable{},
		Groups:         []sbw.VariableGroup{{Name: "default", Values: map[string]string{}, Enabled: true}},
		ExtractionDate: time.Now().Format(time.RFC3339),
	}
}

// ensureVideoStudioDefaultGroup gives the single-project Video Studio surface a
// stable execution scope. The shared workflow runtime uses variable groups to
// name run folders and isolate execution sessions, even when a product has no
// user-facing variables. Video Studio therefore always has one internal
// "default" group; it is not a choice presented to the user.
func ensureVideoStudioDefaultGroup(ctx context.Context, client *workspace.Client, path string) error {
	file, err := client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: path})
	if err != nil {
		return err
	}
	var manifest sbw.VariablesManifest
	if err := json.Unmarshal([]byte(file.Content), &manifest); err != nil {
		return fmt.Errorf("parse variables manifest: %w", err)
	}
	if len(manifest.Groups) > 0 {
		return nil
	}
	manifest.Groups = []sbw.VariableGroup{{Name: "default", Values: map[string]string{}, Enabled: true}}
	if strings.TrimSpace(manifest.ExtractionDate) == "" {
		manifest.ExtractionDate = time.Now().Format(time.RFC3339)
	}
	return updateWorkspaceJSON(ctx, client, path, manifest)
}

// Profile metadata stores the user-relative workspace path (Chats/...), while
// the session folder guard is intentionally rooted at the physical user scope
// (_users/<id>/Chats/...). Go-side profile tools must use the same canonical
// scope as the guard. The workspace API accepts this canonical form and still
// applies the authenticated X-User-ID boundary.
func profileWorkspaceRoot(userID, workspacePath string) string {
	ref, ok := workspaceref.Parse(workspacePath)
	if !ok {
		// A path escaping the workspace maps to a folder that does not exist,
		// never to the user's whole tree.
		return workspaceref.PhysicalPath(userID, "_invalid_workspace_path")
	}
	return ref.PhysicalKeepOwner(strings.TrimSpace(userID))
}

// profileWorkspaceLocalPath resolves the same user-scoped workspace to the
// host folder used by the native workspace service. Managed vendor installers
// need a real cwd for npx; all agent-facing reads still go through the normal
// workspace API and skills resolver after installation.
func profileWorkspaceLocalPath(userID, workspacePath string) (string, error) {
	docsRoot := strings.TrimSpace(os.Getenv("WORKSPACE_DOCS_PATH"))
	if docsRoot == "" {
		return "", errors.New("WORKSPACE_DOCS_PATH is required for managed product skills")
	}
	absRoot, err := filepath.Abs(docsRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace docs root: %w", err)
	}
	local := filepath.Join(absRoot, filepath.FromSlash(profileWorkspaceRoot(userID, workspacePath)))
	absLocal, err := filepath.Abs(local)
	if err != nil {
		return "", fmt.Errorf("resolve product workspace: %w", err)
	}
	if absLocal != absRoot && !strings.HasPrefix(absLocal, absRoot+string(os.PathSeparator)) {
		return "", errors.New("managed product workspace escapes WORKSPACE_DOCS_PATH")
	}
	return absLocal, nil
}

func integratedWorkflowManifest(projectID, title string) map[string]interface{} {
	manifest := videoStudioWorkflowManifest(projectID, title)
	if capabilities, ok := manifest["capabilities"].(map[string]interface{}); ok {
		product := mustVideoStudioManifest()
		// Reuse AgentWorks' built-in browser skill in workflow stages too; the
		// browser executor remains the generic managed implementation.
		capabilities["selected_skills"] = append([]string(nil), product.Workflows.SelectedSkills...)
		capabilities["browser_mode"] = product.Workflows.BrowserMode
	}
	return manifest
}

func integratedStepConfig() map[string]interface{} {
	return stepConfigForAll(pipelineRegistry)
}

func reconcileIntegratedLLMConfig(ctx context.Context, client *workspace.Client, workspacePath string) error {
	workflowPath := filepath.ToSlash(filepath.Join(workspacePath, "workflow.json"))
	workflowFile, err := client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: workflowPath})
	if err != nil {
		return fmt.Errorf("read workflow LLM config: %w", err)
	}
	var workflowConfig map[string]interface{}
	if err := json.Unmarshal([]byte(workflowFile.Content), &workflowConfig); err != nil {
		return fmt.Errorf("decode workflow LLM config: %w", err)
	}
	capabilities, ok := workflowConfig["capabilities"].(map[string]interface{})
	if !ok {
		capabilities = map[string]interface{}{}
		workflowConfig["capabilities"] = capabilities
	}
	wantedWorkflowLLM := videoWorkflowLLMConfig()
	if !reflect.DeepEqual(capabilities["llm_config"], wantedWorkflowLLM) {
		capabilities["llm_config"] = wantedWorkflowLLM
		if err := updateWorkspaceJSON(ctx, client, workflowPath, workflowConfig); err != nil {
			return fmt.Errorf("pin workflow LLM config: %w", err)
		}
	}

	stepConfigPath := filepath.ToSlash(filepath.Join(workspacePath, "planning/step_config.json"))
	stepConfigFile, err := client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: stepConfigPath})
	if err != nil {
		return fmt.Errorf("read step LLM config: %w", err)
	}
	var stepConfig map[string]interface{}
	if err := json.Unmarshal([]byte(stepConfigFile.Content), &stepConfig); err != nil {
		return fmt.Errorf("decode step LLM config: %w", err)
	}
	changed := false
	steps, _ := stepConfig["steps"].([]interface{})
	for _, rawStep := range steps {
		step, ok := rawStep.(map[string]interface{})
		if !ok {
			continue
		}
		agentConfig, ok := step["agent_configs"].(map[string]interface{})
		if !ok {
			agentConfig = map[string]interface{}{}
			step["agent_configs"] = agentConfig
		}
		wantedExecutionLLM := videoAgentLLMConfig()
		if !reflect.DeepEqual(agentConfig["execution_llm"], wantedExecutionLLM) {
			agentConfig["execution_llm"] = wantedExecutionLLM
			changed = true
		}
	}
	if changed {
		if err := updateWorkspaceJSON(ctx, client, stepConfigPath, stepConfig); err != nil {
			return fmt.Errorf("pin step LLM config: %w", err)
		}
	}
	return nil
}

func ensureIntegratedProject(ctx context.Context, workspaceAPIURL string, runtime agentprofiles.RuntimeContext) error {
	client := workspace.NewClient(workspaceAPIURL, workspace.WithUserID(runtime.UserID))
	manifestPath := filepath.ToSlash(filepath.Join(runtime.WorkspacePath, "product.json"))
	manifestFile, err := client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: manifestPath})
	if err != nil {
		return fmt.Errorf("read product manifest: %w", err)
	}
	var product integratedProductManifest
	if err := json.Unmarshal([]byte(manifestFile.Content), &product); err != nil {
		return fmt.Errorf("decode product manifest: %w", err)
	}
	if product.SchemaVersion != 1 || product.Product != "video-studio" || strings.TrimSpace(product.ID) == "" || strings.TrimSpace(product.Title) == "" {
		return errors.New("product.json is not a valid Video Studio project manifest")
	}

	// Every path granted to the step Folder Guard must exist before Landlock
	// constructs its ruleset. builder/ is read-only execution context and may be
	// empty on a new product project, but omitting the directory makes the whole
	// shell sandbox unavailable on hosts that fail closed for missing policy
	// paths.
	for _, folder := range integratedProjectFolders() {
		if err := client.CreateFolder(ctx, filepath.ToSlash(filepath.Join(runtime.WorkspacePath, folder))); err != nil && !strings.Contains(strings.ToLower(err.Error()), "exist") {
			return fmt.Errorf("create %s folder: %w", folder, err)
		}
	}
	// This initializer is the real AgentWorks chat lifecycle used by Video
	// Studio. Sync YAML-declared vendor skills here before selected_skills is
	// resolved, so the generic AgentWorks loader finds ordinary skills/<name>/
	// folders for the main agent and every workflow stage.
	localWorkspace, err := profileWorkspaceLocalPath(runtime.UserID, runtime.WorkspacePath)
	if err != nil {
		return err
	}
	if err := syncManagedProductSkills(ctx, localWorkspace); err != nil {
		return fmt.Errorf("sync Video Studio managed skills: %w", err)
	}
	seeded := map[string]interface{}{
		"planning/plan.json":        planForAll(pipelineRegistry),
		"planning/step_config.json": integratedStepConfig(),
		"workflow.json":             integratedWorkflowManifest(product.ID, product.Title),
		"variables/variables.json":  videoStudioDefaultVariablesManifest(),
	}
	// Upgrade prior generated Video Studio plans too. Besides retiring the old
	// legacy routes and panel-PNG/FFmpeg contracts, keep product-owned plans in
	// sync when the high-quality route gains a new production gate. The
	// fingerprints are deliberately specific: a plan must already be the Video
	// Studio infographic route, so an unrelated user-authored plan is preserved.
	refreshGeneratedWorkflow := false
	planPath := filepath.ToSlash(filepath.Join(runtime.WorkspacePath, "planning/plan.json"))
	if existingPlan, err := client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: planPath}); err == nil {
		refreshGeneratedWorkflow = shouldRefreshGeneratedVideoStudioPlan(existingPlan.Content)
	}
	for relative, value := range seeded {
		path := filepath.ToSlash(filepath.Join(runtime.WorkspacePath, relative))
		generatedWorkflowFile := relative == "planning/plan.json" || relative == "planning/step_config.json" || relative == "workflow.json"
		if workspaceFileExists(ctx, client, path) && !(refreshGeneratedWorkflow && generatedWorkflowFile) {
			continue
		}
		if err := updateWorkspaceJSON(ctx, client, path, value); err != nil {
			return fmt.Errorf("seed %s: %w", relative, err)
		}
	}
	if err := ensureVideoStudioDefaultGroup(ctx, client, filepath.ToSlash(filepath.Join(runtime.WorkspacePath, "variables/variables.json"))); err != nil {
		return fmt.Errorf("ensure Video Studio default execution group: %w", err)
	}
	if err := reconcileIntegratedLLMConfig(ctx, client, runtime.WorkspacePath); err != nil {
		return err
	}
	soulPath := filepath.ToSlash(filepath.Join(runtime.WorkspacePath, "soul/soul.md"))
	if !workspaceFileExists(ctx, client, soulPath) {
		soul := "# " + product.Title + "\n\n## Objective\nCreate strong, source-grounded videos through a clear, approval-led production process.\n\n## Success Criteria\nThe story is coherent, visual and audio quality are checked, and every final export is reproducible from project assets and workflow artifacts.\n\n## Constraints\nDo not publish. Do not invent facts. Do not generate paid media without explicit approval.\n"
		if _, err := client.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: soulPath, Content: soul}); err != nil {
			return err
		}
	}
	_, err = client.InitializeWorkflowDB(ctx, workspace.InitializeWorkflowDBParams{
		DBPath:     filepath.ToSlash(filepath.Join(runtime.WorkspacePath, "db/db.sqlite")),
		Migrations: []string{presentationsMigration, presentationsKindIndexMigration},
	})
	return err
}

func integratedProjectFolders() []string {
	return []string{"uploads", "work", "outputs", "planning", "variables", "soul", "builder", "db"}
}

func shouldRefreshGeneratedVideoStudioPlan(content string) bool {
	// Old product generations used one of these legacy signatures. The current
	// product plan is also deliberately upgraded when it lacks the critic gates;
	// those gates are a required part of the high-quality workflow, not an
	// optional template addition. Requiring both route-specific identifiers
	// avoids overwriting a user-created workflow that happens to mention video.
	if strings.Contains(content, `"cinematic"`) ||
		strings.Contains(content, `"infographic-design.md"`) ||
		strings.Contains(content, `Build each panel in HTML/CSS`) {
		return true
	}
	hasInfographicRoute := strings.Contains(content, `"route_id": "infographic"`) || strings.Contains(content, `"route_id":"infographic"`)
	hasCinematicRoutes := (strings.Contains(content, `"route_id": "shortform"`) || strings.Contains(content, `"route_id":"shortform"`)) &&
		(strings.Contains(content, `"route_id": "longform"`) || strings.Contains(content, `"route_id":"longform"`))
	if !hasInfographicRoute && !hasCinematicRoutes {
		return false
	}
	// A plan this product generated that the platform will not load is worse
	// than an out-of-date one: run_full_workflow fails outright with "plan.json
	// uses an invalid or legacy format", and no marker check catches it because
	// the defect is a missing field inside a step that is otherwise present and
	// current. Re-seeding is always the right answer here — this is our own
	// generated plan, and the generated one is valid by construction (see
	// TestGeneratedPlanSatisfiesThePlatformValidator).
	if !planLoadsOnThisPlatform(content) {
		return true
	}
	required := []string{`"shortform-characters"`, `"shortform-look-sound"`, `"shortform-narration"`, `"longform-shotlist"`}
	for _, marker := range required {
		if !strings.Contains(content, marker) {
			return true
		}
	}
	return false
}

// RegisterAgentProfileRuntime connects Video Studio's product-owned workspace
// initializer and QA-gated presentation tool to the generic Agent Profile
// registry. Authentication, sessions, streaming, and tool execution remain
// owned by AgentWorks.
func RegisterAgentProfileRuntime(registry *agentprofiles.Registry, workspaceAPIURL string) error {
	if err := mediapresentation.Register(registry, workspaceAPIURL); err != nil {
		return err
	}
	if err := registry.RegisterInitializer("video-studio", func(ctx context.Context, runtime agentprofiles.RuntimeContext) error {
		return ensureIntegratedProject(ctx, workspaceAPIURL, runtime)
	}); err != nil {
		return err
	}
	return nil
}

// planLoadsOnThisPlatform reports whether the workflow runtime would accept
// this plan.json. It runs the same exported validator the plan loader uses, so
// a stored product plan that would fail at run_full_workflow time is detected
// during seeding instead of at the moment the user asks for a video.
func planLoadsOnThisPlatform(content string) bool {
	var plan sbw.PlanningResponse
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return false
	}
	return sbw.ValidatePlanStructure(&plan) == nil
}
