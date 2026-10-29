package server

import (
	"context"
	"strings"
	"testing"
)

func TestWorkUIRegistersSamePresentationToolFamilyWithWorkViews(t *testing.T) {
	api := &StreamingAPI{}
	reg := &recordingRegistrar{}
	if err := api.registerOpenWorkWorkspaceViewTool(reg, "user-1", "work-chat", "Crew/demo", true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"list_ui_capabilities", "get_ui_state", "perform_ui_action"} {
		if _, ok := reg.tools[name]; !ok {
			t.Fatalf("missing Work UI tool %q", name)
		}
	}
	if len(reg.tools) != 3 {
		t.Fatalf("expected exactly three Crew UI tools, got %d", len(reg.tools))
	}
	for _, contract := range []uiContract{uiControlContract, workUIControlContract} {
		for _, view := range contract.Views {
			if err := validateUIActionForContract(contract, view.ID, "refresh", ""); err != nil {
				t.Fatalf("refresh %s/%s: %v", contract.Product, view.ID, err)
			}
		}
	}
	capabilities, err := reg.tools["list_ui_capabilities"].exec(context.Background(), map[string]interface{}{})
	if err != nil || !strings.Contains(capabilities, `"product":"work"`) || !strings.Contains(capabilities, `"id":"report"`) {
		t.Fatalf("capabilities=%s err=%v", capabilities, err)
	}
	if !strings.Contains(capabilities, `"targets":["schedules","webhooks"]`) {
		t.Fatalf("Crew schedule sections missing from capabilities: %s", capabilities)
	}
	if !strings.Contains(capabilities, `"targets":["schedules","conditions","triggers","functions","email"]`) {
		t.Fatalf("Crew Automation center sections missing from capabilities: %s", capabilities)
	}
	for _, currentPanel := range []string{`"id":"memory"`, `"id":"identity"`} {
		if !strings.Contains(capabilities, currentPanel) {
			t.Fatalf("Crew current panel missing from capabilities: %s", currentPanel)
		}
	}
	for _, workflowOnly := range []string{`"id":"flow"`, `"id":"pulse"`, `"id":"evaluation"`, `"id":"playbooks"`} {
		if strings.Contains(capabilities, workflowOnly) {
			t.Fatalf("Work advertised workflow-only capability %s: %s", workflowOnly, capabilities)
		}
	}
	if out, err := reg.tools["perform_ui_action"].exec(context.Background(), map[string]interface{}{"view": "report", "action": "open"}); err != nil || !strings.Contains(out, "browser_disconnected") {
		t.Fatalf("report open=%s err=%v", out, err)
	}
	if out, err := reg.tools["perform_ui_action"].exec(context.Background(), map[string]interface{}{"view": "memory", "action": "open"}); err != nil || !strings.Contains(out, "browser_disconnected") {
		t.Fatalf("memory open=%s err=%v", out, err)
	}
	if out, err := reg.tools["perform_ui_action"].exec(context.Background(), map[string]interface{}{"view": "schedules", "action": "open", "target": "webhooks"}); err != nil || !strings.Contains(out, "browser_disconnected") {
		t.Fatalf("webhooks open=%s err=%v", out, err)
	}
	for _, target := range []string{"slack", "share", "agentbridge"} {
		if !strings.Contains(capabilities, `"`+target+`"`) {
			t.Fatalf("Crew panel %s missing from advertised capabilities: %s", target, capabilities)
		}
		if out, err := reg.tools["perform_ui_action"].exec(context.Background(), map[string]interface{}{"view": "mcp", "action": "open", "target": target}); err != nil || !strings.Contains(out, "browser_disconnected") {
			t.Fatalf("panel %s open=%s err=%v", target, out, err)
		}
	}
	if strings.Contains(capabilities, `"share-owners"`) {
		t.Fatalf("Crew advertised the retired separate Owners panel: %s", capabilities)
	}
	if out, err := reg.tools["perform_ui_action"].exec(context.Background(), map[string]interface{}{"view": "mcp", "action": "open", "target": "share-owners"}); err != nil || !strings.Contains(out, "unsupported_target") {
		t.Fatalf("retired Owners target was accepted: %s err=%v", out, err)
	}
	if out, err := reg.tools["perform_ui_action"].exec(context.Background(), map[string]interface{}{"view": "flow", "action": "open"}); err != nil || !strings.Contains(out, "unsupported_view") {
		t.Fatalf("workflow-only view was accepted: %s err=%v", out, err)
	}
}

