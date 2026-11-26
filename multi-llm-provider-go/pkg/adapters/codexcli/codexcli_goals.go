package codexcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const codexGoalObjectiveMaxChars = 4000

var codexGoalFollowupWait = 30 * time.Second

type codexThreadGoal struct {
	Objective       string `json:"objective"`
	Status          string `json:"status"`
	TokensUsed      *int64 `json:"tokensUsed"`
	TokenBudget     *int64 `json:"tokenBudget"`
	TimeUsedSeconds *int64 `json:"timeUsedSeconds"`
}

func (g *codexThreadGoal) projection() (*llmtypes.NativeGoal, error) {
	if g == nil {
		return nil, nil
	}
	status := map[string]string{"active": "active", "paused": "paused", "blocked": "blocked", "usageLimited": "usage_limited", "budgetLimited": "budget_limited", "complete": "complete"}[g.Status]
	if status == "" {
		return nil, fmt.Errorf("Codex reported an unknown goal status")
	}
	if strings.TrimSpace(g.Objective) == "" {
		return nil, fmt.Errorf("Codex reported an empty goal objective")
	}
	return &llmtypes.NativeGoal{Objective: g.Objective, Status: status, TokensUsed: g.TokensUsed, TokenBudget: g.TokenBudget, TimeUsedSeconds: g.TimeUsedSeconds}, nil
}

func recordCodexGoal(s *codexAppSession, goal *llmtypes.NativeGoal, emit func(llmtypes.StreamChunk), reason string) {
	s.mu.Lock()
	changed := !reflect.DeepEqual(s.goal, goal)
	s.goal = goal
	if goal == nil || (goal.Status != "active" && goal.Status != "paused") {
		s.goalPausedForWake = false
	}
	owner, thread, stateChanged := s.owner, s.threadID, s.goalStateChanged
	s.mu.Unlock()
	if changed && stateChanged != nil {
		select {
		case stateChanged <- struct{}{}:
		default:
		}
	}
	if !changed && reason != "native_read" {
		return
	}
	status, action := "cleared", "cleared"
	if goal != nil {
		status = goal.Status
		action = "updated"
		switch status {
		case "complete", "blocked", "paused":
			action = status
		}
	}
	log.Printf("[AGENT_GOAL] session=%s native=%s action=%s status=%s reason=%s", owner, thread, action, status, reason)
	if emit != nil {
		emit(llmtypes.NativeGoalChunk(llmtypes.NativeGoalUpdate{Provider: "codex-cli", NativeSessionID: thread, Goal: goal, Reason: reason}))
	}
}

func readCodexGoal(ctx context.Context, s *codexAppSession, r *codexRPC, thread string, emit func(llmtypes.StreamChunk)) (*llmtypes.NativeGoal, error) {
	var reply struct {
		Goal *codexThreadGoal `json:"goal"`
	}
	if err := r.call(ctx, "thread/goal/get", map[string]any{"threadId": thread}, &reply); err != nil {
		var rpcError *codexRPCRequestError
		if errors.As(err, &rpcError) && rpcError.Code == -32601 {
			log.Printf("[AGENT_GOAL] session=%s native=%s action=skipped status=unavailable reason=unsupported_native_goal", s.owner, thread)
			return nil, llmtypes.NativeGoalUnavailable("this Codex version")
		}
		log.Printf("[AGENT_GOAL] session=%s native=%s action=failed status=unknown reason=goal_get_failed", s.owner, thread)
		return nil, fmt.Errorf("Codex could not read the native goal")
	}
	goal, err := reply.Goal.projection()
	if err != nil {
		return nil, err
	}
	recordCodexGoal(s, goal, emit, "native_read")
	return goal, nil
}

func setCodexGoalStatus(ctx context.Context, s *codexAppSession, r *codexRPC, thread, status string, emit func(llmtypes.StreamChunk), reason string) (*llmtypes.NativeGoal, error) {
	var reply struct {
		Goal *codexThreadGoal `json:"goal"`
	}
	if err := r.call(ctx, "thread/goal/set", map[string]any{"threadId": thread, "status": status}, &reply); err != nil {
		log.Printf("[AGENT_GOAL] session=%s native=%s action=failed status=%s reason=goal_set_failed", s.owner, thread, status)
		return nil, fmt.Errorf("Codex could not change the native goal")
	}
	goal, err := reply.Goal.projection()
	if err != nil {
		return nil, err
	}
	if goal == nil || goal.Status != status {
		log.Printf("[AGENT_GOAL] session=%s native=%s action=failed status=%s reason=goal_status_not_confirmed", s.owner, thread, status)
		return nil, fmt.Errorf("Codex did not confirm the goal status")
	}
	if status == "paused" {
		s.mu.Lock()
		s.goalActivationPending = false
		if reason != "background_wake" {
			s.goalPausedForWake = false
		}
		s.mu.Unlock()
	}
	recordCodexGoal(s, goal, emit, reason)
	return goal, nil
}

