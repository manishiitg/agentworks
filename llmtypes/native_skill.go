package llmtypes

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sync/atomic"
)

// NativeSkill is an explicit, host-admitted request, not a claim of execution.
// The host resolves ID/name/path from this exact chat's usable inventory. A
// browser-provided path must never be put directly in this structure.
type NativeSkill struct{ Name, Path string }
type nativeSkillScope struct {
	session string
	skill   NativeSkill
	claimed *atomic.Bool
}
type nativeSkillKey struct{}

func WithNativeSkill(ctx context.Context, session string, skill NativeSkill) context.Context {
	return context.WithValue(ctx, nativeSkillKey{}, nativeSkillScope{session: session, skill: skill, claimed: &atomic.Bool{}})
}

func NativeSkillForSession(ctx context.Context, session string) (NativeSkill, bool) {
	scope, ok := ctx.Value(nativeSkillKey{}).(nativeSkillScope)
	return scope.skill, ok && session != "" && scope.session == session
}

// One explicit selection belongs to one native input, never to subsequent
// generations or a child that inherited the parent's context.
func ClaimNativeSkillForSession(ctx context.Context, session string) (NativeSkill, bool) {
	scope, ok := ctx.Value(nativeSkillKey{}).(nativeSkillScope)
	if !ok || session == "" || scope.session != session || !scope.claimed.CompareAndSwap(false, true) {
		return NativeSkill{}, false
	}
	return scope.skill, true
}

func NativeSkillUnavailable(program string) error {
	return fmt.Errorf("Explicit skill invocation is not supported by %s here. Choose a supported program or remove the skill selection.", program)
}

var nativeSkillName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_-]{0,127}$`)

func ValidateNativeSkill(skill NativeSkill) error {
	if !nativeSkillName.MatchString(skill.Name) || !filepath.IsAbs(skill.Path) || filepath.Base(skill.Path) != "SKILL.md" {
		return fmt.Errorf("This skill is no longer available. Refresh skills and choose it again.")
	}
	return nil
}
