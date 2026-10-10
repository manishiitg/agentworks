package server

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

func TestEnhanceToolDescriptionTreatsGlobalScopeLikeNoProfile(t *testing.T) {
	const chatsFolder = "_users/user-1/Chats"
	noProfile := enhanceToolDescriptionForMultiAgentMode("execute_shell_command", "desc", chatsFolder, nil)
	globalProfile := &resolvedAgentProfile{Definition: agentprofiles.Profile{
		ID: "global-assistant", Scope: agentprofiles.ProfileScopeGlobal,
	}}
	global := enhanceToolDescriptionForMultiAgentMode("execute_shell_command", "desc", chatsFolder, globalProfile)
	projectProfile := &resolvedAgentProfile{Definition: agentprofiles.Profile{ID: "video-studio"}}
	project := enhanceToolDescriptionForMultiAgentMode("execute_shell_command", "desc", chatsFolder, projectProfile)

	if global != noProfile {
		t.Fatalf("expected global-scoped and profile-less descriptions to match:\nGLOBAL:\n%s\nNO PROFILE:\n%s", global, noProfile)
	}
	if strings.Contains(project, "{plan_id}") {
		t.Fatalf("expected a project-scoped profile's description to stay narrowed, got: %s", project)
	}
}

func TestMultiAgentPlacementGuidanceFallsThroughForGlobalScope(t *testing.T) {
	const chatsFolder = "_users/user-1/Chats"
	noProfile := multiAgentPlacementGuidance("execute_shell_command", chatsFolder, nil)
	globalProfile := &resolvedAgentProfile{Definition: agentprofiles.Profile{
		ID: "global-assistant", Scope: agentprofiles.ProfileScopeGlobal,
	}}
	global := multiAgentPlacementGuidance("execute_shell_command", chatsFolder, globalProfile)
	projectProfile := &resolvedAgentProfile{Definition: agentprofiles.Profile{
		ID: "video-studio",
		Runtime: agentprofiles.RuntimePolicy{Workspace: agentprofiles.WorkspacePolicy{
			Placement: map[string][]string{"execute_shell_command": {"project-specific guidance"}},
		}},
	}}
	project := multiAgentPlacementGuidance("execute_shell_command", chatsFolder, projectProfile)

	if !reflect.DeepEqual(global, noProfile) {
		t.Fatalf("expected global-scoped guidance to match no-profile guidance exactly, got %v vs %v", global, noProfile)
	}
	if reflect.DeepEqual(project, noProfile) {
		t.Fatalf("expected a project-scoped profile's own declared placement to be used, not the no-profile fallback")
	}
}

