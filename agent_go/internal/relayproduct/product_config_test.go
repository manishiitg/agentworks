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
	if len(tools) == 0 || !BuilderAllowsTool("add_step") || !BuilderAllowsTool("manage_workflow_webhook") {
		t.Fatal("Relay Builder is missing graph or trigger tools")
	}
	for _, excluded := range []string{"create_slack_bot_route", "configure_slack_bot", "manage_group", "create_human_input_request"} {
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