// Controls use the already-owned process/thread only; they never create a new
// session or submit an unvalidated objective as an ordinary model message.
func ControlCodexNativeGoal(ctx context.Context, owner, action string) (*llmtypes.NativeGoalUpdate, error) {
	s := codexAppSessionForOwner(owner)
	if s == nil {
		return nil, llmtypes.NativeGoalUnavailable("this Codex session")
	}
	s.mu.Lock()
	r, thread := s.rpc, s.threadID
	s.mu.Unlock()
	if r == nil || thread == "" {
		return nil, llmtypes.NativeGoalUnavailable("this Codex session")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	goal, err := func() (*llmtypes.NativeGoal, error) {
		goal, err := readCodexGoal(ctx, s, r, thread, nil)
		if err != nil || action == "show" || goal == nil {
			return goal, err
		}
		switch action {
		case "pause", "wake_pause":
			reason := "person_pause"
			if action == "wake_pause" {
				reason = "background_wake"
			}
			// Register the paused-wake lifetime before the RPC: its native
			// notification can finish the owning turn before the reply is read.
			s.mu.Lock()
			s.goalPausedForWake = action == "wake_pause" && (goal.Status == "active" || goal.Status == "paused" || goal.Status == "usage_limited")
			s.mu.Unlock()
			if goal.Status == "active" || goal.Status == "usage_limited" {
				goal, err = setCodexGoalStatus(ctx, s, r, thread, "paused", nil, reason)
				if err != nil {
					abandonCodexGoalWakePause(s, r, thread)
					return nil, err
				}
			}
			return goal, nil
		case "clear":
			if err := r.call(ctx, "thread/goal/clear", map[string]any{"threadId": thread}, nil); err != nil {
				return nil, fmt.Errorf("Codex could not clear the native goal")
			}
			s.mu.Lock()
			s.goalActivationPending = false
			s.goalPausedForWake = false
			s.mu.Unlock()
			recordCodexGoal(s, nil, nil, "person_clear")
			return nil, nil
		default:
			return nil, fmt.Errorf("unsupported native goal control")
		}

	}()
	if err != nil {
		return nil, err
	}
	return &llmtypes.NativeGoalUpdate{Provider: "codex-cli", NativeSessionID: thread, Goal: goal, Reason: "person_" + action}, nil
}

func (c *CodexCLIAdapter) prepareCodexGoalCommand(ctx context.Context, s *codexAppSession, r *codexRPC, thread string, command llmtypes.NativeGoalCommand, emit func(llmtypes.StreamChunk)) (string, bool, *llmtypes.ContentResponse, error) {
	objective := strings.TrimSpace(command.Objective)
	if command.Action == "set" && (objective == "" || utf8.RuneCountInString(objective) > codexGoalObjectiveMaxChars) {
		return "", false, nil, fmt.Errorf("The goal objective must be between 1 and 4000 characters; the current goal was not changed")
	}
	current, err := readCodexGoal(ctx, s, r, thread, emit)
	if err != nil {
		return "", false, nil, err
	}
	switch command.Action {
	case "show":
		return "", false, codexAppResponse(s, "", codexAppUsage{}), nil
	case "pause":
		if current != nil {
			_, err = setCodexGoalStatus(ctx, s, r, thread, "paused", emit, "person_pause")
		}
		return "", false, codexAppResponse(s, "", codexAppUsage{}), err
	case "clear":
		err = r.call(ctx, "thread/goal/clear", map[string]any{"threadId": thread}, nil)
		if err == nil {
			s.mu.Lock()
			s.goalActivationPending = false
			s.goalPausedForWake = false
			s.mu.Unlock()
			recordCodexGoal(s, nil, emit, "person_clear")
		}
		return "", false, codexAppResponse(s, "", codexAppUsage{}), err
	case "resume":
		if current == nil {
			return "", false, nil, fmt.Errorf("No goal is set")
		}
		if objective != "" && (current.Objective != objective || (current.Status != "paused" && current.Status != "usage_limited")) {
			log.Printf("[AGENT_GOAL] session=%s native=%s action=skipped status=%s reason=restore_target_changed", s.owner, thread, current.Status)
			return "", false, nil, fmt.Errorf("The goal changed; automatic restoration was canceled")
		}
		objective = current.Objective
	case "set":
		// Validate before clear so a too-long objective cannot destroy the old goal.
		if current != nil {
			if err = r.call(ctx, "thread/goal/clear", map[string]any{"threadId": thread}, nil); err != nil {
				return "", false, nil, err
			}
		}
		var reply struct {
			Goal *codexThreadGoal `json:"goal"`
		}
		err = r.call(ctx, "thread/goal/set", map[string]any{"threadId": thread, "objective": objective, "status": "paused"}, &reply)
		if err != nil {
			return "", false, nil, fmt.Errorf("Codex could not set the native goal")
		}
		goal, decodeErr := reply.Goal.projection()
		if decodeErr != nil {
			return "", false, nil, decodeErr
		}
		if goal == nil || goal.Status != "paused" || goal.Objective != objective {
			return "", false, nil, fmt.Errorf("Codex did not confirm the new goal")
		}
		s.mu.Lock()
		s.goal = goal
		s.mu.Unlock()
		log.Printf("[AGENT_GOAL] session=%s native=%s action=set status=paused reason=awaiting_current_turn_settings", s.owner, thread)
	default:
		return "", false, nil, fmt.Errorf("unsupported native goal command")
	}
	s.mu.Lock()
	s.goalPausedForWake = false
	s.goalActivationPending = true
	s.mu.Unlock()
	return "This thread's goal is already set. Do not call create_goal. Start working on it now: " + objective, true, nil, nil
}

// Goal notifications are thread-scoped and carry no turnId. Handle them before
// ordinary turn filtering, including during the native continuation grace.
func handleCodexGoalNotification(s *codexAppSession, method string, raw json.RawMessage, thread string, emit func(llmtypes.StreamChunk)) (bool, error) {
	if method != "thread/goal/updated" && method != "thread/goal/cleared" {
		return false, nil
	}
	var notification struct {
		ThreadID string           `json:"threadId"`
		Goal     *codexThreadGoal `json:"goal"`
	}
	if err := json.Unmarshal(raw, &notification); err != nil {
		return true, fmt.Errorf("invalid Codex goal notification")
	}
	if notification.ThreadID != thread {
		return true, nil
	}
	var goal *llmtypes.NativeGoal
	if method == "thread/goal/updated" {
		if notification.Goal == nil {
			return true, fmt.Errorf("Codex goal update is missing its goal")
		}
		var err error
		goal, err = notification.Goal.projection()
		if err != nil {
			return true, err
		}
	}
	s.mu.Lock()
	held := s.goalActivationPending && (goal == nil || goal.Status == "paused")
	if !held {
		s.goalActivationPending = false
	}
	s.mu.Unlock()
	if !held {
		recordCodexGoal(s, goal, emit, "native_update")
	}
	return true, nil
}

func codexGoalActive(s *codexAppSession) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.goalActivationPending || (s.goal != nil && s.goal.Status == "active")
}

