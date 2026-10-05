package server

import (
	"context"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestMCPManagementRegistrationFollowsChatPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, session  string
		req            QueryRequest
		active         *ActiveSessionInfo
		readOnly, want bool
		// list: list_mcp_servers is registered (Run mode inspects servers).
		list bool
	}{
		{name: "Builder", want: true},
		{name: "legacy Builder", want: true},
		{name: "legacy Run request by writable user", req: QueryRequest{ExecutionOptions: &ExecutionOptions{WorkshopMode: "run"}}, want: true},
		{name: "manual workflow execution", req: QueryRequest{AgentMode: "workflow"}, list: true},
		{name: "cron shares builder mode", req: QueryRequest{TriggeredBy: "cron"}},
		{name: "retained schedule", session: "schedule-digest_123"},
		{name: "restored origin", active: &ActiveSessionInfo{TriggeredBy: "cron"}},
		{name: "read only Builder", readOnly: true, list: true},
		{name: "Slack channel turn (Run)", req: QueryRequest{BotPlatform: "slack"}, readOnly: true, list: true},
		{name: "Pulse maintenance", req: QueryRequest{TriggeredBy: "cron", PulseLifecycleTurn: true}},
		{name: "Pulse reviewer", req: QueryRequest{SessionKind: "pulse_reviewer", ParentSessionID: "parent"}},
		{name: "restored child", active: &ActiveSessionInfo{ParentSessionID: "parent"}},
		{name: "notification", req: QueryRequest{IsAutoNotification: true}, want: true},
		{name: "owner bot", req: QueryRequest{BotPlatform: "whatsapp"}, want: true},
		{name: "promoted schedule", session: "schedule-digest_123", req: QueryRequest{UserInteractiveContinuation: true}, want: true},
		{name: "promotion cannot elevate child", req: QueryRequest{UserInteractiveContinuation: true, SessionKind: "pulse_reviewer"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &StreamingAPI{}
			reg := &recordingRegistrar{}
			policy := resolveWorkflowChatPolicy(tc.session, tc.req, tc.active, tc.readOnly)
			if err := api.registerMCPToolsForChat(reg, policy, nil); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"search_mcp_catalog", "install_mcp_server", "add_mcp_server", "remove_mcp_server", "trigger_mcp_discovery"} {
				_, got := reg.tools[name]
				if got != tc.want {
					t.Fatalf("%s registered=%v want %v (%+v)", name, got, tc.want, policy)
				}
			}
			if _, got := reg.tools["list_mcp_servers"]; got != (tc.want || tc.list) {
				t.Fatalf("list_mcp_servers registered=%v want %v (%+v)", got, tc.want || tc.list, policy)
			}
			if tc.want {
				// Exercise the real handler without making a network request.
				result, err := reg.tools["install_mcp_server"].exec(context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "policy-test-user"}), map[string]interface{}{})
				if err == nil || !strings.Contains(err.Error(), "name is required") || result != "" {
					t.Fatalf("missing required name: %q %v", result, err)
				}
			}
		})
	}
}

func TestPulseMaintenanceRetainsApprovedImprovementAuthority(t *testing.T) {
	p := resolveWorkflowChatPolicy("schedule-pulse_123", QueryRequest{TriggeredBy: "cron", PulseLifecycleTurn: true}, nil, false)
	if p.Origin != "pulse" || !p.allows("plan_authoring") || p.allows("mcp_management") || p.allows("workspace_ui") {
		t.Fatalf("Pulse authority changed: %+v", p)
	}
}

func TestWritableScheduledTurnsGetBuilderAuthority(t *testing.T) {
	p := resolveWorkflowChatPolicy("schedule-maintenance_123", QueryRequest{TriggeredBy: "cron"}, nil, false)
	if p.Mode != "builder" || p.Origin != "scheduled" || !p.allows("plan_authoring") {
		t.Fatalf("writable scheduled turn lacks Builder authority: %+v", p)
	}
}

