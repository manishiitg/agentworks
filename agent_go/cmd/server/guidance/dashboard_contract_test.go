package guidance

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/dashboardguidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// Exercise the actual product loaders and agent-facing bundles, not just the
// include helper: changes to the common file must reach every transport intact.
func TestDashboardContractReachesProductBundles(t *testing.T) {
	if err := workproduct.RegisterProductSkills(); err != nil {
		t.Fatal(err)
	}
	if err := codeproduct.RegisterProductSkills(); err != nil {
		t.Fatal(err)
	}
	check := func(name, text string) {
		t.Helper()
		if strings.Count(text, dashboardguidance.Content()) != 1 {
			t.Fatalf("%s does not contain exactly one intact shared Dashboard contract", name)
		}
		if strings.Contains(text, "<!-- dashboard:shared -->") {
			t.Fatalf("%s leaked an unexpanded include", name)
		}
	}
	for _, name := range []string{"work-dashboard", "code-dashboard"} {
		loaded := skills.LoadAttachable("", []string{name})
		if len(loaded) != 1 {
			t.Fatalf("%s did not load", name)
		}
		check(name, loaded[0].Content)
		if !strings.Contains(loaded[0].Content, "Do not create a workflow") {
			t.Fatalf("%s lost the project execution boundary", name)
		}
		foundMotion := false
		for _, file := range loaded[0].SupportingFiles {
			foundMotion = foundMotion || file.RelPath == "references/motion-guide.md"
		}
		if !foundMotion {
			t.Fatalf("%s lost its supporting reference", name)
		}
	}
	relaySkills, err := relayproduct.ChatSkills("builder")
	if err != nil {
		t.Fatal(err)
	}
	foundRelay := false
	for _, skill := range relaySkills {
		if skill.Name != "relay-dashboard" {
			continue
		}
		foundRelay = true
		check(skill.Name, skill.Content)
		if !strings.Contains(skill.Content, "independent of `relay.py`") || strings.Contains(skill.Content, "Run `/setup-goals`") {
			t.Fatal("Relay dashboard lost its execution boundary or inherited workflow goals")
		}
		external, ok := relayproduct.ExternalSkill(skill.Name)
		if !ok || external != skill.Content {
			t.Fatal("external Relay agents receive a different Dashboard contract")
		}
	}
	if !foundRelay {
		t.Fatal("Relay Builder did not attach its Dashboard skill")
	}
	check("RenderSystemDoc", RenderSystemDoc("reporting-policy"))
	for _, mode := range []string{"workshop", "run"} {
		bundle := MaterializeReferenceSkill(mode)
		if bundle == nil {
			t.Fatalf("%s has no reference bundle", mode)
		}
		found := false
		for _, file := range bundle.SupportingFiles {
			if file.RelPath != "references/reporting-policy.md" {
				continue
			}
			found = true
			text := string(file.Content)
			check(mode, text)
			if !strings.Contains(text, "Run mode never authors Dashboard pages") || !strings.Contains(text, "workflow_goal_metrics") {
				t.Fatalf("%s lost workflow-specific boundaries/metrics", mode)
			}
			if !strings.Contains(text, "{{dashboard_assets}}") {
				t.Fatalf("%s evaluated the managed-revision placeholder prematurely", mode)
			}
		}
		if !found {
			t.Fatalf("%s did not package reporting-policy", mode)
		}
	}
}
