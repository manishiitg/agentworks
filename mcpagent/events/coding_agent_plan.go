package events

import "github.com/manishiitg/multi-llm-provider-go/llmtypes"

const CodingAgentPlan EventType = "coding_agent_plan"

// CodingAgentPlanEvent replaces the previous native checklist in this turn.
type CodingAgentPlanEvent struct {
	BaseEventData
	llmtypes.CodingAgentPlan
}

func (*CodingAgentPlanEvent) GetEventType() EventType { return CodingAgentPlan }
