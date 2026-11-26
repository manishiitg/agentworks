package events

import "github.com/manishiitg/multi-llm-provider-go/llmtypes"

// CodingAgentNativeActivity is a bounded observation, not a run-completion
// command. The host retains its normal execution and permission decisions.
const CodingAgentNativeActivity EventType = "coding_agent_native_activity"

type CodingAgentNativeActivityEvent struct {
	BaseEventData
	Record llmtypes.NativeRecord `json:"record"`
}

func (*CodingAgentNativeActivityEvent) GetEventType() EventType { return CodingAgentNativeActivity }

// NewCodingAgentNativeActivity excludes reply/question contents from the
// operational projection. Visible messages use their normal chat event path.
func NewCodingAgentNativeActivity(record llmtypes.NativeRecord) *CodingAgentNativeActivityEvent {
	record.Text = ""
	return &CodingAgentNativeActivityEvent{BaseEventData: BaseEventData{EventID: record.ID, Timestamp: record.Timestamp}, Record: record}
}
