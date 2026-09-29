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
	for _, tool := range []string{"list_functions", "call_function", "get_function_call", "reply_function_call", "define_function", "report_function_progress", "return_function_result"} {
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
	// No built-in Gmail/Google accounts in a Code: it reaches Google through
	// MCP servers only (user, 2026-09-29).
	if agentprofiles.FeatureOption(profile, "bots", "gmail") != "" {
		t.Fatal("Code must not enable built-in Gmail (bots gmail)")
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