func TestExtractWorkflowContextFolders(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "normalizes and deduplicates workflow paths",
			input: []string{"Workflow/Alpha", "Workflow/Alpha/../Alpha", " Workflow/Beta "},
			want:  []string{"Workflow/Alpha", "Workflow/Beta"},
		},
		{
			name:  "drops protected and invalid paths",
			input: []string{"", ".", "/", "/abs/path", "../Workflow/Bad", "Chats/test", "Workflow/Good"},
			want:  []string{"Workflow/Good"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractWorkflowContextFolders(tc.input)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("extractWorkflowContextFolders(%v) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestExtractWorkspacePathFromObjectiveUsesFirstFileContextPath(t *testing.T) {
	query := "Please inspect these files.\n📁 Files in context: Workflow/Main/input-a.png, Workflow/Main/input-b.png\n"

	got := extractWorkspacePathFromObjective(query)
	want := "Workflow/Main/input-a.png"

	if got != want {
		t.Fatalf("extractWorkspacePathFromObjective() = %q, want %q", got, want)
	}
}

func TestIsToolBackedChatMode(t *testing.T) {
	tests := []struct {
		name      string
		agentMode string
		want      bool
	}{
		{name: "empty", agentMode: "", want: true},
		{name: "simple", agentMode: "simple", want: true},
		{name: "multi agent", agentMode: "multi-agent", want: true},
		{name: "trimmed multi agent", agentMode: " multi-agent ", want: true},
		{name: "workflow phase", agentMode: "workflow_phase", want: false},
		{name: "workflow", agentMode: "workflow", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isToolBackedChatMode(tt.agentMode); got != tt.want {
				t.Fatalf("isToolBackedChatMode(%q) = %v, want %v", tt.agentMode, got, tt.want)
			}
		})
	}
}

func TestIsWorkflowStepTrackingExecution(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		execName string
		meta     map[string]string
		want     bool
	}{
		{
			name:     "metadata marks workflow step",
			id:       "exec-1",
			execName: "Prepare Regression Fixtures",
			meta:     map[string]string{"execution_type": "workflow-step"},
			want:     true,
		},
		{
			name:     "workflow step display name",
			id:       "workflow-full-123-step-0-456",
			execName: "Workflow step -> step-1-execution-prepare-regression-fixtures",
			want:     true,
		},
		{
			name:     "workflow run is not a step",
			id:       "workflow-full-123",
			execName: "full-workflow [Test Group / iteration-0]",
			want:     false,
		},
		{
			name:     "generic id containing step word is not enough",
			id:       "post-step-cleanup",
			execName: "Learning update",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWorkflowStepTrackingExecution(tt.id, tt.execName, tt.meta); got != tt.want {
				t.Fatalf("isWorkflowStepTrackingExecution(%q, %q, %v) = %v, want %v", tt.id, tt.execName, tt.meta, got, tt.want)
			}
		})
	}
}

func TestCollectSplitFolderGuardFolders(t *testing.T) {
	query := "Please inspect this.\n📁 Files in context: Workflow/Main/plan.json, skills/custom/SKILL.md, Chats/ignore.md\n"
	workflowPaths := []string{"Workflow/Referenced", "Workflow/Main"}

	writeFolders, readOnlyFolders := collectSplitFolderGuardFolders(query, workflowPaths)
	wantWrite := []string{"Workflow/Main/plan.json", "skills/custom/SKILL.md"}
	wantReadOnly := []string{"Workflow/Referenced", "Workflow/Main"}

	if !reflect.DeepEqual(writeFolders, wantWrite) {
		t.Fatalf("write folders = %v, want %v", writeFolders, wantWrite)
	}
	if !reflect.DeepEqual(readOnlyFolders, wantReadOnly) {
		t.Fatalf("read-only folders = %v, want %v", readOnlyFolders, wantReadOnly)
	}
}

func TestWorkspaceAdvancedToolBundleIncludesActiveTextAndSearchTools(t *testing.T) {
	tools, executors, categories := createCustomTools(false, "default", "tool-bundle-test-session")

	toolDefs := map[string]bool{}
	for _, tool := range tools {
		if tool.Function != nil {
			toolDefs[tool.Function.Name] = true
		}
	}

	for _, name := range []string{
		"generate_text_llm",
	} {
		if !toolDefs[name] {
			t.Fatalf("workspace tool definitions missing %q", name)
		}
		if _, ok := executors[name]; !ok {
			t.Fatalf("workspace tool executors missing %q", name)
		}
		if got := categories[name]; got != "workspace_advanced" {
			t.Fatalf("tool %q category = %q, want workspace_advanced", name, got)
		}
	}
}

