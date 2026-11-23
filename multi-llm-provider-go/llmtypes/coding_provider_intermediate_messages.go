package llmtypes

import (
	"encoding/json"
	"strings"
)

const (
	// CodingProviderIntermediateMessagesAdditionalKey mirrors
	// CodingProviderSessionHandleAdditionalKey: the typed value lives
	// on GenerationInfo, with a dup in Additional for older callers
	// that forward Additional verbatim into event metadata.
	CodingProviderIntermediateMessagesAdditionalKey = "coding_provider_intermediate_messages"
)

// CodingProviderIntermediateMessages is the transport-agnostic surface
// for "what happened INSIDE the coding agent's loop during this turn"
// — the text chunks, tool_use calls, and tool_result responses that
// the model and its tooling exchanged before returning the final
// assistant text on ContentResponse.
//
// For tmux transports (Claude Code, Codex CLI, Cursor CLI, and Pi CLI), the messages
// are reconstructed from the CLI's local sidecar transcript by the
// adapter. For structured/API transports the loop is driven by the
// agent layer, not the adapter, so this field is typically empty —
// the agent already sees those tool_use/tool_result turns directly.
//
// Consumers (mcpagent / workflow conversation log) splice the
// Messages slice into their own conversation_history BEFORE the
// final assistant message so the persisted log captures the full
// internal trail in the same shape as outer-loop messages.
type CodingProviderIntermediateMessages struct {
	Provider  string           `json:"provider,omitempty"`
	Transport string           `json:"transport,omitempty"`
	Messages  []MessageContent `json:"messages,omitempty"`
}

func (m CodingProviderIntermediateMessages) Empty() bool {
	return strings.TrimSpace(m.Provider) == "" &&
		strings.TrimSpace(m.Transport) == "" &&
		len(m.Messages) == 0
}

// AttachCodingProviderIntermediateMessages stores the messages in the
// typed GenerationInfo field and mirrors them into Additional for
// callers that forward Additional verbatim.
func AttachCodingProviderIntermediateMessages(genInfo *GenerationInfo, messages CodingProviderIntermediateMessages) {
	if genInfo == nil || messages.Empty() {
		return
	}
	genInfo.CodingProviderIntermediateMessages = &messages
	if genInfo.Additional == nil {
		genInfo.Additional = map[string]interface{}{}
	}
	genInfo.Additional[CodingProviderIntermediateMessagesAdditionalKey] = messages
}

// ExtractCodingProviderIntermediateMessages pulls the typed value
// preferentially, falling back to the Additional mirror so callers
// that lose the typed field over JSON still recover the data.
func ExtractCodingProviderIntermediateMessages(genInfo *GenerationInfo) (CodingProviderIntermediateMessages, bool) {
	if genInfo == nil {
		return CodingProviderIntermediateMessages{}, false
	}
	if genInfo.CodingProviderIntermediateMessages != nil && !genInfo.CodingProviderIntermediateMessages.Empty() {
		return *genInfo.CodingProviderIntermediateMessages, true
	}
	if genInfo.Additional == nil {
		return CodingProviderIntermediateMessages{}, false
	}
	if v, ok := genInfo.Additional[CodingProviderIntermediateMessagesAdditionalKey]; ok {
		if msgs, ok := codingProviderIntermediateMessagesFromValue(v); ok && !msgs.Empty() {
			return msgs, true
		}
	}
	return CodingProviderIntermediateMessages{}, false
}

func ExtractCodingProviderIntermediateMessagesFromResponse(resp *ContentResponse) (CodingProviderIntermediateMessages, bool) {
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		return CodingProviderIntermediateMessages{}, false
	}
	return ExtractCodingProviderIntermediateMessages(resp.Choices[0].GenerationInfo)
}

func codingProviderIntermediateMessagesFromValue(value interface{}) (CodingProviderIntermediateMessages, bool) {
	switch typed := value.(type) {
	case CodingProviderIntermediateMessages:
		return typed, !typed.Empty()
	case *CodingProviderIntermediateMessages:
		if typed == nil {
			return CodingProviderIntermediateMessages{}, false
		}
		return *typed, !typed.Empty()
	case map[string]interface{}:
		// JSON-roundtripped form. We can decode the wrapper, but
		// MessageContent.Parts is []interface{} and won't roundtrip
		// into typed parts. Callers reading from Additional accept
		// the loose shape; reading from the typed field is preferred.
		var msgs CodingProviderIntermediateMessages
		data, err := json.Marshal(typed)
		if err != nil {
			return CodingProviderIntermediateMessages{}, false
		}
		if err := json.Unmarshal(data, &msgs); err != nil {
			return CodingProviderIntermediateMessages{}, false
		}
		return msgs, !msgs.Empty()
	default:
		return CodingProviderIntermediateMessages{}, false
	}
}
