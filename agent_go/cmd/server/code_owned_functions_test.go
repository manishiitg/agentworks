package server

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func privateCodeWorkflowTools(t *testing.T, env crewFunctionEnv, actor string) map[string]recordedTool {
	t.Helper()
	reg := &recordingRegistrar{}
	if err := env.api.registerCrewFunctionTools(reg, actor, "workflow-code-"+actor, QueryRequest{SelectedFolder: "Workflow/reports"}, workflowTriggerLinkCaller("Workflow/reports"), nil); err != nil {
		t.Fatal(err)
	}
	return reg.tools
}

// Code is a private space and Crews and workflows are shared (owner,
// 2026-10-09): nothing outside a Code project can call it, and a Code
// project declares no functions. A Code may still call a Crew, and a chat
// of a Code still resolves its own Code (to answer a call it made to a
// sibling chat).
func TestCodePublishedFunctionsKeepLegacyPeersRemoved(t *testing.T) {
	env := newCrewFunctionEnv(t)
	profile := agentprofiles.Profile{ID: codeproduct.ProfileID, Name: "Code", Version: 1, BuiltIn: true, Product: "code", SystemPromptTemplate: "hi",
		Runtime: agentprofiles.RuntimePolicy{Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
			Workspace: agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: codeproduct.ProjectsRoot}},
		Features: []agentprofiles.FeatureBinding{{ID: "triggers", Options: map[string]string{"mode": "message_only"}}}}
	if err := env.svc.registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	const source = "_users/owner/Chats/Code/projects/source"
	const target = "_users/owner/Chats/Code/projects/target"
	env.mock.mu.Lock()
	env.mock.files[source+"/product.json"] = `{"schema_version":1,"product":"code","id":"source","title":"Source","session_id":"code-source"}`
	env.mock.files[target+"/product.json"] = `{"schema_version":1,"product":"code","id":"target","title":"Target","session_id":"code-target"}`
	env.mock.mu.Unlock()
	registry := withProjectOwnerRegistry(t)
	withProjectSharing(t, true)
	if err := registry.Register(projectOwnerRecord{Product: "code", Folder: "target", OwnerID: "owner", ProjectID: "target"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	claims := &UserClaims{UserID: "owner"}

	callers := map[string]map[string]recordedTool{
		"crew":     env.alpha,
		"workflow": privateCodeWorkflowTools(t, env, "owner"),
		"code":     env.functionTools(t, source, "source-code", nil),
	}
	for kind, tools := range callers {
		for _, name := range []string{"list_functions", "call_function", "define_function"} {
			args := map[string]interface{}{"target": "#code:target", "function": "check", "name": "check", "description": "d", "instructions": "i"}
			if _, err := tools[name].exec(ctx, args); err == nil || !strings.Contains(err.Error(), "Code is private") {
				t.Fatalf("%s: %s reached another Code: %v", kind, name, err)
			}
		}
	}
	// New published functions work privately; implicit same-owner peers stay removed.
	if _, err := callers["code"]["define_function"].exec(ctx, map[string]interface{}{"name": "check", "description": "d", "instructions": "i"}); err != nil {
		t.Fatalf("a Code could not declare a private published function: %v", err)
	}

	// A chat of a Code still resolves that Code (answering a sibling-chat ask), never another.
	codeCaller, err := crewTriggerLinkCaller(source)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if own, err := resolveFunctionTarget(ctx, claims, codeCaller, "#code:source"); err != nil || own.CrewID != "source" {
		t.Fatalf("a Code could not resolve itself: %+v, %v", own, err)
	}
	if _, err := resolveFunctionTarget(ctx, claims, codeCaller, "#code:target"); err == nil {
		t.Fatal("a Code resolved another Code")
	}
	// Explicit published-function targets use current person access; they do
	// not revive the old implicit #code peer route tested above.
	if _, err := resolveFunctionTarget(ctx, claims, codeCaller, "code:target"); err != nil {
		t.Fatalf("owner's published target refused: %v", err)
	}
	other := &UserClaims{UserID: "other"}
	otherCtx := context.WithValue(context.Background(), UserContextKey, other)
	if _, err := resolveFunctionTarget(otherCtx, other, triggerLinkCaller{}, "code:target"); err == nil {
		t.Fatal("private Code admitted another caller")
	}
	if err := registry.SetAudience("code", "target", true, []string{"other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveFunctionTarget(otherCtx, other, triggerLinkCaller{}, "code:target"); err != nil {
		t.Fatalf("selected Run caller refused: %v", err)
	}
	if err := registry.SetAudience("code", "target", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveFunctionTarget(otherCtx, other, triggerLinkCaller{}, "code:target"); err == nil {
		t.Fatal("unshared caller kept function access")
	}
	if _, err := registry.AddCoOwner("code", "target", "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveFunctionTarget(otherCtx, other, triggerLinkCaller{}, "code:target"); err != nil {
		t.Fatalf("co-owner's published target refused: %v", err)
	}

}