func TestCustomToolBundleIncludesHumanTools(t *testing.T) {
	tools, executors, categories := createCustomTools(false, "default", "tool-bundle-test-session")
	chatToolDefs := map[string]bool{}
	for _, tool := range tools {
		if tool.Function != nil {
			chatToolDefs[tool.Function.Name] = true
		}
	}
	if chatToolDefs["submit_human_answer"] {
		t.Fatal("chat/workflow-builder bundle must not expose removed submit_human_answer tool")
	}
	if _, ok := executors["submit_human_answer"]; ok {
		t.Fatal("chat/workflow-builder bundle must not include removed submit_human_answer executor")
	}
	for _, name := range []string{"human_feedback", "notify_user"} {
		if !chatToolDefs[name] {
			t.Fatalf("chat/workflow-builder bundle missing %s", name)
		}
		if _, ok := executors[name]; ok {
			if got := categories[name]; got != "human_tools" {
				t.Fatalf("%s category = %q, want human_tools", name, got)
			}
		} else {
			t.Fatalf("chat/workflow-builder bundle missing %s executor", name)
		}
	}

	workflowTools, workflowExecutors, workflowCategories := createCustomTools(true, "default", "tool-bundle-test-session")
	workflowToolDefs := map[string]bool{}
	for _, tool := range workflowTools {
		if tool.Function != nil {
			workflowToolDefs[tool.Function.Name] = true
		}
	}

	for _, name := range []string{"human_feedback", "notify_user"} {
		if !workflowToolDefs[name] {
			t.Fatalf("workflow bundle tool definitions missing %q", name)
		}
		if _, ok := workflowExecutors[name]; !ok {
			t.Fatalf("workflow bundle executors missing %q", name)
		}
		if got := workflowCategories[name]; got != "human_tools" {
			t.Fatalf("workflow bundle category for %q = %q, want human_tools", name, got)
		}
	}
	if workflowToolDefs["submit_human_answer"] {
		t.Fatal("workflow bundle must not expose removed submit_human_answer tool")
	}
	if _, ok := workflowExecutors["submit_human_answer"]; ok {
		t.Fatal("workflow bundle must not include removed submit_human_answer executor")
	}
}

// TestChatModeFolderGuardBlockedWrite verifies that wrapExecutorsWithChatModeFolderGuard
// denies writes to paths under blockedWriteFolders even when the path is under an allowed
// write prefix. Regression guard for the "option 2" design — this is the prefix+blocklist
// pattern that replaced the enumerated subfolder allow-list, so drift between subfolders
// and allow-list entries becomes structurally impossible.
func TestChatModeFolderGuardBlockedWrite(t *testing.T) {
	const workflowRoot = "Workflow/test-ops"
	const planningBlocked = workflowRoot + "/planning/"

	// Fake executor: succeeds trivially, returning "OK". We care about whether the
	// wrapper blocks the call before the executor runs, not what the executor does.
	noop := func(ctx context.Context, args map[string]interface{}) (string, error) {
		return "OK", nil
	}

	cases := []struct {
		name      string
		tool      string
		filepath  string
		wantError string // substring match; empty = expect success
	}{
		{
			name:      "write under blocked prefix is denied",
			tool:      "diff_patch_workspace_file",
			filepath:  workflowRoot + "/planning/plan.json",
			wantError: "blocked-write prefix",
		},
		{
			name:      "write deeper under blocked prefix is denied",
			tool:      "diff_patch_workspace_file",
			filepath:  workflowRoot + "/planning/nested/deep.json",
			wantError: "blocked-write prefix",
		},
		{
			name:     "write to allowed sibling (reports/) succeeds",
			tool:     "diff_patch_workspace_file",
			filepath: workflowRoot + "/reports/report_plan.md",
		},
		{
			name:     "write to allowed sibling (db/) succeeds",
			tool:     "diff_patch_workspace_file",
			filepath: workflowRoot + "/db/cost_history.json",
		},
		{
			name:     "write to allowed sibling (soul/) succeeds",
			tool:     "diff_patch_workspace_file",
			filepath: workflowRoot + "/soul/soul.md",
		},
		{
			name:      "write to folder outside workflow root is denied",
			tool:      "diff_patch_workspace_file",
			filepath:  "Workflow/other-workflow/reports/x.md",
			wantError: "allowed write folders",
		},
		{
			name:     "read of blocked prefix is allowed (blockedWrite does not affect reads)",
			tool:     "read_image",
			filepath: workflowRoot + "/planning/plan.json",
		},
	}

	executors := map[string]func(ctx context.Context, args map[string]interface{}) (string, error){
		"diff_patch_workspace_file": noop,
		"read_image":                noop,
	}

	// Grant writes to the whole workflow root; block writes to planning/ subtree.
	// Matches the pattern used by server.go for chat-agent #workflow sessions.
	wrapped := wrapExecutorsWithChatModeFolderGuard(
		executors,
		nil,
		[]string{planningBlocked},
		workflowRoot+"/",
	)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			executor, ok := wrapped[tc.tool]
			if !ok {
				t.Fatalf("wrapped executor missing tool %q", tc.tool)
			}
			_, err := executor(context.Background(), map[string]interface{}{"filepath": tc.filepath})
			switch {
			case tc.wantError == "" && err != nil:
				t.Fatalf("expected success for %q, got error: %v", tc.filepath, err)
			case tc.wantError != "" && err == nil:
				t.Fatalf("expected error containing %q for %q, got nil", tc.wantError, tc.filepath)
			case tc.wantError != "" && err != nil && !strings.Contains(err.Error(), tc.wantError):
				t.Fatalf("expected error containing %q, got: %v", tc.wantError, err)
			}
		})
	}
}

