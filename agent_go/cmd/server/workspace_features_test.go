package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestWorkspaceMemoryDefaultsAndSwitch(t *testing.T) {
	_, profile := newCodePrivacyFixture(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	if workspaceMemoryEnabled(ctx, "code", codePrivacyOwnerRoot) || !workspaceMemoryEnabled(ctx, "work", "Crew/existing") {
		t.Fatal("origin memory defaults changed")
	}
	for _, enabled := range []bool{true, false} {
		if err := setWorkspaceMemory(ctx, "code", codePrivacyOwnerRoot, enabled); err != nil {
			t.Fatal(err)
		}
		if err := applyWorkspaceMemoryFeature(ctx, &profile, codePrivacyOwnerRoot); err != nil {
			t.Fatal(err)
		}
		if agentprofiles.HasFeature(profile, "memory") != enabled {
			t.Fatal("runtime ignored saved memory setting")
		}
		if sectionByName(t, "project-memory").Applies(promptContext{HasProfile: true, ProjectMemoryDisabled: !agentprofiles.HasFeature(profile, "memory")}) != enabled {
			t.Fatal("Memory instruction gate disagrees with saved setting")
		}
		raw, _, err := readFileFromWorkspace(ctx, codePrivacyOwnerRoot+"/workflow.json")
		var manifest productProjectManifest
		if err != nil || json.Unmarshal([]byte(raw), &manifest) != nil || manifest.Capabilities.MemoryEnabled == nil || *manifest.Capabilities.MemoryEnabled != enabled {
			t.Fatal("typed manifest lost memory setting")
		}
	}
}

func TestWorkspaceMCPAliasesUseNativeServices(t *testing.T) {
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range []string{"files", "share", "settings", "dashboard", "functions"} {
		for _, tool := range catalog {
			if tool.Name != family {
				continue
			}
			action := "get"
			if family == "files" || family == "functions" {
				action = "list"
			}
			member, args, err := externalResolveMerged(tool, map[string]any{"action": action, "workspace_id": "code:example"})
			if err != nil || args["crew_id"] != "code:example" {
				t.Fatalf("%s lost native Code target: %s %v %v", family, member.Name, args, err)
			}
		}
	}
}

func TestWorkspaceCodeOnlyCreateScope(t *testing.T) {
	claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"code:run"}}}
	if !workspaceCreateTokenAllowed(claims, "code") || workspaceCreateTokenAllowed(claims, "work") {
		t.Fatal("creation scope crossed origin")
	}
	claims.AccessToken.CrewIDs = []string{"one-project"}
	if workspaceCreateTokenAllowed(claims, "code") {
		t.Fatal("bounded machine token created outside its project list")
	}
}
