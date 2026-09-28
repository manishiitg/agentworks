package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWebsiteGrowthCrewCatalogHasInstallableSpecialists(t *testing.T) {
	ids := []string{
		"website-growth-starter", "seo-analyst", "search-opportunity-mapper",
		"content-brief-writer", "content-page-builder", "search-console-optimizer",
		"traffic-engagement-analyst", "ai-visibility-analyst",
		"landing-page-optimizer", "content-distribution-coordinator",
	}
	for _, id := range ids {
		item, err := loadCrewAgentTemplate(id)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if item.ID != id || item.Version != 1 || item.Role == "" || len(item.Files) != 3 {
			t.Fatalf("incomplete %s template: %+v", id, item)
		}
		var setup struct {
			TemplateID string            `json:"template_id"`
			Checks     []json.RawMessage `json:"checks"`
		}
		if err := json.Unmarshal([]byte(item.Files["templates/"+id+"/TEMPLATE_SETUP.json"]), &setup); err != nil {
			t.Fatalf("decode %s setup: %v", id, err)
		}
		if setup.TemplateID != id || len(setup.Checks) < 5 || len(setup.Checks) > 10 {
			t.Fatalf("invalid %s setup: %+v", id, setup)
		}
	}
	for id, expected := range map[string]string{
		"search-opportunity-mapper": "source_brief_artifact_id",
		"content-brief-writer":      "source_search_artifact_id",
		"content-page-builder":      "source_content_artifact_id",
	} {
		item, err := loadCrewAgentTemplate(id)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(item.Files["skills/"+id+"/SKILL.md"], expected) {
			t.Fatalf("%s does not describe exact %s handoff", id, expected)
		}
	}
	if _, err := loadCrewAgentTemplate("not-a-template"); err == nil {
		t.Fatal("unknown template was accepted")
	}
}

func TestFinanceCrewCatalogHasInstallableRoles(t *testing.T) {
	for _, id := range []string{"finance-analyst", "tax-export", "billing-operations-coordinator", "revenue-close-analyst", "spend-payables-coordinator"} {
		item, err := loadCrewAgentTemplate(id)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		setupPath := "templates/" + id + "/TEMPLATE_SETUP.json"
		if id == "finance-analyst" {
			setupPath = "TEMPLATE_SETUP.json"
		}
		var setup struct {
			TemplateID string            `json:"template_id"`
			Checks     []json.RawMessage `json:"checks"`
		}
		if err := json.Unmarshal([]byte(item.Files[setupPath]), &setup); err != nil {
			t.Fatalf("decode %s setup: %v", id, err)
		}
		if item.ID != id || len(item.Files) != 3 || setup.TemplateID != id || len(setup.Checks) < 5 || len(setup.Checks) > 10 {
			t.Fatalf("incomplete %s template: %+v, %+v", id, item, setup)
		}
	}
}

func TestSalesCrewCatalogHasInstallableRoles(t *testing.T) {
	for _, id := range []string{"lead-intake-qualifier", "account-researcher", "sales-followup-coordinator"} {
		item, err := loadCrewAgentTemplate(id)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		var setup struct {
			TemplateID string            `json:"template_id"`
			Checks     []json.RawMessage `json:"checks"`
		}
		if err := json.Unmarshal([]byte(item.Files["templates/"+id+"/TEMPLATE_SETUP.json"]), &setup); err != nil {
			t.Fatalf("decode %s setup: %v", id, err)
		}
		if item.ID != id || item.Version != 1 || len(item.Files) != 3 || setup.TemplateID != id || len(setup.Checks) != 9 {
			t.Fatalf("incomplete %s template: %+v, %+v", id, item, setup)
		}
	}
}
