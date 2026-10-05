package server

import "testing"

// Definition/config drift must keep the native coding-agent conversation;
// only a role change (mode, origin, capabilities) may replace it.
func TestChatPolicyRoleRequiresReconnect(t *testing.T) {
	builder := workflowChatPolicy{Mode: "builder", Origin: "interactive", Capabilities: map[string]bool{"authoring": true}}
	run := workflowChatPolicy{Mode: "run", Origin: "interactive", Capabilities: map[string]bool{}}
	resumable := func(role string) *ChatHistoryAgentRuntime {
		return &ChatHistoryAgentRuntime{ExternalSessionID: "native-1", ChatPolicyKey: "full-key-before-deploy", ChatPolicyRoleKey: role}
	}

	if chatPolicyRoleRequiresReconnect(true, builder.sessionKey(), builder.sessionKey(), true, nil) {
		t.Fatal("same role in this process must not reconnect")
	}
	if !chatPolicyRoleRequiresReconnect(true, builder.sessionKey(), run.sessionKey(), true, nil) {
		t.Fatal("builder -> run in this process must reconnect")
	}
	// After a restart/deploy the full key differs (new chat definition), but
	// the saved role key still matches: resume the same session.
	if chatPolicyRoleRequiresReconnect(true, "", builder.sessionKey(), false, resumable(builder.sessionKey())) {
		t.Fatal("definition drift with an unchanged role must resume")
	}
	if !chatPolicyRoleRequiresReconnect(true, "", run.sessionKey(), false, resumable(builder.sessionKey())) {
		t.Fatal("a saved builder session restored as run must reconnect")
	}
	if chatPolicyRoleRequiresReconnect(true, "", builder.sessionKey(), false, resumable("")) {
		t.Fatal("a legacy runtime without a role key must resume (mode is compared separately)")
	}
	if chatPolicyRoleRequiresReconnect(false, builder.sessionKey(), run.sessionKey(), true, nil) {
		t.Fatal("non-coding providers never reconnect")
	}
}

func TestScheduledPulseTransitionKeepsNativeConversation(t *testing.T) {
	api := &StreamingAPI{}
	for _, readOnly := range []bool{false, true} {
		scheduled := resolveWorkflowChatPolicy("schedule-digest_123", QueryRequest{TriggeredBy: "cron"}, nil, readOnly)
		pulse := resolveWorkflowChatPolicy("schedule-digest_123", QueryRequest{TriggeredBy: "cron", PulseLifecycleTurn: true}, nil, readOnly)
		if scheduled.Origin != "scheduled" || pulse.Origin != "pulse" {
			t.Fatal("stage provenance must remain distinct")
		}
		if chatPolicyRoleRequiresReconnect(true, scheduled.sessionKey(), pulse.sessionKey(), true, nil) {
			t.Fatal("same-permission scheduled -> Pulse transition restarted native conversation")
		}
		saved := &ChatHistoryAgentRuntime{ExternalSessionID: "native-1", ChatPolicyRoleKey: scheduled.sessionKey()}
		if chatPolicyRoleRequiresReconnect(true, "", pulse.sessionKey(), false, saved) {
			t.Fatal("restored scheduled runtime cannot continue into Pulse")
		}
		if chatPolicyRequiresReconnect(true, api.chatPolicySessionKey(scheduled), api.chatPolicySessionKey(pulse), true, nil) {
			t.Fatal("same-definition scheduled -> Pulse transition still reconnects Muse")
		}
		interactive := resolveWorkflowChatPolicy("schedule-digest_123", QueryRequest{UserInteractiveContinuation: true}, nil, readOnly)
		if !chatPolicyRoleRequiresReconnect(true, scheduled.sessionKey(), interactive.sessionKey(), true, nil) {
			t.Fatal("interactive promotion must still refresh authority")
		}
		pulse.Capabilities["mcp_management"] = true
		if !chatPolicyRoleRequiresReconnect(true, scheduled.sessionKey(), pulse.sessionKey(), true, nil) {
			t.Fatal("a real Pulse capability change must still reconnect")
		}
	}
}

