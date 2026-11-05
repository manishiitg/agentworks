package server

import (
	"context"
	"strings"
	"testing"
)

// Owner, 3 Nov: the agent's UI tool shows only the Dashboard; every other panel is refused.
func TestWorkUIRegistersDashboardOnly(t *testing.T) {
	api := &StreamingAPI{}
	reg := &recordingRegistrar{}
	if err := api.registerOpenWorkWorkspaceViewTool(reg, "user-1", "work-chat", "Crew/demo", true); err != nil {
		t.Fatal(err)
	}
	if len(reg.tools) != 3 {
		t.Fatalf("expected the three Crew UI tools, got %d", len(reg.tools))
	}
	capabilities, err := reg.tools["list_ui_capabilities"].exec(context.Background(), map[string]interface{}{})
	if err != nil || !strings.Contains(capabilities, `"id":"report"`) || strings.Count(capabilities, `"id":`) != 1 {
		t.Fatalf("capabilities=%s err=%v", capabilities, err)
	}
	if out, err := reg.tools["perform_ui_action"].exec(context.Background(), map[string]interface{}{"view": "report", "action": "open"}); err != nil || !strings.Contains(out, "browser_disconnected") {
		t.Fatalf("report open=%s err=%v", out, err)
	}
	for _, view := range []string{"memory", "files", "mcp", "flow"} {
		if out, err := reg.tools["perform_ui_action"].exec(context.Background(), map[string]interface{}{"view": view, "action": "open"}); err != nil || !strings.Contains(out, "unsupported_view") {
			t.Fatalf("%s open=%s err=%v", view, out, err)
		}
	}
	if err := validateUIActionForContract(uiContractForScope("Workflow/example"), "flow", "open", ""); err == nil {
		t.Fatal("Goal accepted a view other than the Dashboard")
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

// The person's own Crew chat keeps one tool set on every turn, automatic ones included (owner, 5 Nov).
func TestWorkUIRegistersForEveryOwnCrewChatTurn(t *testing.T) {
	for _, test := range []struct {
		name string
		req  QueryRequest
		want bool
	}{
		{name: "interactive", req: QueryRequest{AgentProfileID: "work"}, want: true},
		{name: "cron", req: QueryRequest{AgentProfileID: "work", TriggeredBy: "cron"}, want: true},
		{name: "background child", req: QueryRequest{AgentProfileID: "work", ParentSessionID: "parent"}, want: true},
		{name: "bot", req: QueryRequest{AgentProfileID: "work", BotPlatform: "slack"}},
		{name: "other person's agent", req: QueryRequest{AgentProfileID: "work", AgentMessageFromOtherPerson: true}},
		{name: "other product", req: QueryRequest{AgentProfileID: "example-product"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := registerWorkUIAllowed(test.req); got != test.want {
				t.Fatalf("allowed=%v want=%v", got, test.want)
			}
		})
	}
}
