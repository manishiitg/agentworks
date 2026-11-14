package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-746-405: changing the native chat must not borrow another chat's run.
func TestCrewStepRunSelectionDoesNotFollowAChangedSession(t *testing.T) {
	for _, authoritative := range []bool{false, true} {
		t.Run(fmt.Sprint(authoritative), func(t *testing.T) {
			store, _ := memoryProductConversationStore()
			profile := routeTestProfile("work", true, "")
			binding := productConversationBinding{ConversationKey: "project", WorkspacePath: "Crew/example", ResourceID: "project"}
			original, err := store.resolveOrCreate(t.Context(), "owner", profile, binding, "")
			if err != nil {
				t.Fatal(err)
			}
			folder := "run-original"
			if _, err := store.workflowStepRun(t.Context(), "owner", original.SessionID, binding.WorkspacePath, "workflow", &folder); err != nil {
				t.Fatal(err)
			}
			var changed ProductConversationRecord
			if authoritative {
				binding.AuthoritativeSessionID = "another-chat"
				changed, err = store.resolveOrCreate(t.Context(), "owner", profile, binding, "")
			} else {
				changed, err = store.switchTo(t.Context(), "owner", profile, binding, "another-chat", true)
			}
			if err != nil || changed.SessionID != "another-chat" || changed.CurrentWorkflowRun != nil {
				t.Fatalf("new session inherited another chat's selection: %+v %v", changed, err)
			}
			got, err := store.workflowStepRun(t.Context(), "owner", "another-chat", binding.WorkspacePath, "workflow", nil)
			if err != nil || got != "" {
				t.Fatalf("new session resolved old run: %q %v", got, err)
			}
		})
	}
}

func TestStoppedOrCancelledCrewChatCannotCreateWorkflowRuntime(t *testing.T) {
	api := &StreamingAPI{stoppedSessions: map[string]bool{"stopped": true}}
	resolved := &resolvedAgentProfile{}
	resolved.Definition.ID = crewProfileID
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tc := range []struct {
		ctx     context.Context
		session string
	}{{ctx, "cancelled"}, {t.Context(), "stopped"}} {
		if _, err := api.agentProfileWorkflowSession(tc.ctx, resolved, "owner", tc.session, "Crew/example", "Workflow/example", "workflow", nil, nil, QueryRequest{}); err == nil {
			t.Fatal("stopped chat initialized a runtime")
		}
	}
	api.workshopChatSessions.Range(func(_, _ interface{}) bool { t.Error("stopped chat cached a runtime"); return false })
}

// PLAT-746: a saved Python start stays lazy and rechecks its Crew owner binding.
func TestCrewPythonStartRequiresOwnEditableWorkflowAndStaysLazy(t *testing.T) {
	_, _, api := newCrewWorkflowRunTestEnv(t)
	api.logger = loggerv2.NewNoop()
	resolved := &resolvedAgentProfile{}
	resolved.Definition.ID = crewProfileID
	resolved.Definition.Runtime.Capabilities.WorkflowExecution = agentprofiles.CapabilityOptional
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	register := func(user string, readOnly bool) *productSurfaceDraft {
		t.Helper()
		reg := &productSurfaceDraft{}
		callCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user})
		if err := api.registerAgentProfileWorkflowTools(callCtx, reg, resolved, user, "owner-chat", crewWorkflowTestCrewPath, nil, nil, QueryRequest{}, readOnly); err != nil {
			t.Fatal(err)
		}
		return reg
	}
	// PLAT-746-500: declaring the capability on another product/root must
	// not expose a targetless legacy workflow start.
	otherRoot := &productSurfaceDraft{}
	if err := api.registerAgentProfileWorkflowTools(ctx, otherRoot, resolved, "owner", "owner-chat", "Chats/work", nil, nil, QueryRequest{}, false); err != nil || len(otherRoot.tools) != 0 {
		t.Fatalf("non-Crew root received a workflow runtime: %v", err)
	}
	if len(register("owner", false).tools) != 0 {
		t.Fatal("Crew without a workflow received workflow tools")
	}
	manifest, _, err := ReadWorkflowManifest(ctx, "Workflow/test")
	if err != nil {
		t.Fatal(err)
	}
	binding := crewExecutableBinding{ID: manifest.ID, CrewID: "crew-project-a", CrewRoot: crewWorkflowTestCrewPath, Root: "Workflow/test", Kind: "script"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	if len(register("reader", false).tools) != 0 || len(register("owner", true).tools) != 0 {
		t.Fatal("Run person received owner step controls")
	}
	if err := initializeCrewScriptSource(ctx, binding, "Saved Python"); err != nil {
		t.Fatal(err)
	}
	reg := register("owner", false)
	start, ok := reg.tools["run_full_workflow"]
	if !ok {
		t.Fatal("editable Crew lost its saved Python start")
	}

	assertNoRuntime := func() {
		t.Helper()
		api.workshopChatSessions.Range(func(_, _ interface{}) bool { t.Error("Workshop initialized before admitted tool use"); return false })
	}
	assertNoRuntime()
	// The account was demoted after its tool catalog was built. Starting work
	// must recheck the intended edit rule before opening its runtime.
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":false}]}`)
	if _, err := start.exec(ctx, map[string]interface{}{}); err == nil || !strings.Contains(err.Error(), "edit access") {
		t.Fatalf("demoted owner kept workflow controls: %v", err)
	}
	assertNoRuntime()
}

// The existing conversation record is the durable selection. Neither another
// chat, another person's registry nor a replacement workflow inherits the run.
func TestCrewCurrentStepRunSurvivesStoreReloadWithinExactChat(t *testing.T) {
	files := map[string]string{}
	store := productConversationRegistryStore{
		read:  func(_ context.Context, p string) (string, bool, error) { raw, ok := files[p]; return raw, ok, nil },
		write: func(_ context.Context, p, raw string) error { files[p] = raw; return nil },
	}
	doc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{
		"work/chat-a": {ProfileID: crewProfileID, SessionID: "chat-a", WorkspacePath: "Crew/example"},
		"work/chat-b": {ProfileID: crewProfileID, SessionID: "chat-b", WorkspacePath: "Crew/example"},
	}}
	raw, _ := json.Marshal(doc)
	files[productConversationRegistryPath("owner")] = string(raw)
	folder := "run-example"
	if _, err := store.workflowStepRun(context.Background(), "owner", "chat-a", "Crew/example", "wf", &folder); err != nil {
		t.Fatal(err)
	}
	reloaded := productConversationRegistryStore{read: store.read, write: store.write}
	for _, tc := range []struct{ user, chat, workflow, want string }{
		{"owner", "chat-a", "wf", folder}, {"owner", "chat-a", "replacement", ""}, {"owner", "chat-b", "wf", ""},
	} {
		got, err := reloaded.workflowStepRun(context.Background(), tc.user, tc.chat, "Crew/example", tc.workflow, nil)
		if err != nil || got != tc.want {
			t.Fatalf("selection for %s/%s: %q %v", tc.chat, tc.workflow, got, err)
		}
	}
	if _, err := reloaded.workflowStepRun(context.Background(), "other-person", "chat-a", "Crew/example", "wf", nil); err == nil {
		t.Fatal("another person inherited the selection")
	}
}
