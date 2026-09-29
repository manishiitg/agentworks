package agentprofiles

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestResolveFeaturesProjectsOneBundleIntoExistingProfileFields(t *testing.T) {
	profile := Profile{
		ToolPolicy: ToolPolicy{Mode: ToolPolicyModeAllowlist},
		Features:   []FeatureBinding{{ID: "dashboard"}, {ID: "browser"}},
	}
	if err := ResolveFeatures(&profile); err != nil {
		t.Fatal(err)
	}
	// dashboard pulls its database and files dependencies in before itself.
	wantOrder := []string{"database", "files", "dashboard", "browser"}
	if len(profile.ResolvedFeatures) != len(wantOrder) {
		t.Fatalf("resolved features = %+v", profile.ResolvedFeatures)
	}
	for i, want := range wantOrder {
		if profile.ResolvedFeatures[i].ID != want {
			t.Fatalf("resolved feature %d = %q, want %q", i, profile.ResolvedFeatures[i].ID, want)
		}
	}
	for _, tool := range []string{"query_workflow_db", "diff_patch_workspace_file", "validate_report_html", "agent_browser"} {
		if !containsString(profile.ToolPolicy.Enabled, tool) {
			t.Fatalf("feature projection omitted tool %q: %v", tool, profile.ToolPolicy.Enabled)
		}
	}
	if !containsString(profile.Skills, "work-dashboard") || !containsString(profile.Skills, "ui-ux-pro-max") || !containsString(profile.Skills, "agent-browser") {
		t.Fatalf("feature projection omitted skills: %v", profile.Skills)
	}
	if !profile.UIPanels.Files || profile.Runtime.Capabilities.Browser != CapabilityPreferred {
		t.Fatalf("feature projection omitted legacy fields: panels=%+v caps=%+v", profile.UIPanels, profile.Runtime.Capabilities)
	}
	if got := strings.Join(FeaturePromptExtensions(profile), "\n"); !strings.Contains(got, "Feature: dashboard") || !strings.Contains(got, "Feature: browser") ||
		!strings.Contains(got, "attached `work-dashboard` skill") || !strings.Contains(got, "attached `ui-ux-pro-max` skill") || !strings.Contains(got, "attached `agent-browser` skill") {
		t.Fatalf("prompt extensions = %q", got)
	}

	beforeTools, beforeSkills := strings.Join(profile.ToolPolicy.Enabled, ","), strings.Join(profile.Skills, ",")
	if err := ResolveFeatures(&profile); err != nil {
		t.Fatal(err)
	}
	if strings.Join(profile.ToolPolicy.Enabled, ",") != beforeTools || strings.Join(profile.Skills, ",") != beforeSkills {
		t.Fatal("feature resolution must be idempotent")
	}
}

func TestLoadProductManifestAcceptsScalarAndConfiguredFeatures(t *testing.T) {
	files := fstest.MapFS{
		"product.yaml": {Data: []byte(`schema_version: 2
dependencies: {}
prompt: {file: prompt.md}
profile:
  id: test-product
  name: Test
  version: 1
  features:
    - files
    - id: schedules
      options: {mode: message_only}
  tool_policy: {mode: allowlist}
  runtime: {transport: auto}
  built_in: true
`)},
		"prompt.md": {Data: []byte("Base product prompt")},
	}
	manifest, err := LoadProductManifest(files, "product.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Profile.ResolvedFeatures) != 2 || manifest.Profile.ResolvedFeatures[1].Options["mode"] != "message_only" {
		t.Fatalf("resolved features = %+v", manifest.Profile.ResolvedFeatures)
	}
	if !containsString(manifest.Profile.ToolPolicy.Enabled, "create_project_schedule") {
		t.Fatalf("schedule tools were not projected: %v", manifest.Profile.ToolPolicy.Enabled)
	}
}

func TestResolveFeaturesRejectsUnknownDuplicateAndDisabledDependency(t *testing.T) {
	disabled := false
	cases := []Profile{
		{Features: []FeatureBinding{{ID: "not-real"}}},
		{Features: []FeatureBinding{{ID: "files"}, {ID: "files"}}},
		{Features: []FeatureBinding{{ID: "files", Enabled: &disabled}, {ID: "dashboard"}}},
	}
	for i := range cases {
		if err := ResolveFeatures(&cases[i]); err == nil {
			t.Fatalf("case %d should fail", i)
		}
	}
}

