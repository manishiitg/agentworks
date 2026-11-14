package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-988: use the real journal/receipt/Stop projection; mock only native
// RPC and the final admitted provider turn, never the state deciding to resume.
func TestNativeGoalWakeRestoreIsOnceAndStopClearCancelIt(t *testing.T) {
	for _, action := range []string{"resume", "stop", "clear", "changed", "failed_wake", "arm_stop_race"} {
		t.Run(action, func(t *testing.T) {
			withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":["work"]}]}`)
			api, store := newDurableChatAPI(t, "goal-lifecycle-"+action, "alice")
			sid := "goal-lifecycle-" + action
			store.SetEventPreparer(api.prepareNativeGoalEvent)
			files := map[string]string{}
			api.internalTurnQueueRead = func(_ context.Context, path string) (string, bool, error) { v, ok := files[path]; return v, ok, nil }
			api.internalTurnQueueWrite = func(_ context.Context, path, raw string) error { files[path] = raw; return nil }
			record := restartContinueRecord{SessionID: sid, QueryID: "parent", Request: QueryRequest{Query: "Original input must not replay", AgentMode: "multi-agent"}, SavedAt: time.Now()}
			if err := api.writeRestartContinueRecords(context.Background(), "alice", restartContinueDocument{Chats: map[string]restartContinueRecord{sid: record}}); err != nil {
				t.Fatal(err)
			}
			native := llmtypes.NativeGoalUpdate{Provider: "codex-cli", NativeSessionID: "native", Goal: &llmtypes.NativeGoal{Objective: "Finish the task", Status: "active"}}
			if err := api.projectNativeGoal(sid, native, "native_update"); err != nil {
				t.Fatal(err)
			}
			oldControl, oldResume := controlNativeGoal, runNativeGoalResume
			t.Cleanup(func() { controlNativeGoal = oldControl; runNativeGoalResume = oldResume })
			raced := false
			controlNativeGoal = func(_ context.Context, owner, op string) (*llmtypes.NativeGoalUpdate, error) {
				if owner != sid {
					t.Fatal("control crossed session")
				}
				goal := *native.Goal
				goal.Status = "paused"
				native.Goal = &goal
				if action == "arm_stop_race" && !raced {
					raced = true
					api.pauseNativeGoalForStop(sid)
				}
				update := native
				return &update, nil
			}
			calls := 0
			runNativeGoalResume = func(_ *StreamingAPI, ctx context.Context, req map[string]interface{}, session, owner string) error {
				calls++
				command, ok := llmtypes.NativeGoalCommandForSession(ctx, sid)
				if !ok || command.Action != "resume" || command.Objective != "Finish the task" || session != sid || owner != "alice" {
					t.Fatal("restore lost goal/principal identity")
				}
				raw, _ := json.Marshal(req)
				if strings.Contains(string(raw), "Original input must not replay") {
					t.Fatal("restore replayed interrupted input")
				}
				if !api.nativeGoalRestoreCurrent(ctx, sid, ctx.Value(nativeGoalRestoreContextKey{}).(string)) {
					t.Fatal("restore did not own its consumed receipt")
				}
				return nil
			}
			receipt := api.pauseNativeGoalForWake(sid)
			if action == "arm_stop_race" {
				if receipt != nil {
					t.Fatal("Stop racing arm resurrected restore")
				}
				return
			}
			if receipt == nil || !api.markNativeGoalWakeStarted(sid, receipt, "wake") {
				t.Fatal("wake did not arm owned restore")
			}
			// A paused SDK read must not erase why the goal is paused.
			if err := api.projectNativeGoal(sid, native, "native_read"); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "stop":
				api.pauseNativeGoalForStop(sid)
			case "clear":
				api.cancelNativeGoalRestore(sid, "person_goal_command")
				native.Goal = nil
				_ = api.projectNativeGoal(sid, native, "person_clear")
			case "changed":
				api.cancelNativeGoalRestore(sid, "person_goal_command")
				native.Goal = &llmtypes.NativeGoal{Objective: "New goal", Status: "paused"}
				_ = api.projectNativeGoal(sid, native, "intent_set")
			}
			if action == "stop" || action == "clear" {
				late := native
				late.Goal = &llmtypes.NativeGoal{Objective: "Finish the task", Status: "active"}
				_ = api.projectNativeGoal(sid, late, "native_update")
				projected, err := api.latestNativeGoal(context.Background(), sid)
				if err != nil {
					t.Fatal(err)
				}
				if action == "stop" && (projected.Goal == nil || projected.Goal.Status != "paused") {
					t.Fatal("late frame revived stopped goal")
				}
				if action == "clear" && projected.Goal != nil {
					t.Fatal("late frame revived cleared goal")
				}
			}
			var failure error
			if action == "failed_wake" {
				failure = errors.New("wake failed")
			}
			api.restoreNativeGoalAfterWake(sid, receipt, failure)
			api.restoreNativeGoalAfterWake(sid, receipt, nil)
			want := 0
			if action == "resume" {
				want = 1
			}
			if calls != want {
				t.Fatalf("restore calls=%d want%d", calls, want)
			}
		})
	}
}

