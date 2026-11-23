package events

import "github.com/manishiitg/multi-llm-provider-go/llmtypes"

const NativeGoalUpdated EventType = "native_goal_updated"

// Private chat state: keep the objective out of usage/account snapshots and
// assistant content. The host decides how to present its latest native goal.
type NativeGoalEvent struct {
	BaseEventData
	Update llmtypes.NativeGoalUpdate `json:"update"`
}

func (*NativeGoalEvent) GetEventType() EventType { return NativeGoalUpdated }
