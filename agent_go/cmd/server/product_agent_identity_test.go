package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// An agent's name and purpose belong to its tab: they are listed by conversation key, changed one field at a time
// with the actor kept, and refused for a tab that no longer exists (PLAT-571).
func TestAgentIdentityBelongsToTheTab(t *testing.T) {
	store, _ := memoryProductConversationStore()
	ctx := context.Background()
	path := productConversationRegistryPath("user-1")
	main := ProductConversationRecord{ConversationKey: "proj-1", SessionID: "s-main", ProfileID: "code"}
	side := ProductConversationRecord{ConversationKey: "proj-1:chat:ab12", SessionID: "s-side", ProfileID: "code"}
	document := productConversationRegistryDocument{Version: productConversationRegistryVersion, Entries: map[string]ProductConversationRecord{
		productConversationRegistryEntryKey("code", "proj-1"):           main,
		productConversationRegistryEntryKey("code", "proj-1:chat:ab12"): side,
	}}
	if err := store.writeDocument(ctx, path, document); err != nil {
		t.Fatal(err)
	}
	name, purpose := "Reviewer", "Reads every change and says what is risky"
	if _, err := store.setAgentIdentity(ctx, "user-1", "code", "proj-1:chat:ab12", &name, &purpose, "the person"); err != nil {
		t.Fatal(err)
	}
	purposeOnly := "Reads changes and tests them"
	identity, err := store.setAgentIdentity(ctx, "user-1", "code", "proj-1:chat:ab12", nil, &purposeOnly, "agent Reviewer")
	if err != nil || identity.Name != "Reviewer" || identity.Purpose != purposeOnly || identity.UpdatedBy != "agent Reviewer" {
		t.Fatalf("a purpose-only change must keep the name and record who changed it: %+v %v", identity, err)
	}
	listed, err := store.agentIdentities(ctx, "user-1", "code", "proj-1")
	if err != nil || listed["proj-1:chat:ab12"].Name != "Reviewer" || len(listed) != 1 {
		t.Fatalf("identities are keyed by conversation key: %+v %v", listed, err)
	}
	if _, err := store.setAgentIdentity(ctx, "user-1", "code", "proj-1:chat:gone", &name, nil, "the person"); err == nil {
		t.Fatal("a tab that no longer exists cannot be given an identity")
	}
	long := strings.Repeat("x", productAgentPurposeMaxRunes+1)
	if _, err := store.setAgentIdentity(ctx, "user-1", "code", "proj-1:chat:ab12", nil, &long, "the person"); err == nil {
		t.Fatal("a purpose longer than one short line is refused")
	}
}

