package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/instructions"
)

type recordingAppender struct {
	texts []string
	fail  error
}

func (r *recordingAppender) AddInstructions(sections ...string) error {
	if r.fail != nil {
		return r.fail
	}
	r.texts = append(r.texts, sections...)
	return nil
}

func sectionByName(t *testing.T, name string) promptSection {
	t.Helper()
	for _, section := range promptSections {
		if section.Name == name {
			return section
		}
	}
	t.Fatalf("no prompt section named %q", name)
	return promptSection{}
}

// The defect this registry was written after: a section asserting "Your native
// tools (Bash, Read, Write, etc.) are disabled" was injected into a hybrid
// profile, whose premise is the opposite. It was gated only on "is this a CLI
// provider" while the profile sat unread 118 lines above.
//
// This is the invariant, not the condition — a rewrite that keeps the behavior
// still has to pass.
func TestNoSectionClaimsNativeToolsAreDisabledForAHybridProfile(t *testing.T) {
	hybrid := promptContext{
		Provider:           "codex-cli",
		ProfileID:          "video-studio",
		HasProfile:         true,
		NativeCodingTools:  true,
		CLIToolEnvironment: "Your native tools (Bash, Read, Write, etc.) are **disabled**.",
	}

	appender := &recordingAppender{}
	included, _, err := assemblePromptSections(appender, hybrid)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	for _, name := range included {
		if name == "cli-tool-environment" {
			t.Fatal("a hybrid profile keeps its native tools; a section denying that must not apply")
		}
	}
	for _, text := range appender.texts {
		if strings.Contains(text, "native tools") && strings.Contains(text, "disabled") {
			t.Fatalf("assembled prompt tells a hybrid agent its native tools are disabled:\n%s", text)
		}
	}
}

// The same section is still required for the bridge-only profiles it was
// written for, so the guard cannot be "delete it".
func TestCLIToolEnvironmentStillAppliesToABridgeOnlyProfile(t *testing.T) {
	section := sectionByName(t, "cli-tool-environment")
	bridgeOnly := promptContext{
		Provider:           "codex-cli",
		HasProfile:         true,
		NativeCodingTools:  false,
		CLIToolEnvironment: "bridge-only text",
	}
	if !section.Applies(bridgeOnly) {
		t.Fatal("mcp_only profiles genuinely have their native tools disabled; they still need this section")
	}
	// A non-CLI provider never has the text built for it in the first place.
	if section.Applies(promptContext{Provider: "anthropic"}) {
		t.Fatal("a provider with no CLI tool environment text must not contribute an empty section")
	}
}

// Every session gets exactly one workspace map, and which one depends on mode
// rather than on statement order in a 1000-line handler.
func TestWorkspaceMapPicksOneVariantPerMode(t *testing.T) {
	section := sectionByName(t, "workspace-map")
	for _, tt := range []struct {
		name string
		ctx  promptContext
		want string
	}{
		{"workflow phase", promptContext{IsWorkflowPhase: true, ShellRoot: "/root", WorkflowPhaseFolder: "Workflow/demo"}, "Workflow/demo"},
		{"product profile", promptContext{HasProfile: true, ShellRoot: "/root", ProfileWorkspace: "Chats/Video Studio/projects/x"}, "Chats/Video Studio/projects/x"},
		{"work profile", promptContext{HasProfile: true, ProfileID: "work", ShellRoot: "/root", PerUserChatsFolder: "_users/alice/Chats", ProfileWorkspace: "/srv/repos/site"}, "/srv/repos/site/"},
		{"plain chat", promptContext{ShellRoot: "/root", PerUserChatsFolder: "_users/default/Chats"}, "_users/default/Chats"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if !section.Applies(tt.ctx) {
				t.Fatal("every session needs a workspace map")
			}
			if got := section.Build(tt.ctx); !strings.Contains(got, tt.want) {
				t.Fatalf("workspace map does not describe %q:\n%s", tt.want, got)
			}
		})
	}
}