// Startup can fail after the native goal accepted activation. Cleanup cannot
// use the canceled caller context, and a failed cleanup must end the process.
func startCodexGoalTurn(ctx context.Context, s *codexAppSession, r *codexRPC, thread string, params any, activate bool, emit func(llmtypes.StreamChunk)) (string, error) {
	startedOK := false
	defer func() {
		if activate && !startedOK {
			s.mu.Lock()
			s.goalActivationPending = false
			s.mu.Unlock()
		}
	}()
	var started struct {
		Turn codexAppTurn `json:"turn"`
	}
	if err := r.call(ctx, "turn/start", params, &started); err != nil {
		if activate {
			log.Printf("[AGENT_GOAL] session=%s native=%s action=failed status=paused reason=initial_turn_failed", s.owner, thread)
		}
		r.close()
		return "", err
	}
	turn := started.Turn.ID
	if turn == "" {
		r.close()
		return "", fmt.Errorf("codex app-server returned no turn id")
	}
	if activate {
		if _, err := setCodexGoalStatus(ctx, s, r, thread, "active", emit, "goal_started"); err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, pauseErr := setCodexGoalStatus(cleanup, s, r, thread, "paused", emit, "activation_failed")
			interruptErr := r.call(cleanup, "turn/interrupt", map[string]any{"threadId": thread, "turnId": turn}, nil)
			if pauseErr != nil || interruptErr != nil {
				log.Printf("[AGENT_GOAL] session=%s native=%s action=failed status=unknown reason=activation_cleanup_failed", s.owner, thread)
				r.close()
			}
			return "", err
		}
	}
	startedOK = true
	return turn, nil
}

func pauseCodexGoalForTurn(ctx context.Context, s *codexAppSession, r *codexRPC, thread string, pause llmtypes.NativeGoalTurnPause, emit func(llmtypes.StreamChunk)) error {
	goal, err := readCodexGoal(ctx, s, r, thread, nil)
	if err != nil {
		abandonCodexGoalWakePause(s, r, thread)
		return err
	}
	s.mu.Lock()
	s.goalPausedForWake = pause.Reason == "background_wake" && goal != nil && (goal.Status == "active" || goal.Status == "paused" || goal.Status == "usage_limited")
	s.mu.Unlock()

	if goal != nil && (goal.Status == "active" || (goal.Status == "usage_limited" && pause.Reason == "background_wake")) {
		_, err = setCodexGoalStatus(ctx, s, r, thread, "paused", emit, pause.Reason)
		if err != nil {
			abandonCodexGoalWakePause(s, r, thread)
		}
		return err
	}
	recordCodexGoal(s, goal, emit, "native_read")
	return nil
}

// A failed pause must not retain a non-persistent native goal without a consumer.
func abandonCodexGoalWakePause(s *codexAppSession, r *codexRPC, thread string) {
	s.mu.Lock()
	s.goalPausedForWake = false
	s.mu.Unlock()
	r.close()
	log.Printf("[AGENT_GOAL] session=%s native=%s action=failed status=unknown reason=wake_pause_unconfirmed_process_closed", s.owner, thread)
}
