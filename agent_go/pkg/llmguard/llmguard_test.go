package llmguard

import "testing"

func TestAgyAlphaRuntimeGate(t *testing.T) {
	t.Setenv("AGY_ALPHA", "")
	t.Setenv("MULTI_USER_MODE", "")
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
