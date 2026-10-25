package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlaybookReinstallPreservesProgressAndUpdateArchivesEvidence(t *testing.T) {
	item, err := findPlaybook("website-growth-loop")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	ctx := context.Background()
	const workspace = "Workflow/growth"
	const skill = "agentworks-playbook-website-growth-loop"
	const setupPath = workspace + "/skills/" + skill + "/SETUP.json"
	originalHash, err := installPlaybookSkill(ctx, workspace, skill, item)
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"scripts/validate_growth_artifact.py", "examples/content-brief.json", "examples/invalid-content-brief.json", "examples/reviewable-page-draft.json", "examples/invalid-page-draft.json"} {
		if mock.files[workspace+"/skills/"+skill+"/"+relative] == "" {
			t.Fatalf("Website Growth installation lacks %s", relative)
		}
	}
	var progress map[string]interface{}
	if err := json.Unmarshal([]byte(mock.files[setupPath]), &progress); err != nil {
		t.Fatal(err)
	}
	progress["completed_steps"] = []string{"goal_owner"}
	progress["evidence"] = map[string]string{"goal_owner": "Owner reviewed the actual website and goal"}
	saved, _ := json.Marshal(progress)
	mock.files[setupPath] = string(saved)
	retryHash, err := installPlaybookSkill(ctx, workspace, skill, item)
	if err != nil {
		t.Fatal(err)
	}
	if retryHash != originalHash || mock.files[setupPath] != string(saved) {
		t.Fatal("reinstall changed the source hash or erased customer setup progress")
	}
	// Emulate an older installed version; update must preserve the evidence
	// before replacing the working checklist with the current source.
	progress["playbook_version"] = "0.2.0"
	saved, _ = json.Marshal(progress)
	mock.files[setupPath] = string(saved)
	if _, err := installPlaybookSkill(ctx, workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	var updated struct {
		Previous  string   `json:"previous_setup_path"`
		Completed []string `json:"completed_steps"`
	}
	if err := json.Unmarshal([]byte(mock.files[setupPath]), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Previous == "" || mock.files[updated.Previous] != string(saved) || len(updated.Completed) != 0 {
		t.Fatal("update lost earlier evidence or carried unreviewed checks into the new version")
	}
}

func TestSEOIntelligenceInstallationCopiesTeamContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("seo-intelligence")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/seo"
	const skill = "agentworks-playbook-seo-intelligence"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/seo-issue-list.json", "examples/seo-opportunity-list.json", "examples/invalid-seo-opportunity-list.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("SEO Intelligence installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
		Checks     []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 || len(setup.Checks) != 10 {
		t.Fatalf("SEO Intelligence setup is not pending with ten checks: %+v", setup)
	}
}

func TestWorkflowGuideInstallationDoesNotClaimCrewSetup(t *testing.T) {
	item, err := findPlaybook("basic-browser-setup")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const base = "Workflow/browser/skills/agentworks-playbook-basic-browser-setup/"
	if _, err := installPlaybookSkill(context.Background(), "Workflow/browser", "agentworks-playbook-basic-browser-setup", item); err != nil {
		t.Fatal(err)
	}
	if mock.files[base+"SKILL.md"] == "" || mock.files[base+"playbook.json"] == "" {
		t.Fatal("Workflow guide was not installed")
	}
	if _, hasSetup := mock.files[base+"SETUP.json"]; hasSetup {
		t.Fatal("Workflow guide unexpectedly has a tracked Crew setup checklist")
	}
}

func TestRewriteExternalPlaybookLinksMaterializesSharedReferences(t *testing.T) {
	item, err := findPlaybook("basic-browser-setup")
	if err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(item.SourceDir, "SKILL.md")
	content, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	external := map[string]string{}
	rewritten := rewriteExternalPlaybookLinks(string(content), skillPath, item.SourceDir, external)
	if strings.Contains(rewritten, "](../references/") {
		t.Fatalf("shared links were not rewritten:\n%s", rewritten)
	}
	if len(external) != 3 {
		t.Fatalf("materialized references = %v, want three", external)
	}
}

func TestInstalledBuilderSkillNamesExcludesDisabledAndDuplicates(t *testing.T) {
	names := installedBuilderSkillNames([]InstalledPlaybook{
		{SkillName: "basic-browser-setup", Status: "draft"},
		{SkillName: "basic-browser-setup", Status: "ready"},
		{SkillName: "disabled-guide", Status: "disabled"},
		{SkillName: "", Status: "ready"},
	})
	if len(names) != 1 || names[0] != "basic-browser-setup" {
		t.Fatalf("builder skills = %v", names)
	}
}
