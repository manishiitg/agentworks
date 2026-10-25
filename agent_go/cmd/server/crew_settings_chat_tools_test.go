package server

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
)

// Real project resolution and settings handlers, including a folder whose
// name differs from its project ID. Tool arguments cannot select another
// project or person, and changing a model cannot select another chat.
func TestCrewSettingsChatToolsStayBoundToCrewAndCaller(t *testing.T) {
	for _, person := range []string{"owner", "other"} {
		t.Run(person, func(t *testing.T) {
			f, _ := newExternalCrewCommandFixture(t)
			if _, err := defaultProjectOwners().AddCoOwner("work", "test-team", "other"); err != nil {
				t.Fatal(err)
			}
			for _, group := range []string{"crew_execution", "crew_integrations"} {
				owner := crewCapabilityHTTP(t, f, crewWriter("owner"), group, map[string]any{"action": "list"}, 200)
				coOwner := crewCapabilityHTTP(t, f, crewWriter("other"), group, map[string]any{"action": "list"}, 200)
				names := func(body string) string {
					var inventory struct {
						Tools []struct {
							Name string `json:"name"`
						} `json:"tools"`
					}
					if err := json.Unmarshal([]byte(body), &inventory); err != nil {
						t.Fatal(err)
					}
					out := []string{}
					for _, tool := range inventory.Tools {
						out = append(out, tool.Name)
					}
					sort.Strings(out)
					return strings.Join(out, ",")
				}
				if names(owner) != names(coOwner) {
					t.Fatalf("%s exposes different owner/co-owner native tools", group)
				}
			}

			claims := &UserClaims{UserID: person, Username: person}
			ctx := context.WithValue(context.Background(), UserContextKey, claims)
			const panelSession = "co-owner-panel-parity"
			f.api.lastQueryRequests = map[string]QueryRequest{panelSession: {userID: person, SelectedFolder: externalCommandCrewRoot}}
			panels := &recordingRegistrar{}
			if err := f.api.registerWorkspacePanelChatTools(panels, nil, person, panelSession, externalCommandCrewRoot, "work"); err != nil {
				t.Fatal(err)
			}
			for name, args := range map[string]map[string]any{"settings": {"action": "get"}, "crew_owners": {"action": "list"}} {
				got, err := panels.tools[name].exec(ctx, args)
				if err != nil {
					t.Fatalf("%s native panel as %s: %v", name, person, err)
				}
				want := crewCapabilityHTTP(t, f, crewWriter(person), name, args, 200)
				if strings.TrimSpace(got) != strings.TrimSpace(want) {
					t.Fatalf("%s chat and MCP differ for %s", name, person)
				}
			}

			// The existing native settings tool and MCP/app adapters share storage.
			if _, err := panels.tools["settings"].exec(ctx, map[string]any{"action": "update", "chat_instructions": "  Ask which date range to compare.  "}); err != nil {
				t.Fatal(err)
			}
			raw, found, err := readFileFromWorkspace(ctx, externalCommandCrewRoot+"/product.json")
			if err != nil || !found || !strings.Contains(raw, "Ask which date range to compare.") {
				t.Fatalf("native guidance was not saved: %v", err)
			}
			if _, err := panels.tools["settings"].exec(ctx, map[string]any{"action": "update", "chat_instructions": ""}); err != nil {
				t.Fatal(err)
			}
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
				if _, err := store.setAgentModel(ctx, claims.UserID, profile.ID, key, "codex-cli", "gpt-6-astra", "", "", claims.UserID); err != nil {
					t.Fatal(err)
				}
			}
			resolved := &resolvedAgentProfile{Definition: profile}
			reg := &recordingRegistrar{}
			if err := f.api.registerCrewSettingsChatTools(reg, newProductToolGate(resolved), resolved, claims, false, chat.SessionID, externalCommandCrewRoot, "https://example.com"); err != nil {
				t.Fatal(err)
			}
			if person == "owner" {
				captured := map[string]recordedTool{}
				for name, tool := range panels.tools {
					captured[name] = tool
				}
				for name, tool := range reg.tools {
					captured[name] = tool
				}
				logConfigurationCatalog(t, "crew-builder", captured)
				logExternalConfigurationCatalog(t)
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
				foreign := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "unrelated"})
				if _, err := tool.exec(foreign, map[string]any{"action": "list"}); err == nil {
					t.Fatalf("%s accepted another caller", name)
				}
			}
			// Forged selectors are ignored even if the transport skips schema checks.
			out, err := reg.tools["browser_connections"].exec(ctx, map[string]any{"action": "list", "crew_id": "another", "workspace": "Crew/another"})
			if err != nil || !strings.Contains(out, "https://example.com/?browser_connections=1") {
				t.Fatalf("saved-browser list: %s %v", out, err)
			}
			// The live native bridge validates this schema before the callback. An
			// empty engine is the existing reset operation and must survive admission.
			encodedSchema, err := json.Marshal(reg.tools["manage_crew_model"].params)
			if err != nil {
				t.Fatal(err)
			}
			var wireSchema any
			if err := json.Unmarshal(encodedSchema, &wireSchema); err != nil {
				t.Fatal(err)
			}
			compiler := jsonschema.NewCompiler()
			if err := compiler.AddResource("https://example.com/crew-model.json", wireSchema); err != nil {
				t.Fatal(err)
			}
			schema, err := compiler.Compile("https://example.com/crew-model.json")
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(map[string]any{"action": "set", "engine": ""}); err != nil {
				t.Fatalf("native schema rejects the documented model reset: %v", err)
			}
			if _, err := reg.tools["manage_crew_model"].exec(ctx, map[string]any{"action": "set", "engine": "", "conversation_key": side.ConversationKey, "chat_id": "other"}); err != nil {
				t.Fatal(err)
			}
			identities, err := store.agentIdentities(ctx, claims.UserID, profile.ID, "team")
			if err != nil || identities[binding.ConversationKey].Engine != "" || identities[side.ConversationKey].Engine != "codex-cli" {
				t.Fatalf("model changed a different chat: %+v %v", identities, err)
			}
			limited := &recordingRegistrar{}
			readClaims := &UserClaims{UserID: person, AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
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
				tokenClaims := &UserClaims{UserID: person, AccessToken: &accesstokens.Token{Scopes: []string{scope}, AllCrews: true}}
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
			if person == "owner" {
				logConfigurationCatalog(t, "crew-run-ask", run.tools)
			}
			if person == "other" {
				if _, err := defaultProjectOwners().RemoveCoOwner("work", "test-team", person); err != nil {
					t.Fatal(err)
				}
				if _, err := panels.tools["settings"].exec(ctx, map[string]any{"action": "get"}); err == nil {
					t.Fatal("retained Builder panel admitted removed co-owner")
				}
				crewCapabilityHTTP(t, f, crewWriter(person), "crew_integrations", map[string]any{"action": "list"}, 403)
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

		})
	}
}

