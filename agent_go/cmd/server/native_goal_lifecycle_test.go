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
	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-988: use the real journal/receipt/Stop projection; mock only native
// RPC and the final admitted provider turn, never the state deciding to resume.
func TestNativeGoalWakeRestoreIsOnceAndStopClearCancelIt(t *testing.T) {
	for _, action := range []string{"resume", "stop", "clear", "changed", "failed_wake", "retry_after_failure", "arm_stop_race", "pause_failed", "no_live_process", "missing_record", "poster_record", "resume_quota", "cold_stop"} {
		t.Run(action, func(t *testing.T) {
			withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"poster","username":"poster","can_create":true,"products":["work"]}]}`)
			api, store := newDurableChatAPI(t, "goal-lifecycle-"+action, "owner")
			sid := "goal-lifecycle-" + action
			store.SetEventPreparer(api.prepareNativeGoalEvent)
			files := map[string]string{}
			api.internalTurnQueueRead = func(_ context.Context, path string) (string, bool, error) { v, ok := files[path]; return v, ok, nil }
			api.internalTurnQueueWrite = func(_ context.Context, path, raw string) error { files[path] = raw; return nil }
			recordOwner := "owner"
			if action == "poster_record" {
				recordOwner = "poster"
				api.lastQueryRequests = map[string]QueryRequest{sid: {userID: "poster"}}
			}
			record := restartContinueRecord{SessionID: sid, QueryID: "parent", Request: QueryRequest{Query: "Original input must not replay", AgentMode: "multi-agent"}, SavedAt: time.Now()}
			if err := api.writeRestartContinueRecords(context.Background(), recordOwner, restartContinueDocument{Chats: map[string]restartContinueRecord{sid: record}}); err != nil {
				t.Fatal(err)
			}
			if action == "missing_record" {
				files = map[string]string{}
			}
			native := llmtypes.NativeGoalUpdate{Provider: "codex-cli", NativeSessionID: "native", Goal: &llmtypes.NativeGoal{Objective: "Finish the task", Status: "active"}}
			if err := api.projectNativeGoal(sid, native, "native_update"); err != nil {
				t.Fatal(err)
			}
			oldControl := controlNativeGoal
			t.Cleanup(func() { controlNativeGoal = oldControl })
			raced, canceled := false, false
			controlNativeGoal = func(_ context.Context, owner, op string) (*llmtypes.NativeGoalUpdate, error) {
				if action == "pause_failed" && op != "show" {
					return nil, errors.New("native pause refused")
				}
				if action == "no_live_process" || (action == "cold_stop" && op == "pause") {
					return nil, llmtypes.NativeGoalUnavailable("this Codex session")
				}
				if owner != sid {
					t.Fatal("control crossed session")
				}
				if action == "stop" && op == "pause" && !canceled {
					t.Fatal("goal pause delayed turn cancellation")
				}
				if op == "show" {
					update := native
					return &update, nil
				}
				if action == "missing_record" {
					t.Fatal("paused native goal without a restore record")
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
			api.internalNativeGoalResume = func(ctx context.Context, req map[string]interface{}, session, owner string) error {
				calls++
				command, ok := llmtypes.NativeGoalCommandForSession(ctx, sid)
				if !ok || command.Action != "resume" || command.Objective != "Finish the task" || session != sid || owner != "owner" {
					t.Fatal("restore lost goal/principal identity")
				}
				raw, _ := json.Marshal(req)
				if strings.Contains(string(raw), "Original input must not replay") {
					t.Fatal("restore replayed interrupted input")
				}
				if !api.nativeGoalRestoreCurrent(ctx, sid, ctx.Value(nativeGoalRestoreContextKey{}).(string)) {
					t.Fatal("restore did not own its consumed receipt")
				}
				if action == "resume_quota" {
					return &llmerrors.Error{Kind: llmerrors.KindQuotaExhausted, Err: errors.New("typed quota"), RetryAt: time.Now().Add(time.Hour)}
				}
				return nil
			}
			receipt := api.pauseNativeGoalForWake(sid)
			if action == "pause_failed" || action == "no_live_process" || action == "missing_record" {
				if receipt != nil {
					t.Fatal("unconfirmed pause armed a restore")
				}
				state, err := api.latestNativeGoal(context.Background(), sid)
				if err != nil || state == nil {
					t.Fatal("wake bookkeeping lost row", err)
				}
				if action == "missing_record" && state.Goal.Status != "active" {
					t.Fatal("missing receipt stranded native pause")
				}
				if action != "missing_record" && state.Goal.Status != "paused" {
					t.Fatal("missing process left active row")
				}
				if action == "no_live_process" && state.Reason != "no_live_process" {
					t.Fatal("missing process not explained")
				}
				return
			}
			if action == "arm_stop_race" {
				if receipt != nil {
					t.Fatal("Stop racing arm resurrected restore")
				}
				return
			}
			if receipt == nil || !api.markNativeGoalWakeStarted(sid, receipt, "wake") {
				t.Fatal("wake did not arm owned restore")
			}
			if action == "poster_record" {
				if receipt.RecordOwner != "poster" {
					t.Fatal("receipt read under chat owner instead of poster")
				}
				api.cancelNativeGoalRestore(sid, "person_message")
				if api.pendingNativeGoalRestore(context.Background(), sid) != nil {
					t.Fatal("poster receipt not cancelled")
				}
				return
			}
			// A paused SDK read must not erase why the goal is paused.
			if err := api.projectNativeGoal(sid, native, "native_read"); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "stop":
				api.activeSessions = map[string]*ActiveSessionInfo{sid: {SessionID: sid, UserID: "owner"}}
				api.agentCancelFuncs = map[string]context.CancelFunc{sid: func() { canceled = true }}
				r := sharedSecretsRequest(http.MethodPost, "/api/cancel-turn", "owner", nil)
				r.Header.Set("X-Session-ID", sid)
				w := httptest.NewRecorder()
				api.handleCancelCurrentTurn(w, r)
				if w.Code != http.StatusNoContent || !canceled {
					t.Fatal("Stop did not cancel owned turn", w.Code)
				}
			case "cold_stop":
				r := sharedSecretsRequest(http.MethodPost, "/api/cancel-turn", "owner", nil)
				r.Header.Set("X-Session-ID", sid)
				w := httptest.NewRecorder()
				api.handleCancelCurrentTurn(w, r)
				if w.Code != http.StatusNoContent {
					t.Fatal("cold Stop refused stored owner", w.Code)
				}
			case "clear":
				api.cancelNativeGoalRestore(sid, "person_goal_command")
				native.Goal = nil
				_ = api.projectNativeGoal(sid, native, "person_clear")
			case "changed":
				api.cancelNativeGoalRestore(sid, "person_goal_command")
				native.Goal = &llmtypes.NativeGoal{Objective: "New goal", Status: "paused"}
				_ = api.projectNativeGoal(sid, native, "intent_set")
			}
			if action == "stop" || action == "cold_stop" || action == "clear" {
				late := native
				late.Goal = &llmtypes.NativeGoal{Objective: "Finish the task", Status: "active"}
				_ = api.projectNativeGoal(sid, late, "native_update")
				projected, err := api.latestNativeGoal(context.Background(), sid)
				if err != nil {
					t.Fatal(err)
				}
				if (action == "stop" || action == "cold_stop") && (projected.Goal == nil || projected.Goal.Status != "paused") {
					t.Fatal("late frame revived stopped goal")
				}
				if action == "clear" && projected.Goal != nil {
					t.Fatal("late frame revived cleared goal")
				}
			}
			var failure error
			if action == "failed_wake" || action == "retry_after_failure" {
				failure = errors.New("wake failed")
			}
			api.restoreNativeGoalAfterWake(sid, receipt, failure)
			if action == "retry_after_failure" {
				receipt = api.pauseNativeGoalForWake(sid)
				if receipt == nil || !api.markNativeGoalWakeStarted(sid, receipt, "second-wake") {
					t.Fatal("existing wake retry lost paused goal restoration")
				}
			}
			api.restoreNativeGoalAfterWake(sid, receipt, nil)
			want := 0
			if action == "resume" || action == "retry_after_failure" || action == "resume_quota" {
				want = 1
			}
			if action == "resume_quota" {
				state, _ := api.latestNativeGoal(context.Background(), sid)
				if state == nil || state.Goal == nil || state.Goal.Status != "usage_limited" {
					t.Fatal("claimed restore quota left paused goal")
				}
				r := sharedSecretsRequest(http.MethodPost, "/api/query", "owner", nil)
				req := QueryRequest{Query: usageLimitContinueMessage, AgentMode: "multi-agent", IsAutoNotification: true, TriggeredBy: usageLimitContinueTrigger}
				r, handled := api.prepareNativeGoalQuery(httptest.NewRecorder(), r, &req, sid, "codex-cli", true, true)
				command, ok := llmtypes.NativeGoalCommandForSession(r.Context(), sid)
				if handled || !ok || command.Action != "resume" {
					t.Fatal("reset did not resume quota-blocked restore")
				}
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
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "codex-cli,muse-cli")
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
	for _, action := range []string{"Task", "pause", "clear"} {
		req.Query, req.PinRunMode = "/goal "+action, true
		denied := send("owner")
		if denied.Code != http.StatusForbidden || calls != 0 || !strings.Contains(denied.Body.String(), "chat's Builder") {
			t.Fatalf("Run goal control escaped owner/Builder gate: %d %s", denied.Code, denied.Body.String())
		}
	}
	req.Query, req.PinRunMode = "/goal pause", false
	allowed := send("owner")
	if allowed.Code != 200 || calls != 1 {
		t.Fatalf("own admitted control failed: %d %s calls=%d", allowed.Code, allowed.Body.String(), calls)
	}
	// Owner live regression: an unsupported /goal is information, not a failed
	// native turn. Check the same admitted HTTP and internal/MCP input paths.
	muse := (&providerAccountsEnv{api: f.api}).addAccount(t, "owner", map[string]interface{}{"provider": "muse-cli", "display_name": "Muse test account", "auth_method": "cli_login"})
	req.Provider, req.ModelID, req.ConnectionID, req.Query = "muse-cli", "muse-cli", muse.ID, "/goal Task"
	req.LLMConfigSource = llmConfigSourceAgentProfile // the same saved project selection the composer submits
	req.LLMConfig = &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "muse-cli", ModelID: "muse-cli"}}
	info := send("owner")
	var notice struct {
		Status  string `json:"status"`
		QueryID string `json:"query_id"`
		Notice  struct {
			Type string `json:"type"`
		} `json:"notice"`
	}
	if info.Code != http.StatusOK || json.Unmarshal(info.Body.Bytes(), &notice) != nil || notice.Status != "notice" || notice.QueryID != "" || notice.Notice.Type != "native_goal_notice" || calls != 1 {
		t.Fatalf("unsupported goal became a failed/native turn: %d %s", info.Code, info.Body.String())
	}
	f.api.agentCancelFuncs = map[string]context.CancelFunc{}
	request, err := queryRequestToMap(req)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.api.startSessionInternalWithResult(context.Background(), request, sid, "owner", nil)
	if err != nil || result.QueryID != "" || result.FinalResponse != "Goal mode is not available for this AI program" {
		t.Fatal("internal goal notice failed", result, err)
	}
	capabilities := nativeGoalProgramCapabilities()
	if !capabilities["codex-cli"] || capabilities["muse-cli"] || capabilities["claude-code"] || capabilities["cursor-cli"] || capabilities["pi-cli"] || capabilities["agy-cli"] {
		t.Fatal("server offered unsupported native goals", capabilities)
	}
	state, err := f.api.latestNativeGoal(context.Background(), sid)
	if err != nil || state == nil || state.NativeSessionID != "native" || state.Goal.Status != "paused" {
		t.Fatal("admitted pause was not projected", err)
	}
}