func TestProjectMemoryIsSharedByEveryProductAndWorkflow(t *testing.T) {
	section := sectionByName(t, "project-memory")
	for _, ctx := range []promptContext{
		{HasProfile: true, ProfileID: "work", Provider: "claude-code"},
		{HasProfile: true, ProfileID: "video-studio", Provider: "codex-cli"},
		{HasProfile: true, ProfileID: "dominion", Provider: "cursor-cli"},
		{IsWorkflowPhase: true, Provider: "cursor-cli"},
	} {
		if !section.Applies(ctx) {
			t.Fatalf("project memory did not apply to %+v", ctx)
		}
	}
	if section.Applies(promptContext{}) {
		t.Fatal("an unscoped chat has no project root for project memory")
	}
	text := section.Build(promptContext{HasProfile: true})
	for _, required := range []string{
		"project root MEMORY.md", "one durable memory store", "project/MEMORY.md",
		"Read it before", "provider-native memory", "Never retain secrets",
		"explicit user request", "Save stable verified facts proactively", "project-memory skill",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("shared project-memory instructions are missing %q", required)
		}
	}
}

func TestWorkWorkspaceMapDoesNotLeakAgentWorksStorageConcepts(t *testing.T) {
	text := sectionByName(t, "workspace-map").Build(promptContext{
		HasProfile: true, ProfileID: "work", ShellRoot: "/srv/docs", PerUserChatsFolder: "_users/alice/Chats", ProfileWorkspace: "/srv/repos/site",
	})
	for _, forbidden := range []string{"Chats", "Workflow", "Pulse", "docs root"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("Work workspace map leaked %q:\n%s", forbidden, text)
		}
	}
	if !strings.Contains(text, "/srv/docs/_users/alice/chat_history/") || !strings.Contains(text, "another Crew belonging to the same account") {
		t.Fatalf("Work workspace map does not expose the signed-in user's conversation memory:\n%s", text)
	}
}

