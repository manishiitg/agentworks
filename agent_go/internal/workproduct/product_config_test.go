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
	// Project, not global -- same reasoning as Finance/Dominion: global
	// scope makes provider_options non-authoritative and skips this
	// profile's own prompt.file in favor of the dynamic delegation
	// prompt. Crew's prompt (plain coding work, no workflow vocabulary)
	// must actually reach the model.
	if manifest.Profile.Scope != agentprofiles.ProfileScopeProject {
		t.Fatalf("work must declare scope: project, got %q", manifest.Profile.Scope)
	}
	for _, want := range []string{"agent-browser", "code-reviewer", "work-integrations", "work-workflow-files", "work-skills", "work-schedules-and-bots", "work-dashboard", "work-ui-control", "background-work"} {
		if !contains(manifest.Profile.Skills, want) {
			t.Fatalf("work feature bundles omitted skill %q: %v", want, manifest.Profile.Skills)
		}
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
	if manifest.Profile.Runtime.Workspace.ProjectsRoot != "Chats/Work/projects" {
		t.Fatalf("work projects root = %q, want Chats/Work/projects", manifest.Profile.Runtime.Workspace.ProjectsRoot)
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
	// Crew reuses platform coding tools plus its deliberately small,
	// project-scoped schedule surface. General workflow authoring stays out;
	// attached workflows have only the scoped internal-trigger run surface.
	wantEnabled := map[string]bool{
		"get_file_link":                          false,
		"get_report_link":                        false,
		"diff_patch_workspace_file":              false,
		"execute_shell_command":                  false,
		"agent_browser":                          false,
		"read_image":                             false,
		"list_secrets":                           false,
		"set_workflow_secret":                    false,
		"delete_workflow_secret":                 false,
		"manage_global_secret":                   false,
		"update_project_global_secret_selection": false,
		"list_skills":                            false,
		"search_skills":                          false,
		"install_skill":                          false,
		"import_skill":                           false,
		"uninstall_skill":                        false,
		"update_project_skill_selection":         false,
		"list_mcp_servers":                       false,
		"search_mcp_catalog":                     false,
		"install_mcp_server":                     false,
		"add_mcp_server":                         false,
		"remove_mcp_server":                      false,
		"get_mcp_server_logs":                    false,
		"trigger_mcp_discovery":                  false,
		"update_project_mcp_server_selection":    false,
		"list_work_folders":                      false,
		"attach_work_folder":                     false,
		"detach_work_folder":                     false,
		"list_accessible_workflows":              false,
		"attach_workflow_reference":              false,
		"detach_workflow_reference":              false,
		"list_attached_workflows":                false,
		"list_workflow_triggers":                 false,
		"run_workflow_trigger":                   false,
		"get_workflow_trigger_run":               false,
		"define_function":                        false,
		"delete_function":                        false,
		"list_functions":                         false,
		"call_function":                          false,
		"get_function_call":                      false,
		"reply_function_call":                    false,
		"ask_function_update":                    false,
		"report_function_progress":               false,
		"return_function_result":                 false,
		"list_project_schedules":                 false,
		"create_project_schedule":                false,
		"update_project_schedule":                false,
		"delete_project_schedule":                false,
		"trigger_project_schedule":               false,
		"list_project_triggers":                  false,
		"create_project_trigger":                 false,
		"update_project_trigger":                 false,
		"delete_project_trigger":                 false,
		"google_workspace_cli":                   false,

		"send_slack_message": false, "slack": false, "get_slack_bot_settings": false, "get_slack_bot_credentials": false, "configure_slack_bot": false, "test_slack_bot_connection": false, "create_slack_bot_route": false, "update_slack_bot_route_permission": false, "remove_slack_bot_route": false,
		"list_gmail_connections":            false,
		"get_gmail_trigger":                 false,
		"manage_gmail_trigger":              false,
		"setup_gmail_inbound":               false,
		"update_gmail_connection_grants":    false,
		"query_workflow_db":                 false,
		"mutate_workflow_db":                false,
		"apply_workflow_db_migration":       false,
		"create_workflow_database_snapshot": false,
		"validate_report_html":              false,
		"preview_report":                    false,
		"run_in_background":                 false,
		"query_agent":                       false,
		"list_agents":                       false,
		"terminate_agent":                   false,
		"list_ui_capabilities":              false,
		"get_ui_state":                      false,
		"perform_ui_action":                 false,
		"manage_vault_access":               false,
		"manage_my_vaults":                  false,
		"brain_browse":                      false,
		"brain_read":                        false,
		"brain_update":                      false,
		"brain_skills":                      false,
		"brain_access":                      false,
	}
	for _, name := range manifest.Profile.ToolPolicy.Enabled {
		if _, expected := wantEnabled[name]; !expected {
			t.Fatalf("unexpected tool in coding and Knowledge Base allowlist: %q", name)
		}
		wantEnabled[name] = true
	}
	for name, found := range wantEnabled {
		if !found {
			t.Fatalf("expected tool_policy.enabled to include %q", name)
		}
	}
	if len(manifest.Profile.Tools) != 3 || manifest.Profile.Tools[0].ID != "work.create-project" || manifest.Profile.Tools[1].ID != "work.set-identity" || manifest.Profile.Tools[2].ID != "work.custom-commands" {
		t.Fatalf("work must expose project creation, identity, and custom-command tools, got %+v", manifest.Profile.Tools)
	}
	createInteraction := manifest.Profile.Tools[0].Interaction
	if createInteraction == nil || createInteraction.Kind != "project_created" || createInteraction.Render != "product.refresh" {
		t.Fatalf("work project creation interaction must refresh the product, got %+v", createInteraction)
	}
	identityInteraction := manifest.Profile.Tools[1].Interaction
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
	checks := map[string][]string{
		"crew-builder":            {"## Coding rules", "create_crew", "builder/conversation/", "Crew should remember that", "When asked to do X, Crew should"},
		"work-mcp":                {"list_mcp_servers", "Integrations > Connections", "Vault", "trigger_mcp_discovery", "update_project_mcp_server_selection", "next user message"},
		"work-integrations":       {"set_workflow_secret", "available to shell", "do not ask the user to start", "manage_global_secret", "update_project_global_secret_selection", "selected_global_secret_names", "list_work_folders", "Setup > Models"},
		"work-workflow-files":     {"list_accessible_workflows", "WORK_FOLDER_<ALIAS>", "workflow.json", "knowledgebase/", "learnings/", "db/db.sqlite", "db/reports/", "runs/run_index.json", "sqlite3 -readonly", "get_file_link", "get_report_link", "same signed-in Crew account", "list_attached_workflows", "list_workflow_triggers", "run_workflow_trigger", "get_workflow_trigger_run", "delivery_id", "public webhook"},
		"work-skills":             {"list_skills", "search_skills", "update_project_skill_selection", "skills/<skill-name>/SKILL.md", "keep skills scoped to this workspace", "Crew should remember that", "When asked to do X, Crew should", "link them rather than duplicating", "same topic", "independently reusable topics", "catch-all", "150 lines or fewer", "references/", "scripts/", "skill authoring is a capability", "Setup > Skills"},
		"work-schedules-and-bots": {"list_project_schedules", "five-field cron", "list_project_triggers", "Project webhook triggers", "Setup > Bots", "Slack", "WhatsApp", "list_gmail_connections", "google_workspace_cli", "gmail.readonly"},
		"work-dashboard":          {"db/reports/index.html", "window.report.sendChatMessage", "query_workflow_db", "validate_report_html", "get_report_link"},
		"work-ui-control":         {"list_ui_capabilities", "perform_ui_action", "search_tools", "bridge tool routing", "`memory`", "`workshop`", "`identity`", "`mcp`", "Do not use Workflow-only views", "Scheduled, webhook, bot"},
		"background-work":         {"run_in_background", "[AUTO-NOTIFICATION]", "query_agent"},
	}
	for name, required := range checks {
		if !skills.IsBuiltinSkill(name) {
			t.Fatalf("%s did not register as a built-in Crew skill", name)
		}
		attached := skills.LoadAttachable("", []string{name})
		if len(attached) != 1 {
			t.Fatalf("LoadAttachable(%s) = %v, want one skill", name, attached)
		}
		for _, text := range required {
			if !strings.Contains(attached[0].Content, text) {
				t.Fatalf("%s skill is missing %q", name, text)
			}
		}
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
	for _, required := range []string{
		"general-purpose, chat-first agent",
		"Coding is a first-class capability",
		"not the only kind of work",
		"questions, research, analysis, writing, planning",
		"## Working contract",
		"relevant attached skill before using or configuring",
		"only backend-authorized tools grant access",
		"workflow selected with `#` is",
		"general-purpose",
		"Read the relevant attached skill",
		"Read `crew-builder`",
		"work-workflow-files",
		"## How to talk to the user",
		"runs a small business and is not technical",
		"Use business words, never platform words alone",
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
		Product:      map[string]string{"WORK_IDENTITY": "Name: Nova\nRole: Engineering partner"},
	})
	if err != nil {
		t.Fatalf("render Crew prompt: %v", err)
	}
	for _, required := range []string{"## Project agent identity", "changes behavior, never permissions", "Name: Nova", "Role: Engineering partner"} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered Crew prompt is missing %q", required)
		}
	}
}
