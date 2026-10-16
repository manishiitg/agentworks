package server

import (
	"testing"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

func TestResolveDelegationTierConfigExpandsProviderProfile(t *testing.T) {
	resolved := resolveDelegationTierConfig(&virtualtools.DelegationTierConfig{
		SchemaVersion: delegationTierConfigSchemaVersion,
		Mode:          "provider_profile",
		Provider:      "claude-code",
	})
	if resolved == nil {
		t.Fatal("resolveDelegationTierConfig() = nil")
	}
	// Main is the provider's builder tier: Sonnet 5.5 at medium effort; Medium
	// is Haiku 5.5 at high effort (multi-llm-provider-go coding_agent_tier_defaults.go, 2026-10-16).
	if resolved.Main == nil || resolved.Main.ModelID != "claude-sonnet-5-5" {
		t.Fatalf("main = %+v, want claude-sonnet-5-5", resolved.Main)
	}
	if got := resolved.Main.Options["reasoning_effort"]; got != "medium" {
		t.Fatalf("main reasoning_effort = %#v, want medium", got)
	}
	if resolved.High == nil || resolved.High.ModelID != "claude-sonnet-5-5" {
		t.Fatalf("high = %+v, want claude-sonnet-5-5", resolved.High)
	}
	if got := resolved.High.Options["reasoning_effort"]; got != "high" {
		t.Fatalf("high reasoning_effort = %#v, want high", got)
	}
	if resolved.Medium == nil || resolved.Medium.ModelID != "claude-haiku-5-5" {
		t.Fatalf("medium = %+v, want claude-haiku-5-5", resolved.Medium)
	}
	if got := resolved.Medium.Options["reasoning_effort"]; got != "high" {
		t.Fatalf("medium reasoning_effort = %#v, want high", got)
	}
}

func TestResolveDelegationTierConfigPreservesExplicitOptions(t *testing.T) {
	resolved := resolveDelegationTierConfig(&virtualtools.DelegationTierConfig{
		SchemaVersion: delegationTierConfigSchemaVersion,
		Mode:          "explicit",
		Main: &virtualtools.TierModel{
			Provider: "codex-cli",
			ModelID:  "gpt-5.5",
			Options:  map[string]interface{}{"reasoning_effort": "xhigh"},
		},
	})
	if resolved == nil || resolved.Main == nil {
		t.Fatalf("resolveDelegationTierConfig() = %+v", resolved)
	}
	if got := resolved.Main.Options["reasoning_effort"]; got != "xhigh" {
		t.Fatalf("main reasoning_effort = %#v, want xhigh", got)
	}
}
