package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relayapi"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relayreleases"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

const relayReleasesFolder = ".relay_releases"

var relayVersionPattern = regexp.MustCompile(`^v[1-9][0-9]*$`)
var relayPublishLocks sync.Map
var errRelayVersionNotFound = relayapi.ErrVersionNotFound

type relayRelease = relayapi.Release

func relayReleaseWorkspace(workspace, version string) string {
	return path.Join(relayReleaseRoot(workspace), version)
}

func relayReleaseRoot(workspace string) string {
	return workflowtypes.RelayReleaseRoot(workspace)
}

// relayDraftWorkspaceForRelease maps a release's execution workspace back to
// its live Relay identity. Credentials and grants belong to that identity,
// while plan/code and run artifacts remain in the frozen release workspace.
func relayDraftWorkspaceForRelease(ctx context.Context, workspace string) (string, error) {
	clean := path.Clean(workspace)
	parts := strings.Split(clean, "/")
	if len(parts) < 2 || parts[0] != "Workflow" || parts[1] != relayReleasesFolder {
		return workspace, nil
	}
	if root := workflowtypes.RelayReleaseWorkspace(clean); root != "" {
		clean = root
		parts = strings.Split(root, "/")
	}
	if len(parts) != 4 || !relayVersionPattern.MatchString(parts[3]) {
		return "", errors.New("invalid Relay release workspace")
	}
	manifest, found, err := ReadWorkflowManifest(ctx, clean)
	if err != nil || !found || manifest.Kind != "relay" {
		return "", errors.New("published Relay manifest unavailable")
	}
	draft, live, err := findWorkflowManifestByID(ctx, manifest.ID)
	if err != nil || live == nil || live.Kind != "relay" || relayReleaseWorkspace(draft, parts[3]) != clean {
		return "", errors.New("published Relay has no matching live identity")
	}
	return draft, nil
}

func relayReleasePorts() relayapi.ReleasePorts {
	return relayapi.ReleasePorts{
		Read: readFileFromWorkspace, ListFiles: listWorkspaceFilesRecursive, ListFolders: listWorkspaceChildFolderNames,
		CreateFolder: createWorkspaceFolder, Write: writeFileToWorkspace, Prepare: prepareRelayPublication,
		ValidatePublished: func(ctx context.Context, workspace string, release *relayRelease) error {
			manifest, found, err := ReadWorkflowManifest(ctx, workspace)
			if err != nil || !found || manifest.Kind != "relay" {
				return errors.New("published Relay manifest unavailable")
			}
			if isPythonRelay(manifest) {
				if err := validatePythonRelaySource(ctx, workspace); err != nil {
					return fmt.Errorf("verify published program: %w", err)
				}
				return nil
			}
			if err := validateRelayOutputStep(ctx, workspace, release.OutputStep); err != nil {
				return fmt.Errorf("verify published graph: %w", err)
			}
			return nil
		},
		Published: publishPlanChanged,
		LockPublication: func(workspace string) func() {
			lockAny, _ := relayPublishLocks.LoadOrStore(workspace, &sync.Mutex{})
			lock := lockAny.(*sync.Mutex)
			lock.Lock()
			return lock.Unlock
		},
	}
}
func readRelayRelease(ctx context.Context, workspace, version string) (*relayRelease, string, error) {
	return relayreleases.Read(ctx, relayReleasePorts(), workspace, version)
}
func activeRelayRelease(ctx context.Context, workspace string) (*relayRelease, string, error) {
	return relayreleases.Active(ctx, relayReleasePorts(), workspace)
}
func resolveRelayRelease(ctx context.Context, workspace, version string) (*relayRelease, string, error) {
	return relayreleases.Resolve(ctx, relayReleasePorts(), workspace, version)
}
func listRelayReleases(ctx context.Context, workspace string) ([]relayRelease, error) {
	return relayreleases.List(ctx, relayReleasePorts(), workspace)
}
func verifyRelayRelease(ctx context.Context, release *relayRelease, workspace string) error {
	return relayreleases.Verify(ctx, relayReleasePorts(), release, workspace)
}
func publishRelayRelease(ctx context.Context, workspace string) (*relayRelease, error) {
	return relayreleases.Publish(ctx, relayReleasePorts(), workspace)
}
func relaySnapshotHash(content map[string]string) string { return relayreleases.SnapshotHash(content) }
func relaySnapshotFile(relative string) bool             { return relayreleases.SnapshotFile(relative) }
func relayRuntimeFile(relative string) bool              { return relayreleases.RuntimeFile(relative) }

