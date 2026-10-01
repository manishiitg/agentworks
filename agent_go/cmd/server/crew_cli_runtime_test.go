package server

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

func TestCrewAdmissionAndRetainedInputKeepTrustedMode(t *testing.T) {
	fx := newCrewRunModeFixture(t)
	var builderKey string
	for _, tc := range []struct {
		name, user, guest string
		pin, run          bool
	}{
		{name: "owner", user: "owner"},
		{name: "reader requests workshop", user: "reader", run: true},
		{name: "owner pinned run", user: "owner", pin: true, run: true},
		{name: "guest call", user: "owner", guest: "reader", run: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: tc.user, Username: tc.user})
			req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: crewProfileID, AgentProfileConversationKey: "crew-aaa", SelectedFolder: crewRunModeOwnerRoot, PinRunMode: tc.pin, CrewGuestCaller: tc.guest, ExecutionOptions: &ExecutionOptions{WorkshopMode: "workshop"}}
			req.AgentProfileContext.ProjectTitle = "Alpha"
			profile, _, err := fx.api.admitQueryTarget(ctx, &req, tc.user, "mode-chat")
			if err != nil {
				t.Fatal(err)
			}
			mode := "crew-" + crewCLIMode(tc.run)
			if !slices.Contains(req.SelectedSkills, mode) || !slices.Contains(profile.Definition.Skills, mode) {
				t.Fatalf("trusted access selected wrong mode: %v", req.SelectedSkills)
			}
			key := agentProfileSessionKey(profile)
			if tc.run && (!strings.Contains(profile.Prompt, "Crew Run") || key == builderKey) {
				t.Fatal("Run inherited the owner's prompt/fingerprint")
			}
			if !tc.run {
				builderKey = key
			}
			fx.api.launchedAgentProfileKeyBySession = map[string]string{"mode-chat": key}
			if compatible, err := fx.api.agentProfileRetainedPolicyCompatible(ctx, "mode-chat", req); err != nil || !compatible {
				t.Fatalf("same-mode live input rejected: %v, %v", compatible, err)
			}
			fx.api.launchedAgentProfileKeyBySession["mode-chat"] = builderKey
			if compatible, err := fx.api.agentProfileRetainedPolicyCompatible(ctx, "mode-chat", req); err != nil || compatible == tc.run {
				t.Fatalf("live input crossed Builder/Run modes: %v, %v", compatible, err)
			}
		})
	}
}

func TestCrewChatModesSeparatePromptSkillsAndSessionIdentity(t *testing.T) {
	base := workproduct.BuiltinAgentProfile()
	context := agentprofiles.PromptContext{ProjectTitle: "Support", Product: map[string]string{"WORK_IDENTITY": "Role: Support\nPurpose: Help customers"}}
	newProfile := func(readOnly bool) (*resolvedAgentProfile, QueryRequest) {
		t.Helper()
		profile := &resolvedAgentProfile{Definition: base}
		req := QueryRequest{AgentProfileContext: context, SelectedSkills: append(append([]string{}, base.Skills...), "support-triage")}
		if err := applyCrewChatMode(profile, &req, readOnly); err != nil {
			t.Fatal(err)
		}
		return profile, req
	}
	builder, buildReq := newProfile(false)
	run, runReq := newProfile(true)
	if builder.Prompt == run.Prompt || agentProfileSessionKey(builder) == agentProfileSessionKey(run) {
		t.Fatal("Run shares Builder prompt or session fingerprint")
	}
	if !strings.Contains(run.Prompt, "Crew Run") || !strings.Contains(run.Prompt, "Support") || strings.Contains(run.Prompt, "set_work_identity") || strings.Contains(run.Prompt, "When asked for another Crew") {
		t.Fatalf("Run prompt inherited authoring instructions: %s", run.Prompt)
	}
	if !slices.Contains(buildReq.SelectedSkills, "work-mcp") || !slices.Contains(buildReq.SelectedSkills, "crew-builder") {
		t.Fatalf("Builder lost feature skills: %v", buildReq.SelectedSkills)
	}
	for _, name := range []string{"work-mcp", "work-skills", "background-work", "crew-builder"} {
		if slices.Contains(runReq.SelectedSkills, name) {
			t.Fatalf("Run kept authoring skill %s", name)
		}
	}
	if !slices.Contains(runReq.SelectedSkills, "crew-run") || !slices.Contains(runReq.SelectedSkills, "support-triage") {
		t.Fatalf("Run lost its contract or domain skill: %v", runReq.SelectedSkills)
	}
	if slices.Contains(base.Skills, "crew-run") || slices.Contains(base.Skills, "crew-builder") {
		t.Fatal("mode mutated the registered base definition")
	}
	code := &resolvedAgentProfile{Definition: codeproduct.BuiltinAgentProfile(), Prompt: "Code prompt"}
	req := QueryRequest{SelectedSkills: []string{"code-mcp"}}
	before := agentProfileSessionKey(code)
	if err := applyCrewChatMode(code, &req, true); err != nil || code.Prompt != "Code prompt" || before != agentProfileSessionKey(code) || len(req.SelectedSkills) != 1 {
		t.Fatal("Crew mode selection changed Code")
	}
}

