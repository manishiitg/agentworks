package musecli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Muse's own goal (`muse serve` goal/set, goal/pause, goal/resume, goal/clear):
// Muse keeps working until the objective is met. /goal in a Muse chat sends
// these; Muse reports the goal's state with session/goalChanged, which becomes
// the chat's goal bar. AgentWorks runs no loop of its own.

// museMSPGoal is Muse's goal block (session/goalChanged).
type museMSPGoal struct {
	Objective       string `json:"objective"`
	Status          string `json:"status"`
	PercentComplete *int   `json:"percentComplete"`
	CurrentWork     string `json:"currentWork"`
	NextWork        string `json:"nextWork"`
	CreatedAtMs     int64  `json:"createdAtMs"`
	UpdatedAtMs     int64  `json:"updatedAtMs"`
}

func (g *museMSPGoal) native() *llmtypes.NativeGoal {
	if g == nil || strings.TrimSpace(g.Objective) == "" {
		return nil
	}
	goal := &llmtypes.NativeGoal{Objective: g.Objective, Status: g.Status, PercentComplete: g.PercentComplete, CurrentWork: strings.TrimSpace(g.CurrentWork), NextWork: strings.TrimSpace(g.NextWork)}
	if g.CreatedAtMs > 0 && g.UpdatedAtMs >= g.CreatedAtMs {
		seconds := (g.UpdatedAtMs - g.CreatedAtMs) / 1000
		goal.TimeUsedSeconds = &seconds
	}
	return goal
}

// museMSPGoalFollowWait is how long a finished turn waits for Muse to start
// its next turn of a goal that is still active.
var museMSPGoalFollowWait = 20 * time.Second

// observeMuseMSPGoal records a session/goalChanged of this session and
// returns the chat's goal update; ok is false for any other event.
func observeMuseMSPGoal(s *museMSPSession, msg museRPCMessage) (llmtypes.NativeGoalUpdate, bool) {
	if msg.Method != "session/goalChanged" {
		return llmtypes.NativeGoalUpdate{}, false
	}
	var p struct {
		SessionID string       `json:"sessionId"`
		Goal      *museMSPGoal `json:"goal"`
	}
	if json.Unmarshal(msg.Params, &p) != nil {
		return llmtypes.NativeGoalUpdate{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.SessionID != "" && s.sessionID != "" && p.SessionID != s.sessionID {
		return llmtypes.NativeGoalUpdate{}, false
	}
	s.goal = p.Goal.native()
	s.goalKnown = true
	status := "cleared"
	if s.goal != nil {
		status = s.goal.Status
	}
	log.Printf("[AGENT_GOAL] session=%s provider=muse-cli native=%s action=recorded status=%s reason=native_goal_changed", s.owner, s.sessionID, status)
	return llmtypes.NativeGoalUpdate{Provider: "muse-cli", NativeSessionID: s.sessionID, Goal: s.goal, Reason: "native_goal_changed"}, true
}

func (s *museMSPSession) goalActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.goal != nil && s.goal.Status == "active"
}

func (s *museMSPSession) goalUpdate(reason string) llmtypes.NativeGoalUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return llmtypes.NativeGoalUpdate{Provider: "muse-cli", NativeSessionID: s.sessionID, Goal: s.goal, Reason: reason}
}

// museMSPGoalCall sends one goal verb for this session; set and edit carry the objective.
func museMSPGoalCall(ctx context.Context, s *museMSPSession, action, objective string) error {
	params := map[string]any{"commandId": museCommandID(), "sessionId": s.sessionID}
	if action == "set" || action == "edit" {
		params["objective"] = objective
	}
	if err := s.rpc.call(ctx, "goal/"+action, params, nil); err != nil {
		log.Printf("[AGENT_GOAL] session=%s provider=muse-cli native=%s action=failed command=%s error=%v", s.owner, s.sessionID, action, err)
		return fmt.Errorf("Muse could not %s the goal: %w", action, err)
	}
	log.Printf("[AGENT_GOAL] session=%s provider=muse-cli native=%s action=sent command=%s", s.owner, s.sessionID, action)
	return nil
}

// museMSPGoalTurnText is the turn that carries a goal: the objective, as the
// person's message, so the turn also carries AgentWorks' instructions; the
// goal verb then attaches the goal to this running turn.
func museMSPGoalTurnText(command llmtypes.NativeGoalCommand, current *llmtypes.NativeGoal) (string, error) {
	switch command.Action {
	case "set":
		objective := strings.TrimSpace(command.Objective)
		if objective == "" {
			return "", fmt.Errorf("Set a goal with /goal and what to do")
		}
		return "Goal: " + objective, nil
	case "resume":
		if current == nil {
			return "", fmt.Errorf("No goal is set")
		}
		return "Continue the goal: " + current.Objective, nil
	}
	return "", nil
}

// controlMuseMSPGoal answers show, pause and clear without a turn.
func controlMuseMSPGoal(ctx context.Context, s *museMSPSession, action string) (*llmtypes.NativeGoalUpdate, error) {
	switch action {
	case "show":
	case "pause", "clear":
		if err := museMSPGoalCall(ctx, s, action, ""); err != nil {
			return nil, err
		}
		s.mu.Lock()
		if action == "clear" {
			s.goal = nil
		} else if s.goal != nil {
			paused := *s.goal
			paused.Status = "paused"
			s.goal = &paused
		}
		s.mu.Unlock()
	default:
		return nil, fmt.Errorf("unsupported native goal control")
	}
	update := s.goalUpdate("person_" + action)
	return &update, nil
}

// ControlMuseNativeGoal shows, pauses or clears the goal of this chat's Muse
// session while a turn runs. ok is false when the chat has no `muse serve`
// session.
func ControlMuseNativeGoal(ctx context.Context, owner, action string) (*llmtypes.NativeGoalUpdate, bool, error) {
	museMSPSessions.Lock()
	s := museMSPSessions.m[strings.TrimSpace(owner)]
	museMSPSessions.Unlock()
	if s == nil || s.rpc == nil {
		return nil, false, nil
	}
	if action == "wake_pause" {
		action = "pause"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	update, err := controlMuseMSPGoal(ctx, s, action)
	return update, true, err
}

// museMSPNextGoalTurn waits, after a turn ended, for the next turn Muse starts
// for a goal that is still active. "" when the goal stopped or none started.
func (a *MuseCLIAdapter) museMSPNextGoalTurn(ctx context.Context, s *museMSPSession, events <-chan museRPCMessage, finished string, emit func(llmtypes.StreamChunk)) string {
	timer := time.NewTimer(museMSPGoalFollowWait)
	defer timer.Stop()
	for s.goalActive() {
		select {
		case <-ctx.Done():
			return ""
		case <-s.rpc.done:
			return ""
		case <-timer.C:
			log.Printf("[AGENT_GOAL] session=%s provider=muse-cli native=%s action=skipped reason=no_next_goal_turn", s.owner, s.sessionID)
			return ""
		case msg := <-events:
			if update, ok := observeMuseMSPGoal(s, msg); ok {
				emit(llmtypes.NativeGoalChunk(update))
				continue
			}
			if msg.Method == "turn/started" {
				var p struct {
					TurnID string `json:"turnId"`
				}
				if json.Unmarshal(msg.Params, &p) == nil && p.TurnID != "" && p.TurnID != finished {
					return p.TurnID
				}
			}
			a.handleIdleMuseMSP(s, msg)
		}
	}
	return ""
}
