package agentprofiles

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

func TestEmbeddedSkillDiscoveryDescriptionHasOneSource(t *testing.T) {
	files := fstest.MapFS{"skill/SKILL.md": {Data: []byte("---\nname: example\ndescription: Read before {{product}} scheduling or recurring work.\n---\n\nBODY\n")}}
	for _, tc := range []struct{ name, override, want string }{
		{"discovery-frontmatter-test", "", "Read before Example scheduling or recurring work."},
		{"discovery-override-test", "Explicit variant", "Explicit variant"},
	} {
		if err := RegisterEmbeddedSkillsRendered(files, []SkillFileBinding{{Name: tc.name, Description: tc.override, Path: "skill/SKILL.md"}}, func(s string) string { return strings.ReplaceAll(s, "{{product}}", "Example") }); err != nil {
			t.Fatal(err)
		}
		loaded := skills.LoadAttachable("", []string{tc.name})
		if len(loaded) != 1 || loaded[0].Description != tc.want {
			t.Fatalf("description: %#v", loaded)
		}
		if strings.Contains(loaded[0].Content, "description:") || !strings.Contains(loaded[0].Content, "BODY") {
			t.Fatal("frontmatter was not separated from body")
		}
	}
	bad := fstest.MapFS{"bad/SKILL.md": {Data: []byte("---\ndescription: [\n---\nBODY")}}
	if err := RegisterEmbeddedSkills(bad, []SkillFileBinding{{Name: "bad-discovery", Path: "bad/SKILL.md"}}); err == nil {
		t.Fatal("invalid discovery metadata accepted")
	}
}