func TestPlanFolderGuardBlocksConfigWriteByDefault(t *testing.T) {
	const chatsFolder = "_users/default/Chats"

	noop := func(ctx context.Context, args map[string]interface{}) (string, error) {
		return "OK", nil
	}
	shellCalled := false
	shellExecutor := func(ctx context.Context, args map[string]interface{}) (string, error) {
		shellCalled = true
		writePaths, ok := ctx.Value(common.FolderGuardAllowedWriteFolderKey).([]string)
		if !ok {
			t.Fatalf("plan guard did not inject chat-mode write paths")
		}
		for _, folder := range writePaths {
			if folder == "config" || folder == "config/" {
				t.Fatalf("config/ should not be injected into write paths, got %v", writePaths)
			}
		}
		return "OK", nil
	}

	wrapped := wrapExecutorsWithPlanFolderGuard(
		map[string]func(ctx context.Context, args map[string]interface{}) (string, error){
			"diff_patch_workspace_file": noop,
			"execute_shell_command":     shellExecutor,
		},
		chatsFolder,
		nil,
	)

	if _, err := wrapped["diff_patch_workspace_file"](context.Background(), map[string]interface{}{"filepath": "config/published-llms.json"}); err == nil {
		t.Fatal("config write should be blocked by default")
	} else if !strings.Contains(err.Error(), "writes restricted") {
		t.Fatalf("expected writes restricted error, got: %v", err)
	}

	if _, err := wrapped["execute_shell_command"](context.Background(), map[string]interface{}{"command": "true"}); err != nil {
		t.Fatalf("shell command should be allowed, got: %v", err)
	}
	if !shellCalled {
		t.Fatal("shell executor was not called")
	}
}

// Seen live in a real SparkQuill session: a model retried a blocked
// diff_patch_workspace_file write 5 times in a row (a bare relative path,
// the real host filesystem path, "./...") without ever landing on the one
// thing that works -- prefixing its relative path with the allowed folder.
// The error now spells out that exact combination as a concrete suggestion.
func TestPlanFolderGuardWriteDeniedSuggestsThePrefixedPath(t *testing.T) {
	const activityFolder = "_users/default/Chats/SparkQuill/activities/2026-09-09-science"

	noop := func(ctx context.Context, args map[string]interface{}) (string, error) {
		return "OK", nil
	}
	wrapped := wrapExecutorsWithPlanFolderGuard(
		map[string]func(ctx context.Context, args map[string]interface{}) (string, error){
			"diff_patch_workspace_file": noop,
		},
		activityFolder,
		nil,
	)

	_, err := wrapped["diff_patch_workspace_file"](context.Background(), map[string]interface{}{"filepath": "attempts/myra-attempts.md"})
	if err == nil {
		t.Fatal("write outside the activity folder should be blocked")
	}
	wantSuggestion := activityFolder + "/attempts/myra-attempts.md"
	if !strings.Contains(err.Error(), wantSuggestion) {
		t.Fatalf("error should suggest the prefixed path %q, got: %v", wantSuggestion, err)
	}
}

