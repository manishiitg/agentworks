package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestNotifyUserRequiresWorkflowAuthority(t *testing.T) {
	for _, profile := range []string{"work", "code", "relays", "sparkquill", "sparkquill-child", "video-studio"} {
		t.Run(profile, func(t *testing.T) {
			gate := newProductToolGate(profileWithPolicy(profile, agentprofiles.ToolPolicy{Mode: agentprofiles.ToolPolicyModeAllowlist, Enabled: []string{"notify_user", "read_image"}}))
			gate.AllowWorkflowNotifications(true) // A shared runner cannot grant a product this tool.
			gate.Declare("notify_user")           // A factory declaration cannot bypass the boundary either.
			if gate.Allows("notify_user") || gate.Admit("notify_user") || !gate.Admit("read_image") {
				t.Fatal("workflow-only tool admitted or unrelated tool removed")
			}
		})
	}
	gate := newProductToolGate(nil)
	if gate.Admit("notify_user") {
		t.Fatal("ordinary chat admitted notify_user")
	}
	gate.AllowWorkflowNotifications(true)
	if !gate.Admit("notify_user") {
		t.Fatal("workflow cannot notify its configured recipients")
	}
	gate.DenyWhere(func(name string) bool { return name == "notify_user" })
	if gate.Admit("notify_user") {
		t.Fatal("workflow notification capability bypassed an existing authority boundary")
	}
}

func TestRelayRunnerHasNoNotificationDefinitionOrExecutor(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		tools, executors, categories := createCustomTools(true, "owner", "scope-test")
		tools = restrictWorkflowNotificationTools(tools, executors, categories, enabled)
		found := false
		for _, tool := range tools {
			if tool.Function != nil && tool.Function.Name == "notify_user" {
				found = true
			}
		}
		_, executable := executors["notify_user"]
		_, categorized := categories["notify_user"]
		if found != enabled || executable != enabled || categorized != enabled {
			t.Fatalf("workflow=%v: definition=%v executor=%v category=%v", enabled, found, executable, categorized)
		}
		if _, ok := executors["human_feedback"]; !ok {
			t.Fatal("notification restriction removed unrelated human tools")
		}
	}
}

func TestWorkflowNotificationScopeUsesSavedKind(t *testing.T) {
	_, _, workspace := gmailTriggerFixture(t)
	if !workflowNotificationsForPath("Workflow/mail") {
		t.Fatal("legacy workflow lost notifications")
	}
	workspace.files["Workflow/mail/workflow.json"] = `{"schema_version":1,"kind":"relay","id":"relay","label":"Relay"}`
	if workflowNotificationsForPath("Workflow/mail") || workflowNotificationsForPath("") || workflowNotificationsForPath("Workflow/missing") {
		t.Fatal("Relay or missing manifest gained workflow notifications")
	}
	for _, product := range []string{"work", "code", "sparkquill"} {
		workspace.files["Workflow/mail/workflow.json"] = `{"product":"` + product + `","id":"project"}`
		if workflowNotificationsForPath("Workflow/mail") {
			t.Fatalf("%s project gained notifications through the shared runner", product)
		}
	}
}