func prepareRelayPublication(ctx context.Context, workspace string) (relayapi.PublishSpec, error) {
	manifest, found, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil || !found || manifest.Kind != "relay" {
		return relayapi.PublishSpec{}, errors.New("Relay manifest not found")
	}
	if err := ValidateManifest(manifest); err != nil {
		return relayapi.PublishSpec{}, err
	}
	variableRaw, exists, err := readFileFromWorkspace(ctx, path.Join(workspace, "variables/variables.json"))
	if err != nil {
		return relayapi.PublishSpec{}, fmt.Errorf("Relay configuration unavailable: %w", err)
	}
	if !exists {
		return relayapi.PublishSpec{}, errors.New("Relay configuration unavailable: variables/variables.json not found")
	}
	var variables stepworkflow.VariablesManifest
	if err := json.Unmarshal([]byte(variableRaw), &variables); err != nil {
		return relayapi.PublishSpec{}, fmt.Errorf("invalid Relay configuration: %w", err)
	}
	var plan *stepworkflow.PlanningResponse
	if isPythonRelay(manifest) {
		if err := validatePythonRelaySource(ctx, workspace); err != nil {
			return relayapi.PublishSpec{}, err
		}
	} else {
		plan, err = readPlanFromWorkspace(ctx, workspace)
		if err != nil {
			return relayapi.PublishSpec{}, fmt.Errorf("read Relay graph: %w", err)
		}
		if err := stepworkflow.ValidateRelayPlanStructure(plan, manifest.RelayOutputStepID); err != nil {
			return relayapi.PublishSpec{}, err
		}
	}
	functions := make([]string, 0)
	for _, sched := range manifest.Schedules {
		if sched.IsFunctionTrigger() && sched.Enabled {
			legacy := ""
			if groups := normalizeScheduleGroupNames(sched.GroupNames); len(groups) == 1 {
				legacy = groups[0]
			}
			if _, err := stepworkflow.ResolveRelayVariableValues(&variables, legacy); err != nil {
				return relayapi.PublishSpec{}, fmt.Errorf("function %q configuration: %w", sched.Function.Name, err)
			}
			inputOK := false
			for _, input := range sched.Function.Inputs {
				if input.Name == "INPUT" && workflowFunctionInputType(input) == "object" && input.Required {
					inputOK = true
				}
			}
			if !inputOK {
				return relayapi.PublishSpec{}, fmt.Errorf("function %q needs a required object INPUT argument", sched.Function.Name)
			}
			if err := validateWebhookVariableNames(ctx, workspace, workflowFunctionInputNames(sched.Function)); err != nil {
				return relayapi.PublishSpec{}, fmt.Errorf("function %q: %w", sched.Function.Name, err)
			}
			functions = append(functions, sched.Function.Name)
		}
	}
	if len(functions) == 0 {
		return relayapi.PublishSpec{}, errors.New("Relay needs an enabled function trigger before publishing")
	}
	sort.Strings(functions)

	requiredFiles := []string{"workflow.json", "variables/variables.json", "planning/plan.json"}
	if isPythonRelay(manifest) {
		requiredFiles[2] = "relay.py"
	}
	var scripts []*stepworkflow.ScriptedPlanStep
	if plan != nil {
		for _, step := range plan.Steps {
			if script, ok := step.(*stepworkflow.ScriptedPlanStep); ok && script.ScriptOnly {
				scripts = append(scripts, script)
			}
			if agent, ok := step.(*stepworkflow.AgentPlanStep); ok {
				for _, route := range agent.PredefinedRoutes {
					if script, ok := route.SubAgentStep.(*stepworkflow.ScriptedPlanStep); ok {
						scripts = append(scripts, script)
					}
				}
			}
		}
	}
	var requiredScripts []string
	for _, script := range scripts {
		if manifest.CodeLayoutVersion != 1 {
			return relayapi.PublishSpec{}, fmt.Errorf("Relay script %q needs code_layout_version 1 before publishing", script.ID)
		}
		requiredScripts = append(requiredScripts, path.Join("code", script.ID, "main.py"))
	}
	return relayapi.PublishSpec{Functions: functions, OutputStep: manifest.RelayOutputStepID, RequiredFiles: requiredFiles, RequiredScripts: requiredScripts}, nil
}

