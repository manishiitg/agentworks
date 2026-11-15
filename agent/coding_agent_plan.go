package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func (sm *streamingManager) emitCodingAgentPlan(ctx context.Context, a *Agent, plan *llmtypes.CodingAgentPlan) {
	if plan == nil {
		return
	}
	if plan.Provider == "claude-code" {
		if lifecycle := canonicalTurnLifecycleFromContext(ctx); lifecycle != nil {
			lifecycle.mu.Lock()
			lifecycle.plan.Replace(plan)
			lifecycle.mu.Unlock()
		} else {
			sm.claudeTaskPlan.Replace(plan)
		}
	}
	snapshot := *plan
	snapshot.Items = append([]llmtypes.CodingAgentPlanItem{}, plan.Items...)
	if snapshot.Provider == "" {
		snapshot.Provider = string(a.provider)
	}
	turn := fmt.Sprintf("generation-%d", sm.turn)
	if lifecycle := canonicalTurnLifecycleFromContext(ctx); lifecycle != nil {
		turn = lifecycle.id
	}
	if snapshot.PlanID == "" {
		snapshot.PlanID = a.sessionID + ":" + turn
	}
	// Identity is not part of the content fingerprint: each native tool invocation
	// has its own call ID but may repeat the exact same checklist.
	raw, _ := json.Marshal(struct {
		Items       []llmtypes.CodingAgentPlanItem
		Explanation string
	}{snapshot.Items, snapshot.Explanation})
	fingerprint := snapshot.PlanID + ":" + string(raw)
	if sm.lastPlanSnapshot == fingerprint {
		return
	}
	sm.lastPlanSnapshot = fingerprint
	done := 0
	for _, item := range snapshot.Items {
		if item.Status == "completed" {
			done++
		}
	}
	a.emitTypedEvent(ctx, &events.CodingAgentPlanEvent{BaseEventData: events.BaseEventData{Timestamp: time.Now()}, CodingAgentPlan: snapshot})
	if logger := getLogger(a); logger != nil {
		logger.Info("[CODING_AGENT_PLAN] snapshot",
			loggerv2.String("session_id", a.sessionID), loggerv2.String("turn_id", turn), loggerv2.String("native_turn_id", snapshot.NativeTurnID), loggerv2.String("plan_id", snapshot.PlanID), loggerv2.String("provider", snapshot.Provider), loggerv2.Int("items", len(snapshot.Items)), loggerv2.Int("done", done))
	}
}

// Task tools are deltas; keep their state with the accepted turn, including
// retained terminal sends that bypass the normal GenerateContent stream.
func (sm *streamingManager) successfulToolPlan(ctx context.Context, name, args, result string) (*llmtypes.CodingAgentPlan, bool) {
	switch name {
	case "TaskCreate", "TaskUpdate", "TaskList", "TodoWrite":
		if lifecycle := canonicalTurnLifecycleFromContext(ctx); lifecycle != nil {
			lifecycle.mu.Lock()
			defer lifecycle.mu.Unlock()
			return lifecycle.plan.Apply(name, args, result)
		}
		return sm.claudeTaskPlan.Apply(name, args, result)
	default:
		return llmtypes.CodingAgentToolPlan(name, args)
	}
}