func TestExternalRecommendationWriteAccessIsImproveLogOnly(t *testing.T) {
	const chatsFolder = "_users/default/Chats"
	const workflowRoot = "Workflow/latency-report"
	const improveLog = workflowRoot + "/builder/improve.html"

	noop := func(ctx context.Context, args map[string]interface{}) (string, error) {
		return "OK", nil
	}

	mainWrapped := wrapExecutorsWithPlanFolderGuard(
		map[string]func(ctx context.Context, args map[string]interface{}) (string, error){
			"diff_patch_workspace_file": noop,
		},
		chatsFolder,
		nil,
		improveLog,
	)
	delegatedWrapped := wrapExecutorsWithChatModeFolderGuard(
		map[string]func(ctx context.Context, args map[string]interface{}) (string, error){
			"diff_patch_workspace_file": noop,
		},
		nil,
		nil,
		improveLog,
	)

	cases := []struct {
		name      string
		wrapped   map[string]func(ctx context.Context, args map[string]interface{}) (string, error)
		filepath  string
		wantError bool
	}{
		{name: "main can write improve log", wrapped: mainWrapped, filepath: improveLog},
		{name: "main cannot write workflow manifest", wrapped: mainWrapped, filepath: workflowRoot + "/workflow.json", wantError: true},
		{name: "main cannot write sibling builder file", wrapped: mainWrapped, filepath: workflowRoot + "/builder/other.html", wantError: true},
		{name: "main exact file path is not prefix writable", wrapped: mainWrapped, filepath: improveLog + "/child.html", wantError: true},
		{name: "delegate can write improve log", wrapped: delegatedWrapped, filepath: improveLog},
		{name: "delegate cannot write workflow manifest", wrapped: delegatedWrapped, filepath: workflowRoot + "/workflow.json", wantError: true},
		{name: "delegate cannot write sibling builder file", wrapped: delegatedWrapped, filepath: workflowRoot + "/builder/other.html", wantError: true},
		{name: "delegate exact file path is not prefix writable", wrapped: delegatedWrapped, filepath: improveLog + "/child.html", wantError: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.wrapped["diff_patch_workspace_file"](context.Background(), map[string]interface{}{"filepath": tc.filepath})
			if tc.wantError && err == nil {
				t.Fatalf("expected write to %q to be blocked", tc.filepath)
			}
			if !tc.wantError && err != nil {
				t.Fatalf("expected write to %q to be allowed, got: %v", tc.filepath, err)
			}
		})
	}
}

func TestWorkflowPhaseFolderGuardDoesNotAllowChatsByDefault(t *testing.T) {
	const workflowRoot = "Workflow/latency-report"

	noop := func(ctx context.Context, args map[string]interface{}) (string, error) {
		return "OK", nil
	}
	shellCalled := false
	shellExecutor := func(ctx context.Context, args map[string]interface{}) (string, error) {
		shellCalled = true
		if _, ok := ctx.Value(common.FolderGuardAllowedWriteFolderKey).([]string); ok {
			t.Fatalf("workflow phase guard should not inject the chat-mode write context key")
		}
		writePaths, ok := ctx.Value(common.FolderGuardWritePathsKey).([]string)
		if !ok || len(writePaths) == 0 {
			t.Fatalf("workflow phase guard did not inject workflow write paths")
		}
		for _, folder := range writePaths {
			if isChatsWriteFolder(folder) {
				t.Fatalf("workflow phase guard should filter Chats write paths, got %v", writePaths)
			}
		}
		return "OK", nil
	}

	wrapped := wrapExecutorsWithWorkflowPhaseFolderGuard(
		map[string]func(ctx context.Context, args map[string]interface{}) (string, error){
			"diff_patch_workspace_file": noop,
			"execute_shell_command":     shellExecutor,
		},
		workflowRoot,
		nil,
		[]string{workflowRoot + "/planning/"},
		workflowRoot+"/",
		"_users/default/Chats/",
		"_users/default/memories/",
		"_users/default/chat_history/",
	)

	executor := wrapped["diff_patch_workspace_file"]
	if _, err := executor(context.Background(), map[string]interface{}{"filepath": workflowRoot + "/knowledgebase/notes/architecture-map.md"}); err != nil {
		t.Fatalf("workflow write should be allowed, got: %v", err)
	}

	_, err := executor(context.Background(), map[string]interface{}{"filepath": "_users/default/Chats/project-a-architecture-latency-map.md"})
	if err == nil {
		t.Fatal("expected Chats write to be denied in workflow phase guard")
	}
	if !strings.Contains(err.Error(), "allowed write folders") {
		t.Fatalf("expected allowed write folders error, got: %v", err)
	}

	_, err = wrapped["execute_shell_command"](context.Background(), map[string]interface{}{"command": "true"})
	if err != nil {
		t.Fatalf("workflow shell command should be allowed, got: %v", err)
	}
	if !shellCalled {
		t.Fatal("expected shell executor to be called")
	}
}

