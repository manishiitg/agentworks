package mcpagent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Without a resolver, read_skill must behave exactly as it did before: attached
// skills only. This change is additive, so no case that worked may start
// failing and no new one may start succeeding.
func TestReadSkillWithoutResolverStaysAttachedOnly(t *testing.T) {
	a := &Agent{}
	a.attachedSkills = []*llmtypes.Skill{{Name: "router", Description: "d", Content: "body"}}

	if _, err := a.readOneAttachedSkill("router", ""); err != nil {
		t.Fatalf("attached skill must still be readable: %v", err)
	}
	_, err := a.readOneAttachedSkill("specialist", "")
	if err == nil {
		t.Fatal("an unattached skill must fail when no resolver is installed")
	}
	if !strings.Contains(err.Error(), "attached skill") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Progressive disclosure attaches a router and leaves specialists on disk.
// Reaching one previously meant knowing a filesystem path and shelling out.
func TestReadSkillFallsBackToAnInstalledSkill(t *testing.T) {
	a := &Agent{}
	a.attachedSkills = []*llmtypes.Skill{{Name: "router", Description: "d", Content: "body"}}
	a.SetInstalledSkillResolver(func(name, relPath string) (InstalledSkillFile, error) {
		if name != "specialist" {
			return InstalledSkillFile{}, fmt.Errorf("not installed: %s", name)
		}
		return InstalledSkillFile{
			Content:        "specialist body for " + relPath,
			Description:    "the specialist",
			AvailableFiles: []string{"SKILL.md", "references/deep.md"},
		}, nil
	})

	result, err := a.readOneAttachedSkill("specialist", "")
	if err != nil {
		t.Fatalf("installed skill should resolve: %v", err)
	}
	if result.Source != "installed" {
		t.Fatalf("Source = %q, want \"installed\" so the caller can tell it was read on demand", result.Source)
	}
	if result.Path != "SKILL.md" || !strings.Contains(result.Content, "SKILL.md") {
		t.Fatalf("default path should be SKILL.md, got %+v", result)
	}

	// The attached skill must still win, so attaching a skill never changes
	// where its content comes from.
	attached, err := a.readOneAttachedSkill("router", "")
	if err != nil || attached.Source != "attached" {
		t.Fatalf("attached skill must resolve from memory, got %+v err=%v", attached, err)
	}
}

// "not attached" and "not installed" need different fixes; collapsing them
// sends the reader to the wrong one.
func TestReadSkillDistinguishesNotAttachedFromNotInstalled(t *testing.T) {
	a := &Agent{}
	a.attachedSkills = []*llmtypes.Skill{{Name: "router", Content: "b"}}
	a.SetInstalledSkillResolver(func(name, relPath string) (InstalledSkillFile, error) {
		return InstalledSkillFile{}, fmt.Errorf("file not found")
	})
	_, err := a.readOneAttachedSkill("ghost", "")
	if err == nil {
		t.Fatal("a skill that is neither attached nor installed must error")
	}
	for _, want := range []string{"not attached", "workspace", "router"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should mention %q so the fix is obvious: %v", want, err)
		}
	}
}

// Path traversal must be rejected before the resolver is consulted — the host
// resolver should never be handed ../ to interpret.
func TestReadSkillRejectsUnsafePathsBeforeConsultingTheResolver(t *testing.T) {
	a := &Agent{}
	consulted := false
	a.SetInstalledSkillResolver(func(name, relPath string) (InstalledSkillFile, error) {
		consulted = true
		return InstalledSkillFile{Content: "x"}, nil
	})
	if _, err := a.readOneAttachedSkill("specialist", "../../etc/passwd"); err == nil {
		t.Fatal("an escaping path must be rejected")
	}
	if consulted {
		t.Fatal("the resolver must never be asked to interpret an unsafe path")
	}
}

func TestReadSkillRejectsUnsafeNamesBeforeConsultingTheResolver(t *testing.T) {
	for _, name := range []string{"../specialist", "skills/specialist", `skills\specialist`, "/tmp/specialist", ".", "..", "bad\x00name"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			a := &Agent{}
			consulted := false
			a.SetInstalledSkillResolver(func(name, relPath string) (InstalledSkillFile, error) {
				consulted = true
				return InstalledSkillFile{Content: "x"}, nil
			})
			if _, err := a.readOneAttachedSkill(name, ""); err == nil {
				t.Fatal("an unsafe installed-skill name must be rejected")
			}
			if consulted {
				t.Fatal("the resolver must never be asked to interpret an unsafe skill name")
			}
		})
	}
}

// The reviewer's point: storing the resolver is not the contract -- being able
// to CALL read_skill is. Registration previously happened only via
// ensureSkillReaderTool when an attached skill was added, so a host that
// configured a resolver and attached nothing had a documented capability with
// no callable tool behind it. Serving installed-but-unattached skills is
// exactly the case where nothing may be attached.
func TestInstalledSkillResolverRegistersTheCallableReadSkillTool(t *testing.T) {
	a := &Agent{}
	if _, exists := a.lookupDirectTool(readSkillToolName); exists {
		t.Fatal("read_skill should not exist before anything configures it")
	}

	a.SetInstalledSkillResolver(func(name, relPath string) (InstalledSkillFile, error) {
		return InstalledSkillFile{Content: "body", Description: "d", AvailableFiles: []string{"SKILL.md"}}, nil
	})

	// No skill was ever attached -- this is the path that used to leave the
	// resolver unreachable.
	if len(a.attachedSkills) != 0 {
		t.Fatalf("precondition: no attached skills, got %d", len(a.attachedSkills))
	}
	if _, exists := a.lookupDirectTool(readSkillToolName); !exists {
		t.Fatal("read_skill is not registered, so the configured resolver is unreachable by the model")
	}

	// And it must be advertised over the bridge, or a coding agent still
	// cannot call it.
	advertised := false
	for _, name := range a.additionalBridgeTools {
		if name == readSkillToolName {
			advertised = true
			break
		}
	}
	if !advertised {
		t.Fatalf("read_skill missing from additionalBridgeTools: %v", a.additionalBridgeTools)
	}

	// Identity tools remain callable even when an owning workflow applies a
	// narrower agent or per-turn allowlist. Resolver-only agents have the same
	// read_skill identity contract as agents with attached skills.
	a.toolAllowList = map[string]bool{"some_other_tool": true}
	filtered := a.applyToolAllowList(a.tools)
	if len(filtered) != 1 || filtered[0].Function == nil || filtered[0].Function.Name != readSkillToolName {
		t.Fatalf("resolver-backed read_skill was filtered out: %#v", filtered)
	}
	policy, err := normalizeToolPolicy(ToolPolicy{AllowedTools: []string{"some_other_tool"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), turnPolicyContextKey{}, policy)
	if !a.isToolAllowedForContext(ctx, readSkillToolName) {
		t.Fatal("turn policy blocked resolver-backed read_skill")
	}
}

// A nil resolver must stay inert: it means "attached skills only", and
// registering a reader tool for it would change the surface of every existing
// caller that never set one.
func TestNilInstalledSkillResolverDoesNotRegisterAnything(t *testing.T) {
	a := &Agent{}
	a.SetInstalledSkillResolver(nil)
	if _, exists := a.lookupDirectTool(readSkillToolName); exists {
		t.Fatal("a nil resolver must not register read_skill")
	}
}
