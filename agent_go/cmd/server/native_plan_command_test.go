package server

import "testing"

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
