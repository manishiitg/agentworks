package guidance

import (
	"strings"
	"testing"
)

// TestCanonicalPlanningDocsDistinguishBranchFromRouting is the guidance-
// contract test a third independent PLAT-259 review required: the canonical
// planning entry points must actually offer `branch` as an alternative to
// `routing` for a fixed choice, not just document `branch` in its own
// dedicated reference doc. The review found several of these docs still told
// the builder to use deterministic `routing` for every fixed branch choice,
// which silently defeats the whole point of PLAT-259's `branch` type (the
// small in-flow decision) -- new plans could keep producing `routing` steps
// for decisions `branch` was introduced to represent.
func TestCanonicalPlanningDocsDistinguishBranchFromRouting(t *testing.T) {
	// A doc documents a type as an actual step-type option (not just using
	// "branch"/"routing" as ordinary English) when it names the type
	// directly -- as a backtick-quoted type name, part of a tool name, or a
	// bolded type heading.
	hasStepType := func(doc, typeName string) bool {
		for _, marker := range []string{
			"`" + typeName + "`",
			"_" + typeName + "_step",
			"**" + strings.ToUpper(typeName[:1]) + typeName[1:] + "**",
			"add_" + typeName + "_step",
		} {
			if strings.Contains(doc, marker) {
				return true
			}
		}
		return false
	}

	for _, kind := range []string{"plan-design", "planning-steps", "agent", "scripted", "workflow-tools"} {
		doc := RenderSystemDoc(kind)
		if !hasStepType(doc, "branch") {
			t.Errorf("%s.md never documents `branch` as a real step-type option -- a fixed choice reader would only ever be told to use `routing`", kind)
		}
		if !hasStepType(doc, "routing") {
			t.Errorf("%s.md never documents `routing` as a real step-type option", kind)
		}
	}
}
