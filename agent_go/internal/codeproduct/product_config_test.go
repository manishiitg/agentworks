package codeproduct

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestCodeProfileIsAPrivateSubsetOfCrewFeatures(t *testing.T) {
	profile := BuiltinAgentProfile()
	if err := agentprofiles.ResolveFeatures(&profile); err != nil {
		t.Fatal(err)
	}
	// No voice or project Memory; chat apps are 1:1 bots only. Schedules and
	// triggers are message-only, owner-created (user, 2026-09-29).
	for _, feature := range []string{"voice", "memory"} {
		if agentprofiles.HasFeature(profile, feature) {
			t.Fatalf("Code must not enable %s", feature)
		}
	}
	for _, feature := range []string{"live-chat", "coding", "files", "terminal", "skills", "secrets", "attached-folders", "browser", "workflow-references", "dashboard", "database", "costs", "mcp", "background-work", "models", "bots", "schedules", "triggers"} {
		if !agentprofiles.HasFeature(profile, feature) {
			t.Fatalf("Code must enable %s", feature)
		}
	}
	for _, tool := range profile.ToolPolicy.Enabled {
		switch tool {
		case "set_work_identity", "create_slack_bot_route", "update_gmail_connection_grants_shared":
			t.Fatalf("Code enables forbidden tool %s", tool)
		}
	}
	for _, tool := range []string{"list_functions", "call_function", "get_function_call", "reply_function_call", "report_function_progress", "return_function_result"} {
		found := false
		for _, enabled := range profile.ToolPolicy.Enabled {
			if enabled == tool {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Code cannot use private peer function tool %s", tool)
		}
	}
	for _, binding := range profile.Tools {
		if strings.HasPrefix(binding.ID, "work.") {
			t.Fatalf("Code binds Crew tool %s", binding.ID)
		}
	}
	// Google tools stay, for this Code's own private accounts (gmail: own);
	// scoping is enforced at use (services.GmailUseScope). Restored 2026-09-30
	// after being switched off on 2026-09-29.
	if agentprofiles.FeatureOption(profile, "bots", "gmail") != "own" {
		t.Fatal("Code's Google accounts must be its own (bots gmail: own)")
	}
	if profile.Runtime.AgentTools.Mode != "mcp_only" {
		t.Fatalf("Code must default to MCP-only agent tools until native reads are sandboxed, got %q", profile.Runtime.AgentTools.Mode)
	}
	if profile.Runtime.Workspace.ProjectsRoot != ProjectsRoot {
		t.Fatalf("projects_root = %q", profile.Runtime.Workspace.ProjectsRoot)
	}
	for _, word := range []string{"Crew member", "identity", "WORK_IDENTITY"} {
		if strings.Contains(profile.SystemPromptTemplate, word) {
			t.Fatalf("Code prompt contains Crew vocabulary %q", word)
		}
	}
}

func TestNewProjectFilesHaveANameOnly(t *testing.T) {
	path, _, product := NewProjectFiles("0123456789abcdef", "My App!", timeZero)
	if path != "Chats/Code/projects/my-app-01234567" {
		t.Fatalf("path = %q", path)
	}
	if product["product"] != "code" || product["session_id"] != "code:project:0123456789abcdef" {
		t.Fatalf("product manifest = %v", product)
	}
	for _, key := range []string{"identity", "description"} {
		if _, ok := product[key]; ok {
			t.Fatalf("Code manifest carries %s", key)
		}
	}
}

// A Code's MCP connections are personal: its agent gets manage_my_mcp_servers
// and none of the tools that create, edit, remove or select connections shared
// with everyone (an admin's "connect my Gmail" must never share their account),
// nor the shared-connection skill.
func TestCodeMCPIsPersonalOnly(t *testing.T) {
	profile := BuiltinAgentProfile()
	if err := agentprofiles.ResolveFeatures(&profile); err != nil {
		t.Fatal(err)
	}
	enabled := map[string]bool{}
	for _, tool := range profile.ToolPolicy.Enabled {
		enabled[tool] = true
	}
	if !enabled["manage_my_mcp_servers"] {
		t.Fatal("Code lost its personal MCP tool")
	}
	for _, tool := range []string{"install_mcp_server", "add_mcp_server", "remove_mcp_server", "update_project_mcp_server_selection", "list_mcp_servers", "search_mcp_catalog", "get_mcp_server_logs", "trigger_mcp_discovery"} {
		if enabled[tool] {
			t.Fatalf("Code can use the shared-connection tool %s", tool)
		}
	}
	hasSkill := false
	for _, skill := range profile.Skills {
		hasSkill = hasSkill || skill == "code-mcp"
		if skill == "work-mcp" {
			t.Fatalf("Code attaches Crew's MCP skill %s", skill)
		}
	}
	if !hasSkill {
		t.Fatalf("Code has no MCP skill: %v", profile.Skills)
	}
	guidance := strings.Join(agentprofiles.FeaturePromptExtensions(profile), "\n")
	if !strings.Contains(guidance, "Personal MCP connections") || !strings.Contains(guidance, "Only the owner may connect") || strings.Contains(guidance, "manage_my_mcp_servers") {
		t.Fatalf("Code's MCP guidance is not the personal one: %s", guidance)
	}
}
