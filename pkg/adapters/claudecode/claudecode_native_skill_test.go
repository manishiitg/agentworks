package claudecode

import (
	"context"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"testing"
)

// PLAT-746-1008: the SDK wire receives one explicit skill request, not a
// prompt claiming it already ran. Controls retain the exact session fence.
func TestClaudeNativeSkillPromptAndSteerKeepExactSession(t *testing.T) {
	owner := "skill-chat"
	wire := &sdkImageFrameWriter{}
	process := &claudeSDKProcess{session: &claudeSDKSession{owner: owner, input: &claudeSDKInput{w: wire}, kill: func() {}}, done: make(chan struct{})}
	wire.process = process
	skill := llmtypes.NativeSkill{Name: "review", Path: "/project/.claude/skills/review/SKILL.md"}
	ctx := llmtypes.WithNativeSkill(context.Background(), owner, skill)
	params, cleanup, err := claudeSDKParamsWithImages(ctx, owner, map[string]any{"type": "prompt", "text": "Review changes"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err = process.write(params); err != nil {
		t.Fatal(err)
	}
	if err = process.steer(llmtypes.WithNativeSkill(context.Background(), owner, skill), "Also inspect tests"); err != nil {
		t.Fatal(err)
	}
	if len(wire.frames) != 2 {
		t.Fatal("native input missing")
	}
	for _, frame := range wire.frames {
		if frame["skill_name"] != "review" {
			t.Fatal("native skill request missing")
		}
	}
	params, cleanup, err = claudeSDKParamsWithImages(ctx, owner, map[string]any{"type": "prompt", "text": "Later generation"}, nil)
	cleanup()
	if err != nil || params.(map[string]any)["skill_name"] != nil {
		t.Fatal("later generation replayed skill")
	}
	params, cleanup, err = claudeSDKParamsWithImages(llmtypes.WithNativeSkill(context.Background(), owner, skill), "child", map[string]any{"type": "prompt", "text": "Child"}, nil)
	cleanup()
	if err != nil || params.(map[string]any)["skill_name"] != nil {
		t.Fatal("child inherited skill")
	}
}