func TestCrewRunSharedSectionsExcludeAuthoringGuidance(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		ctx := promptContext{HasProfile: true, ProfileID: crewProfileID, CrewReadOnly: readOnly, FeatureExtensions: []string{"Configure the Crew's connections and schedules."}}
		appender := &recordingAppender{}
		_, _, err := assemblePromptSections(appender, ctx)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(appender.texts, "\n")
		if strings.Contains(text, "Configure the Crew") == readOnly {
			t.Fatalf("authoring feature instructions in wrong mode: %s", text)
		}
		if readOnly && (!strings.Contains(text, "cannot update memory") || strings.Contains(text, "dated-entry")) {
			t.Fatalf("Run received memory-writing instructions: %s", text)
		}
	}
}

func TestCrewProviderSkillsStayInPrivateModeDirectories(t *testing.T) {
	type projector interface {
		ProjectSkills(string, []*llmtypes.Skill) error
	}
	for _, tc := range []struct {
		provider string
		dir      string
		adapter  projector
	}{
		{"claude-code", ".claude/skills", &claudecode.ClaudeCodeAdapter{}},
		{"codex-cli", ".agents/skills", &codexcli.CodexCLIAdapter{}},
		{"cursor-cli", ".cursor/skills", &cursorcli.CursorCLIAdapter{}},
		{"pi-cli", ".pi/skills", &picli.PiCLIAdapter{}},
		{"muse-cli", ".agents/skills", &musecli.MuseCLIAdapter{}},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			root := t.TempDir()
			workspace := filepath.Join(root, "docs")
			project := filepath.Join(workspace, "Crew", "support")
			if err := os.MkdirAll(project, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte("User project guidance"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("WORKSPACE_DOCS_PATH", workspace)
			t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(root, "state"))
			for _, readOnly := range []bool{false, true} {
				dir, err := crewCLIWorkingDir("Crew/support", "owner", "chat", tc.provider, readOnly)
				if err != nil {
					t.Fatal(err)
				}
				name := "crew-" + crewCLIMode(readOnly)
				if err := tc.adapter.ProjectSkills(dir, []*llmtypes.Skill{{Name: name, Description: name, Content: name + " contract"}}); err != nil {
					t.Fatal(err)
				}
				if content, err := os.ReadFile(filepath.Join(dir, tc.dir, name, "SKILL.md")); err != nil || !strings.Contains(string(content), name+" contract") {
					t.Fatalf("missing mode skill: %s, %v", content, err)
				}
				other := "crew-" + crewCLIMode(!readOnly)
				if _, err := os.Stat(filepath.Join(dir, tc.dir, other)); !os.IsNotExist(err) {
					t.Fatalf("other mode skill reached this runtime: %v", err)
				}
			}
			if content, err := os.ReadFile(filepath.Join(project, "AGENTS.md")); err != nil || string(content) != "User project guidance" {
				t.Fatal("provider projection replaced real project instructions")
			}
			if _, err := os.Stat(filepath.Join(project, tc.dir)); !os.IsNotExist(err) {
				t.Fatal("provider projected generated skills into the real project")
			}
		})
	}
}