// PLAT-750-76: function tabs lost their job on mode rendering and answered as the whole Crew.
func TestFunctionTabPromptKeepsItsOwnJobAfterModeSelection(t *testing.T) {
	fx := newCrewRunModeFixture(t)
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "codex-cli")
	t.Setenv("AGENTWORKS_PROVIDER_POLICY", "")
	const functionKey = "crew-aaa:chat:fn.sample-desk"
	const mainKey = "crew-aaa:chat:root-sample"
	fx.files[crewRunModeOwnerRoot+"/product.json"] = `{"product":"work","id":"crew-aaa","title":"Sample team","main_chat_key":"crew-aaa:chat:root-sample","session_id":"ownercrew-aaa:chat:root-sample"}`
	write := func(path string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		fx.files[path] = string(raw)
	}
	for _, person := range []string{"owner", "reader"} {
		doc := productConversationRegistryDocument{Version: productConversationRegistryVersion,
			Entries: map[string]ProductConversationRecord{}, Agents: map[string]ProductAgentIdentity{}}
		for _, key := range []string{mainKey, functionKey, "crew-aaa:chat:other"} {
			entry := productConversationRegistryEntryKey("work", key)
			doc.Entries[entry] = ProductConversationRecord{ProfileID: "work", ConversationID: "conversation-" + person + key, ConversationKey: key, ResourceID: "crew-aaa", WorkspacePath: crewRunModeOwnerRoot, SessionID: person + key, Title: "Main chat"}
		}
		doc.Agents[productConversationRegistryEntryKey("work", functionKey)] = ProductAgentIdentity{Purpose: person + " sample job"}
		doc.Agents[productConversationRegistryEntryKey("work", "crew-aaa:chat:other")] = ProductAgentIdentity{Name: "Other desk", Purpose: "OTHER_PRIVATE_PURPOSE"}
		write(productConversationRegistryPath(person), doc)
	}
	publication := &publishedProject{Root: crewRunModeOwnerRoot, OwnerID: "owner", WorkspaceID: "crew-aaa", ProfileID: "work", Revision: "release1", Provider: "codex-cli", ModelID: "gpt-6-luna", ConnectionID: "global:codex-cli"}
	functionDocument := func(root string, functions ...crewFunction) projectFunctionsDocument {
		doc := projectFunctionsDocument{}
		for _, fn := range functions {
			record, err := crewFunctionDefinition(root, fn)
			if err != nil {
				t.Fatal(err)
			}
			doc.Functions = append(doc.Functions, record)
		}
		return doc
	}
	write(publication.sourceRoot()+"/functions.json", functionDocument(publication.sourceRoot(), crewFunction{Name: "sample_reply", Agent: "Sample desk", Description: "Draft a reply using the sample inbox."}, crewFunction{Name: "other_job", Agent: "Other desk", Description: "OTHER_FUNCTION_DESCRIPTION"}))
	write(crewRunModeOwnerRoot+"/functions.json", functionDocument(crewRunModeOwnerRoot, crewFunction{Name: "draft_only", Agent: "Sample desk", Description: "UNPUBLISHED_DESCRIPTION"}))

	for _, tc := range []struct {
		person string
		run    bool
	}{{"owner", false}, {"owner", true}, {"reader", true}} {
		ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: tc.person, Username: tc.person})
		ctx = context.WithValue(ctx, publicationExecutionKey{}, publication)
		assemble := func(key string) *resolvedAgentProfile {
			t.Helper()
			req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: "work", AgentProfileConversationKey: key, SelectedFolder: crewRunModeOwnerRoot, PinRunMode: tc.run,
				AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Sample team"}}
			profile, _, err := fx.api.admitQueryTarget(ctx, &req, tc.person, tc.person+key)
			if err != nil {
				t.Fatal(err)
			}
			return profile
		}
		function := assemble(functionKey)
		for _, fact := range []string{"Sample desk", "sample_reply", "Draft a reply using the sample inbox.", tc.person + " sample job"} {
			if !strings.Contains(function.Prompt, fact) {
				t.Fatalf("%s run=%t: function tab lost its own job %q after mode selection", tc.person, tc.run, fact)
			}
		}
		for _, hidden := range []string{"OTHER_PRIVATE_PURPOSE", "OTHER_FUNCTION_DESCRIPTION", "UNPUBLISHED_DESCRIPTION"} {
			if strings.Contains(function.Prompt, hidden) {
				t.Fatalf("function role included unrelated or unpublished guidance: %s", hidden)
			}
		}
		if tc.person == "reader" && strings.Contains(function.Prompt, "owner sample job") {
			t.Fatal("member inherited another person's saved tab purpose")
		}
		if agentProfileSessionKey(function) != agentProfileSessionKey(assemble(functionKey)) {
			t.Fatal("unchanged tab role reset the retained session on the next turn")
		}
		main := assemble(mainKey)
		if !strings.Contains(main.Prompt, "Main chat") || strings.Contains(main.Prompt, "sample_reply") || strings.Contains(main.Prompt, tc.person+" sample job") {
			t.Fatal("main tab inherited the function tab's job or lost its own name")
		}
	}
}
