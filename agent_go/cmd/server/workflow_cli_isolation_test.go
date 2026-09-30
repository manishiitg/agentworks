package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestWorkflowCLIIsolationSelectionAndResume(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace-docs")
	if err := os.MkdirAll(filepath.Join(workspace, "Workflow", "testing"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_DOCS_PATH", workspace)
	t.Setenv("AGENTWORKS_STATE_ROOT", root)
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "true")
	get := func(user, session, provider, mode string) string {
		t.Helper()
		dir, err := workflowCLIWorkingDir("Workflow/testing", user, session, provider, mode)
		if err != nil {
			t.Fatal(err)
		}
		return dir
	}
	dir := get("owner", "chat-a", "codex-cli", "workshop")
	if dir == codingAgentWorkspaceWorkingDir("Workflow/testing") {
		t.Fatal("shared workflow selected as CLI cwd")
	}
	if dir != get("owner", "chat-a", "codex-cli", "workshop") {
		t.Fatal("directory changed on restart")
	}
	for _, other := range []string{get("runner", "chat-a", "codex-cli", "workshop"), get("owner", "chat-b", "codex-cli", "workshop"), get("owner", "chat-a", "claude-code", "workshop"), get("owner", "chat-a", "codex-cli", "run")} {
		if other == dir {
			t.Fatal("identity collision")
		}
	}
	for _, savedDir := range []string{dir, codingAgentWorkspaceWorkingDir("Workflow/testing"), get("owner", "chat-b", "codex-cli", "workshop"), ""} {
		agent := testAgentWithHandle("chat-a", llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", WorkingDir: dir})
		runtime := &ChatHistoryAgentRuntime{Kind: "coding_agent", Provider: "codex-cli", ResumeSupported: true, WorkshopMode: "workshop", ExternalSessionID: "native-1"}
		runtime.AgentSessionHandle = requireAgentHandle(t, testAgentWithHandle("old", llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", WorkingDir: savedDir, NativeSessionID: "native-1"}))
		got := (&StreamingAPI{}).seedCodingAgentRuntimeFromRestoredConversation("chat-a", "codex-cli", "workshop", runtime, agent)
		if got != (savedDir == dir) {
			t.Fatalf("resume from %q = %v", savedDir, got)
		}
		if requireAgentHandle(t, agent).Provider.WorkingDir != dir {
			t.Fatal("resume replaced private directory")
		}
	}
	for _, nested := range []bool{false, true} {
		agent := testAgentWithHandle("chat-a", llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", WorkingDir: dir})
		runtime := &ChatHistoryAgentRuntime{Kind: "coding_agent", Provider: "codex-cli", ResumeSupported: true, ExternalSessionID: "native-1"}
		runtime.AgentSessionHandle = requireAgentHandle(t, testAgentWithHandle("old", llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", WorkingDir: dir}))
		if nested {
			runtime.AgentSessionHandle.Provider.ProjectDirID = codingAgentWorkspaceWorkingDir("Workflow/testing")
		} else {
			runtime.ProjectDirID = codingAgentWorkspaceWorkingDir("Workflow/testing")
		}
		if (&StreamingAPI{}).seedCodingAgentRuntimeFromRestoredConversation("chat-a", "codex-cli", "workshop", runtime, agent) {
			t.Fatal("Codex project directory bypassed isolation")
		}
	}
	for _, provider := range []string{"claude-code", "pi-cli", "cursor-cli"} {
		private := get("owner", "chat-a", provider, "workshop")
		agent := testAgentWithHandle("chat-a", llmtypes.CodingProviderSessionHandle{Provider: provider, WorkingDir: private})
		runtime := &ChatHistoryAgentRuntime{Kind: "coding_agent", Provider: provider, ResumeSupported: true, ExternalSessionID: "native-1"}
		runtime.AgentSessionHandle = requireAgentHandle(t, testAgentWithHandle("old", llmtypes.CodingProviderSessionHandle{Provider: provider, WorkingDir: private}))
		if !(&StreamingAPI{}).seedCodingAgentRuntimeFromRestoredConversation("chat-a", provider, "workshop", runtime, agent) {
			t.Fatalf("same-directory resume rejected for %s", provider)
		}
	}
	if workflowCLIMode(&QueryRequest{ExecutionOptions: &ExecutionOptions{WorkshopMode: "workshop"}}, true) != "run" {
		t.Fatal("read-only identity was not pinned to run")
	}
	t.Setenv("AGENTWORKS_STATE_ROOT", "")
	t.Setenv("RUNLOOP_USER_DATA_DIR", filepath.Join(root, "ordinary-user-data"))
	defaulted, err := workflowCLIWorkingDir("Workflow/testing", "owner", "chat-a", "codex-cli", "workshop")
	if err != nil {
		t.Fatalf("missing explicit state root did not use durable application default: %v", err)
	}
	if defaulted == codingAgentWorkspaceWorkingDir("Workflow/testing") {
		t.Fatal("missing explicit state root fell back to shared cwd")
	}
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	if get("owner", "chat-a", "codex-cli", "workshop") != codingAgentWorkspaceWorkingDir("Workflow/testing") {
		t.Fatal("disabled rollout changed existing behavior")
	}
	if get("reader", "chat-a", "codex-cli", "run") == codingAgentWorkspaceWorkingDir("Workflow/testing") {
		t.Fatal("Builder rollback made Run's CLI cwd writable workflow data")
	}
}

func TestWorkflowCLIWorkingDirTrustsPrivateAgyRuntime(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	settingsPath := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspace-docs")
	if err := os.MkdirAll(filepath.Join(workspace, "Workflow", "testing"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("WORKSPACE_DOCS_PATH", workspace)
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(root, "state"))
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "true")
	for i := 0; i < 2; i++ {
		dir, err := workflowCLIWorkingDir("Workflow/testing", "owner", "chat-a", "agy-cli", "workshop")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(settingsPath)
		if err != nil {
			t.Fatal(err)
		}
		var settings struct {
			TrustedWorkspaces []string `json:"trustedWorkspaces"`
		}
		if err := json.Unmarshal(raw, &settings); err != nil {
			t.Fatal(err)
		}
		if len(settings.TrustedWorkspaces) != 0 {
			t.Fatalf("AGY global trusted workspaces leaked %q: %v", dir, settings.TrustedWorkspaces)
		}
	}
}

func TestWorkflowCLIIsolationDefaultsOnWithExplicitRollback(t *testing.T) {
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "")
	if !workflowCLIIsolationEnabled() {
		t.Fatal("workflow CLI isolation must default on")
	}
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	if workflowCLIIsolationEnabled() {
		t.Fatal("explicit rollback did not disable workflow CLI isolation")
	}
}

func TestWorkflowCLIStateRootSurvivesOrdinaryRestartWithoutExplicitOverride(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", "")
	userData := filepath.Join(t.TempDir(), "user-data")
	t.Setenv("RUNLOOP_USER_DATA_DIR", userData)

	first, err := workflowCLIStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	second, err := workflowCLIStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(userData, "state")
	if first != want || second != want {
		t.Fatalf("state root changed across restart: first=%q second=%q want=%q", first, second, want)
	}
}

func TestWorkflowCLIStateRootRejectsRelativeLauncherConfiguration(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", "relative-state")
	if _, err := workflowCLIStateRoot(); err == nil {
		t.Fatal("relative AGENTWORKS_STATE_ROOT was accepted")
	}

	t.Setenv("AGENTWORKS_STATE_ROOT", "")
	t.Setenv("RUNLOOP_USER_DATA_DIR", "relative-user-data")
	if _, err := workflowCLIStateRoot(); err == nil {
		t.Fatal("relative RUNLOOP_USER_DATA_DIR was accepted")
	}
}

func TestScheduledWorkflowUsesPrivateRunRuntimeAndSeparateMaintenanceRuntime(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace-docs")
	if err := os.MkdirAll(filepath.Join(workspace, "Workflow", "testing"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_DOCS_PATH", workspace)
	t.Setenv("AGENTWORKS_STATE_ROOT", root)
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "true")

	sctx := &ScheduleContext{
		WorkflowID:    "testing",
		WorkspacePath: "Workflow/testing",
		Schedule:      WorkflowSchedule{ID: "daily", Name: "Daily", WorkshopMode: "run"}, // legacy metadata is ignored
	}
	base := (&SchedulerService{}).buildWorkshopRequest(context.Background(), sctx)
	execOpts, ok := base["execution_options"].(map[string]interface{})
	if !ok || execOpts["workshop_mode"] != "workshop" {
		t.Fatalf("scheduled request execution_options = %#v, want Workshop mode", base["execution_options"])
	}

	sessionID := "schedule-cron--daily_123"
	workshopDir, err := workflowCLIWorkingDir(sctx.WorkspacePath, "owner", sessionID, "codex-cli", "workshop")
	if err != nil {
		t.Fatal(err)
	}
	if workshopDir == codingAgentWorkspaceWorkingDir(sctx.WorkspacePath) {
		t.Fatal("scheduled Workshop selected the shared workflow as its CLI cwd")
	}
}

// The offline harness consumes these paths to run real provider projection and
// cleanup against the SAME selector that workflow chat construction uses.
func TestPLAT296WorkflowCLIDirectories(t *testing.T) {
	root := os.Getenv("AGENTWORKS_PROJECTION_TEST_ROOT")
	fixture := os.Getenv("AGENTWORKS_PROJECTION_TEST_FIXTURE")
	if root == "" || fixture == "" {
		t.Skip("invoked by test-workflow-isolation.py --server-isolation")
	}
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "true")
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(root, "server-state"))
	t.Setenv("WORKSPACE_DOCS_PATH", filepath.Join(root, "cases"))
	plan, err := os.ReadFile(filepath.Join(fixture, "planning", "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	directories := map[string]map[string]string{}
	for provider, cliProvider := range map[string]string{
		"codexcli": "codex-cli", "claudecode": "claude-code",
		"cursorcli": "cursor-cli", "picli": "pi-cli",
	} {
		for _, first := range []string{"owner", "run"} {
			for _, cleanup := range []string{"remove", "restore"} {
				key := provider + "/private-control/" + first + "/" + cleanup
				folder := key + "/Workflow/testing"
				shared := codingAgentWorkspaceWorkingDir(folder)
				if err := os.MkdirAll(filepath.Join(shared, "planning"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(shared, "planning", "plan.json"), plan, 0600); err != nil {
					t.Fatal(err)
				}
				directories[key] = map[string]string{}
				for _, role := range []string{"owner", "run"} {
					mode := "workshop"
					if role == "run" {
						mode = "run"
					}
					dir, err := workflowCLIWorkingDir(folder, role, "test-"+role, cliProvider, mode)
					if err != nil {
						t.Fatal(err)
					}
					directories[key][role] = dir
				}
			}
		}
	}
	data, err := json.MarshalIndent(directories, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "server-runtime-directories.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestMuseLegacyResumeMissingWorkingDirP0(t *testing.T) {
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "true")
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	currentDir := filepath.Join(t.TempDir(), "cli-runtimes", "v1", "chat-a")
	if err := os.MkdirAll(currentDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, nativeDir, payloadType string
		allowed                      bool
	}{
		{"matching native metadata", currentDir, "runtime.session.metadata", true},
		{"different chat", filepath.Join(filepath.Dir(currentDir), "chat-b"), "runtime.session.metadata", false},
		{"tool output is not metadata", currentDir, "runtime.tool.result", false},
		{"missing metadata", "", "runtime.session.metadata", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := testAgentWithHandle("chat-a", llmtypes.CodingProviderSessionHandle{Provider: "muse-cli", WorkingDir: currentDir})
			runtime := &ChatHistoryAgentRuntime{Kind: "coding_agent", Provider: "muse-cli", ResumeSupported: true, ExternalSessionID: "muse-native-id", WorkshopMode: "workshop"}
			runtime.AgentSessionHandle = requireAgentHandle(t, testAgentWithHandle("chat-a", llmtypes.CodingProviderSessionHandle{Provider: "muse-cli", NativeSessionID: "muse-native-id"}))
			originalHandle := runtime.AgentSessionHandle
			path := filepath.Join(data, "muse", "sessions", "2026", "09", "12", "muse-native-id", "session.jsonl")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(map[string]any{"payload_type": tc.payloadType, "payload": map[string]any{"record": map[string]string{"workspace_root": tc.nativeDir}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if got := (&StreamingAPI{}).seedCodingAgentRuntimeFromRestoredConversation("chat-a", "muse-cli", "workshop", runtime, agent); got != tc.allowed {
				t.Fatalf("resume = %v want %v", got, tc.allowed)
			}
			if originalHandle.Provider.WorkingDir != "" {
				t.Fatal("mutated shared persisted handle")
			}
			if tc.allowed {
				handle := requireAgentHandle(t, agent)
				if handle.Provider.NativeSessionID != "muse-native-id" || handle.Provider.WorkingDir != currentDir {
					t.Fatalf("resume identity lost: %+v", handle.Provider)
				}
			}
		})
	}
}
