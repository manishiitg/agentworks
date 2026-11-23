package mcpagent

import (
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativereplies"
	"testing"
)

func TestNativeRuntimeRegistrationPrecedesEvents(t *testing.T) {
	agent := &Agent{sessionID: "native-runtime-chat", provider: llm.ProviderClaudeCode, claudeCodeSessionID: "native-runtime"}
	lifecycle := newCanonicalTurnLifecycle("turn-a")
	session := &Session{agent: agent, activeTurn: lifecycle}
	notified := false
	SetNativeRuntimeObserver(func(handle *AgentSessionHandle) {
		if handle != nil && handle.SessionID == agent.sessionID {
			notified = true
		}
	})
	defer SetNativeRuntimeObserver(nil)
	registerTurnSession(agent.sessionID, session)
	defer unregisterTurnSession(session)
	if !notified {
		t.Fatal("runtime registration waited for an event")
	}
	if nativereplies.ActiveForeground("claude-code", "native-runtime") != "turn-a" {
		t.Fatal("live turn unavailable to native publication")
	}
	lifecycle.setNativePublication("native-runtime", []string{"included-root"})
	completion := events.NewUnifiedCompletionEvent("session", "builder", "", "answer", "completed", 0, 1)
	if !lifecycle.prepareEvent(completion) {
		t.Fatal("completion rejected")
	}
	if nativereplies.ActiveForeground("claude-code", "native-runtime") != "" {
		t.Fatal("terminal turn can still acquire native publication")
	}
	roots, _ := completion.Metadata["native_continuation_roots"].([]string)
	if len(roots) != 1 || roots[0] != "included-root" || completion.Metadata["native_session_id"] != "native-runtime" {
		t.Fatal("completion lost exact included native roots")
	}
}
