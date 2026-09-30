package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/cliruntime"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestWorkflowLinkedTerminalRestoreRequiresFreshAdmissionDuringRollback(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	ws, _ := newFakeWorkspaceServer(t)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	dir := filepath.Join(root, "Workflow", "linked", "builder", "conversation", "2026-09-30")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"workshop", "run"} {
		payload, err := json.Marshal(map[string]interface{}{
			"user_id": "default", "session_id": "saved", "workshop_mode": mode,
			"runtime": &ChatHistoryAgentRuntime{Kind: "coding_agent", Provider: "agy-cli", WorkspacePath: "Workflow/linked", WorkshopMode: mode, ResumeSupported: true, ExternalSessionID: "native-before-isolation"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "session-saved-conversation.json"), payload, 0600); err != nil {
			t.Fatal(err)
		}
		api := &StreamingAPI{terminalStore: terminals.NewStore()}
		req := httptest.NewRequest(http.MethodPost, "/api/chat-history/restored-terminal", strings.NewReader(`{"session_id":"current","restored_conversation_session_id":"saved","workspace_path":"Workflow/linked"}`))
		rec := httptest.NewRecorder()
		startRestoredTerminalHandler(api)(rec, req)
		var result startRestoredTerminalResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Started || result.Reason != "private_runtime_requires_query" {
			t.Fatalf("%s restored without fresh mode/runtime admission: %d %s", mode, rec.Code, rec.Body.String())
		}
	}
}

func TestWorkflowLinkedRuntimesPreserveExistingPrivateSessions(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	state := filepath.Join(root, "state")
	project := filepath.Join(docs, "Workflow", "linked")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "true")
	for _, provider := range []string{"claude-code", "codex-cli", "cursor-cli", "pi-cli", "muse-cli", "agy-cli"} {
		t.Run(provider, func(t *testing.T) {
			dirs := map[string]string{}
			for _, mode := range []string{"workshop", "run"} {
				// Before the linked design these same private directories held
				// the provider prompt and skills. Adding a link must not move them.
				old, err := cliruntime.Prepare(state, docs, "owner", project, "chat", provider, mode)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(old, "AGENTS.md"), []byte(mode+" instructions"), 0600); err != nil {
					t.Fatal(err)
				}
				dir, err := workflowCLIWorkingDir("Workflow/linked", "owner", "chat", provider, mode)
				if err != nil || dir != old {
					t.Fatalf("existing mode runtime moved: %q -> %q, %v", old, dir, err)
				}
				dirs[mode] = dir
				canonicalProject, err := filepath.EvalSymlinks(project)
				if err != nil {
					t.Fatal(err)
				}
				if got, err := os.Readlink(filepath.Join(dir, cliruntime.ProjectLink)); err != nil || got != canonicalProject {
					t.Fatalf("real workflow link = %q, %v", got, err)
				}
				if got, err := os.ReadFile(filepath.Join(dir, "AGENTS.md")); err != nil || string(got) != mode+" instructions" {
					t.Fatal("link preparation replaced an existing private projection")
				}
				current := testAgentWithHandle("chat", llmtypes.CodingProviderSessionHandle{Provider: provider, WorkingDir: dir})
				saved := &ChatHistoryAgentRuntime{Provider: provider, AgentSessionHandle: requireAgentHandle(t, testAgentWithHandle("chat", llmtypes.CodingProviderSessionHandle{Provider: provider, WorkingDir: old, NativeSessionID: "native-chat"}))}
				if !workflowCLIResumeAllowed(current, saved) {
					t.Fatal("adding a project link discarded a compatible native session")
				}
			}
			if dirs["workshop"] == dirs["run"] {
				t.Fatal("Run and Builder share projection files")
			}
			if err := os.WriteFile(filepath.Join(dirs["workshop"], "project", "new-output"), []byte("durable"), 0600); err != nil {
				t.Fatal(err)
			}
			if content, err := os.ReadFile(filepath.Join(dirs["run"], "project", "new-output")); err != nil || string(content) != "durable" {
				t.Fatal("the modes do not see authoritative workflow data")
			}
			current := testAgentWithHandle("chat", llmtypes.CodingProviderSessionHandle{Provider: provider, WorkingDir: dirs["run"]})
			saved := &ChatHistoryAgentRuntime{Provider: provider, AgentSessionHandle: requireAgentHandle(t, testAgentWithHandle("chat", llmtypes.CodingProviderSessionHandle{Provider: provider, WorkingDir: dirs["workshop"]}))}
			if workflowCLIResumeAllowed(current, saved) {
				t.Fatal("native resume crossed from Builder into Run")
			}
		})
	}
	if content, err := os.ReadFile(filepath.Join(project, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("runtime instructions entered workflow data: %q, %v", content, err)
	}
	if dir, err := workflowCLIWorkingDir("Workflow/linked", "owner", "chat", "openai", "run"); err != nil || dir != project {
		t.Fatal("an API model unexpectedly needs a native runtime")
	}
}

