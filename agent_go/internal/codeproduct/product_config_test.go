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
	// Basic setup: MCP servers come later; chat apps are 1:1 bots only.
	for _, feature := range []string{"triggers", "schedules", "voice", "mcp"} {
		if agentprofiles.HasFeature(profile, feature) {
			t.Fatalf("Code must not enable %s", feature)
		}
	}
	for _, feature := range []string{"live-chat", "coding", "files", "terminal", "skills", "secrets", "attached-folders", "browser", "workflow-references", "dashboard", "database", "memory", "costs", "background-work", "models", "bots"} {
		if !agentprofiles.HasFeature(profile, feature) {
			t.Fatalf("Code must enable %s", feature)
		}
	}
	for _, tool := range profile.ToolPolicy.Enabled {
		switch tool {
		case "set_work_identity", "define_function", "return_function_result", "report_function_progress",
			"create_project_trigger", "create_project_schedule", "create_slack_bot_route", "update_gmail_connection_grants_shared":
			t.Fatalf("Code enables forbidden tool %s", tool)
		}
	}
	for _, binding := range profile.Tools {
		if strings.HasPrefix(binding.ID, "work.") {
			t.Fatalf("Code binds Crew tool %s", binding.ID)
		}
	}
	// Google tools stay, for this Code's own private accounts (gmail: own);
	// scoping is enforced at use (services.GmailUseScope).
	if agentprofiles.FeatureOption(profile, "bots", "gmail") != "own" {
		t.Fatal("Code's Google accounts must be its own (bots gmail: own)")
	}
	if profile.Runtime.AgentTools.Mode != "mcp_only" {
		t.Fatalf("Code must default to MCP-only agent tools until native reads are sandboxed, got %q", profile.Runtime.AgentTools.Mode)
	}
	if profile.Runtime.Workspace.ProjectsRoot != ProjectsRoot {
		t.Fatalf("projects_root = %q", profile.Runtime.Workspace.ProjectsRoot)
	}
	for _, word := range []string{"Crew member", "identity", "define_function", "WORK_IDENTITY"} {
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
