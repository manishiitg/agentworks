package workproduct

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

func TestWorkManifestDeclaresProjectScopeAndCodingAllowlist(t *testing.T) {
	manifest, err := WorkManifest()
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Profile.ID != "work" {
		t.Fatalf("unexpected profile id: %q", manifest.Profile.ID)
	}
	if len(manifest.Dependencies.Skills) != 0 {
		t.Fatalf("Crew must not install external skills merely because a project opens: %+v", manifest.Dependencies.Skills)
	}
	// Project, not global -- same reasoning as fixed-workspace products: global
	// scope makes provider_options non-authoritative and skips this
	// profile's own prompt.file in favor of the dynamic delegation
	// prompt. Crew's prompt (plain coding work, no workflow vocabulary)
	// must actually reach the model.
	if manifest.Profile.Scope != agentprofiles.ProfileScopeProject {
		t.Fatalf("work must declare scope: project, got %q", manifest.Profile.Scope)
	}
	for _, want := range []string{"agent-browser", "panel-access", "panel-dashboard", "panel-data", "panel-automations", "panel-dashboard", "work-ui-control", "background-work"} {
		if !contains(manifest.Profile.Skills, want) {
			t.Fatalf("work feature bundles omitted skill %q: %v", want, manifest.Profile.Skills)
		}
	}
	if contains(manifest.Profile.Skills, "code-reviewer") {
		t.Fatal("Crew must not automatically attach code-reviewer")
	}
	if len(manifest.Profile.ResolvedFeatures) == 0 {
		t.Fatal("Crew must expose resolved feature bundles to the shared product UI")
	}
	for _, name := range manifest.Profile.Skills {
		if name == "skill-creator" {
			t.Fatal("skill-creator must be selected on demand; attaching it to every Crew chat replaces Crew's general-purpose identity with Skill Builder mode")
		}
	}
	// Runtime choice reuses AgentWorks' complete coding-agent provider set and
	// maps each choice to its globally installed CLI.
	if manifest.Profile.Runtime.Provider != "claude-code" || manifest.Profile.Runtime.ModelID != "claude-sonnet-5-5" {
		t.Fatalf("work must report its default provider/model to the shared composer, got provider=%q model_id=%q", manifest.Profile.Runtime.Provider, manifest.Profile.Runtime.ModelID)
	}
	options := manifest.Profile.Runtime.ProviderOptions
	wantProviders := []string{"claude-code", "codex-cli", "cursor-cli", "pi-cli", "muse-cli", "agy-cli"}
	if len(options) != len(wantProviders) {
		t.Fatalf("work runtime options = %+v, want all AgentWorks coding CLIs", options)
	}
	for i, want := range wantProviders {
		if options[i].Provider != want {
			t.Fatalf("work runtime option %d provider = %q, want %q", i, options[i].Provider, want)
		}
		if effort, ok := options[i].Options["reasoning_effort"].(string); ok && effort != "" {
			offered := false
			for _, candidate := range options[i].ReasoningEfforts {
				if candidate == effort {
					offered = true
					break
				}
			}
			if !offered {
				t.Fatalf("work runtime option %q defaults to reasoning effort %q outside its offered levels %v", want, effort, options[i].ReasoningEfforts)
			}
		}
	}
	if options[len(options)-1].Options["reasoning_effort"] != "high" {
		t.Fatalf("AGY must declare its high effort default: %+v", options[len(options)-1])
	}
	if !options[0].Default {
		t.Fatal("Claude Code must remain Crew's default coding CLI")
	}
	// Crew uses the shared AgentWorks execution policy instead of defining a
	// product-specific transport policy.
	if manifest.Profile.Runtime.Transport != "auto" {
		t.Fatalf("work must use the shared runtime transport policy, got transport=%q", manifest.Profile.Runtime.Transport)
	}
	if manifest.Profile.Runtime.Workspace.ProjectsRoot != ProjectsRoot {
		t.Fatalf("work projects root = %q, want %q", manifest.Profile.Runtime.Workspace.ProjectsRoot, ProjectsRoot)
	}
	if manifest.Profile.Runtime.Workspace.Mode != agentprofiles.WorkspaceModeProject || manifest.Profile.Runtime.Conversation.Mode != agentprofiles.ConversationModeKeyed {
		t.Fatalf("work must use keyed session conversations: workspace=%+v conversation=%+v", manifest.Profile.Runtime.Workspace, manifest.Profile.Runtime.Conversation)
	}
	if !strings.EqualFold(manifest.Profile.Runtime.AgentTools.Mode, "mcp_only") {
		t.Fatalf("work must route coding operations through governed MCP tools, got agent_tools.mode=%q", manifest.Profile.Runtime.AgentTools.Mode)
	}
	if !manifest.Profile.Runtime.Sandbox.IsStrict() || !manifest.Profile.Runtime.Sandbox.ChatHistoryDenied() {
		t.Fatalf("work MCP tools must stay strict and must not see the account-wide chat_history/ (it indexes other products' chats): %+v", manifest.Profile.Runtime.Sandbox)
	}
	if !manifest.Profile.ToolPolicy.IsAllowlist() {
		t.Fatal("work must declare tool_policy.mode: allowlist -- fail-open would silently reach workflow/schedule/pulse tools")
	}
	// Pin the product boundary rather than duplicate every shared feature's
	// tool list: Crew keeps project operations, while Goal planning/execution
	// stays with the workflow product.
	for _, name := range []string{"read_project_files", "execute_shell_command", "read_image", "browser_connections", "manage_crew_model", "list_project_schedules", "create_project_schedule", "manage_condition_triggers", "project_functions_call"} {
		if !contains(manifest.Profile.ToolPolicy.Enabled, name) {
			t.Fatalf("Crew lost project tool %q", name)
		}
	}
	for _, name := range []string{"run_full_workflow", "execute_step", "workflow_team", "manage_workflow_schedule", "update_plan", "create_human_input_request"} {
		if contains(manifest.Profile.ToolPolicy.Enabled, name) {
			t.Fatalf("Crew admitted Goal planning/execution tool %q", name)
		}
	}
	bindings := map[string]agentprofiles.ToolBinding{}
	for _, binding := range manifest.Profile.Tools {
		bindings[binding.ID] = binding
	}
	for id, kind := range map[string]string{"media.show-video": "media.video", "media.show-character": "media.character", "media.show-reference": "media.reference", "media.show-document": "document.markdown"} {
		if binding := bindings[id]; binding.Presentation == nil || binding.Presentation.Kind != kind {
			t.Fatalf("Crew media binding %s must present %s, got %+v", id, kind, binding)
		}
	}
	if _, ok := bindings["work.custom-commands"]; !ok {
		t.Fatal("Crew lost custom commands")
	}
	createInteraction := bindings["work.create-project"].Interaction
	if createInteraction == nil || createInteraction.Kind != "project_created" || createInteraction.Render != "product.refresh" {
		t.Fatalf("work project creation interaction must refresh the product, got %+v", createInteraction)
	}
	identityInteraction := bindings["work.set-identity"].Interaction
	if identityInteraction == nil || identityInteraction.Kind != "identity_updated" || identityInteraction.Render != "product.refresh" {
		t.Fatalf("work identity interaction must be declared in product.yaml, got %+v", identityInteraction)
	}
	if !manifest.Profile.UIPanels.Schedules {
		t.Fatal("Crew must expose its project schedule panel")
	}
	caps := manifest.Profile.Runtime.Capabilities
	if caps.WorkflowExecution != agentprofiles.CapabilityDisabled {
		t.Fatalf("work must declare capabilities.workflow_execution: disabled, got %q", caps.WorkflowExecution)
	}
	if caps.RawTerminal == agentprofiles.CapabilityDisabled {
		t.Fatalf("work must expose the selected coding CLI terminal, got %q", caps.RawTerminal)
	}
	if caps.MCPSelection == agentprofiles.CapabilityDisabled || caps.SkillSelection == agentprofiles.CapabilityDisabled {
		t.Fatalf("work must expose shared MCP and skill selection, got %+v", caps)
	}
	if caps.WorkflowReferences == agentprofiles.CapabilityDisabled {
		t.Fatalf("work must expose shared read-only workflow references, got %+v", caps)
	}
	if manifest.UI.Surface != "work" {
		t.Fatalf("unexpected ui.surface: %q", manifest.UI.Surface)
	}
	if len(manifest.Workflows.Enabled) != 0 {
		t.Fatalf("work has no fixed pipelines; workflows.enabled should stay empty: %v", manifest.Workflows.Enabled)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestWorkPlatformSkillsRegisterAndLoad(t *testing.T) {
	if err := RegisterProductSkills(); err != nil {
		t.Fatalf("RegisterProductSkills: %v", err)
	}
	manifest, err := WorkManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range manifest.Profile.Skills {
		attached := skills.LoadAttachable("", []string{name})
		if len(attached) != 1 || strings.TrimSpace(attached[0].Content) == "" {
			t.Fatalf("feature skill %s has no readable source", name)
		}
	}
	for _, mode := range []string{"builder", "run"} {
		if !contains(manifest.Chat[mode].Skills, "chat-visuals") {
			t.Fatalf("%s chat cannot discover the presentation skill", mode)
		}
	}
	visuals := skills.LoadAttachable("", []string{"chat-visuals"})
	if len(visuals) != 1 || len(visuals[0].SupportingFiles) != 1 || visuals[0].SupportingFiles[0].RelPath != "references/formats.md" {
		t.Fatal("native visual formats must be readable through the existing skill resolver")
	}
	dashboard := skills.LoadAttachable("", []string{"panel-dashboard"})[0]
	sources := map[string]bool{}
	for _, file := range dashboard.SupportingFiles {
		sources[file.RelPath] = len(file.Content) > 0
	}
	if !sources["references/dashboard/references/authoring.md"] || !sources["references/dashboard/references/motion-guide.md"] {
		t.Fatal("shared dashboard authoring and motion sources must be available through read_skill")
	}
}

// Specialist methods remain ordinary skills after the separate template catalog is removed.
func TestCrewDomainSkillReferences(t *testing.T) {
	if err := RegisterProductSkills(); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, name := range []string{"finance-work", "customer-work", "growth-work", "sales-work", "team-work", "software-work", "store-work"} {
		loaded := skills.LoadAttachable("", []string{name})
		if !skills.IsBuiltinSkill(name) || len(loaded) != 1 {
			t.Fatalf("missing builtin %s", name)
		}
		for _, file := range loaded[0].SupportingFiles {
			if !strings.HasPrefix(file.RelPath, "references/") || strings.TrimSpace(string(file.Content)) == "" {
				t.Fatalf("invalid procedure %s/%s", name, file.RelPath)
			}
			if !strings.Contains(loaded[0].Content, "("+file.RelPath+")") {
				t.Fatalf("unindexed procedure %s/%s", name, file.RelPath)
			}
			if strings.Contains(string(file.Content), "TEMPLATE_SETUP") || strings.Contains(string(file.Content), "completed_steps") {
				t.Fatalf("retired setup dependency in %s/%s", name, file.RelPath)
			}
			count++
		}
	}
	if count != 79 {
		t.Fatalf("retained %d specialist procedures, want 79", count)
	}
}

func TestBuiltinAgentProfileValidatesAndResolvesProjectScope(t *testing.T) {
	profile := BuiltinAgentProfile()
	if err := agentprofiles.Validate(profile); err != nil {
		t.Fatalf("built-in profile failed validation: %v", err)
	}
	if profile.EffectiveScope() != agentprofiles.ProfileScopeProject {
		t.Fatalf("EffectiveScope() = %q, want project", profile.EffectiveScope())
	}
	if profile.SystemPromptTemplate == "" {
		t.Fatal("expected a non-empty rendered system prompt template")
	}
	if !profile.BuiltIn {
		t.Fatal("expected BuiltIn to be true")
	}
}

func TestBuiltinAgentProfilesReturnsExactlyOneVersion(t *testing.T) {
	profiles := BuiltinAgentProfiles()
	if len(profiles) != 1 {
		t.Fatalf("expected exactly one built-in profile, got %d", len(profiles))
	}
	if profiles[0].Version != 3 {
		t.Fatalf("expected version 3, got %d", profiles[0].Version)
	}
}

func TestWorkManifestShipsUpdateMemoryCommand(t *testing.T) {
	manifest, err := WorkManifest()
	if err != nil {
		t.Fatal(err)
	}
	prompt := ""
	for _, command := range manifest.Profile.Commands {
		if command.Name == "update-memory" {
			prompt = command.Prompt
		}
	}
	if strings.TrimSpace(prompt) == "" {
		t.Fatal("Crew product manifest must ship an update-memory slash command with a resolved prompt")
	}
	if !strings.Contains(prompt, "{{context}}") {
		t.Fatal("update-memory prompt must carry the {{context}} placeholder for slash-prefixed text")
	}
	if !strings.Contains(prompt, "MEMORY.md") || !strings.Contains(prompt, "skills/") {
		t.Fatal("update-memory prompt must cover both project memory and project skills")
	}
	profile := BuiltinAgentProfile()
	served := false
	for _, command := range profile.Commands {
		if command.Name == "update-memory" && strings.TrimSpace(command.Prompt) != "" {
			served = true
		}
	}
	if !served {
		t.Fatal("update-memory command must reach the served agent profile")
	}
}

func TestCrewShipsDailyLearningsCommand(t *testing.T) {
	var prompt string
	for _, command := range BuiltinAgentProfile().Commands {
		if command.Name == "daily-learnings" {
			prompt = command.Prompt
		}
	}
	if strings.TrimSpace(prompt) == "" {
		t.Fatal("Crew must serve a daily-learnings slash command with a resolved prompt")
	}
	for _, want := range []string{"{{context}}", "builder/conversation/", "builder/crew-chats/users/", "MEMORY.md", "skills/", "update_project_skill_selection"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("daily-learnings prompt must mention %q", want)
		}
	}
}

func TestRenderPromptSucceedsAgainstAPromptContext(t *testing.T) {
	profile := BuiltinAgentProfile()
	rendered, err := agentprofiles.RenderPrompt(profile, agentprofiles.PromptContext{
		ProjectTitle:  "Crew",
		LocalDateTime: "Monday, 1 January 2026 at 9:00 AM UTC",
	})
	if err != nil {
		t.Fatalf("RenderPrompt failed: %v", err)
	}
	if rendered == "" {
		t.Fatal("expected non-empty rendered prompt")
	}
	// Rules that keep work correct when the prompt is short: where standing
	// rules and durable work go, the managed database, read-only # workflows,
	// folder grants and secret handling.
	for _, required := range []string{
		"PROJECT_INSTRUCTIONS.md",
		"never to AGENTS.md",
		"never /tmp",
		"WORK_FOLDER_<ALIAS>",
		"never open `db.sqlite`",
		"selected with `#` is reference only",
		"confirm it by name only",
		"[AGENTWORKS SESSION]",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered Crew prompt is missing %q", required)
		}
	}
}

