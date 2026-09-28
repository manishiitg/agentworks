package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type playbookSearchResult struct {
	ID                   string                   `json:"id"`
	Title                string                   `json:"title"`
	Category             string                   `json:"category"`
	Description          string                   `json:"description"`
	Version              string                   `json:"version"`
	Changelog            []playbookChangelogEntry `json:"changelog,omitempty"`
	SetupAreas           []string                 `json:"setup_areas"`
	RequiredCapabilities []string                 `json:"required_capabilities"`
	Outputs              []string                 `json:"outputs"`
	RecommendedTools     []map[string]interface{} `json:"recommended_tools,omitempty"`
	PulseFocus           []map[string]interface{} `json:"pulse_focus,omitempty"`
	AgentSlots           []playbookAgentSlot      `json:"agent_slots,omitempty"`
	Handoffs             []playbookHandoff        `json:"handoffs,omitempty"`
	SetupChecks          []string                 `json:"setup_checks,omitempty"`
	Installed            bool                     `json:"installed"`
	InstalledStatus      string                   `json:"installed_status,omitempty"`
	InstalledVersion     string                   `json:"installed_version,omitempty"`
	UpdateAvailable      bool                     `json:"update_available"`
	Availability         string                   `json:"availability"`
	RequiredAction       string                   `json:"required_action"`
	Score                int                      `json:"-"`
}

func isNewerPlaybookVersion(candidate, installed string) bool {
	nextParts, currentParts := strings.Split(candidate, "."), strings.Split(installed, ".")
	if len(nextParts) != 3 || len(currentParts) != 3 {
		return candidate != installed
	}
	for index := 0; index < 3; index++ {
		next, nextErr := strconv.Atoi(nextParts[index])
		current, currentErr := strconv.Atoi(currentParts[index])
		if nextErr != nil || currentErr != nil {
			return candidate != installed
		}
		if next != current {
			return next > current
		}
	}
	return false
}

