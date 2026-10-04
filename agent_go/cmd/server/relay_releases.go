package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

const relayReleasesFolder = ".relay_releases"

var relayVersionPattern = regexp.MustCompile(`^v[1-9][0-9]*$`)
var relayPublishLocks sync.Map
var errRelayVersionNotFound = errors.New("Relay version not found")

type relayRelease struct {
	Version         string    `json:"version"`
	Error           string    `json:"error,omitempty"`
	SourceWorkspace string    `json:"source_workspace,omitempty"`
	Hash            string    `json:"hash"`
	PublishedAt     time.Time `json:"published_at"`
	Functions       []string  `json:"functions"`
	OutputStep      string    `json:"output_step_id"`
	FileCount       int       `json:"file_count"`
	Files           []string  `json:"files"`
}

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

func readRelayRelease(ctx context.Context, workspace, version string) (*relayRelease, string, error) {
	if !relayVersionPattern.MatchString(version) {
		return nil, "", fmt.Errorf("invalid Relay version %q", version)
	}
	releaseWorkspace := relayReleaseWorkspace(workspace, version)
	raw, exists, err := readFileFromWorkspace(ctx, path.Join(releaseWorkspace, "release.json"))
	if err != nil {
		return nil, "", err
	}
	if !exists {
		return nil, "", fmt.Errorf("%w: %q", errRelayVersionNotFound, version)
	}
	var release relayRelease
	if err := json.Unmarshal([]byte(raw), &release); err != nil || release.Version != version || release.Hash == "" || len(release.Files) == 0 || release.FileCount != len(release.Files) {
		return nil, "", fmt.Errorf("Relay version %q has invalid release metadata", version)
	}
	return &release, releaseWorkspace, nil
}