func TestTriggersReuseSchedulesAndAddProductTools(t *testing.T) {
	profile := Profile{ToolPolicy: ToolPolicy{Mode: ToolPolicyModeAllowlist}, Features: []FeatureBinding{{ID: "triggers"}}}
	if err := ResolveFeatures(&profile); err != nil {
		t.Fatal(err)
	}
	if len(profile.ResolvedFeatures) != 2 || profile.ResolvedFeatures[0].ID != "schedules" || profile.ResolvedFeatures[1].ID != "triggers" {
		t.Fatalf("resolved features = %+v", profile.ResolvedFeatures)
	}
	for _, tool := range []string{"create_project_schedule", "create_project_trigger", "delete_project_trigger"} {
		if !containsString(profile.ToolPolicy.Enabled, tool) {
			t.Fatalf("missing %s in %v", tool, profile.ToolPolicy.Enabled)
		}
	}
}

func TestMemoryFeatureExposesProjectMemoryPanel(t *testing.T) {
	profile := Profile{Features: []FeatureBinding{{ID: "memory"}}}
	if err := ResolveFeatures(&profile); err != nil {
		t.Fatal(err)
	}
	if len(profile.ResolvedFeatures) != 1 || profile.ResolvedFeatures[0].ID != "memory" {
		t.Fatalf("resolved features = %+v", profile.ResolvedFeatures)
	}
	if !containsString(profile.ResolvedFeatures[0].UIPanels, "memory") {
		t.Fatalf("memory panel missing from %+v", profile.ResolvedFeatures[0].UIPanels)
	}
	if got := strings.Join(FeaturePromptExtensions(profile), "\n"); !strings.Contains(got, "project-root `MEMORY.md`") || !strings.Contains(got, "dated-entry template") || !strings.Contains(got, "custom skills") {
		t.Fatalf("memory prompt extension = %q", got)
	}
}

func TestWorkflowReferencesProjectScopedCrewInvocationTools(t *testing.T) {
	profile := Profile{ToolPolicy: ToolPolicy{Mode: ToolPolicyModeAllowlist}, Features: []FeatureBinding{{ID: "workflow-references"}}}
	if err := ResolveFeatures(&profile); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"list_accessible_workflows", "attach_workflow_reference", "list_attached_workflows", "list_workflow_triggers", "run_workflow_trigger", "get_workflow_trigger_run"} {
		if !containsString(profile.ToolPolicy.Enabled, tool) {
			t.Fatalf("workflow-references feature omitted %s: %v", tool, profile.ToolPolicy.Enabled)
		}
	}
	if got := strings.Join(FeaturePromptExtensions(profile), "\n"); !strings.Contains(got, "Crew-scoped secretless internal trigger") || !strings.Contains(got, "public webhook triggers") {
		t.Fatalf("workflow-reference guidance does not preserve the invocation boundary: %q", got)
	}
}

func TestBotsProjectSharedGmailTools(t *testing.T) {
	profile := Profile{ToolPolicy: ToolPolicy{Mode: ToolPolicyModeAllowlist}, Features: []FeatureBinding{{ID: "bots"}}}
	if err := ResolveFeatures(&profile); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"google_workspace_cli", "list_gmail_connections", "update_gmail_connection_grants"} {
		if !containsString(profile.ToolPolicy.Enabled, tool) {
			t.Fatalf("bots feature omitted %s: %v", tool, profile.ToolPolicy.Enabled)
		}
	}
	for _, skill := range []string{"work-schedules-and-bots"} {
		if !containsString(profile.Skills, skill) {
			t.Fatalf("bots feature omitted operating skill %q: %v", skill, profile.Skills)
		}
	}
	if got := strings.Join(FeaturePromptExtensions(profile), "\n"); !strings.Contains(got, "install_skill") || !strings.Contains(got, "https://github.com/openclaw/gogcli") {
		t.Fatalf("bots feature does not tell the agent how to install versioned gog guidance on demand: %q", got)
	}
}

