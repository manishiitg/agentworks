package guidance

import (
	"fmt"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// MaterializePulseSkill builds the Pulse conversation's own skill pack ("pulse")
// from exactly the skills product.yaml lists (pulse.skills), instead of the
// Builder's whole reference pack.
func MaterializePulseSkill(names []string) (*llmtypes.Skill, error) {
	want := map[string]bool{}
	for _, name := range names {
		if _, ok := referenceKinds[name]; !ok {
			return nil, fmt.Errorf("pulse skill %q has no reference file", name)
		}
		want[name] = true
	}
	skill := buildMegaSkill(buildMegaSkillSpec{
		Registry:         referenceKinds,
		Name:             "pulse",
		DescriptionIntro: "Pulse's skills for owning a workflow's goal.",
		Intro:            "Load the matching skill before the work it covers. You read; the Builder chat acts.",
		Render:           renderReferenceKind,
		Select:           func(kind string, _ kindMeta) bool { return want[kind] },
	})
	if skill == nil {
		return nil, fmt.Errorf("pulse skill pack is empty")
	}
	return skill, nil
}

// isPulseSkillKind: Pulse's own skills, kept out of the Builder's reference pack.
func isPulseSkillKind(kind string) bool { return strings.HasPrefix(kind, "goal-lead-") }
