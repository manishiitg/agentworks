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
	// These saved stores must not pass through the executor's lossy projection
	// during an upgrade. Keep dates, anchors and future fields byte-for-byte.
	savedStores := map[string]string{
		"schedules.json": `{"schedules":[{"id":"cron","name":"Daily","project":{"kind":"goal","id":"upgrade-report"},"when":{"cron":"0 9 * * *","timezone":"UTC","anchor":"2026-06-01T00:00:00Z"},"action":{"type":"run","tab":"main"},"input":{},"run_as":"owner","enabled":false},{"id":"interval","name":"Every two hours","project":{"kind":"goal","id":"upgrade-report"},"when":{"cron":"0 */2 * * *","timezone":"UTC","anchor":"2026-06-02T00:00:00Z","interval_minutes":120},"action":{"type":"run","tab":"main"},"input":{},"run_as":"owner","enabled":false},{"id":"dates","name":"Dated","project":{"kind":"goal","id":"upgrade-report"},"when":{"dates":[{"id":"date","date":"2026-12-01","time":"09:00"}],"timezone":"UTC","anchor":"2026-06-03T00:00:00Z"},"action":{"type":"run","tab":"main"},"input":{},"run_as":"owner","enabled":false}]}`,
		"functions.json": `{"functions":[],"future":{"keep":"function metadata"}}`,
		"webhooks.json":  `{"webhooks":[],"future":{"keep":"webhook metadata"}}`,
	}
	for name, raw := range savedStores {
		if err := writeFileToWorkspace(ctx, child.Root+"/"+name, raw); err != nil {
			t.Fatal(err)
		}
	}
	rawManifest, err := readWorkflowUpgradeFile(ctx, child.Root+"/workflow.json")
	if err != nil {
		t.Fatal(err)
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawManifest), &rawFields); err != nil {
		t.Fatal(err)
	}
	rawFields["future"] = json.RawMessage(`{"nested":{"keep":[1,2,"original"]}}`)
	var rawCapabilities map[string]json.RawMessage
	if err := json.Unmarshal(rawFields["capabilities"], &rawCapabilities); err != nil {
		t.Fatal(err)
	}
	rawCapabilities["future"] = json.RawMessage(`{"nested":"original"}`)
	rawFields["capabilities"], _ = json.Marshal(rawCapabilities)
	seeded, _ := json.Marshal(rawFields)
	if err := writeFileToWorkspace(ctx, child.Root+"/workflow.json", string(seeded)); err != nil {
		t.Fatal(err)
	}
	assertStoresPreserved := func() {
		t.Helper()
		for name, want := range savedStores {
			got, err := readWorkflowUpgradeFile(ctx, child.Root+"/"+name)
			if err != nil || got != want {
				t.Fatalf("upgrade rewrote %s: %s %v", name, got, err)
			}
		}
		raw, err := readWorkflowUpgradeFile(ctx, child.Root+"/workflow.json")
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			t.Fatal(err)
		}
		var caps map[string]json.RawMessage
		if err := json.Unmarshal(fields["capabilities"], &caps); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(fields["future"]), "original") || !strings.Contains(string(caps["future"]), "original") {
			t.Fatal("upgrade dropped unknown nested settings")
		}
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
	assertStoresPreserved()
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
	retiredRaw, err := readWorkflowUpgradeFile(ctx, child.Root+"/workflow.json")
	if err != nil {
		t.Fatal(err)
	}
	var retiredFields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(retiredRaw), &retiredFields); err != nil {
		t.Fatal(err)
	}
	var retiredCaps map[string]json.RawMessage
	if err := json.Unmarshal(retiredFields["capabilities"], &retiredCaps); err != nil {
		t.Fatal(err)
	}
	retiredCaps["browser_mode"] = json.RawMessage(`"auto"`)
	retiredFields["capabilities"], _ = json.Marshal(retiredCaps)
	retired, _ := json.Marshal(retiredFields)
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
	assertStoresPreserved()
	beforeFailure, _, err = ReadWorkflowManifest(ctx, child.Root)
	if err != nil || beforeFailure.Capabilities.BrowserMode != "extension" || beforeFailure.Version != workflowContractSkillsVersion {
		t.Fatalf("named repair changed version or selected the wrong browser: %+v %v", beforeFailure, err)
	}
	// PLAT-746: a portable plan could be stamped with retired tool names.
	// Check actual status/stamp paths, including the separate override file;
	// current action identities and a category wildcard remain valid.
	configPath := child.Root + "/planning/step_config.json"
	if err := writeFileToWorkspace(ctx, configPath, `{"steps":[{"id":"draft","agent_configs":{"enabled_custom_tools":["human_tools:notify_user","workspace_advanced:files.image","workspace_advanced:*"]}}]}`); err != nil {
		t.Fatal(err)
	}
	overridePath := child.Root + "/planning/step_override.json"
	if err := writeFileToWorkspace(ctx, overridePath, `{"step_override":{"enabled_custom_tools":["human_tools:missing_tool","human_tools:needs_you.ask"]}}`); err != nil {
		t.Fatal(err)
	}
	status, err = call(map[string]interface{}{})
	if err != nil || !strings.Contains(status, "check_failure") || !strings.Contains(status, "notify_user") || !strings.Contains(status, "files.image") || !strings.Contains(status, "missing_tool") || !strings.Contains(status, "planning/step_override.json") {
		t.Fatalf("retired tools were not all reported with their locations: %s %v", status, err)
	}
	if applied, err := api.checkAndStampWorkflowContract(ctx, child.Root, beforeFailure, workflowContractPortableVersion, "Check retired tools"); err == nil || applied || !strings.Contains(err.Error(), "send_email") || !strings.Contains(err.Error(), "native image reader") {
		t.Fatalf("retired selections were stamped or their repairs were unclear: %v %v", applied, err)
	}
	if err := writeFileToWorkspace(ctx, configPath, `{"steps":[]}`); err != nil {
		t.Fatal(err)
	}
	if err := writeFileToWorkspace(ctx, overridePath, `{"step_override":{"enabled_custom_tools":["human_tools:messages.email_send","workspace_advanced:terminal.execute","workspace_advanced:*"]}}`); err != nil {
		t.Fatal(err)
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
	assertStoresPreserved()
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
	// The connection repair also keeps stores and unknown attachment fields.
	raw, err := readWorkflowUpgradeFile(ctx, child.Root+"/workflow.json")
	if err != nil {
		t.Fatal(err)
	}
	var withAttachment map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &withAttachment); err != nil {
		t.Fatal(err)
	}
	withAttachment["crew_attachments"], _ = json.Marshal([]map[string]any{{"id": "source", "alias": "source", "crew_profile_id": "work", "crew_project_id": crew.CrewID, "crew_workspace_path": "_users/owner/Chats/Work/projects/" + strings.TrimPrefix(crew.WorkspacePath, "Crew/"), "future": map[string]any{"nested": "original"}}})
	attachedRaw, _ := json.Marshal(withAttachment)
	if err := writeFileToWorkspace(ctx, child.Root+"/workflow.json", string(attachedRaw)); err != nil {
		t.Fatal(err)
	}
	if _, err := call(map[string]interface{}{"repair_attachments": true, "reason": "Refresh only the same registered project"}); err != nil {
		t.Fatal(err)
	}
	assertStoresPreserved()
	raw, err = readWorkflowUpgradeFile(ctx, child.Root+"/workflow.json")
	if err != nil || !strings.Contains(raw, `"nested": "original"`) {
		t.Fatalf("unknown attachment metadata was lost: %s %v", raw, err)
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
	// Two writers may start with the same old typed snapshot. Each patches its
	// field in the latest raw file under the real lock, preserving the other.
	versionChange, browserChange := *current, *current
	versionChange.Version = "9.9.9"
	browserChange.Capabilities.BrowserMode = "cdp"
	results := make(chan error, 2)
	for field, candidate := range map[string]*WorkflowManifest{"version": &versionChange, "capabilities.browser_mode": &browserChange} {
		go func(field string, candidate *WorkflowManifest) {
			release, err := teamLock(ctx, child.Root)
			if err != nil {
				results <- err
				return
			}
			defer release()
			results <- writeWorkflowUpgradeField(context.WithValue(ctx, teamLockHeldKey{}, child.Root), child.Root, field, candidate)
		}(field, candidate)
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	current, _, err = ReadWorkflowManifest(ctx, child.Root)
	if err != nil || current.Version != "9.9.9" || current.Capabilities.BrowserMode != "cdp" {
		t.Fatalf("concurrent writer lost another field: %+v %v", current, err)
	}
	assertStoresPreserved()
	status, err = call(map[string]interface{}{})
	if err != nil || !strings.Contains(status, `"unsupported_version":true`) {
		t.Fatalf("unknown future version was not reported: %s %v", status, err)
	}
	if _, err := call(map[string]interface{}{"version": workflowContractPortableVersion, "reason": "Do not downgrade"}); err == nil {
		t.Fatal("an unknown future version was silently downgraded")
	}
}
