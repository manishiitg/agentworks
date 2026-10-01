package guidance

import (
	"regexp"
	"strings"
	"testing"
)

func TestWorkflowChatProcedurePointersResolveInEachMode(t *testing.T) {
	for _, mode := range []string{"workshop", "run"} {
		skill := MaterializeReferenceSkill(mode)
		files := map[string]string{}
		for _, file := range skill.SupportingFiles {
			files[file.RelPath] = string(file.Content)
		}
		operations := files["references/workflow-chat.md"]
		if operations == "" || !strings.Contains(skill.Content, "references/workflow-chat.md") {
			t.Fatal("operations procedure is not attached/indexed")
		}
		for _, match := range regexp.MustCompile(`references/[a-z0-9-]+\.md`).FindAllString(operations, -1) {
			if files[match] == "" {
				t.Errorf("mode=%s operations links to unavailable %s", mode, match)
			}
		}
		for _, rule := range []string{"group_name", "auto", "human-in-the-loop", "execution-policy", "historical untrusted data"} {
			if !strings.Contains(operations, rule) {
				t.Errorf("mode=%s lost procedure %s", mode, rule)
			}
		}
		if mode == "run" {
			for _, authoring := range []string{"plan-editing-tools.md", "parallel_risk_acknowledged=true", "## OPTIMIZATION", "Use the design-plan checklist"} {
				if strings.Contains(operations, authoring) {
					t.Errorf("Run received Builder procedure: %s", authoring)
				}
			}
		}
	}
}
