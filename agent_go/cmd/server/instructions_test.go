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

func TestNewWorkflowInstructionsUseModernPlanShape(t *testing.T) {
	out := GetWorkspaceReference("/tmp/workspace-docs", "_users/default/Chats")

	for _, want := range []string{
		"Plan-shape rule — use this for every new workflow",
		"one large `agent` per coherent shared-context span",
		"prove every criterion, repair gaps, and double-check",
		"Use multiple large sequences when their contexts should not be shared",
		"fetch-authoritative-data",
		`"type": "agent"`,
		"analysis_proof.json",
		"scripted-fetcher → large-agent structure",
		"Before the first production run",
		"10-run bar applies only before",
		"validation_schema` — required for every output-producing step",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("new-workflow instructions missing %q", want)
		}
	}

	for _, stale := range []string{
		"use `regular` by default",
		"single atomic action with no verify-and-fix follow-up",
		"one step per proof check",
		`"id": "step-one"`,
	} {
		if strings.Contains(out, stale) {
			t.Fatalf("new-workflow instructions retain stale plan guidance %q", stale)
		}
	}
}

// The always-on base prompt must describe learnings as reusable HOW-to-run
// knowledge, not "domain knowledge" — that is what knowledgebase holds. A
// mislabel here routes discovered facts into SKILL.md (see stores.md).
func TestWorkspaceReferenceLabelsLearningsAsHowNotDomainKnowledge(t *testing.T) {
	out := GetWorkspaceReference("/tmp/workspace-docs", "_users/default/Chats")

	for _, want := range []string{
		"Learnings (reusable HOW-to-run knowledge)",
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
