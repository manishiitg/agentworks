package events

// CodingAgentBackgroundTaskEvent carries a provider journal transition outside
// the foreground turn lifecycle. NativeSequence identifies a committed row.
type CodingAgentBackgroundTaskEvent struct {
	BaseEventData
	Provider        string `json:"provider"`
	NativeSessionID string `json:"native_session_id"`
	RunID           string `json:"run_id"`
	TaskID          string `json:"task_id"`
	NativeSequence  int64  `json:"native_sequence"`
	Kind            string `json:"kind"`
	Message         string `json:"message,omitempty"`
}

func (*CodingAgentBackgroundTaskEvent) GetEventType() EventType { return CodingAgentBackgroundTask }
