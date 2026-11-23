package llmtypes

import (
	"encoding/json"
	"testing"
)

func TestMessageContentUnmarshalRestoresTextContent(t *testing.T) {
	raw := []byte(`{
		"Role": "human",
		"Parts": [
			{"Text": "hi"},
			{"type": "text", "content": "next"}
		]
	}`)

	var msg MessageContent
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}
	if msg.Role != ChatMessageTypeHuman {
		t.Fatalf("Role = %q, want human", msg.Role)
	}
	if len(msg.Parts) != 2 {
		t.Fatalf("Parts len = %d, want 2", len(msg.Parts))
	}
	first, ok := msg.Parts[0].(TextContent)
	if !ok || first.Text != "hi" {
		t.Fatalf("Parts[0] = %#v, want TextContent hi", msg.Parts[0])
	}
	second, ok := msg.Parts[1].(TextContent)
	if !ok || second.Text != "next" {
		t.Fatalf("Parts[1] = %#v, want TextContent next", msg.Parts[1])
	}
}

func TestMessageContentUnmarshalRestoresKnownNonTextParts(t *testing.T) {
	raw := []byte(`{
		"Role": "ai",
		"Parts": [
			{"SourceType": "url", "MediaType": "image/png", "Data": "https://example.com/a.png"},
			{"ToolCallID": "call-1", "Name": "search", "Content": "done", "IsError": true},
			{"ID": "call-2", "Type": "function", "FunctionCall": {"Name": "lookup", "Arguments": "{\"q\":\"x\"}"}}
		]
	}`)

	var msg MessageContent
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}
	if len(msg.Parts) != 3 {
		t.Fatalf("Parts len = %d, want 3", len(msg.Parts))
	}

	image, ok := msg.Parts[0].(ImageContent)
	if !ok || image.SourceType != "url" || image.MediaType != "image/png" || image.Data == "" {
		t.Fatalf("Parts[0] = %#v, want ImageContent", msg.Parts[0])
	}

	toolResponse, ok := msg.Parts[1].(ToolCallResponse)
	if !ok || toolResponse.ToolCallID != "call-1" || toolResponse.Name != "search" || toolResponse.Content != "done" || !toolResponse.IsError {
		t.Fatalf("Parts[1] = %#v, want ToolCallResponse", msg.Parts[1])
	}

	toolCall, ok := msg.Parts[2].(ToolCall)
	if !ok || toolCall.ID != "call-2" || toolCall.FunctionCall == nil || toolCall.FunctionCall.Name != "lookup" {
		t.Fatalf("Parts[2] = %#v, want ToolCall", msg.Parts[2])
	}
}

func TestMessageContentUnmarshalLeavesUnknownPartsAsMap(t *testing.T) {
	raw := []byte(`{
		"Role": "human",
		"Parts": [
			{"Custom": "value"}
		]
	}`)

	var msg MessageContent
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}
	part, ok := msg.Parts[0].(map[string]interface{})
	if !ok || part["Custom"] != "value" {
		t.Fatalf("Parts[0] = %#v, want generic map", msg.Parts[0])
	}
}
