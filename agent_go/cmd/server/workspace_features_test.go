package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"strings"
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

func TestWorkspaceFunctionAuthoringKeepsProjectIdentity(t *testing.T) {
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range catalog {
		if tool.Name != "functions" {
			continue
		}
		for _, action := range []string{"define", "update", "delete"} {
			member, args, err := externalResolveMerged(tool, map[string]any{"action": action, "workspace_id": "code:example", "name": "reconcile", "description": "Function description", "instructions": "Function instructions", "input_schema": map[string]any{"type": "object"}, "result_schema": map[string]any{"type": "object"}})
			if err != nil || member.Name != "update_crew" || args["crew_id"] != "code:example" {
				t.Fatalf("%s: %s %v", action, member.Name, err)
			}
			for _, key := range []string{"name", "description", "instructions", "input_schema", "result_schema"} {
				if _, exists := args[key]; exists {
					t.Fatalf("%s can change project field %s: %v", action, key, args)
				}
			}
			raw, err := json.Marshal(args["functions"])
			if err != nil || !strings.Contains(string(raw), "reconcile") {
				t.Fatalf("%s lost the function name: %s %v", action, raw, err)
			}
		}
		return
	}
	t.Fatal("functions tool missing")
}
