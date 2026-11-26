package events

import "github.com/manishiitg/multi-llm-provider-go/llmtypes"

// NativeSubagentTranscriptEvent keeps a CLI child conversation separate from
// the parent's assistant answer and tool lifecycle.
type NativeSubagentTranscriptEvent struct {
	BaseEventData
	Transcripts []llmtypes.NativeSubagentTranscript `json:"transcripts"`
}

func (*NativeSubagentTranscriptEvent) GetEventType() EventType {
	return NativeSubagentTranscript
}