func relaySnapshotHash(content map[string]string) string {
	keys := make([]string, 0, len(content))
	for key := range content {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hasher := sha256.New()
	for _, key := range keys {
		hasher.Write([]byte(key))
		hasher.Write([]byte{0})
		hasher.Write([]byte(content[key]))
		hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func verifyRelayRelease(ctx context.Context, release *relayRelease, workspace string) error {
	if release == nil {
		return errors.New("missing Relay release")
	}
	paths, err := listWorkspaceFilesRecursive(ctx, workspace)
	if err != nil {
		return err
	}
	content := make(map[string]string, len(release.Files))
	known := make(map[string]bool, len(release.Files))
	for _, file := range release.Files {
		known[file] = true
	}
	for _, file := range paths {
		relative := strings.TrimPrefix(file, workspace+"/")
		if relative == file || (!known[relative] && (relative == "release.json" || relayRuntimeFile(relative))) {
			continue
		}
		if !known[relative] {
			return fmt.Errorf("published Relay contains unexpected file %q", relative)
		}
		raw, exists, err := readFileFromWorkspace(ctx, file)
		if err != nil || !exists {
			return fmt.Errorf("published Relay file %q is unavailable", relative)
		}
		content[relative] = raw
	}
	if len(content) != release.FileCount || relaySnapshotHash(content) != release.Hash {
		return fmt.Errorf("published Relay %s changed after publishing", release.Version)
	}
	return nil
}

func activeRelayRelease(ctx context.Context, workspace string) (*relayRelease, string, error) {
	raw, exists, err := readFileFromWorkspace(ctx, path.Join(relayReleaseRoot(workspace), "active.json"))
	if err != nil || !exists {
		return nil, "", errors.New("Relay has no published version")
	}
	var pointer struct {
		Version string `json:"version"`
	}
	if json.Unmarshal([]byte(raw), &pointer) != nil {
		return nil, "", errors.New("Relay active version is invalid")
	}
	return readRelayRelease(ctx, workspace, pointer.Version)
}

func resolveRelayRelease(ctx context.Context, workspace, version string) (*relayRelease, string, error) {
	if version == "" {
		return activeRelayRelease(ctx, workspace)
	}
	return readRelayRelease(ctx, workspace, version)
}

func listRelayReleases(ctx context.Context, workspace string) ([]relayRelease, error) {
	names, err := listWorkspaceChildFolderNames(ctx, relayReleaseRoot(workspace))
	if err != nil {
		return nil, err
	}
	var releases []relayRelease
	for _, name := range names {
		if !relayVersionPattern.MatchString(name) {
			continue
		}
		release, _, err := readRelayRelease(ctx, workspace, name)
		if err != nil {
			release = &relayRelease{Version: name, Error: err.Error()}
		}
		releases = append(releases, *release)
	}
	sort.Slice(releases, func(i, j int) bool {
		var left, right int
		fmt.Sscanf(releases[i].Version, "v%d", &left)
		fmt.Sscanf(releases[j].Version, "v%d", &right)
		return left < right
	})
	return releases, nil
}

func relaySnapshotFile(relative string) bool {
	if relative == "" || relative == "release.json" || relayRuntimeFile(relative) || strings.HasPrefix(relative, ".") {
		return false
	}
	for _, part := range strings.Split(relative, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

func relayRuntimeFile(relative string) bool {
	if relative == "schedule-runs.json" {
		return true
	}
	for _, prefix := range []string{"runs/", "relay_releases/", "chat/", "chats/", "chat_history/", "backup/", "publish/", "planning/revisions/", "planning/changelog/", "variables/changelog/", "knowledgebase/notes/", ".sandbox-cache/", "costs/", "db/", "config/", "webhooks/", "builder/", "session/", "sessions/", "logs/"} {
		if strings.HasPrefix(relative, prefix) {
			return true
		}
	}
	return false
}

// publishRelayRelease freezes a Relay in a nested workspace. The ordinary
// executor then reads that workspace unchanged while the parent remains a draft.
func publishRelayRelease(ctx context.Context, workspace string) (*relayRelease, error) {
	lockAny, _ := relayPublishLocks.LoadOrStore(workspace, &sync.Mutex{})
	lock := lockAny.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	manifest, found, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil || !found || manifest.Kind != "relay" {
		return nil, errors.New("Relay manifest not found")
	}
	plan, err := readPlanFromWorkspace(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("read Relay graph: %w", err)
	}
	if err := stepworkflow.ValidateRelayPlanStructure(plan, manifest.RelayOutputStepID); err != nil {
		return nil, err
	}
	functions := make([]string, 0)
	for _, sched := range manifest.Schedules {
		if sched.IsFunctionTrigger() && sched.Enabled {
			inputOK := false
			for _, input := range sched.Function.Inputs {
				if input.Name == "INPUT" && workflowFunctionInputType(input) == "object" && input.Required {
					inputOK = true
				}
			}
			if !inputOK {
				return nil, fmt.Errorf("function %q needs a required object INPUT argument", sched.Function.Name)
			}
			if err := validateWebhookVariableNames(ctx, workspace, workflowFunctionInputNames(sched.Function)); err != nil {
				return nil, fmt.Errorf("function %q: %w", sched.Function.Name, err)
			}
			functions = append(functions, sched.Function.Name)
		}
	}
	if len(functions) == 0 {
		return nil, errors.New("Relay needs an enabled function trigger before publishing")
	}
	sort.Strings(functions)
	files, err := listWorkspaceFilesRecursive(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("list Relay files: %w", err)
	}
	content := map[string]string{}
	var size int
	for _, file := range files {
		relative := strings.TrimPrefix(file, workspace+"/")
		if relative == file || !relaySnapshotFile(relative) {
			continue
		}
		if len(content) >= 5000 {
			return nil, errors.New("Relay has more than 5000 publishable files")
		}
		raw, exists, err := readFileFromWorkspace(ctx, file)
		if err != nil || !exists {
			return nil, fmt.Errorf("read Relay file %q: %w", relative, err)
		}
		if !utf8.ValidString(raw) {
			return nil, fmt.Errorf("Relay file %q is not UTF-8 text", relative)
		}
		size += len(raw)
		if size > 50*1024*1024 {
			return nil, errors.New("Relay publishable files exceed 50 MiB")
		}
		content[relative] = raw
	}
	for _, required := range []string{"workflow.json", "planning/plan.json", "variables/variables.json"} {
		if _, ok := content[required]; !ok {
			return nil, fmt.Errorf("Relay needs %s before publishing", required)
		}
	}
	for _, step := range plan.Steps {
		if script, ok := step.(*stepworkflow.RegularPlanStep); ok && script.ScriptOnly {
			if manifest.CodeLayoutVersion != 1 {
				return nil, fmt.Errorf("Relay script %q needs code_layout_version 1 before publishing", script.ID)
			}
			if _, exists := content[path.Join("code", script.ID, "main.py")]; !exists {
				return nil, fmt.Errorf("Relay script %q needs saved code/%s/main.py", script.ID, script.ID)
			}
		}
	}
	if err := validateRelayPythonTools(ctx, content); err != nil {
		return nil, err
	}
	// Hash the exact file names and contents, independent of workspace listing order.
	keys := make([]string, 0, len(content))
	for key := range content {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := relaySnapshotHash(content)
	if active, _, err := activeRelayRelease(ctx, workspace); err == nil && active.Hash == hash {
		return active, nil
	}
	names, err := listWorkspaceChildFolderNames(ctx, relayReleaseRoot(workspace))
	if err != nil {
		return nil, err
	}
	next := 1
	for _, name := range names {
		if !relayVersionPattern.MatchString(name) {
			continue
		}
		var number int
		fmt.Sscanf(name, "v%d", &number)
		if number >= next {
			next = number + 1
		}
	}
	version := fmt.Sprintf("v%d", next)
	root := relayReleaseRoot(workspace)
	destination := relayReleaseWorkspace(workspace, version)
	if err := createWorkspaceFolder(ctx, path.Dir(root)); err != nil {
		return nil, err
	}
	if err := createWorkspaceFolder(ctx, root); err != nil {
		return nil, err
	}
	if err := createWorkspaceFolder(ctx, destination); err != nil {
		return nil, err
	}
	created := map[string]bool{destination: true}
	for _, key := range keys {
		folder := destination
		for _, part := range strings.Split(path.Dir(key), "/") {
			if part == "." {
				break
			}
			folder = path.Join(folder, part)
			if !created[folder] {
				if err := createWorkspaceFolder(ctx, folder); err != nil {
					return nil, err
				}
				created[folder] = true
			}
		}
		if err := writeFileToWorkspace(ctx, path.Join(destination, key), content[key]); err != nil {
			return nil, fmt.Errorf("copy Relay file %q: %w", key, err)
		}
	}
	release := &relayRelease{Version: version, SourceWorkspace: workspace, Hash: hash, PublishedAt: time.Now().UTC(), Functions: functions, OutputStep: manifest.RelayOutputStepID, FileCount: len(keys), Files: keys}
	encoded, _ := json.Marshal(release)
	if err := writeFileToWorkspace(ctx, path.Join(destination, "release.json"), string(encoded)); err != nil {
		return nil, err
	}
	if err := validateRelayOutputStep(ctx, destination, release.OutputStep); err != nil {
		return nil, fmt.Errorf("verify published graph: %w", err)
	}
	if err := verifyRelayRelease(ctx, release, destination); err != nil {
		return nil, err
	}
	pointer, _ := json.Marshal(map[string]string{"version": version})
	if err := writeFileToWorkspace(ctx, path.Join(root, "active.json"), string(pointer)); err != nil {
		return nil, err
	}
	publishPlanChanged(path.Join(workspace, "planning/plan.json"))
	return release, nil
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
	return registrar.RegisterCustomTool("publish_relay", "Publish the current validated Relay graph and executable files as a new immutable API version. Returns the active version and content hash. Builder tests still use the draft; API calls use published versions.", params, func(ctx context.Context, _ map[string]interface{}) (string, error) {
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