// TestWorkflowAccessModeMatrix is the release contract for the single
// permission-to-mode rule. Transports may select an identity and provenance,
// but they do not independently select conversational authority.
func TestWorkflowAccessModeMatrix(t *testing.T) {
	tests := []struct {
		name, session, wantMode, wantOrigin string
		access                              WorkflowAccessLevel
		req                                 QueryRequest
		active                              *ActiveSessionInfo
		wantPlanAuthoring                   bool
	}{
		{name: "interactive owner", access: WorkflowAccessOwner, wantMode: "builder", wantOrigin: "interactive", wantPlanAuthoring: true},
		{name: "interactive writer", access: WorkflowAccessWrite, wantMode: "builder", wantOrigin: "interactive", wantPlanAuthoring: true},
		{name: "interactive reader", access: WorkflowAccessRead, wantMode: "run", wantOrigin: "interactive"},
		{name: "owner ignores stale client Run", access: WorkflowAccessOwner, req: QueryRequest{ExecutionOptions: &ExecutionOptions{WorkshopMode: "run"}}, wantMode: "builder", wantOrigin: "interactive", wantPlanAuthoring: true},
		{name: "owner explicit downgrade", access: WorkflowAccessOwner, req: QueryRequest{PinRunMode: true}, wantMode: "run", wantOrigin: "interactive"},
		{name: "cron owner", access: WorkflowAccessOwner, session: "schedule-cron--daily", req: QueryRequest{TriggeredBy: "cron"}, wantMode: "builder", wantOrigin: "scheduled", wantPlanAuthoring: true},
		{name: "manual trigger owner", access: WorkflowAccessOwner, session: "schedule-manual--daily", req: QueryRequest{TriggeredBy: "manual"}, wantMode: "builder", wantOrigin: "scheduled", wantPlanAuthoring: true},
		{name: "API trigger owner", access: WorkflowAccessOwner, session: "schedule-api--hook", req: QueryRequest{TriggeredBy: "api"}, wantMode: "builder", wantOrigin: "scheduled", wantPlanAuthoring: true},
		{name: "internal trigger owner", access: WorkflowAccessOwner, session: "schedule-internal--hook", req: QueryRequest{TriggeredBy: "internal"}, wantMode: "builder", wantOrigin: "scheduled", wantPlanAuthoring: true},
		{name: "restored schedule owner", access: WorkflowAccessOwner, active: &ActiveSessionInfo{TriggeredBy: "cron"}, wantMode: "builder", wantOrigin: "scheduled", wantPlanAuthoring: true},
		{name: "Slack read principal", access: WorkflowAccessRead, req: QueryRequest{BotPlatform: "slack"}, wantMode: "run", wantOrigin: "bot"},
		{name: "WhatsApp paired owner", access: WorkflowAccessOwner, req: QueryRequest{BotPlatform: "whatsapp"}, wantMode: "builder", wantOrigin: "bot", wantPlanAuthoring: true},
		{name: "headless executor is not chat", access: WorkflowAccessOwner, req: QueryRequest{AgentMode: "workflow"}, wantMode: "run", wantOrigin: "interactive"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			readOnly := readOnlyForRequest(tc.access, tc.req)
			policy := resolveWorkflowChatPolicy(tc.session, tc.req, tc.active, readOnly)
			if policy.Mode != tc.wantMode || policy.Origin != tc.wantOrigin || policy.allows("plan_authoring") != tc.wantPlanAuthoring {
				t.Fatalf("profile = mode %q origin %q plan_authoring=%v, want %q/%q/%v", policy.Mode, policy.Origin, policy.allows("plan_authoring"), tc.wantMode, tc.wantOrigin, tc.wantPlanAuthoring)
			}
		})
	}
}

// Exercise the production phase setup entry point. Stop before opening a live
// workflow controller: admission and registration must already be complete.
type chatPolicyTestDefinition struct{ recordingRegistrar }