// "Not applicable" and "had nothing to say" are different, and both are
// recorded. Several bugs in this archive were written as "update if present,
// else silently skip", which is indistinguishable from correctly doing nothing.
func TestEmptySectionsAreSkippedAndDistinguishedFromInapplicableOnes(t *testing.T) {
	appender := &recordingAppender{}
	included, skipped, err := assemblePromptSections(appender, promptContext{
		ShellRoot:          "/root",
		PerUserChatsFolder: "_users/default/Chats",
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(included) == 0 {
		t.Fatal("the workspace map applies to every session")
	}

	joined := strings.Join(skipped, " ")
	if !strings.Contains(joined, "workflow-context(empty)") {
		t.Fatalf("a section that built nothing must be recorded as empty, not silently dropped: %v", skipped)
	}
	if !strings.Contains(joined, "llm-capability") || strings.Contains(joined, "llm-capability(empty)") {
		t.Fatalf("an inapplicable section must be recorded as inapplicable, not empty: %v", skipped)
	}
}

// The capability snapshot instructs the agent to call list_llm_capabilities,
// text_to_speech, generate_music and set_provider_auth. Video Studio's
// allow-list contains none of them, so injecting it told the agent to reach for
// four tools that were not there — the same shape as several defects in
// docs/bugs/.
func TestCapabilitySnapshotIsWithheldWhenItsToolsAreNotAvailable(t *testing.T) {
	section := sectionByName(t, "llm-capability")
	snapshot := "## Workspace LLM And Media Capability Snapshot\ncall `text_to_speech` …"

	if section.Applies(promptContext{CapabilitySection: snapshot, HasLLMCapabilityTools: false}) {
		t.Fatal("a profile with none of these tools must not be told to call them")
	}
	if !section.Applies(promptContext{CapabilitySection: snapshot, HasLLMCapabilityTools: true}) {
		t.Fatal("a session that can call them still needs the snapshot")
	}
}

func TestProductFeatureInstructionsAreAdditiveNamedSections(t *testing.T) {
	section := sectionByName(t, "product-features")
	ctx := promptContext{
		HasProfile:        true,
		FeatureExtensions: []string{"## Feature: files\n\nFiles are enabled.", "## Feature: mcp\n\nMCP is enabled."},
	}
	if !section.Applies(ctx) {
		t.Fatal("a profile with resolved features must receive their extensions")
	}
	got := section.Build(ctx)
	if !strings.Contains(got, "Feature: files") || !strings.Contains(got, "Feature: mcp") {
		t.Fatalf("feature extension section = %q", got)
	}
	if section.Applies(promptContext{HasProfile: false, FeatureExtensions: ctx.FeatureExtensions}) {
		t.Fatal("profile feature extensions must never leak into generic chat")
	}
}

// The general reference must not leak back into shared workflow assembly.
// Detailed Builder guidance lives in mode-scoped reference skills.
func TestWorkspaceReferenceIsNotASharedSection(t *testing.T) {
	for _, section := range promptSections {
		if section.Name == "workspace-reference" {
			t.Fatal("shared assembly must not inline the generic workflow guide")
		}
	}
}

// All 20 previous call sites discarded this error with `_ =`.
func TestAssemblyReportsWhichSectionFailedToAppend(t *testing.T) {
	appender := &recordingAppender{fail: errors.New("definition is immutable")}
	_, _, err := assemblePromptSections(appender, promptContext{
		ShellRoot:          "/root",
		PerUserChatsFolder: "_users/default/Chats",
	})
	if err == nil {
		t.Fatal("an AddInstructions failure must surface, not vanish")
	}
	var sectionErr *promptSectionError
	if !errors.As(err, &sectionErr) || sectionErr.Section != "workspace-map" {
		t.Fatalf("error must name the failing section, got %v", err)
	}
}

// Names are the handle used by the log, these tests, and grep. A duplicate or
// blank one makes the assembly log ambiguous.
func TestSectionNamesAreUniqueAndNonEmpty(t *testing.T) {
	seen := map[string]bool{}
	for i, section := range promptSections {
		if strings.TrimSpace(section.Name) == "" {
			t.Fatalf("prompt section %d has no name", i)
		}
		if seen[section.Name] {
			t.Fatalf("duplicate prompt section name %q", section.Name)
		}
		seen[section.Name] = true
		if section.Applies == nil || section.Build == nil {
			t.Fatalf("prompt section %q must define both Applies and Build", section.Name)
		}
	}
}

// The shared-server rules are for the Code product only: not Crew, workflows or other products.
func TestHostSafetyRulesApplyToCodeOnly(t *testing.T) {
	section := sectionByName(t, "code-host-safety")
	if !section.Applies(promptContext{ProfileID: codeproduct.ProfileID, HasProfile: true}) {
		t.Fatal("the Code product did not get the shared-server rules")
	}
	for _, profile := range []string{"work", "video-studio", "workflow", ""} {
		if section.Applies(promptContext{ProfileID: profile, HasProfile: profile != ""}) {
			t.Fatalf("profile %q got the Code-only shared-server rules", profile)
		}
	}
	text := section.Build(promptContext{})
	for _, must := range []string{"working folder", "code-server", "127.0.0.1", "\"~\""} {
		if !strings.Contains(text, must) {
			t.Fatalf("the shared-server rules no longer mention %s", must)
		}
	}
}

func TestMemorySkillAndPromptAgreeAcrossModes(t *testing.T) {
	section := sectionByName(t, "project-memory")
	for _, ctx := range []promptContext{{HasProfile: true}, {HasProfile: true, CrewReadOnly: true},
		{HasProfile: true, ProfileID: "code", MemoryReadOnly: true}, {IsWorkflowPhase: true, WorkflowMode: "workshop"}, {IsWorkflowPhase: true, WorkflowMode: "run"}} {
		readonly := ctx.MemoryReadOnly || ctx.CrewReadOnly || ctx.WorkflowMode == "run"
		prompt := section.Build(ctx)
		skill := instructions.ProjectMemorySkill(readonly)
		if !strings.Contains(prompt, skill.Name) || strings.Contains(prompt, "**Summary:**") {
			t.Fatal("missing skill pointer or inlined memory template")
		}
		if readonly && (!strings.Contains(prompt, "cannot update") || strings.Contains(skill.Content, "**Summary:**")) {
			t.Fatal("Run received writer instructions")
		}
		if !readonly && !strings.Contains(skill.Content, "**Summary:**") {
			t.Fatal("writer lost format")
		}
	}
}

// Project paths come from the manifest relative to the docs root, whereas
// already-resolved host paths must remain unchanged.
func TestProjectWorkspaceMapResolvesAuthorizedAbsolutePaths(t *testing.T) {
	section := sectionByName(t, "workspace-map")
	for _, profile := range []string{"code", "work"} {
		for _, workspace := range []string{"_users/alice/Chats/Code/projects/site", "/srv/docs/_users/alice/Chats/Code/projects/site"} {
			text := section.Build(promptContext{ProfileID: profile, HasProfile: true,
				ShellRoot: "/srv/docs", ProfileWorkspace: workspace, PerUserChatsFolder: "_users/alice/Chats"})
			if !strings.Contains(text, "primary workspace is `/srv/docs/_users/alice/Chats/Code/projects/site/`") {
				t.Fatalf("%s workspace %q was not resolved correctly: %s", profile, workspace, text)
			}
			if !strings.Contains(text, "`/srv/docs/_users/alice/chat_history/`") {
				t.Fatalf("%s lost the signed-in user's history path", profile)
			}
		}
	}
	text := sectionByName(t, "code-host-safety").Build(promptContext{})
	for _, must := range []string{"additional paths explicitly authorized in this prompt", "signed-in user's chat history for requested history lookups", "read_write attached folders only through guarded file tools", "Never inspect unlisted server folders or other people's projects"} {
		if !strings.Contains(text, must) {
			t.Fatalf("missing authorization boundary %q", must)
		}
	}
}

// Full CLI chats are told to use their own subagents and to keep
// run_in_background for read-only reviewers and long supervision loops;
// bridge-only chats, which have no subagents, are not.
func TestNativeSubagentsGuidanceOnlyWithNativeTools(t *testing.T) {
	section := sectionByName(t, "native-subagents")
	if !section.Applies(promptContext{Provider: "claude-code", NativeCodingTools: true}) {
		t.Fatal("a Full CLI chat must get the subagent guidance")
	}
	if section.Applies(promptContext{Provider: "claude-code", NativeCodingTools: false}) {
		t.Fatal("a bridge-only chat has no subagents of its own")
	}
	text := section.Build(promptContext{})
	for _, want := range []string{"own subagents", "execute_step", "run_full_workflow", "yourself", "not to write"} {
		if !strings.Contains(text, want) {
			t.Errorf("guidance misses %q: %s", want, text)
		}
	}
}

func TestClarificationGuidanceOnlyAdvertisesRegisteredTool(t *testing.T) {
	section := sectionByName(t, "clarification")
	if section.Applies(promptContext{}) {
		t.Fatal("unattended or unregistered tool must not be advertised")
	}
	ctx := promptContext{ClarificationAvailable: true}
	if !section.Applies(ctx) || !strings.Contains(section.Build(ctx), "request_clarification") {
		t.Fatal("attended chat is missing selectable question guidance")
	}
	if strings.Contains(section.Build(ctx), "AskUserQuestion") {
		t.Fatal("native Claude tool advertised without its answer hook")
	}
	ctx.NativeClaudeQuestionsAvailable = true
	if !strings.Contains(section.Build(ctx), "use AskUserQuestion") {
		t.Fatal("native Claude clarification guidance missing")
	}
}
