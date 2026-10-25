package server

import (
	"strings"
	"testing"
)

func TestWorkspaceMapForbidsWebFetchForLocalArtifacts(t *testing.T) {
	out := GetWorkspaceMap("/tmp/workspace-docs", "_users/default/Chats")

	mustContain := []string{
		"LOCAL paths RELATIVE to the docs root",
		"Never use WebFetch/raw GitHub URLs for workspace artifacts, skills, or reference docs",
		`read_skill(skills=[{"name":"builder-reference","path":"references/....md"}])`,
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Fatalf("workspace map missing local artifact guardrail %q", s)
		}
	}
}

// The always-on base prompt must describe learnings as reusable HOW-to-run
// knowledge, not "domain knowledge" — that is what knowledgebase holds. A
// mislabel here routes discovered facts into SKILL.md (see stores.md).
func TestWorkspaceReferenceLabelsLearningsAsHowNotDomainKnowledge(t *testing.T) {
	out := GetWorkspaceReference("/tmp/workspace-docs", "_users/default/Chats")

	for _, want := range []string{
		"reusable HOW-to-run knowledge for a workflow",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("base prompt missing correct learnings framing %q", want)
		}
	}
	for _, stale := range []string{
		"Learnings (accumulated knowledge)",
		"domain knowledge, conventions, patterns shared across all steps",
		"accumulated domain knowledge for a workflow",
	} {
		if strings.Contains(out, stale) {
			t.Fatalf("base prompt still mislabels learnings as domain knowledge: %q", stale)
		}
	}
}
