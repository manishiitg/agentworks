package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestWorkflowEmailRequiresWorkflowAuthority(t *testing.T) {
	for _, profile := range []string{"work", "code", "relays", "example-app", "example-app-child", "example-product"} {
		t.Run(profile, func(t *testing.T) {
			gate := newProductToolGate(profileWithPolicy(profile, agentprofiles.ToolPolicy{Mode: agentprofiles.ToolPolicyModeAllowlist, Enabled: []string{"send_email", "read_skill"}}))
			gate.AllowWorkflowNotifications(true) // A shared runner cannot grant a product this tool.
			gate.Declare("send_email")            // A factory declaration cannot bypass the boundary either.
			if gate.Allows("send_email") || gate.Admit("send_email") || !gate.Admit("read_skill") {
				t.Fatal("workflow-only tool admitted or unrelated tool removed")
			}
		})
	}
	gate := newProductToolGate(nil)
	if gate.Admit("send_email") {
		t.Fatal("ordinary chat admitted send_email")
	}
	gate.AllowWorkflowNotifications(true)
	if !gate.Admit("send_email") {
		t.Fatal("workflow cannot notify its configured recipients")
	}
	gate.DenyWhere(func(name string) bool { return name == "send_email" })
	if gate.Admit("send_email") {
		t.Fatal("workflow notification capability bypassed an existing authority boundary")
	}
}

func TestRelayRunnerHasNoNotificationDefinitionOrExecutor(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		tools, executors, categories := createCustomTools(true, "owner", "scope-test")
		tools = restrictWorkflowNotificationTools(tools, executors, categories, enabled)
		found := false
		for _, tool := range tools {
			if tool.Function != nil && tool.Function.Name == "send_email" {
				found = true
			}
		}
		_, executable := executors["send_email"]
		_, categorized := categories["send_email"]
		if found != enabled || executable != enabled || categorized != enabled {
			t.Fatalf("workflow=%v: definition=%v executor=%v category=%v", enabled, found, executable, categorized)
		}
		if _, ok := executors["human_feedback"]; !ok {
			t.Fatal("notification restriction removed unrelated human tools")
		}
	}
}

func TestWorkflowNotificationScopeUsesSavedKind(t *testing.T) {
	_, _, workspace := gmailConnectionFixture(t)
	if !workflowNotificationsForPath("Workflow/mail") {
		t.Fatal("legacy workflow lost notifications")
	}
	workspace.files["Workflow/mail/workflow.json"] = `{"schema_version":1,"kind":"relay","id":"relay","label":"Relay"}`
	if workflowNotificationsForPath("Workflow/mail") || workflowNotificationsForPath("") || workflowNotificationsForPath("Workflow/missing") {
		t.Fatal("Relay or missing manifest gained workflow notifications")
	}
	for _, product := range []string{"work", "code", "example-app"} {
		workspace.files["Workflow/mail/workflow.json"] = `{"product":"` + product + `","id":"project"}`
		if workflowNotificationsForPath("Workflow/mail") {
			t.Fatalf("%s project gained notifications through the shared runner", product)
		}
	}
}