func TestWorkUIContractCoversCurrentCrewPanels(t *testing.T) {
	for _, view := range []string{"report", "memory", "database", "browser", "costs", "workshop", "schedules", "files", "identity", "mcp", "history"} {
		if !validUIViewForContract(workUIControlContract, view) {
			t.Errorf("Crew UI contract does not accept current panel %q", view)
		}
	}
}

func TestWorkUIUsesPublicWorkspaceForUserScopedProductConversation(t *testing.T) {
	for _, workspace := range []string{
		"Chats/Work/projects/demo",
		"_users/user-1/Chats/Work/projects/demo",
	} {
		t.Run(workspace, func(t *testing.T) {
			api := &StreamingAPI{}
			reg := &recordingRegistrar{}
			if err := api.registerOpenWorkWorkspaceViewTool(reg, "user-1", "work-chat", workspace, true); err != nil {
				t.Fatal(err)
			}
			if got, want := api.uiBroker().scope("work-chat"), "Chats/Work/projects/demo"; got != want {
				t.Fatalf("UI workspace scope = %q, want %q", got, want)
			}
		})
	}
}

func TestWorkUIOnlyRegistersForInteractiveWorkChat(t *testing.T) {
	for _, test := range []struct {
		name string
		req  QueryRequest
		want bool
	}{
		{name: "interactive", req: QueryRequest{AgentProfileID: "work"}, want: true},
		{name: "manual interactive", req: QueryRequest{AgentProfileID: "work", TriggeredBy: "manual"}, want: true},
		{name: "cron", req: QueryRequest{AgentProfileID: "work", TriggeredBy: "cron"}},
		{name: "bot", req: QueryRequest{AgentProfileID: "work", BotPlatform: "slack"}},
		{name: "background child", req: QueryRequest{AgentProfileID: "work", ParentSessionID: "parent"}},
		{name: "other product", req: QueryRequest{AgentProfileID: "example-product"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := registerWorkUIAllowed(test.req); got != test.want {
				t.Fatalf("allowed=%v want=%v", got, test.want)
			}
		})
	}
}

func TestWorkUIVaultUsesSharedPanel(t *testing.T) {
	for _, contract := range []uiContract{workUIControlContract} {
		if err := validateUIActionForContract(contract, "mcp", "open", "vault"); err != nil {
			t.Fatalf("%s Vault refused: %v", contract.Product, err)
		}
		if err := validateUIActionForContract(contract, "secrets", "open", ""); err == nil {
			t.Fatalf("%s retained standalone Secrets alias", contract.Product)
		}
		if err := validateUIActionForContract(contract, "mcp", "open", "guessed"); err == nil {
			t.Fatalf("%s accepted unknown Connections target", contract.Product)
		}
	}
}

func TestProjectUIScopeUsesSameSectionsOnServer(t *testing.T) {
	for _, scope := range []string{"Crew/demo"} {
		contract := uiContractForScope(scope)
		if err := validateUIActionForContract(contract, "memory", "open", "skills"); err != nil {
			t.Fatalf("%s refused its Skills section: %v", scope, err)
		}
		if err := validateUIActionForContract(contract, "memory", "open", "guessed"); err == nil {
			t.Fatalf("%s accepted a guessed section", scope)
		}
	}
	for _, scope := range []string{"Crew/demo", "Workflow/example"} {
		if err := validateUIActionForContract(uiContractForScope(scope), "mcp", "open", "vault"); err != nil {
			t.Fatalf("%s refused Vault: %v", scope, err)
		}
	}
	if err := validateUIActionForContract(uiContractForScope("Workflow/example"), "memory", "open", "skills"); err == nil {
		t.Fatal("Goal accepted a Crew-only view")
	}
}