func TestWorkflowReadOnlyLandlockPolicyDoesNotPromoteLinkedData(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	for _, readOnly := range []bool{false, true} {
		mode := "builder"
		if readOnly {
			mode = "run"
		}
		session := "workflow-linked-policy-" + mode
		common.SetSessionFolderGuard(session, []string{"Workflow/linked", "Workflow/reference"}, []string{"Workflow/linked", "Downloads"})
		common.SetSessionWorkflowReadOnly(session, readOnly)
		t.Cleanup(func() { common.ClearSessionShellConfig(session) })
		project := codingAgentWorkspaceWorkingDir("Workflow/linked")
		runtime := filepath.Join(t.TempDir(), "private")
		base := &llmtypes.CLISecurityPolicy{WorkspaceWritePaths: []string{project}}
		policy := cliLandlockPolicyForSession(session, "agy-cli", runtime, base)
		if !slices.Contains(policy.WorkspaceReadPaths, project) || !slices.Contains(policy.WorkspaceWritePaths, runtime) {
			t.Fatalf("missing linked workflow read/private runtime write: %+v", policy)
		}
		if readOnly && len(policy.WorkspaceWritePaths) != 1 {
			t.Fatalf("read-only workflow kept an initial or attached write grant: %+v", policy)
		}
		if !readOnly && (!slices.Contains(policy.WorkspaceWritePaths, project) || !slices.Contains(policy.WorkspaceWritePaths, codingAgentWorkspaceWorkingDir("Downloads"))) {
			t.Fatal("Builder lost an authorized write grant")
		}
		if len(base.WorkspaceWritePaths) != 1 || base.WorkspaceWritePaths[0] != project {
			t.Fatal("session policy changed its caller-owned base")
		}
	}
}

func TestWorkflowLinkedRuntimeRefusesWrongLinkWithoutFallback(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	if err := os.MkdirAll(filepath.Join(docs, "Workflow", "linked"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(root, "state"))
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	// Run isolation is mandatory even when the Builder rollback is enabled.
	dir, err := workflowCLIWorkingDir("Workflow/linked", "reader", "chat", "agy-cli", "run")
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "project")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if got, err := workflowCLIWorkingDir("Workflow/linked", "reader", "chat", "agy-cli", "run"); err == nil || got != "" {
		t.Fatal("unsafe linked runtime fell back to the writable workflow root")
	}
}

func TestWorkflowModePromptsKeepSeparateContractsWithLinkedPaths(t *testing.T) {
	if agentworksproduct.ChatDefinitionKey("builder") == agentworksproduct.ChatDefinitionKey("run") {
		t.Fatal("the modes share a prompt/skill fingerprint")
	}
	for _, mode := range []string{"builder", "run"} {
		prompt := agentworksproduct.ChatPromptTemplate(mode)
		if !strings.Contains(prompt, "project/<path>") || !strings.Contains(prompt, "without the `project/` prefix") {
			t.Fatalf("%s has no linked native/bridge path distinction", mode)
		}
	}
	if slices.Contains(agentworksproduct.ChatSkills("run"), "workflow-commands") || !slices.Contains(agentworksproduct.ChatSkills("builder"), "workflow-commands") {
		t.Fatal("Run received Builder-only authoring skills")
	}
	if !strings.Contains(workflowCLIWorkspaceInstructions("Workflow/linked"), "`cd project && ...`") {
		t.Fatal("native shell commands lack a real-workflow working directory")
	}
}
