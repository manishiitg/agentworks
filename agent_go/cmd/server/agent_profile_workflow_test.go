package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"strings"
	"testing"
)

func TestProfileRequiresWorkflowRuntime(t *testing.T) {
	if profileRequiresWorkflowRuntime(nil) {
		t.Fatal("nil profile requires workflow runtime")
	}
	resolved := &resolvedAgentProfile{}
	if profileRequiresWorkflowRuntime(resolved) {
		t.Fatal("empty capability requires workflow runtime")
	}
	resolved.Definition.Runtime.Capabilities.WorkflowExecution = "required"
	if !profileRequiresWorkflowRuntime(resolved) {
		t.Fatal("required capability did not enable workflow runtime")
	}
}

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

// PLAT-746-405: Crew's old policy refused single-step work entirely. Register
// native tools only for an editable Crew's bound workflow, without initializing it.
func TestCrewStepToolsRequireOwnEditableWorkflowAndStayLazy(t *testing.T) {
	_, files, api := newCrewWorkflowRunTestEnv(t)
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
	if len(register("owner", false).tools) != 0 {
		t.Fatal("Crew without a workflow received workflow tools")
	}
	manifest, _, err := ReadWorkflowManifest(ctx, "Workflow/test")
	if err != nil {
		t.Fatal(err)
	}
	binding := crewExecutableBinding{ID: manifest.ID, CrewID: "crew-project-a", CrewRoot: crewWorkflowTestCrewPath, Root: "Workflow/test", Kind: "workflow"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	if len(register("reader", false).tools) != 0 || len(register("owner", true).tools) != 0 {
		t.Fatal("Run person received owner step controls")
	}
	reg := register("owner", false)
	for _, name := range []string{"execute_step", "query_step", "send_step_message", "stop_step", "stop_all_executions", "list_executions", "run_full_workflow", "debug_step"} {
		if _, ok := reg.tools[name]; !ok {
			t.Fatalf("missing existing step control %s", name)
		}
	}
	assertNoRuntime := func() {
		t.Helper()
		api.workshopChatSessions.Range(func(_, _ interface{}) bool { t.Error("Workshop initialized before admitted tool use"); return false })
	}
	assertNoRuntime()
	manifest.Version = workflowContractSkillsVersion
	raw, _ := json.Marshal(manifest)
	files.files[manifestPath(binding.Root)] = string(raw)
	if _, err := reg.tools["execute_step"].exec(ctx, map[string]interface{}{"step_id": "work"}); err == nil || !strings.Contains(err.Error(), "update") {
		t.Fatalf("Crew start bypassed contract gate: %v", err)
	}
	assertNoRuntime()
	// The account was demoted after its tool catalog was built. Even a status
	// call must recheck the intended edit rule before opening its runtime.
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":false}]}`)
	if _, err := reg.tools["query_step"].exec(ctx, map[string]interface{}{"step_id": "work"}); err == nil || !strings.Contains(err.Error(), "edit access") {
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