func TestWorkflowPhaseToolDescriptionDoesNotSayChatsOnly(t *testing.T) {
	desc := enhanceToolDescriptionForWorkflowPhase("diff_patch_workspace_file", "Patch files.", "Workflow/latency-report")

	for _, want := range []string{
		"DIRECTORY ACCESS RESTRICTIONS (WORKFLOW BUILDER)",
		"Workflow/latency-report/",
		"Do NOT write workflow artifacts",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("workflow phase description missing %q:\n%s", want, desc)
		}
	}

	if strings.Contains(desc, "ONLY write/modify files") {
		t.Fatalf("workflow phase description should not contain chat-only write wording:\n%s", desc)
	}
}

// TestWorkflowWritableSubfoldersConsistency is a drift guard: it fails if
// WorkflowWritableSubfolders is missing one of the canonical workflow subfolders
// or accidentally includes planning/. The list feeds folder-guard construction
// for workflow-scoped sessions (server.go:3318 + phase-chat at server.go:4016);
// a silent omission is exactly how reports/, db/, soul/ previously fell out of
// sync and denied legitimate writes.
func TestWorkflowWritableSubfoldersConsistency(t *testing.T) {
	required := map[string]string{
		todo_creation_human.KnowledgebaseFolderName: "knowledgebase facts",
		todo_creation_human.DBFolderName:            "per-run JSON state",
		todo_creation_human.SoulFolderName:          "objective + success criteria (post-migration canonical source)",
		todo_creation_human.ReportsFolderName:       "report_plan.md and widgets",
		todo_creation_human.ExecutionFolderName:     "per-step execution outputs",
		todo_creation_human.LearningsFolderName:     "learnings/_global and per-step learnings",
		todo_creation_human.ScriptsFolderName:       "skill support scripts",
		todo_creation_human.RunsFolderName:          "iteration snapshots",
	}

	have := make(map[string]bool, len(todo_creation_human.WorkflowWritableSubfolders))
	for _, entry := range todo_creation_human.WorkflowWritableSubfolders {
		if !strings.HasSuffix(entry, "/") {
			t.Errorf("WorkflowWritableSubfolders entry %q should end with '/' (consumers use prefix match with trailing slash)", entry)
		}
		have[strings.TrimSuffix(entry, "/")] = true
	}

	for name, purpose := range required {
		if !have[name] {
			t.Errorf("WorkflowWritableSubfolders is missing %q (%s) — adding a *FolderName constant without adding it here causes silent folder-guard drift", name, purpose)
		}
	}

	if have[todo_creation_human.PlanningFolderName] {
		t.Errorf("WorkflowWritableSubfolders must NOT include %q — planning files are managed by typed plan-mod tools, not raw writes", todo_creation_human.PlanningFolderName)
	}
}
