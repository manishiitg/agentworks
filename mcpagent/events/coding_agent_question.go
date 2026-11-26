package events

// CodingAgentQuestionOption is one provider-authored choice. Providers retain
// option order because it is also the native widget order.
type CodingAgentQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// CodingAgentQuestionPrompt is shared by native question tools such as Muse
// request_user_input and Claude AskUserQuestion. IDs may be synthesized from
// the provider's ordered question indexes when its journal omits them.
type CodingAgentQuestionPrompt struct {
	ID          string `json:"id"`
	Header      string `json:"header,omitempty"`
	Question    string `json:"question"`
	MultiSelect bool   `json:"multi_select,omitempty"`
	AllowOther  bool   `json:"allow_other,omitempty"`
	// MinSelections/MaxSelections bound a multi-select answer; zero means
	// at least one and up to every option.
	MinSelections int                         `json:"min_selections,omitempty"`
	MaxSelections int                         `json:"max_selections,omitempty"`
	Options       []CodingAgentQuestionOption `json:"options"`
}

type CodingAgentQuestionAnswer struct {
	ID             string   `json:"id"`
	SelectedLabels []string `json:"selected_labels,omitempty"`
	OtherText      string   `json:"other_text,omitempty"`
}

// CodingAgentQuestionEvent is a provider-neutral requested/settled lifecycle.
// PromptID is a native prompt ID where available or a provider tool-use ID.
// The provider adapter owns correlation and validated native input delivery.
type CodingAgentQuestionEvent struct {
	BaseEventData
	Provider        string                      `json:"provider"`
	NativeSessionID string                      `json:"native_session_id"`
	RunID           string                      `json:"run_id,omitempty"`
	NativeSequence  int64                       `json:"native_sequence,omitempty"`
	PromptID        string                      `json:"prompt_id"`
	Kind            string                      `json:"kind"` // requested or settled
	Questions       []CodingAgentQuestionPrompt `json:"questions,omitempty"`
	Answers         []CodingAgentQuestionAnswer `json:"answers,omitempty"`
	Outcome         string                      `json:"outcome,omitempty"`
}

func (*CodingAgentQuestionEvent) GetEventType() EventType { return CodingAgentQuestion }
