package llmtypes

import (
	"context"
	"testing"
)

// PLAT-746-1008: an explicit skill must not reach another chat, a delegated
// child, or a later native generation that reuses the same context.
func TestNativeSkillOnlyOneInputInExactSession(t *testing.T) {
	ctx := WithNativeSkill(context.Background(), "parent", NativeSkill{Name: "review", Path: "/project/.agents/skills/review/SKILL.md"})
	if _, ok := ClaimNativeSkillForSession(ctx, "child"); ok {
		t.Fatal("child inherited skill")
	}
	if skill, ok := ClaimNativeSkillForSession(ctx, "parent"); !ok || skill.Name != "review" {
		t.Fatal("own explicit skill unavailable")
	}
	if _, ok := ClaimNativeSkillForSession(ctx, "parent"); ok {
		t.Fatal("later input replayed skill")
	}
}