func searchPlaybooks(items []playbookCatalogItem, installed []InstalledPlaybook, query string, limit int) []playbookSearchResult {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	if limit < 1 || limit > 10 {
		limit = 6
	}
	installedByID := make(map[string]InstalledPlaybook, len(installed))
	for _, item := range installed {
		installedByID[item.ID] = item
	}
	tokens := strings.Fields(query)
	results := make([]playbookSearchResult, 0)
	for _, item := range items {
		setupAreas := make([]string, 0, len(item.SetupInputs))
		var searchable []string
		searchable = append(searchable, item.ID, item.Title, item.Category, item.Description)
		for _, input := range item.SetupInputs {
			if label, _ := input["label"].(string); strings.TrimSpace(label) != "" {
				setupAreas = append(setupAreas, label)
				searchable = append(searchable, label)
			}
		}
		searchable = append(searchable, item.RequiredCapabilities...)
		searchable = append(searchable, item.Outputs...)
		for _, slot := range item.AgentSlots {
			searchable = append(searchable, slot.ID, slot.AgentPlaybookID)
			searchable = append(searchable, slot.Accepts...)
		}
		for _, tool := range item.RecommendedTools {
			for _, key := range []string{"name", "purpose", "capability", "type"} {
				if value, _ := tool[key].(string); value != "" {
					searchable = append(searchable, value)
				}
			}
		}
		for _, focus := range item.PulseFocus {
			for _, key := range []string{"module", "label"} {
				if value, _ := focus[key].(string); value != "" {
					searchable = append(searchable, value)
				}
			}
			for _, key := range []string{"focus_areas", "review_when"} {
				if values, ok := focus[key].([]interface{}); ok {
					for _, raw := range values {
						if value, ok := raw.(string); ok {
							searchable = append(searchable, value)
						}
					}
				}
			}
		}
		corpus := strings.ToLower(strings.Join(searchable, " "))
		score := 0
		if strings.Contains(strings.ToLower(item.Title), query) || strings.Contains(strings.ToLower(item.ID), query) {
			score += 20
		} else if strings.Contains(corpus, query) {
			score += 10
		}
		for _, token := range tokens {
			if strings.Contains(strings.ToLower(item.Title), token) {
				score += 4
			} else if strings.Contains(corpus, token) {
				score += 2
			}
		}
		if score == 0 {
			continue
		}
		installedItem, isInstalled := installedByID[item.ID]
		availability, requiredAction := "catalog_only", "open_playbooks_view_and_wait_for_user_installation"
		if isInstalled {
			availability, requiredAction = "installed_in_this_workflow", "read_installed_skill_before_setup"
		}
		results = append(results, playbookSearchResult{
			ID: item.ID, Title: item.Title, Category: item.Category, Description: item.Description,
			Version: item.Version, Changelog: item.Changelog, SetupAreas: setupAreas, RequiredCapabilities: item.RequiredCapabilities,
			Outputs: item.Outputs, RecommendedTools: item.RecommendedTools, PulseFocus: item.PulseFocus,
			AgentSlots: item.AgentSlots, Handoffs: item.Handoffs, SetupChecks: item.SetupChecks, Installed: isInstalled,
			InstalledStatus: installedItem.Status, InstalledVersion: installedItem.Version,
			UpdateAvailable: isInstalled && isNewerPlaybookVersion(item.Version, installedItem.Version),
			Availability:    availability, RequiredAction: requiredAction, Score: score,
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].Category != results[j].Category {
			return results[i].Category < results[j].Category
		}
		return results[i].Title < results[j].Title
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

func (api *StreamingAPI) registerPlaybookSearchTool(registrar definitionToolRegistrar, workspacePath string) error {
	description := "Search the AgentWorks playbook catalog by the user's intent, operating area, capability, tool, or expected outcome. Results include setup areas, deliverables, recommended tools, proposed Crew slots and handoffs when applicable, installed and latest versions, and update availability. The catalog is platform-wide, but installation and read_skill availability are workflow-specific. This tool never installs anything. A catalog_only result is not usable in the current workflow: when the user asks to use that Playbook, do not recreate it manually; tell the user it is not installed here, perform_ui_action(action=\"open\", view=\"playbooks\"), and wait for installation. For an installed Playbook, read its skill, inspect the current workflow and existing Crews, and propose a concrete plan for review before executing reviewed actions through Builder tools. Playbook installation is not approval to create Crews, enable recurrence, publish, or send outreach."
	params := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{"type": "string", "description": "Plain-language workflow need, capability, area, tool, or desired outcome."},
			"limit": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 10, "default": 6},
		},
		"required": []string{"query"},
	}
	return registrar.RegisterCustomTool("search_playbooks", description, params, func(ctx context.Context, args map[string]interface{}) (string, error) {
		query, _ := args["query"].(string)
		if strings.TrimSpace(query) == "" {
			return "", fmt.Errorf("query is required")
		}
		limit := 6
		switch value := args["limit"].(type) {
		case float64:
			limit = int(value)
		case int:
			limit = value
		}
		items, err := loadPlaybookCatalog()
		if err != nil {
			return "", err
		}
		var installed []InstalledPlaybook
		if strings.TrimSpace(workspacePath) != "" {
			if manifest, found, readErr := ReadWorkflowManifest(ctx, workspacePath); readErr == nil && found {
				installed = manifest.InstalledPlaybooks
			}
		}
		matches := searchPlaybooks(items, installed, query, limit)
		payload := map[string]interface{}{
			"query": query, "matches": matches, "total": len(matches),
			"installation_scope":                              "workflow_specific",
			"installation_method":                             "The user installs or updates from the Playbooks view; search_playbooks cannot install.",
			"manual_fallback_allowed_when_playbook_requested": false,
			"next_action":                                     "For catalog_only: STOP, tell the user the playbook is not installed in this workflow, open the Playbooks view, and wait for installation. Do not recreate or apply the playbook manually. For installed_in_this_workflow: read the installed skill first. Then inspect the workflow, summarize reuse and gaps, ask focused questions for material unresolved customer choices, and record the answers before proposing changes. Installation is not approval to mutate or run the workflow.",
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}, "playbook_tools")
}
