package guidance

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var topicBracePattern = regexp.MustCompile(`((?:[a-z0-9]+-)+)\{([^}]*)\}`)

var builderReferenceReadPattern = regexp.MustCompile(`"name":"builder-reference","path":"(references/[^"]+\.md)"`)

func TestMegaSkillDiscoveryFitsFormatAndKeepsEveryTopic(t *testing.T) {
	check := func(t *testing.T, skill *llmtypes.Skill) {
		t.Helper()
		if skill == nil {
			return
		}
		if n := utf8.RuneCountInString(skill.Description); n == 0 || n > 1024 {
			t.Fatalf("%s description has %d characters; skill metadata must have 1–1024", skill.Name, n)
		}
		if strings.ContainsAny(skill.Description, "<>") {
			t.Fatalf("%s description contains angle brackets rejected by skill validators", skill.Name)
		}
		// Topics sharing a first word are written once: pulse-{gate,finalizer}.
		discoverable := topicBracePattern.ReplaceAllStringFunc(skill.Description, func(group string) string {
			parts := topicBracePattern.FindStringSubmatch(group)
			names := strings.Split(parts[2], ",")
			for i, rest := range names {
				names[i] = parts[1] + rest
			}
			return strings.Join(names, ",")
		})
		for _, file := range skill.SupportingFiles {
			topic := strings.TrimSuffix(strings.TrimPrefix(file.RelPath, "references/"), ".md")
			if !strings.Contains(discoverable, topic) {
				t.Errorf("%s is missing from %s discovery metadata", topic, skill.Name)
			}
			if !strings.Contains(skill.Content, file.RelPath) || len(file.Content) == 0 {
				t.Errorf("%s must remain readable through the full reference index", file.RelPath)
			}
		}
	}
	for _, mode := range []string{"workshop", "multi-agent", "run", ""} {
		t.Run(mode, func(t *testing.T) {
			check(t, MaterializeReferenceSkill(mode))
			check(t, MaterializeGuidanceSkill(mode))
		})
	}
	var tools []string
	for _, meta := range referenceKinds {
		tools = append(tools, meta.Tools...)
	}
	t.Run("step with all capabilities", func(t *testing.T) {
		check(t, MaterializeStepExecutionReferenceSkill(StepExecutionSignals{
			ToolNames: tools, CodeExecutionMode: true, ScriptedStep: true,
		}))
	})
}

// Guidance may name a reference only through read_skill. The reference bundle
// is the authority for those paths, so a deleted or renamed file must fail in
// tests rather than reaching an agent as an opaque runtime error.
func TestGuidanceDoesNotRequestMissingBuilderReferenceFile(t *testing.T) {
	available := map[string]bool{}
	for _, mode := range []string{"workshop", "multi-agent"} {
		bundle := MaterializeReferenceSkill(mode)
		if bundle == nil {
			t.Fatalf("expected %s builder-reference bundle", mode)
		}
		for _, file := range bundle.SupportingFiles {
			available[file.RelPath] = true
		}
	}

	registries := []map[string]kindMeta{allKinds, referenceKinds}
	for _, registry := range registries {
		for kind := range registry {
			rendered, err := renderFromRegistry(kind, tmplData{}, registry)
			if err != nil {
				t.Errorf("render %s: %v", kind, err)
				continue
			}
			for _, match := range builderReferenceReadPattern.FindAllStringSubmatch(rendered, -1) {
				if strings.Contains(match[1], "<") {
					continue // documented placeholder, not a literal request
				}
				if !available[match[1]] {
					t.Errorf("%s requests %q from builder-reference, but that file is not bundled", kind, match[1])
				}
			}
		}
	}
}