func (*chatPolicyTestDefinition) AttachSkill(*llmtypes.Skill) error { return nil }
func (*chatPolicyTestDefinition) AttachedSkills() []*llmtypes.Skill { return nil }

func TestBuilderPhaseActuallyRegistersMCPManagement(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		api := &StreamingAPI{logger: loggerv2.NewNoop(), stoppedSessions: map[string]bool{"policy-chat": true}}
		reg := &chatPolicyTestDefinition{}
		err := api.installWorkflowPhaseTools(context.Background(), reg, "policy-chat", "test-user", "workflow-builder", "", "", map[string]string{"WorkshopMode": "workshop"}, nil, nil, nil, nil, nil, QueryRequest{}, readOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"search_mcp_catalog", "install_mcp_server", "add_mcp_server"} {
			_, exists := reg.tools[name]
			if exists == readOnly {
				t.Fatalf("phase setup %s present=%v readOnly=%v", name, exists, readOnly)
			}
		}
	}
}

func TestScheduledBuilderActuallyRegistersPlanMigrationTools(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		api := &StreamingAPI{logger: loggerv2.NewNoop(), stoppedSessions: map[string]bool{"schedule-policy-chat": true}}
		reg := &chatPolicyTestDefinition{}
		err := api.installWorkflowPhaseTools(
			context.Background(), reg, "schedule-policy-chat", "test-user", "workflow-builder", "", "",
			map[string]string{"WorkshopMode": "workshop"}, nil, nil, nil, nil, nil,
			QueryRequest{TriggeredBy: "cron"}, readOnly,
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"maintain_plan"} {
			_, exists := reg.tools[name]
			if exists == readOnly {
				t.Fatalf("scheduled phase tool %s present=%v readOnly=%v", name, exists, readOnly)
			}
		}
	}
}

func TestChatPolicyReconnectPreservesCompatibleHistory(t *testing.T) {
	for _, tc := range []struct {
		name          string
		coding, known bool
		previous      string
		saved         *ChatHistoryAgentRuntime
		want          bool
	}{
		{name: "fresh API chat"},
		{name: "changed policy on API chat", known: true, previous: "old"},
		{name: "fresh coding chat", coding: true},
		{name: "unchanged live coding chat", coding: true, known: true, previous: "current"},
		{name: "changed live coding chat", coding: true, known: true, previous: "old", want: true},
		{name: "retained compatible after restart", coding: true, saved: &ChatHistoryAgentRuntime{ExternalSessionID: "native-id", ChatPolicyKey: "current"}},
		{name: "legacy retained needs new catalog", coding: true, saved: &ChatHistoryAgentRuntime{ExternalSessionID: "native-id"}, want: true},
		{name: "retained changed policy", coding: true, saved: &ChatHistoryAgentRuntime{ExternalSessionID: "native-id", ChatPolicyKey: "old"}, want: true},
		{name: "saved API history has no native session", coding: true, saved: &ChatHistoryAgentRuntime{Kind: "llm_agent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := chatPolicyRequiresReconnect(tc.coding, tc.previous, "current", tc.known, tc.saved); got != tc.want {
				t.Fatalf("reconnect=%v want=%v", got, tc.want)
			}
		})
	}
	api := &StreamingAPI{
		lastChatPolicyBySession:      map[string]string{"chat": "current"},
		lastAgentProfileKeyBySession: map[string]string{"chat": "profile-sha256:current"},
	}
	runtime := api.captureChatHistoryAgentRuntime("chat", "cursor-cli", "test-model", "Workflow/test", nil)
	if runtime.ChatPolicyKey != "current" {
		t.Fatal("native runtime did not persist its policy fingerprint")
	}
	if runtime.AgentProfileKey != "profile-sha256:current" {
		t.Fatal("native runtime did not persist its agent profile fingerprint")
	}
}