func TestCrewCLIPrivateModeDirectoriesAndResume(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "docs")
	folder := "Crew/support"
	project := filepath.Join(workspace, folder)
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_DOCS_PATH", workspace)
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(root, "state"))
	// The workflow rollback must not disable Crew isolation/resume checks.
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	for _, provider := range []string{"claude-code", "codex-cli", "cursor-cli", "pi-cli", "muse-cli", "agy-cli"} {
		t.Run(provider, func(t *testing.T) {
			builder, err := crewCLIWorkingDir(folder, "owner", "chat", provider, false)
			if err != nil {
				t.Fatal(err)
			}
			run, err := crewCLIWorkingDir(folder, "owner", "chat", provider, true)
			if err != nil || run == builder || builder == project || run == project {
				t.Fatalf("private mode directory: %q/%q, %v", builder, run, err)
			}
			canonicalProject, err := filepath.EvalSymlinks(project)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := filepath.EvalSymlinks(filepath.Join(run, "project")); err != nil || got != canonicalProject {
				t.Fatalf("project link = %q, %v", got, err)
			}
			agent := testAgentWithHandle("chat", llmtypes.CodingProviderSessionHandle{Provider: provider, WorkingDir: run})
			for _, saved := range []string{run, builder, project} {
				runtime := &ChatHistoryAgentRuntime{Provider: provider, AgentSessionHandle: requireAgentHandle(t, testAgentWithHandle("old", llmtypes.CodingProviderSessionHandle{Provider: provider, WorkingDir: saved}))}
				if got := workflowCLIResumeAllowed(agent, runtime); got != (saved == run) {
					t.Fatalf("resume %s from %q = %v", provider, saved, got)
				}
			}
		})
	}
	if dir, err := crewCLIWorkingDir(folder, "owner", "chat", "openai", true); err != nil || dir != project {
		t.Fatal("API model unexpectedly needs a CLI runtime")
	}
}

func TestCrewRunLandlockPolicyDoesNotGrantLinkedTargetWrite(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	for _, reader := range []bool{false, true} {
		session := "crew-policy-" + crewCLIMode(reader)
		project := codingAgentWorkspaceWorkingDir("Crew/support")
		runtime := filepath.Join(t.TempDir(), "runtime")
		common.SetSessionFolderGuard(session, []string{"Crew/support"}, []string{"Crew/support", "other-grant"})
		common.SetSessionCrewReader(session, reader)
		defer common.ClearSessionShellConfig(session)
		base := &llmtypes.CLISecurityPolicy{WorkspaceWritePaths: []string{project}}
		policy := cliLandlockPolicyForSession(session, "codex-cli", runtime, base)
		if !slices.Contains(policy.WorkspaceReadPaths, project) || !slices.Contains(policy.WorkspaceWritePaths, runtime) {
			t.Fatalf("missing project read/runtime write: %+v", policy)
		}
		if reader && (len(policy.WorkspaceWritePaths) != 1 || slices.Contains(policy.WorkspaceWritePaths, project)) {
			t.Fatalf("Run can write linked data: %+v", policy)
		}
		if !reader && !slices.Contains(policy.WorkspaceWritePaths, project) {
			t.Fatal("Builder lost project writes")
		}
		if !slices.Contains(base.WorkspaceWritePaths, project) {
			t.Fatal("policy resolution mutated caller-owned base")
		}
	}
}
