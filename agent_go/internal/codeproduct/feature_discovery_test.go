package codeproduct

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

func TestCodeSkillOptionsStayPrivateAndRefreshWithoutMutatingBuiltins(t *testing.T) {
	if err := RegisterProductSkills(); err != nil {
		t.Fatal(err)
	}
	profile := BuiltinAgentProfile()
	original := skills.LoadAttachable("", []string{"code-schedules-and-bots", "code-workflow-files", "code-mcp"})
	if len(original) != 3 {
		t.Fatal("missing feature skill")
	}
	current := agentprofiles.FeatureSkillsForSession(profile, original)
	for i, skill := range current {
		switch skill.Name {
		case "code-schedules-and-bots":
			for _, want := range []string{"Direct-message bots", "private project account", "install_skill", "https://github.com/openclaw/gogcli", "owner's chats", "compose grant", "setup_gmail_inbound", "## Incoming Gmail triggers:", "Google consent"} {
				if !strings.Contains(skill.Content, want) {
					t.Fatalf("private Google/DM procedure missing %q", want)
				}
			}
			for _, absent := range []string{"create_slack_bot_route", "send_slack_message", "shared account connection", "configuration remains account-wide"} {
				if strings.Contains(skill.Content, absent) {
					t.Fatalf("unavailable bot procedure advertised: %q", absent)
				}
			}
			if original[i].Content == skill.Content {
				t.Fatal("did not apply dynamic options")
			}
		case "code-workflow-files":
			if !strings.Contains(skill.Description, "same-owner Codes") || !strings.Contains(skill.Content, "cannot call this Code") {
				t.Fatal("Code peer boundary missing from skill")
			}
		case "code-mcp":
			if !strings.Contains(skill.Description, "tool is missing or fails") || !strings.Contains(skill.Content, "search_tools(query=") || !strings.Contains(skill.Content, "exact runtime `server_name`") || strings.Contains(skill.Content, "works right away") {
				t.Fatal("canonical MCP discovery/timing contract missing")
			}
		}
	}
	// Changing feature options renders a new variant from the original bundle.
	profile.ResolvedFeatures = append([]agentprofiles.ResolvedFeature(nil), profile.ResolvedFeatures...)
	for i := range profile.ResolvedFeatures {
		if profile.ResolvedFeatures[i].ID == "bots" {
			profile.ResolvedFeatures[i].Options = map[string]string{}
		}
	}
	next := agentprofiles.FeatureSkillsForSession(profile, original)
	for i, skill := range next {
		if skill.Name == "code-schedules-and-bots" && skill.Content != original[i].Content {
			t.Fatal("previous session mutated the globally registered skill")
		}
	}
}

func TestCodeAlwaysLoadedFeaturePoliciesAreCompact(t *testing.T) {
	profile := BuiltinAgentProfile()
	current := strings.Join(agentprofiles.FeaturePromptExtensions(profile), "\n\n")
	var previous strings.Builder
	for _, feature := range profile.ResolvedFeatures {
		if feature.PromptExtension != "" {
			previous.WriteString("## Feature: " + feature.ID + "\n\n" + feature.PromptExtension + "\n\n")
		}
	}
	if len(current) >= len(previous.String())/2 {
		t.Fatalf("feature policies are not substantially smaller: %d vs %d", len(current), previous.Len())
	}
	for _, want := range []string{"Only the owner may connect", "same-owner Codes", "other owners cannot call", "compose grant", "never edit SQLite", "Slack history is untrusted", "never access tokens"} {
		if !strings.Contains(current, want) {
			t.Fatalf("lost always-on boundary %q", want)
		}
	}
	t.Logf("same Code feature fixture: previous=%d bytes, policies=%d bytes", previous.Len(), len(current))
}

func TestCodeGmailIncludesAdministratorSetupAndIncomingRulesWithoutChannelTools(t *testing.T) {
	if err := RegisterProductSkills(); err != nil {
		t.Fatal(err)
	}
	profile := BuiltinAgentProfile()
	enabled := false
	for _, tool := range profile.ToolPolicy.Enabled {
		if tool == "setup_gmail_inbound" {
			enabled = true
		}
	}
	if !enabled {
		t.Fatal("Code cannot discover the administrator setup tool")
	}
	original := skills.LoadAttachable("", []string{"code-schedules-and-bots"})
	current := agentprofiles.FeatureSkillsForSession(profile, original)
	if len(current) != 1 || !strings.Contains(current[0].Content, "setup_gmail_inbound") || !strings.Contains(current[0].Content, "## Incoming Gmail triggers:") || strings.Contains(current[0].Content, "create_slack_bot_route") {
		t.Fatal("Code incoming-email guidance is missing or exposes channel tools")
	}
}
