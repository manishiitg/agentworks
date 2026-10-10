package step_based_workflow

import (
	"strings"
	"testing"
)

// PLAT-055 / K. Unset knowledgebase_access used to silently resolve to
// KBAccessNone regardless of whether a contribution was already staged — a
// genuine trap, distinct from an operator deliberately writing "none".
// latency-report's two worst-offending steps had no legitimate KB destination
// during execution because nothing was configured either way, not because
// anyone decided against it. The fix mirrors resolveLearningsAccess's
// already-safe pattern: read by default, auto-promoted to read-write only when
// a contribution is already staged.

func TestResolveKnowledgebaseAccessDefaultsToReadWhenUnset(t *testing.T) {
	got := resolveKnowledgebaseAccess(&AgentConfigs{}, true)
	if got != KBAccessRead {
		t.Fatalf("unset access = %q, want %q (same baseline learnings already grants everyone)", got, KBAccessRead)
	}
}

func TestKnowledgebaseAccessBuilderDescriptionMatchesRuntimeDefault(t *testing.T) {
	if !strings.Contains(knowledgebaseAccessDescription, "Defaults to 'read'") {
		t.Fatalf("Builder description does not state the runtime default: %q", knowledgebaseAccessDescription)
	}
	if strings.Contains(knowledgebaseAccessDescription, "Defaults to 'none'") || strings.Contains(knowledgebaseAccessDescription, "opt-in per step") {
		t.Fatalf("Builder description retains the retired opt-in contract: %q", knowledgebaseAccessDescription)
	}
}

func TestResolveKnowledgebaseAccessPromotesToReadWriteWhenContributionStaged(t *testing.T) {
	got := resolveKnowledgebaseAccess(&AgentConfigs{
		KnowledgebaseContribution: "Record durable client-quality signals.",
	}, true)
	if got != KBAccessReadWrite {
		t.Fatalf("unset access with a staged contribution = %q, want %q", got, KBAccessReadWrite)
	}
}

func TestResolveKnowledgebaseAccessNeverPromotesWithoutPresetEnabled(t *testing.T) {
	// The preset-level gate stays authoritative regardless of anything staged
	// at the step level — a workflow that hasn't turned KB on at all must not
	// leak content through a step's contribution field.
	got := resolveKnowledgebaseAccess(&AgentConfigs{
		KnowledgebaseContribution: "Record durable client-quality signals.",
	}, false)
	if got != KBAccessNone {
		t.Fatalf("preset disabled = %q, want %q even with a staged contribution", got, KBAccessNone)
	}
}

func TestResolveKnowledgebaseAccessExplicitNoneAlwaysWins(t *testing.T) {
	// This is the case this change deliberately does NOT touch: latency-report's
	// steps had kb_access explicitly set to "none", which is an operator
	// decision (however undocumented) and must keep winning over any default.
	got := resolveKnowledgebaseAccess(&AgentConfigs{
		KnowledgebaseAccess:       KBAccessNone,
		KnowledgebaseContribution: "Would have been written if access allowed it.",
	}, true)
	if got != KBAccessNone {
		t.Fatalf("explicit none = %q, want %q to be preserved", got, KBAccessNone)
	}
}

func TestResolveKnowledgebaseAccessExplicitValuesStillWin(t *testing.T) {
	for _, explicit := range []string{KBAccessRead, KBAccessWrite, KBAccessReadWrite, KBAccessNone} {
		got := resolveKnowledgebaseAccess(&AgentConfigs{KnowledgebaseAccess: explicit}, true)
		if got != explicit {
			t.Errorf("explicit %q became %q", explicit, got)
		}
	}
}

// The write path itself is unaffected: a promoted step still needs a
// non-empty contribution to actually write, so the default change adds zero
// new writers by itself — only visibility, and only conditional write
// eligibility that a real contribution must still activate.
func TestResolveKnowledgebaseAccessDefaultDoesNotEnableWriteWithoutContribution(t *testing.T) {
	access := resolveKnowledgebaseAccess(&AgentConfigs{}, true)
	if kbAccessAllowsWrite(access) {
		t.Fatalf("default access %q allows write with no contribution staged", access)
	}
}
