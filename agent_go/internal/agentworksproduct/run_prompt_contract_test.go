package agentworksproduct

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
)

// Run mode must be told to consult what the workflow knows and how to turn a
// request into a run (RTS 2026-09-25 review: learnings/KB were optional,
// route matching and per-run values were not described).
func TestRunPromptCoversKnowledgeAndRouteSelection(t *testing.T) {
	raw, err := productConfigFiles.ReadFile("prompts/run.md")
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(raw)
	if !strings.Contains(prompt, "references/workflow-chat.md") || !strings.Contains(prompt, "Do not edit plan/config") {
		t.Fatal("Run must retain its mode limit and procedure trigger")
	}
	procedure, err := guidance.RenderReferenceKindForTest("workflow-chat", "run")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"learnings/_global/SKILL.md",
		"learnings/<step-id>/",
		"Attached knowledge bases",
		"route_selections",
		"run_full_workflow` `variables`",
		"ask for exactly that value",
		"your reply is the answer",
		"Do not answer with \"see report.md\"",
	} {
		if !strings.Contains(procedure, want) {
			t.Fatalf("Run operations skill no longer says %q", want)
		}
	}
}
