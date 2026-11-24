package server

import (
	"encoding/json"
	"testing"
)

func TestNativePlanCommand(t *testing.T) {
	for query, want := range map[string]string{"/plan add dark mode": "add dark mode", "  /plan\n fix the login ": "fix the login", "/plan": ""} {
		if task, ok := parseNativePlanCommand(query); !ok || task != want {
			t.Fatalf("%q: got %q %v", query, task, ok)
		}
	}
	for _, query := range []string{"/planner x", "please /plan x", "/goal x", "plan x"} {
		if _, ok := parseNativePlanCommand(query); ok {
			t.Fatalf("%q must stay ordinary text", query)
		}
	}
	if nativePlanPrompt("/plan add dark mode") != "add dark mode" || nativePlanPrompt("hello") != "hello" {
		t.Fatal("plan prompt")
	}
	for provider, want := range map[string]bool{"claude-code": true, "codex-cli": true, "cursor-cli": true, "gemini-cli": false, "openai": false} {
		if nativePlanSupported(provider) != want {
			t.Fatalf("%s support", provider)
		}
	}
}

// #3699: the composer's Plan switch is offered only where the server says so, and its flag
// survives the Crew chat request into the turn.
func TestNativePlanSwitchCapabilityAndFlag(t *testing.T) {
	caps := nativePlanProgramCapabilities()
	for provider, want := range map[string]bool{"claude-code": true, "codex-cli": true, "cursor-cli": true} {
		if caps[provider] != want {
			t.Fatalf("%s capability: %v", provider, caps[provider])
		}
	}
	for provider, supported := range caps {
		if supported != nativePlanSupported(provider) {
			t.Fatalf("%s capability disagrees with admission", provider)
		}
	}
	var req AgentProfileChatRequest
	if err := json.Unmarshal([]byte(`{"message":"add dark mode","plan_mode":true}`), &req); err != nil || !req.PlanMode {
		t.Fatalf("plan_mode not read: %v %+v", err, req)
	}
}
