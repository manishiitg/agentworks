package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// PLAT-746-396: one bad migration record must not apply the Crew stamp gate
// to an unmoved server's unrelated workflow or cut its scheduled work off.
func TestUnmovedWorkflowKeepsStartsWhenBindingMetadataIsUnreadable(t *testing.T) {
	root := serveContractGuardManifest(t, workflowContractSkillsVersion)
	ctx := context.Background()
	manifest, found, err := ReadWorkflowManifest(ctx, root)
	if err != nil || !found {
		t.Fatal(err)
	}
	if binding, err := findCrewExecutableByID(manifest.ID); err != nil || binding != nil {
		t.Fatalf("server without Crew data was treated as migrated: %v %v", binding, err)
	}
	directory, err := crewExecutablesPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "broken.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := requireCurrentWorkflowContractForManualRun(ctx, root); err != nil {
		t.Fatalf("unrelated broken binding stopped an unmoved workflow: %v", err)
	}
	if err := directWebhookPreflight(manifest); err != nil {
		t.Fatalf("unrelated broken binding stopped its webhook: %v", err)
	}
	// Force a directory-read error as well as a malformed JSON record. This
	// reaches the lookup error branch even when discovery skips bad records.
	if err := os.Rename(directory, directory+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directory, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := directWebhookPreflight(manifest); err != nil {
		t.Fatalf("discovery error falsely marked an unmoved workflow as Crew-bound: %v", err)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(directory+"-saved", directory); err != nil {
		t.Fatal(err)
	}
	draft := &productSurfaceDraft{}
	guard := workflowContractExecutionGuardRegistrar{definitionRegistrar: draft, workspacePath: root, scheduled: true}
	started := false
	if err := guard.RegisterCustomTool("run_full_workflow", "run", map[string]interface{}{}, func(context.Context, map[string]interface{}) (string, error) { started = true; return "started", nil }, "workflow"); err != nil {
		t.Fatal(err)
	}
	// Also exercise the schedule's exact-root lookup failure, rather than a
	// global discovery failure that could bypass the schedule branch entirely.
	file, err := crewExecutableFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := draft.tools["run_full_workflow"].exec(ctx, nil); err != nil || !started {
		t.Fatalf("binding lookup failure added a new schedule refusal: %v", err)
	}
}

// PLAT-746-396: exercise the tool registered in Crew Main, not a detached MCP
// stamp helper. Broken old definitions must be repairable without admitting a
// run or stamping past a failure; a demoted owner/another Crew cannot mutate.
func TestCrewMainWorkflowUpgradeChecksRepairsAndStampsCurrentDefinitions(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	svc, files, ctx := newCrewCreationTestEnv(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"}]}`)
	crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Report", Purpose: "Draft reports", IdempotencyKey: "upgrade"})
	if err != nil {
		t.Fatal(err)
	}
	child := crewExecutableBinding{ID: "upgrade-report", CrewID: crew.CrewID, CrewRoot: crew.WorkspacePath, Root: "Workflow/upgrade-report", Kind: "workflow"}
	refuseManifestWrite := false
	upgradeRecordWrites := 0
	// Native commits holding the workflow lock write real files to avoid a
	// cross-process lock deadlock. Its HTTP reader must see the SAME files,
	// rather than the old in-memory mock left behind after a native repair.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/api/documents/")
		if !strings.HasPrefix(name, child.Root+"/") {
			files.ServeHTTP(w, r)
			return
		}
		file, err := fsutil.WorkspacePath(getWorkspaceDocsAbsPath(), name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodGet {
			raw, err := os.ReadFile(file)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"content": string(raw)}})
			return
		}
		if r.Method != http.MethodPut {
			http.Error(w, "unexpected mutation", http.StatusMethodNotAllowed)
			return
		}
		var body struct{ Content string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.Contains(name, "/planning/changelog/") && strings.Contains(body.Content, `"tool": "workflow_upgrade"`) {
			upgradeRecordWrites++
		}
		if refuseManifestWrite && name == child.Root+"/workflow.json" {
			http.Error(w, "file edit serialization unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(file, []byte(body.Content), 0600); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	}))
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	for _, dir := range []string{filepath.Join(getWorkspaceDocsAbsPath(), crew.WorkspacePath, ".agentworks/workflows", child.ID), filepath.Join(getWorkspaceDocsAbsPath(), crew.WorkspacePath, ".agentworks/code", child.ID), filepath.Join(getWorkspaceDocsAbsPath(), "_system/crew-data", crew.CrewID, "workflows", child.ID)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := NewWorkflowManifest("Report")
	manifest.ID, manifest.Version, manifest.CodeLayoutVersion = child.ID, workflowContractSkillsVersion, 1
	if err := WriteWorkflowManifest(ctx, child.Root, manifest); err != nil {
		t.Fatal(err)
	}
	// The same saved version keeps today's behavior until a trusted Crew
	// binding exists. A stale scheduled tool must recheck after the move.
	draft := &productSurfaceDraft{}
	guard := workflowContractExecutionGuardRegistrar{definitionRegistrar: draft, workspacePath: child.Root, scheduled: true}
	started := 0
	if err := guard.RegisterCustomTool("run_full_workflow", "run", map[string]interface{}{}, func(context.Context, map[string]interface{}) (string, error) { started++; return "started", nil }, "workflow"); err != nil {
		t.Fatal(err)
	}
	if _, err := draft.tools["run_full_workflow"].exec(ctx, nil); err != nil || started != 1 {
		t.Fatalf("unmoved schedule refused: %v", err)
	}
	if err := directWebhookPreflight(manifest); err != nil {
		t.Fatalf("unmoved webhook refused: %v", err)
	}
	if err := requireCurrentWorkflowContractForManualRun(ctx, child.Root); err != nil {
		t.Fatalf("unmoved current manual workflow refused: %v", err)
	}
	if err := registerCrewExecutable(child); err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkflowManifest(ctx, child.Root, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := draft.tools["run_full_workflow"].exec(ctx, nil); err == nil || !strings.Contains(err.Error(), "Crew's chat") || started != 1 {
		t.Fatalf("moved schedule bypassed the update or hit the wrong refusal: %v", err)
	}
	plan := `{"steps":[{"id":"draft","type":"crew","title":"Draft","description":"Draft the approved report","retained_note":"keep the original task"},{"id":"other","type":"agent","title":"Keep","description":"Keep this task","prompt_contract":"description-first-v1"}]}`
	if err := writeFileToWorkspace(ctx, child.Root+"/planning/plan.json", plan); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{projectActions: svc}
	reg := &recordingRegistrar{}
	if err := api.registerCrewExecutableFileTools(reg, "owner", crew.WorkspacePath); err != nil {
		t.Fatal(err)
	}
	tool := reg.tools["crew_workflow_files"].exec
	call := func(args map[string]interface{}) (string, error) {
		args["action"], args["workflow_id"] = "upgrade", child.ID
		return tool(ctx, args)
	}
	status, err := call(map[string]interface{}{})
	if err != nil || !strings.Contains(status, "check_failure") || !strings.Contains(status, workflowContractPortableVersion) {
		t.Fatalf("Crew Main cannot see its next update and failing check: %s %v", status, err)
	}
	for _, phase := range []string{"before repair", "after repair"} {
		if err := requireCurrentWorkflowContractForManualRun(ctx, child.Root); err == nil {
			t.Fatalf("%s: manual run admitted unstamped workflow", phase)
		}
		if err := directWebhookPreflight(manifest); err == nil || !strings.Contains(err.Error(), "Crew's chat") {
			t.Fatalf("%s: unattended entrance admitted old workflow: %v", phase, err)
		}
		if phase == "before repair" {
			if _, err := call(map[string]interface{}{"version": workflowContractPortableVersion, "reason": "Review complete"}); err == nil {
				t.Fatal("broken plan was stamped")
			}
			current, _, err := ReadWorkflowManifest(ctx, child.Root)
			if err != nil || current.Version != workflowContractSkillsVersion {
				t.Fatal("failed check changed the saved version", err)
			}
			if _, err := call(map[string]interface{}{"reason": "Convert the same task to an ordinary agent", "repair_step": map[string]interface{}{"step_id": "draft", "changes": map[string]interface{}{"type": "agent", "prompt_contract": "description-first-v1"}}}); err != nil {
				t.Fatal(err)
			}
			currentPlan, err := readWorkflowUpgradeFile(ctx, child.Root+"/planning/plan.json")
			if err != nil {
				t.Fatal(err)
			}
			var repaired struct {
				Steps []struct {
					ID   string `json:"id"`
					Type string `json:"type"`
				} `json:"steps"`
			}
			if err := json.Unmarshal([]byte(currentPlan), &repaired); err != nil || len(repaired.Steps) != 2 || repaired.Steps[0].ID != "draft" || repaired.Steps[0].Type != "agent" {
				t.Fatalf("repair was not visible in the caller's current definition view: %s %v", currentPlan, err)
			}
			if !strings.Contains(currentPlan, "retained_note") || !strings.Contains(currentPlan, "Keep this task") {
				t.Fatal("repair lost an unmentioned field or another step")
			}

		}
	}
	*directory = `{"users":[{"id":"owner","username":"owner","role":"viewer"}]}`
	invalidateUserDirectoryCache()
	if _, err := call(map[string]interface{}{"version": workflowContractPortableVersion, "reason": "Review complete"}); err == nil || !strings.Contains(err.Error(), "account cannot edit") {
		t.Fatalf("demoted owner refusal came from the wrong boundary: %v", err)
	}
	*directory = `{"users":[{"id":"owner","username":"owner","role":"creator"}]}`
	invalidateUserDirectoryCache()
	// A real workspace write refusal must not leave a successful stamp receipt.
	// The HTTP boundary is the only mock: the actual stamp/write/log code runs.
	beforeFailure, _, err := ReadWorkflowManifest(ctx, child.Root)
	if err != nil {
		t.Fatal(err)
	}
	beforeFailure.Capabilities.BrowserMode = "auto"
	retired, _ := json.Marshal(beforeFailure)
	if err := writeFileToWorkspace(ctx, child.Root+"/workflow.json", string(retired)); err != nil {
		t.Fatal(err)
	}
	status, err = call(map[string]interface{}{})
	if err != nil || !strings.Contains(status, "repair_browser_mode") {
		t.Fatalf("retired setting has no repair: %s %v", status, err)
	}
	if applied, err := api.checkAndStampWorkflowContract(ctx, child.Root, beforeFailure, workflowContractPortableVersion, "Check retired setting"); err == nil || applied || !strings.Contains(err.Error(), "capabilities.browser_mode") {
		t.Fatalf("retired setting was stamped: %v %v", applied, err)
	}
	if _, err := call(map[string]interface{}{"repair_browser_mode": true, "reason": "Use the existing local browser conversion policy"}); err != nil {
		t.Fatal(err)
	}
	beforeFailure, _, err = ReadWorkflowManifest(ctx, child.Root)
	if err != nil || beforeFailure.Capabilities.BrowserMode != "extension" || beforeFailure.Version != workflowContractSkillsVersion {
		t.Fatalf("named repair changed version or selected the wrong browser: %+v %v", beforeFailure, err)
	}
	refuseManifestWrite = true
	if applied, err := api.checkAndStampWorkflowContract(ctx, child.Root, beforeFailure, workflowContractPortableVersion, "Check write refusal"); err == nil || applied || !strings.Contains(err.Error(), "503") {
		t.Fatalf("failed manifest write was stamped: applied=%v err=%v", applied, err)
	}
	refuseManifestWrite = false
	if upgradeRecordWrites != 0 {
		t.Fatal("failed manifest write recorded a successful upgrade")
	}
	status, err = call(map[string]interface{}{})
	if err != nil || !strings.Contains(status, `"version":"1.0.51"`) || !strings.Contains(status, `"pending":[{`) {
		t.Fatalf("failed save lost pending update: %s %v", status, err)
	}
	out, err := call(map[string]interface{}{"version": workflowContractPortableVersion, "reason": "Reviewed all current steps and retained the tasks"})
	if err != nil || !strings.Contains(out, `"applied":true`) {
		t.Fatalf("authorized Crew Main stamp failed: %s %v", out, err)
	}
	current, _, err := ReadWorkflowManifest(ctx, child.Root)
	if err != nil || current.Version != workflowContractPortableVersion || directWebhookPreflight(current) != nil || requireCurrentWorkflowContractForManualRun(ctx, child.Root) != nil {
		t.Fatal("successful stamp did not admit the current workflow", err)
	}
	logged := false
	logRoot, err := fsutil.WorkspacePath(getWorkspaceDocsAbsPath(), child.Root+"/planning/changelog")
	if err != nil {
		t.Fatal(err)
	}
	logs, err := filepath.Glob(filepath.Join(logRoot, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range logs {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		logged = logged || strings.Contains(string(raw), `"tool": "workflow_upgrade"`)
	}
	if !logged {
		t.Fatal("version changed without its existing plan change log")
	}
	other, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Different", Purpose: "Other reports", IdempotencyKey: "different-upgrade"})
	if err != nil {
		t.Fatal(err)
	}
	foreign := &recordingRegistrar{}
	if err := api.registerCrewExecutableFileTools(foreign, "owner", other.WorkspacePath); err != nil {
		t.Fatal(err)
	}
	if _, err := foreign.tools["crew_workflow_files"].exec(ctx, map[string]interface{}{"action": "upgrade", "workflow_id": child.ID}); err == nil || !strings.Contains(err.Error(), "this Crew's own list") {
		t.Fatalf("foreign Crew refusal came from the wrong boundary: %v", err)
	}
	current.Version = "9.9.9"
	if err := WriteWorkflowManifest(ctx, child.Root, current); err != nil {
		t.Fatal(err)
	}
	status, err = call(map[string]interface{}{})
	if err != nil || !strings.Contains(status, `"unsupported_version":true`) {
		t.Fatalf("unknown future version was not reported: %s %v", status, err)
	}
	if _, err := call(map[string]interface{}{"version": workflowContractPortableVersion, "reason": "Do not downgrade"}); err == nil {
		t.Fatal("an unknown future version was silently downgraded")
	}
}
