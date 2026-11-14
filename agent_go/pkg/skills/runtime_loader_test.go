package skills

import (
	"strings"
	"testing"
)

// PLAT-746-1026: a large skill read from a parent Crew (the Team Crew) must
// point the agent at read_skill, not at a folder it cannot open.
func TestLazySkillBodyPointsParentSkillsAtReadSkill(t *testing.T) {
	body := strings.Repeat("line\n", 200)
	own := lazySkillBody("skills/ship/SKILL.md", "ship", body)
	if !strings.Contains(own, "`skills/ship/SKILL.md`") {
		t.Fatalf("own skill should keep its file pointer: %q", own[len(own)-200:])
	}
	parent := lazySkillBody("Crew/team-source/skills/ship/SKILL.md", "ship", body)
	if !strings.Contains(parent, "read_skill") || strings.Contains(parent, "Crew/team-source") {
		t.Fatalf("parent skill should point at read_skill, not the source path: %q", parent[len(parent)-300:])
	}
}
