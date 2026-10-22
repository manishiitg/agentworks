package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// Real project resolution and settings handlers, including a folder whose
// name differs from its project ID. Tool arguments cannot select another
// project or person, and changing a model cannot select another chat.
func TestCrewSettingsChatToolsStayBoundToCrewAndCaller(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	ctx := context.WithValue(context.Background(), UserContextKey, claims)
	profile, err := f.api.agentProfiles.Resolve(crewProfileID, 0, claims.UserID)
	if err != nil {
		t.Fatal(err)
	}
	binding, canBuild, err := resolveConversationBindingForUser(ctx, claims.UserID, profile, "team")
	if err != nil || !canBuild {
		t.Fatalf("binding: %v %t", err, canBuild)
	}
	store := defaultProductConversationRegistryStore()
	chat, err := store.resolveOrCreate(ctx, claims.UserID, profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	side := binding
	side.ConversationKey = "team" + codeChatSideMarker + "other"
	if _, err := store.resolveOrCreate(ctx, claims.UserID, profile, side, ""); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{binding.ConversationKey, side.ConversationKey} {
		if _, err := store.setAgentModel(ctx, claims.UserID, profile.ID, key, "codex-cli", "gpt-6-astra", "", claims.UserID); err != nil {
			t.Fatal(err)
		}
	}
	resolved := &resolvedAgentProfile{Definition: profile}
	reg := &recordingRegistrar{}
	if err := f.api.registerCrewSettingsChatTools(reg, newProductToolGate(resolved), resolved, claims, false, chat.SessionID, externalCommandCrewRoot, "https://example.com"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"browser_connections", "get_crew_costs", "manage_crew_model"} {
		tool, ok := reg.tools[name]
		if !ok {
			t.Fatalf("missing %s", name)
		}
		props := tool.params["properties"].(map[string]any)
		for _, selector := range []string{"crew_id", "workflow_id", "project_id", "workspace", "workspace_id", "chat_id", "conversation_key"} {
			if props[selector] != nil {
				t.Fatalf("%s exposes %s", name, selector)
			}
		}
		foreign := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "other"})
		if _, err := tool.exec(foreign, map[string]any{"action": "list"}); err == nil {
			t.Fatalf("%s accepted another caller", name)
		}
	}
	// Forged selectors are ignored even if the transport skips schema checks.
	out, err := reg.tools["browser_connections"].exec(ctx, map[string]any{"action": "list", "crew_id": "another", "workspace": "Crew/another"})
	if err != nil || !strings.Contains(out, "https://example.com/?browser_connections=1") {
		t.Fatalf("saved-browser list: %s %v", out, err)
	}
	if _, err := reg.tools["manage_crew_model"].exec(ctx, map[string]any{"action": "set", "engine": "", "conversation_key": side.ConversationKey, "chat_id": "other"}); err != nil {
		t.Fatal(err)
	}
	identities, err := store.agentIdentities(ctx, claims.UserID, profile.ID, "team")
	if err != nil || identities[binding.ConversationKey].Engine != "" || identities[side.ConversationKey].Engine != "codex-cli" {
		t.Fatalf("model changed a different chat: %+v %v", identities, err)
	}
	limited := &recordingRegistrar{}
	readClaims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	if err := f.api.registerCrewSettingsChatTools(limited, nil, resolved, readClaims, false, chat.SessionID, externalCommandCrewRoot, "https://example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := limited.tools["manage_crew_model"].exec(context.Background(), map[string]any{"action": "set", "engine": ""}); err == nil {
		t.Fatal("model setter bypassed the existing chat tool's MCP consent")
	}
	if _, err := limited.tools["manage_crew_model"].exec(ctx, map[string]any{"action": "set", "engine": ""}); err == nil {
		t.Fatal("same-user unscoped context widened model consent")
	}
	if _, err := limited.tools["browser_connections"].exec(ctx, map[string]any{"action": "select", "browser_id": "test-browser"}); err == nil || !strings.Contains(err.Error(), "Builder connection") {
		t.Fatalf("same-user unscoped context widened browser consent: %v", err)
	}
	for _, scope := range []string{"crews:run", "crews:write"} {
		bounded := &recordingRegistrar{}
		tokenClaims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{scope}, AllCrews: true}}
		if err := f.api.registerCrewSettingsChatTools(bounded, nil, resolved, tokenClaims, false, chat.SessionID, externalCommandCrewRoot, "https://example.com"); err != nil {
			t.Fatal(err)
		}
		if _, err := bounded.tools["get_crew_costs"].exec(ctx, nil); err == nil || !strings.Contains(err.Error(), "This connection does not allow") {
			t.Fatalf("%s bypassed public costs read consent: %v", scope, err)
		}
		if scope == "crews:run" {
			for _, action := range []string{"list", "setup"} {
				if _, err := bounded.tools["browser_connections"].exec(ctx, map[string]any{"action": action}); err == nil || !strings.Contains(err.Error(), "This connection does not allow") {
					t.Fatalf("run-only bypassed public browser %s consent: %v", action, err)
				}
			}
		}
	}
	run := &recordingRegistrar{}
	if err := f.api.registerCrewSettingsChatTools(run, nil, resolved, claims, true, chat.SessionID, externalCommandCrewRoot, "https://example.com"); err != nil || len(run.tools) != 0 {
		t.Fatalf("Run acquired settings tools: %+v %v", run.tools, err)
	}
	// The feature declaration and actual registrar admit the same sharing tools.
	if !agentprofiles.HasFeature(profile, "dashboard") {
		t.Fatal("fixture has no dashboard feature")
	}
	gate := newProductToolGate(resolved)
	for _, name := range []string{"get_dashboard_share", "share_dashboard"} {
		if !gate.Admit(name) {
			t.Fatalf("feature filters %s", name)
		}
	}
}

func TestCrewDashboardChatCannotRetargetSharing(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	reg := &recordingRegistrar{}
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	if err := f.api.registerDashboardTools(reg, "owner", externalCommandCrewRoot, claims); err != nil {
		t.Fatal(err)
	}
	tool := reg.tools["share_dashboard"]
	props := tool.params["properties"].(map[string]any)
	for _, key := range []string{"workspace", "workspace_id", "workflow_id", "crew_id", "flow_id"} {
		if props[key] != nil {
			t.Fatalf("current-Crew sharing exposes %s", key)
		}
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	out, err := tool.exec(ctx, map[string]any{"mode": "private", "workspace": "Crew/unrelated", "action": "list"})
	var body map[string]any
	if err != nil || json.Unmarshal([]byte(out), &body) != nil || body["mode"] != "private" {
		t.Fatalf("bound share did not run its canonical service: %s %v", out, err)
	}
	if _, err := reg.tools["get_dashboard_share"].exec(ctx, nil); err != nil {
		t.Fatal(err)
	}
	limited := &recordingRegistrar{}
	readClaims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	if err := f.api.registerDashboardTools(limited, "owner", externalCommandCrewRoot, readClaims); err != nil {
		t.Fatal(err)
	}
	// Native execution contexts may omit claims. Retain the original token's
	// consent instead of reconstructing an unrestricted website principal.
	if _, err := limited.tools["share_dashboard"].exec(context.Background(), map[string]any{"mode": "everyone"}); err == nil {
		t.Fatal("dashboard tool dropped the captured token's scope")
	}
	if _, err := limited.tools["share_dashboard"].exec(ctx, map[string]any{"mode": "everyone"}); err == nil {
		t.Fatal("same-user unscoped context widened sharing consent")
	}
}