// PLAT-746-988: the actual query admission must refuse a foreign chat before
// any goal RPC, while the same existing private chat's owner can pause it.
func TestNativeGoalQueryControlsUseNormalChatAndAccountAdmission(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", "goal-query-test")
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "codex-cli")
	t.Setenv("LLM_CONFIG_LOCKED", "")
	t.Setenv("AGENTWORKS_PROVIDER_POLICY", "")
	var err error
	f.api.chatStore, err = chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const sid = "private-goal-query"
	_, store := newDurableChatAPI(t, sid, "owner")
	f.api.eventStore = store
	f.api.activeSessions = map[string]*ActiveSessionInfo{sid: {SessionID: sid, UserID: "owner", WorkspacePath: externalCommandCrewRoot}}
	f.api.agentCancelFuncs = map[string]context.CancelFunc{sid: func() {}}
	account := (&providerAccountsEnv{api: f.api}).addAccount(t, "owner", map[string]interface{}{"provider": "codex-cli", "display_name": "Goal test account", "auth_method": "cli_login"})
	req := QueryRequest{Query: "/goal pause", AgentMode: "multi-agent", AgentProfileID: crewProfileID, AgentProfileConversationKey: "team", SelectedFolder: externalCommandCrewRoot, ConnectionID: account.ID, Provider: "codex-cli", ModelID: "codex-cli", LLMConfig: &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "codex-cli", ModelID: "codex-cli"}}, AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Fixture Crew"}}
	calls := 0
	oldControl := controlNativeGoal
	t.Cleanup(func() { controlNativeGoal = oldControl })
	controlNativeGoal = func(_ context.Context, owner, op string) (*llmtypes.NativeGoalUpdate, error) {
		calls++
		if owner != sid || op != "pause" {
			t.Fatal("wrong native goal target")
		}
		return &llmtypes.NativeGoalUpdate{Provider: "codex-cli", NativeSessionID: "native", Goal: &llmtypes.NativeGoal{Objective: "Task", Status: "paused"}}, nil
	}
	send := func(user string) *httptest.ResponseRecorder {
		r := sharedSecretsRequest(http.MethodPost, "/api/query", user, req)
		r.Header.Set("X-Session-ID", sid)
		w := httptest.NewRecorder()
		f.api.handleQuery(w, r)
		return w
	}
	refused := send("other")
	if refused.Code != 404 || calls != 0 || !strings.Contains(refused.Body.String(), "Session not found") {
		t.Fatalf("foreign refusal not from session boundary: %d %s calls=%d", refused.Code, refused.Body.String(), calls)
	}
	allowed := send("owner")
	if allowed.Code != 200 || calls != 1 {
		t.Fatalf("own admitted control failed: %d %s calls=%d", allowed.Code, allowed.Body.String(), calls)
	}
	state, err := f.api.latestNativeGoal(context.Background(), sid)
	if err != nil || state == nil || state.NativeSessionID != "native" || state.Goal.Status != "paused" {
		t.Fatal("admitted pause was not projected", err)
	}
}