func TestCrewDashboardChatCannotRetargetSharing(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	logDashboardCatalogFootprints(t, f.api)
	reg := &recordingRegistrar{}
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	if err := f.api.registerDashboardTools(reg, "owner", externalCommandCrewRoot, claims); err != nil {
		t.Fatal(err)
	}
	projectRecordedCatalog(t, reg)
	tool := reg.tools["share"]
	props := tool.params["properties"].(map[string]any)
	for _, key := range []string{"workspace", "workspace_id", "workflow_id", "crew_id", "flow_id"} {
		if props[key] != nil {
			t.Fatalf("current-Crew sharing exposes %s", key)
		}
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	if _, err := tool.exec(ctx, map[string]any{"action": "set", "mode": "private", "workspace": "Crew/unrelated"}); err == nil {
		t.Fatal("bound sharing accepted a foreign selector")
	}
	out, err := tool.exec(ctx, map[string]any{"mode": "private", "action": "set"})
	var body map[string]any
	if err != nil || json.Unmarshal([]byte(out), &body) != nil || body["mode"] != "private" {
		t.Fatalf("bound share did not run its canonical service: %s %v", out, err)
	}
	if _, err := tool.exec(ctx, map[string]any{"action": "get"}); err != nil {
		t.Fatal(err)
	}
	// PLAT-825-113: a selected action must not admit its whole family. Seed a
	// real draft through the same bound service so get succeeds before refusal.
	if _, err := reg.tools["dashboard"].exec(ctx, map[string]any{"action": "create", "dashboard_id": "selection", "title": "Selection", "files": map[string]any{"index.html": "<!doctype html><html><head><title>Selection</title></head><body>Selection</body></html>"}}); err != nil {
		t.Fatal(err)
	}
	partial := &productSurfaceDraft{gate: newProductToolGateForAllowlist("partial", []string{"dashboard.get"})}
	if err := f.api.registerDashboardTools(partial, "owner", externalCommandCrewRoot, claims); err != nil {
		t.Fatal(err)
	}
	projectRecordedCatalog(t, &partial.recordingRegistrar)
	if _, err := partial.tools["dashboard"].exec(ctx, map[string]any{"action": "get", "dashboard_id": "selection"}); err != nil {
		t.Fatalf("selected dashboard.get refused: %v", err)
	}
	if _, err := partial.tools["dashboard"].exec(ctx, map[string]any{"action": "create", "dashboard_id": "forbidden", "title": "Forbidden", "files": map[string]any{"index.html": "<p>Forbidden</p>"}}); err == nil || !strings.Contains(err.Error(), "unavailable in this chat") {
		t.Fatalf("partial selection widened to create: %v", err)
	}
	foreign := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "other"})
	if _, err := partial.tools["dashboard"].exec(foreign, map[string]any{"action": "get", "dashboard_id": "selection"}); err == nil {
		t.Fatal("grouped dashboard accepted another person")
	}
	for _, filterCustom := range []bool{false, true} {
		selected := &recordingRegistrar{}
		if err := f.api.registerDashboardTools(selected, "owner", externalCommandCrewRoot, claims); err != nil {
			t.Fatal(err)
		}
		projectRecordedCatalog(t, selected, platformtools.Selection{Tools: []string{"dashboard:dashboard.get"}, FilterCustom: filterCustom})
		if _, err := selected.tools["dashboard"].exec(ctx, map[string]any{"action": "get", "dashboard_id": "selection"}); err != nil {
			t.Fatalf("saved selection refused get (API=%v): %v", filterCustom, err)
		}
		if _, err := selected.tools["dashboard"].exec(ctx, map[string]any{"action": "create"}); err == nil || !strings.Contains(err.Error(), "unavailable in this chat") {
			t.Fatalf("saved selection widened (API=%v): %v", filterCustom, err)
		}
	}
	limited := &recordingRegistrar{}
	readClaims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	if err := f.api.registerDashboardTools(limited, "owner", externalCommandCrewRoot, readClaims); err != nil {
		t.Fatal(err)
	}
	projectRecordedCatalog(t, limited)
	// Native execution contexts may omit claims. Retain the original token's
	// consent instead of reconstructing an unrestricted website principal.
	if _, err := limited.tools["share"].exec(context.Background(), map[string]any{"action": "set", "mode": "everyone"}); err == nil {
		t.Fatal("dashboard tool dropped the captured token's scope")
	}
	if _, err := limited.tools["share"].exec(ctx, map[string]any{"action": "set", "mode": "everyone"}); err == nil {
		t.Fatal("same-user unscoped context widened sharing consent")
	}
}