func TestFeatureOptionsNarrowToolsForOutboundAndDirectMessageOnly(t *testing.T) {
	profile := Profile{ToolPolicy: ToolPolicy{Mode: ToolPolicyModeAllowlist}, Features: []FeatureBinding{
		{ID: "workflow-references", Options: map[string]string{"direction": "outbound"}},
		{ID: "bots", Options: map[string]string{"channels": "slack,whatsapp", "dm_only": "true"}},
	}}
	if err := ResolveFeatures(&profile); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"list_functions", "call_function", "get_function_call", "reply_function_call", "ask_function_update", "list_accessible_workflows", "run_workflow_trigger", "get_slack_bot_settings"} {
		if !containsString(profile.ToolPolicy.Enabled, tool) {
			t.Fatalf("narrowed features dropped caller tool %s: %v", tool, profile.ToolPolicy.Enabled)
		}
	}
	for _, tool := range []string{"define_function", "delete_function", "report_function_progress", "return_function_result", "google_workspace_cli", "list_gmail_connections", "create_slack_bot_route", "send_slack_message", "slack"} {
		if containsString(profile.ToolPolicy.Enabled, tool) {
			t.Fatalf("narrowed features kept %s: %v", tool, profile.ToolPolicy.Enabled)
		}
	}
	got := strings.Join(FeaturePromptExtensions(profile), "\n")
	if strings.Contains(got, "Gmail/Google Workspace accounts are enabled") || strings.Contains(got, "define_function") || !strings.Contains(got, "outbound only") {
		t.Fatalf("prompt extensions still describe removed tools: %q", got)
	}
	if FeatureOption(profile, "bots", "dm_only") != "true" {
		t.Fatalf("FeatureOption lost dm_only")
	}
	bad := Profile{Features: []FeatureBinding{{ID: "workflow-references", Options: map[string]string{"direction": "inbound"}}}}
	if err := ResolveFeatures(&bad); err == nil {
		t.Fatal("invalid direction accepted")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestBotsFeatureControlsSlackCredentialTools(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		profile := Profile{ToolPolicy: ToolPolicy{Mode: ToolPolicyModeAllowlist}}
		if enabled {
			profile.Features = []FeatureBinding{{ID: "bots"}}
		}
		if err := ResolveFeatures(&profile); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"configure_slack_bot", "get_slack_bot_credentials", "test_slack_bot_connection", "create_slack_bot_route"} {
			if containsString(profile.ToolPolicy.Enabled, name) != enabled {
				t.Fatalf("%s admission does not follow bots feature", name)
			}
		}
	}
}

// The MCP feature's scope option: personal drops every platform-wide tool and
// the shared skill and rewrites the guidance; shared (the default, a Crew's)
// keeps them all; anything else is refused.
func TestMCPFeatureScope(t *testing.T) {
	resolve := func(scope string) (Profile, error) {
		p := Profile{ID: "work", Name: "Crew", Features: []FeatureBinding{{ID: "mcp", Options: map[string]string{"scope": scope}}}}
		return p, ResolveFeatures(&p)
	}
	shared, err := resolve("")
	if err != nil || len(shared.ResolvedFeatures) == 0 || len(shared.ResolvedFeatures[0].Tools) == 0 || len(shared.ResolvedFeatures[0].Skills) == 0 {
		t.Fatalf("shared MCP lost its tools or skill: %+v %v", shared.ResolvedFeatures, err)
	}
	personal, err := resolve("personal")
	if err != nil {
		t.Fatal(err)
	}
	feature := personal.ResolvedFeatures[0]
	// The platform-wide tools go; the skill stays (the product supplies the
	// text for its own kind of connection) and the guidance names the tool.
	if len(feature.Tools) != 0 || len(feature.Skills) != 1 || !strings.Contains(feature.PromptExtension, "manage_my_mcp_servers") {
		t.Fatalf("personal MCP = %+v", feature)
	}
	if _, err := resolve("everyone"); err == nil {
		t.Fatal("an unknown scope was accepted")
	}
}
