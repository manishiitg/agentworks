package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSearchPlaybooksFindsIntentAndReportsInstalledStatus(t *testing.T) {
	items, err := loadPlaybookCatalog()
	if err != nil {
		t.Fatal(err)
	}
	matches := searchPlaybooks(items, []InstalledPlaybook{{ID: "browser-performance-validation", Version: "0.2.0", Status: "draft"}}, "browser performance budgets", 3)
	if len(matches) == 0 || matches[0].ID != "browser-performance-validation" {
		t.Fatalf("matches = %+v", matches)
	}
	if !matches[0].Installed || matches[0].InstalledStatus != "draft" || matches[0].InstalledVersion != "0.2.0" || !matches[0].UpdateAvailable || len(matches[0].Changelog) == 0 || len(matches[0].Outputs) == 0 || len(matches[0].SetupAreas) == 0 || len(matches[0].PulseFocus) != 1 {
		t.Fatalf("incomplete result = %+v", matches[0])
	}
	if module, _ := matches[0].PulseFocus[0]["module"].(string); module != pulseModuleStrategicReview {
		t.Fatalf("expected strategic-only playbook focus, got %#v", matches[0].PulseFocus)
	}
	if matches[0].Availability != "installed_in_this_workflow" || matches[0].RequiredAction != "read_installed_skill_before_setup" {
		t.Fatalf("installed availability = %+v", matches[0])
	}
}

func TestSearchPlaybooksReturnsWebsiteGrowthTeamProposal(t *testing.T) {
	items, err := loadPlaybookCatalog()
	if err != nil {
		t.Fatal(err)
	}
	matches := searchPlaybooks(items, nil, "website growth loop", 3)
	if len(matches) == 0 || matches[0].ID != "website-growth-loop" {
		t.Fatalf("matches = %+v", matches)
	}
	if len(matches[0].AgentSlots) != 4 || len(matches[0].Handoffs) != 3 || len(matches[0].SetupChecks) != 9 {
		t.Fatalf("incomplete Website Growth proposal = %+v", matches[0])
	}
}

func TestPlaybookSearchToolIsRecommendationOnly(t *testing.T) {
	api := &StreamingAPI{}
	reg := &recordingRegistrar{}
	if err := api.registerPlaybookSearchTool(reg, ""); err != nil {
		t.Fatal(err)
	}
	tool, ok := reg.tools["search_playbooks"]
	if !ok || !strings.Contains(tool.desc, "never installs") || !strings.Contains(tool.desc, "workflow-specific") || !strings.Contains(tool.desc, "do not recreate it manually") || !strings.Contains(tool.desc, "perform_ui_action") || !strings.Contains(tool.desc, "inspect the current workflow") || !strings.Contains(tool.desc, "installation is not approval") {
		t.Fatalf("tool = %+v", tool)
	}
	out, err := tool.exec(context.Background(), map[string]interface{}{"query": "cost anomalies", "limit": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Matches           []playbookSearchResult `json:"matches"`
		InstallationScope string                 `json:"installation_scope"`
		ManualFallback    bool                   `json:"manual_fallback_allowed_when_playbook_requested"`
		NextAction        string                 `json:"next_action"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Matches) == 0 || payload.Matches[0].ID != "cost-anomaly-to-verified-savings" {
		t.Fatalf("out = %s", out)
	}
	if payload.Matches[0].Availability != "catalog_only" || payload.Matches[0].RequiredAction != "open_playbooks_view_and_wait_for_user_installation" || payload.InstallationScope != "workflow_specific" || payload.ManualFallback || !strings.Contains(payload.NextAction, "STOP") {
		t.Fatalf("ambiguous installation boundary: %s", out)
	}
}
