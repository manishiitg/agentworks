package llmguard

import "testing"

func TestAgyAlphaRuntimeGate(t *testing.T) {
	t.Setenv("AGY_ALPHA", "")
	t.Setenv("MULTI_USER_MODE", "")
	t.Setenv("AGY_ALPHA_MULTI_USER", "")
	t.Setenv("AGENTWORKS_CLI_LANDLOCK", "")
	if err := RequireCodingAgentProvider("agy-cli"); err == nil {
		t.Fatal("AGY ran without alpha flag")
	}
	t.Setenv("AGY_ALPHA", "1")
	if err := RequireCodingAgentProvider("agy-cli"); err != nil {
		t.Fatalf("AGY rejected in local alpha mode: %v", err)
	}
	t.Setenv("MULTI_USER_MODE", "true")
	if err := RequireCodingAgentProvider("agy-cli"); err == nil {
		t.Fatal("AGY ran in multi-user mode")
	}
	if err := RequireCodingAgentProvider("codex-cli"); err != nil {
		t.Fatalf("unrelated CLI rejected: %v", err)
	}
}

func TestAgyMultiUserOptInRequiresConfinement(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	for _, tc := range []struct {
		name, alpha, server, lock string
		allowed                   bool
	}{
		{"locked without server opt-in", "1", "", "on", false},
		{"server opt-in without alpha", "", "1", "on", false},
		{"server opt-in without lock", "1", "1", "", false},
		{"server opt-in with lock disabled", "1", "1", "off", false},
		{"confined server opt-in", "1", "1", "on", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGY_ALPHA", tc.alpha)
			t.Setenv("AGY_ALPHA_MULTI_USER", tc.server)
			t.Setenv("AGENTWORKS_CLI_LANDLOCK", tc.lock)
			if got := AgyAlphaEnabled(); got != tc.allowed {
				t.Fatalf("provider publication allowed = %v, want %v", got, tc.allowed)
			}
			if got := RequireCodingAgentProvider("agy-cli") == nil; got != tc.allowed {
				t.Fatalf("provider execution allowed = %v, want %v", got, tc.allowed)
			}
		})
	}
}