func (api *StreamingAPI) registerRelayReleaseTools(registrar interface {
	RegisterCustomTool(string, string, map[string]interface{}, func(context.Context, map[string]interface{}) (string, error), string) error
}, workspace, userID string) error {
	params := map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
	if err := registrar.RegisterCustomTool("get_relay_releases", "List published versions and the active version of this Relay.", params, func(ctx context.Context, _ map[string]interface{}) (string, error) {
		manifest, found, err := ReadWorkflowManifest(ctx, workspace)
		if err != nil || !found || manifest.Kind != "relay" {
			return "Relay not found", nil
		}
		releases, err := listRelayReleases(ctx, workspace)
		if err != nil {
			return "", err
		}
		active, _, _ := activeRelayRelease(ctx, workspace)
		var current string
		if active != nil {
			current = active.Version
		}
		data, _ := json.Marshal(map[string]interface{}{"active_version": current, "releases": releases})
		return string(data), nil
	}, "relay_release_tools"); err != nil {
		return err
	}
	return registrar.RegisterCustomTool("publish_relay", "Publish the current validated Relay program and executable files as a new immutable API version. Returns the active version and content hash. Builder tests still use the draft; API calls use published versions.", params, func(ctx context.Context, _ map[string]interface{}) (string, error) {
		manifest, found, err := ReadWorkflowManifest(ctx, workspace)
		if err != nil || !found || manifest.Kind != "relay" || workflowAccessForManifest(&UserClaims{UserID: userID}, manifest) != WorkflowAccessOwner && workflowAccessForManifest(&UserClaims{UserID: userID}, manifest) != WorkflowAccessWrite {
			return "Relay write access required", nil
		}
		release, err := publishRelayRelease(ctx, workspace)
		if err != nil {
			return "Publish failed: " + err.Error(), nil
		}
		data, _ := json.Marshal(release)
		return string(data), nil
	}, "relay_release_tools")
}

func (api *StreamingAPI) handleListRelayReleases(w http.ResponseWriter, r *http.Request) {
	workspace, _, _, ok := api.relayForRequest(w, r, "workflows:read")
	if !ok {
		return
	}
	releases, err := listRelayReleases(r.Context(), workspace)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	active, _, activeErr := activeRelayRelease(r.Context(), workspace)
	var version, activeError string
	if active != nil {
		version = active.Version
	} else if raw, exists, _ := readFileFromWorkspace(r.Context(), path.Join(relayReleaseRoot(workspace), "active.json")); exists {
		var pointer struct {
			Version string `json:"version"`
		}
		_ = json.Unmarshal([]byte(raw), &pointer)
		version = pointer.Version
		if activeErr != nil {
			activeError = activeErr.Error()
		}
	}
	// The existing run-folder and execution-log readers take a workspace path.
	// Give the Relay UI each release's path so it can reuse those readers.
	type releaseWithWorkspace struct {
		relayRelease
		WorkspacePath string `json:"workspace_path"`
	}
	items := make([]releaseWithWorkspace, 0, len(releases))
	for _, release := range releases {
		items = append(items, releaseWithWorkspace{relayRelease: release, WorkspacePath: relayReleaseWorkspace(workspace, release.Version)})
	}
	externalJSON(w, map[string]interface{}{"active_version": version, "active_error": activeError, "releases": items})
}
