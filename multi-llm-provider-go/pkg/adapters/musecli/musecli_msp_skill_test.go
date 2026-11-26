package musecli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestMuseSkillInputSendsThePickedSkillByName(t *testing.T) {
	home, _ := os.UserHomeDir()
	rows := []museSkillRow{{Selector: "fix-bug", Source: "project"}, {Selector: "fix-bug", Source: "user"}}
	list := func(context.Context, string) ([]museSkillRow, error) { return rows, nil }

	if part, err := museSkillInput(context.Background(), "chat", "s1", list); part != nil || err != nil {
		t.Fatalf("no skill picked: got %v, %v", part, err)
	}

	userPath := filepath.Join(home, ".agents", "skills", "fix-bug", "SKILL.md")
	ctx := llmtypes.WithNativeSkill(context.Background(), "chat", llmtypes.NativeSkill{Name: "fix-bug", Path: userPath})
	part, err := museSkillInput(ctx, "chat", "s1", list)
	if err != nil || part["type"] != "skill" || part["selector"] != "fix-bug" || part["source"] != "user" {
		t.Fatalf("user skill: got %v, %v", part, err)
	}
	if again, _ := museSkillInput(ctx, "chat", "s1", list); again != nil {
		t.Fatalf("the pick belongs to one message only, got %v", again)
	}

	projectCtx := llmtypes.WithNativeSkill(context.Background(), "chat", llmtypes.NativeSkill{Name: "fix-bug", Path: "/work/.agents/skills/fix-bug/SKILL.md"})
	if part, _ := museSkillInput(projectCtx, "chat", "s1", list); part["source"] != "project" {
		t.Fatalf("project skill: got %v", part)
	}

	missing := llmtypes.WithNativeSkill(context.Background(), "chat", llmtypes.NativeSkill{Name: "other", Path: "/work/.agents/skills/other/SKILL.md"})
	if _, err := museSkillInput(missing, "chat", "s1", list); err == nil || !strings.Contains(err.Error(), "does not have the skill") {
		t.Fatalf("unknown skill: got %v", err)
	}
}