func TestCodingProviderReloadsInstructionsOnResume(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex-cli", "cursor-cli", "pi-cli"} {
		if !codingProviderReloadsInstructionsOnResume(provider) {
			t.Fatalf("%s should keep its session across prompt changes", provider)
		}
	}
	if codingProviderReloadsInstructionsOnResume("muse-cli") || codingProviderReloadsInstructionsOnResume(" Muse-CLI ") {
		t.Fatal("muse-cli cannot take new instructions into a resumed session")
	}
}

// There is no general-purpose chat: an interactive multi-agent request with no
// workflow, Crew or Code is refused, while bots, schedules, triggers, child
// sessions and profiled chats are not.
func TestRetiredGeneralChatIsRefusedOnlyForInteractiveProfilelessChat(t *testing.T) {
	profile := &resolvedAgentProfile{}
	cases := []struct {
		name    string
		req     QueryRequest
		profile *resolvedAgentProfile
		session string
		want    bool
	}{
		{"plain chat", QueryRequest{AgentMode: "multi-agent"}, nil, "chat-1", true},
		{"crew or code chat", QueryRequest{AgentMode: "multi-agent"}, profile, "chat-1", false},
		{"workflow builder", QueryRequest{AgentMode: "workflow_phase"}, nil, "chat-1", false},
		{"bot", QueryRequest{AgentMode: "multi-agent", BotPlatform: "slack"}, nil, "chat-1", false},
		{"cron", QueryRequest{AgentMode: "multi-agent", TriggeredBy: "cron"}, nil, "chat-1", false},
		{"schedule session", QueryRequest{AgentMode: "multi-agent"}, nil, "schedule-x", false},
		{"child session", QueryRequest{AgentMode: "multi-agent", ParentSessionID: "p"}, nil, "chat-1", false},
		{"auto notification", QueryRequest{AgentMode: "multi-agent", IsAutoNotification: true}, nil, "chat-1", false},
	}
	for _, c := range cases {
		req := c.req
		if got := isRetiredGeneralChat(&req, c.profile, c.session); got != c.want {
			t.Errorf("%s: refused=%v, want %v", c.name, got, c.want)
		}
	}
}

// An auto-notification and a typed message in the same chat are one role. They used to differ by origin,
// so each switch between them threw the coding CLI's session away and the chat showed "Conversation
// restored" again (Upwork chat, 2026-10-05: a browser-extension notice, then the owner's message).
func TestAutoNotificationKeepsTheInteractiveNativeConversation(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		typed := resolveWorkflowChatPolicy("chat-1", QueryRequest{}, nil, readOnly)
		notice := resolveWorkflowChatPolicy("chat-1", QueryRequest{IsAutoNotification: true}, nil, readOnly)
		if notice.Origin != "notification" {
			t.Fatalf("origin = %q, want notification (provenance is kept)", notice.Origin)
		}
		if typed.sessionKey() != notice.sessionKey() {
			t.Fatalf("readOnly=%v: a notification and a typed message must share the role key", readOnly)
		}
		if chatPolicyRoleRequiresReconnect(true, typed.sessionKey(), notice.sessionKey(), true, nil) {
			t.Fatalf("readOnly=%v: switching between a notification and a typed message must not reconnect", readOnly)
		}
	}
	// A real capability difference still reconnects.
	notice := workflowChatPolicy{Mode: "builder", Origin: "notification", Capabilities: map[string]bool{"authoring": true}}
	narrower := workflowChatPolicy{Mode: "builder", Origin: "interactive", Capabilities: map[string]bool{}}
	if !chatPolicyRoleRequiresReconnect(true, notice.sessionKey(), narrower.sessionKey(), true, nil) {
		t.Fatal("different capabilities must still reconnect")
	}
}