func TestRegisterAgentProfileRuntimeRegistersCrewTools(t *testing.T) {
	registry := agentprofiles.NewRegistry()
	if err := RegisterAgentProfileRuntime(registry, "http://127.0.0.1:0"); err != nil {
		t.Fatalf("RegisterAgentProfileRuntime failed: %v", err)
	}
	tool, err := registry.BuildTool(agentprofiles.ToolBinding{ID: "work.set-identity"}, agentprofiles.ToolRuntimeContext{
		UserID: "user-1", SessionID: "session-1", WorkspacePath: "Chats/Work/projects/demo",
	})
	if err != nil {
		t.Fatalf("BuildTool failed: %v", err)
	}
	if tool.Name != "set_work_identity" || tool.Execute == nil {
		t.Fatalf("unexpected identity tool: %+v", tool)
	}
	createTool, err := registry.BuildTool(agentprofiles.ToolBinding{ID: "work.create-project"}, agentprofiles.ToolRuntimeContext{
		UserID: "user-1", SessionID: "session-1", WorkspacePath: "Chats/Work/projects/demo",
	})
	if err != nil {
		t.Fatalf("BuildTool(create Crew) failed: %v", err)
	}
	if createTool.Name != "create_crew" || createTool.Execute == nil {
		t.Fatalf("unexpected create Crew tool: %+v", createTool)
	}
}

func TestWorkPromptIncludesConfiguredIdentity(t *testing.T) {
	profile := BuiltinAgentProfile()
	rendered, err := agentprofiles.RenderPrompt(profile, agentprofiles.PromptContext{
		ProjectTitle: "Demo",
		Product:      map[string]string{"WORK_IDENTITY": "Name: Nova"},
	})
	if err != nil {
		t.Fatalf("render Crew prompt: %v", err)
	}
	for _, required := range []string{"Follow this saved identity", "Name: Nova"} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered Crew prompt is missing %q", required)
		}
	}
}
