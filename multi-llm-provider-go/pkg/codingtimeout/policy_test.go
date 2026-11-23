package codingtimeout

import (
	"testing"
	"time"
)

func TestLongRunningMCPToolTimeout(t *testing.T) {
	t.Setenv(EnvLongRunningMCPToolTimeout, "")
	if got := LongRunningMCPToolTimeout(); got != 90*time.Minute {
		t.Fatalf("default timeout = %v, want 90m", got)
	}

	t.Setenv(EnvLongRunningMCPToolTimeout, "17m")
	if got := LongRunningMCPToolTimeout(); got != 17*time.Minute {
		t.Fatalf("override timeout = %v, want 17m", got)
	}

	for _, invalid := range []string{"bad", "0", "-1s"} {
		t.Setenv(EnvLongRunningMCPToolTimeout, invalid)
		if got := LongRunningMCPToolTimeout(); got != 90*time.Minute {
			t.Fatalf("timeout for %q = %v, want default 90m", invalid, got)
		}
	}
}

func TestActiveProviderPolicies(t *testing.T) {
	t.Setenv(EnvLongRunningMCPToolTimeout, "11m")
	policies := ActiveProviderPolicies()
	if len(policies) != 4 {
		t.Fatalf("policy count = %d, want 4", len(policies))
	}

	for _, provider := range []string{"claude-code", "codex-cli"} {
		policy, ok := PolicyForProvider(provider)
		if !ok {
			t.Fatalf("missing policy for %s", provider)
		}
		if !policy.MCPClientConfigurable || policy.MCPClientTimeout != 11*time.Minute {
			t.Fatalf("policy for %s = %+v, want configurable 11m timeout", provider, policy)
		}
	}

	for _, provider := range []string{"cursor-cli", "pi-cli"} {
		policy, ok := PolicyForProvider(provider)
		if !ok {
			t.Fatalf("missing policy for %s", provider)
		}
		if policy.MCPClientConfigurable || policy.MCPClientTimeout != 0 {
			t.Fatalf("policy for %s = %+v, want unsupported client timeout", provider, policy)
		}
	}
}
