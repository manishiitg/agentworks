package relayproduct

import (
	"bytes"
	"strings"
	"testing"
	"text/template"
)

func TestBuilderPromptLoadsProductManifest(t *testing.T) {
	prompt, err := BuilderPrompt()
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"planning/plan.json", "authored_prompt", "script_only", "value_path"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("Relay product prompt is missing %q", required)
		}
	}
}

func TestBuilderPromptRendersRelayVariableExamples(t *testing.T) {
	prompt, err := BuilderPrompt()
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := template.New("relay-builder").Parse(prompt)
	if err != nil {
		t.Fatal(err)
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, nil); err != nil {
		t.Fatal(err)
	}
	for _, variable := range []string{"{{input}}", "{{input.field}}", "{{steps.id.output.field}}"} {
		if !strings.Contains(rendered.String(), variable) {
			t.Fatalf("rendered Relay prompt is missing %q", variable)
		}
	}
}

func TestBuilderSurfaceIsRelaySpecific(t *testing.T) {
	tools, err := BuilderTools()
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) == 0 || !BuilderAllowsTool("add_step") || !BuilderAllowsTool("manage_workflow_webhook") || !BuilderAllowsTool("google_workspace_cli") || !BuilderAllowsTool("list_gmail_connections") {
		t.Fatal("Relay Builder is missing graph or trigger tools")
	}
	for _, excluded := range []string{"slack", "send_slack_message", "create_slack_bot_route", "configure_slack_bot", "manage_group", "create_human_input_request", "notify_user"} {
		if BuilderAllowsTool(excluded) {
			t.Errorf("Relay Builder admits %s", excluded)
		}
	}
	skill, err := BuilderSkill()
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "relay-builder" || !strings.Contains(skill.Content, "authored_prompt") {
		t.Fatal("Relay Builder skill is missing its graph contract")
	}
	if key, err := BuilderDefinitionKey(); err != nil || key == "" {
		t.Fatalf("Relay Builder definition key: %q, %v", key, err)
	}
}

func TestRelayCommandCatalog(t *testing.T) {
	profiles, err := BuiltinAgentProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].ID != "relays" {
		t.Fatal("wrong Relay catalog")
	}
	commands := map[string]string{}
	for _, command := range profiles[0].Commands {
		if command.Name == "" || !strings.Contains(command.Prompt, "{{context}}") {
			t.Fatalf("invalid command: %+v", command)
		}
		commands[command.Name] = command.Prompt
	}
	for _, name := range []string{"design-graph", "test", "publish", "versions", "setup-api", "schedule"} {
		if commands[name] == "" {
			t.Errorf("missing Relay command %s", name)
		}
	}
	if !strings.Contains(commands["publish"], "publish_relay") {
		t.Fatal("publishes the wrong artifact")
	}
	for _, name := range []string{"design-dashboard", "setup-goals", "run-goal-work", "pulse", "backup"} {
		if commands[name] != "" {
			t.Errorf("workflow command leaked: %s", name)
		}
	}
}

func TestRelayMCPAdmissionIsOwnedByBuilderManifest(t *testing.T) {
	names, err := BuilderExternalTools()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			t.Fatalf("duplicate %s", name)
		}
		seen[name] = true
	}
	for _, name := range []string{"create_relay", "builder_chat", "test_relay", "publish_relay", "run_relay", "get_relay_run"} {
		if !seen[name] {
			t.Fatalf("missing %s", name)
		}
	}
	names[0] = "changed"
	again, _ := BuilderExternalTools()
	if again[0] == "changed" {
		t.Fatal("caller mutated manifest")
	}
	if _, err := ChatTools("run"); err == nil {
		t.Fatal("MCP added Run chat")
	}
}
