package server

import (
	"strings"
	"testing"
	"time"

	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #4080: idle completed replies must release their cached conversationHistory
// (5 min) and idle sessionAgents (15 min) instead of pinning them for 24 h,
// while keeping activeSessions alive for follow-ups and protecting any session
// with active work, pending completions, or queued native wakes.
func TestCleanupReleasesIdleReplyHistoryAndAgentWithoutDroppingSession(t *testing.T) {
	now := time.Now()
	msg := llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "hello")
	api := &StreamingAPI{
		activeSessions: map[string]*ActiveSessionInfo{
			"idle-history": {SessionID: "idle-history", Status: "completed", LastActivity: now.Add(-6 * time.Minute)},
			"idle-agent":   {SessionID: "idle-agent", Status: "completed", LastActivity: now.Add(-16 * time.Minute)},
			"has-pending":  {SessionID: "has-pending", Status: "completed", LastActivity: now.Add(-20 * time.Minute)},
			"has-wake":     {SessionID: "has-wake", Status: "completed", LastActivity: now.Add(-20 * time.Minute)},
			"still-active": {SessionID: "still-active", Status: "running", LastActivity: now.Add(-6 * time.Minute)},
			"recent-reply": {SessionID: "recent-reply", Status: "completed", LastActivity: now.Add(-2 * time.Minute)},
		},
		conversationHistory: map[string][]llmtypes.MessageContent{
			"idle-history": {msg, msg},
			"idle-agent":   {msg},
			"has-pending":  {msg},
			"has-wake":     {msg},
			"still-active": {msg},
			"recent-reply": {msg},
		},
		resumeHistorySources: map[string]*resumeHistorySource{
			"idle-history": {},
			"idle-agent":   {},
			"has-pending":  {},
			"has-wake":     {},
			"still-active": {},
			"recent-reply": {},
		},
		sessionAgents: map[string]*agent.LLMAgentWrapper{
			"idle-history": nil,
			"idle-agent":   nil,
			"has-pending":  nil,
			"has-wake":     nil,
			"still-active": nil,
			"recent-reply": nil,
		},
		pendingCompletions: map[string][]string{
			"has-pending": {"bg-1"},
		},
	}
	nativeWakes.addClaude("has-wake")
	t.Cleanup(func() { nativeWakes.take("has-wake") })

	api.cleanupInactiveSessionsAt(now)

	// activeSessions stays intact (<24h) for every session.
	if len(api.activeSessions) != 6 {
		t.Fatalf("activeSessions count = %d, want 6", len(api.activeSessions))
	}

	// idle-history (6m): history + resume source released, agent kept (<15m).
	if _, ok := api.conversationHistory["idle-history"]; ok {
		t.Fatal("idle-history conversationHistory was not released")
	}
	if _, ok := api.resumeHistorySources["idle-history"]; ok {
		t.Fatal("idle-history resumeHistorySources was not released")
	}
	if _, ok := api.sessionAgents["idle-history"]; !ok {
		t.Fatal("idle-history sessionAgents was evicted before idleAgentRetention")
	}

	// idle-agent (16m): history, resume source, and agent all released.
	if _, ok := api.conversationHistory["idle-agent"]; ok {
		t.Fatal("idle-agent conversationHistory was not released")
	}
	if _, ok := api.sessionAgents["idle-agent"]; ok {
		t.Fatal("idle-agent sessionAgents was not released")
	}

	// has-pending (20m, pending completion): both kept.
	if _, ok := api.conversationHistory["has-pending"]; !ok {
		t.Fatal("has-pending conversationHistory was evicted while completions were pending")
	}
	if _, ok := api.sessionAgents["has-pending"]; !ok {
		t.Fatal("has-pending sessionAgents was evicted while completions were pending")
	}

	// has-wake (20m, queued native wake): history released, agent kept for wake.
	if _, ok := api.conversationHistory["has-wake"]; ok {
		t.Fatal("has-wake conversationHistory was not released")
	}
	if _, ok := api.sessionAgents["has-wake"]; !ok {
		t.Fatal("has-wake sessionAgents was evicted while a native wake was pending")
	}

	// still-active (running, 6m) and recent-reply (completed, 2m): both kept.
	for _, sid := range []string{"still-active", "recent-reply"} {
		if _, ok := api.conversationHistory[sid]; !ok {
			t.Fatalf("%s conversationHistory was evicted prematurely", sid)
		}
		if _, ok := api.sessionAgents[sid]; !ok {
			t.Fatalf("%s sessionAgents was evicted prematurely", sid)
		}
	}
}

func TestMemoryLogLineReportsCachedHistoryAndSessionAgents(t *testing.T) {
	msg := llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "hello")
	api := &StreamingAPI{
		conversationHistory: map[string][]llmtypes.MessageContent{
			"s1": {msg, msg},
			"s2": {msg},
		},
		sessionAgents: map[string]*agent.LLMAgentWrapper{
			"s1": nil,
		},
	}
	line := memoryLogLine(nil, api)
	want := "history_sessions=2 history_msgs=3 session_agents=1"
	if !strings.Contains(line, want) {
		t.Fatalf("memoryLogLine = %q, want substring %q", line, want)
	}
}
