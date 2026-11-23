package events

import (
	"testing"
	"time"
)

func TestCloneAgentEventDeepCopiesMetadata(t *testing.T) {
	nested := map[string]interface{}{"status": "ok"}
	listItem := map[string]interface{}{"name": "alpha"}
	original := &AgentEvent{
		Type:          LLMGenerationError,
		Timestamp:     time.Now(),
		CorrelationID: "corr-1",
		Data: &LLMGenerationErrorEvent{
			BaseEventData: BaseEventData{
				Metadata: map[string]interface{}{
					"nested": nested,
					"items":  []interface{}{listItem},
				},
			},
			Turn:     1,
			ModelID:  "codex-cli/high",
			Error:    "choice.Content is empty",
			Duration: time.Second,
		},
	}

	cloned := CloneAgentEvent(original)
	if cloned == nil {
		t.Fatal("expected clone, got nil")
	}
	if cloned == original {
		t.Fatal("expected cloned wrapper to be detached from original")
	}

	original.CorrelationID = "corr-2"
	originalData := original.Data.(*LLMGenerationErrorEvent)
	originalData.Error = "mutated"
	originalData.Metadata["added"] = true
	nested["status"] = "changed"
	listItem["name"] = "beta"

	clonedData, ok := cloned.Data.(*LLMGenerationErrorEvent)
	if !ok {
		t.Fatalf("expected *LLMGenerationErrorEvent, got %T", cloned.Data)
	}

	if cloned.CorrelationID != "corr-1" {
		t.Fatalf("expected cloned correlation ID to remain unchanged, got %q", cloned.CorrelationID)
	}
	if clonedData.Error != "choice.Content is empty" {
		t.Fatalf("expected cloned error to remain unchanged, got %q", clonedData.Error)
	}
	if _, exists := clonedData.Metadata["added"]; exists {
		t.Fatal("expected cloned metadata to be detached from original map")
	}
	if clonedData.Metadata["nested"].(map[string]interface{})["status"] != "ok" {
		t.Fatal("expected nested metadata map to be deep copied")
	}
	if clonedData.Metadata["items"].([]interface{})[0].(map[string]interface{})["name"] != "alpha" {
		t.Fatal("expected nested metadata slice item to be deep copied")
	}
}

func TestCloneAgentEventDeepCopiesAdditionalMapFields(t *testing.T) {
	serverInfo := map[string]interface{}{
		"version": "1.0.0",
		"nested":  map[string]interface{}{"region": "us-east-1"},
	}
	original := &AgentEvent{
		Type:      MCPServerConnectionStart,
		Timestamp: time.Now(),
		Data: &MCPServerConnectionEvent{
			BaseEventData: BaseEventData{
				Metadata: map[string]interface{}{"source": "test"},
			},
			ServerName: "workspace",
			Status:     "connected",
			ServerInfo: serverInfo,
		},
	}

	cloned := CloneAgentEvent(original)
	if cloned == nil {
		t.Fatal("expected clone, got nil")
	}

	serverInfo["version"] = "2.0.0"
	serverInfo["nested"].(map[string]interface{})["region"] = "eu-west-1"

	clonedData, ok := cloned.Data.(*MCPServerConnectionEvent)
	if !ok {
		t.Fatalf("expected *MCPServerConnectionEvent, got %T", cloned.Data)
	}

	if clonedData.ServerInfo["version"] != "1.0.0" {
		t.Fatalf("expected cloned server info to remain unchanged, got %v", clonedData.ServerInfo["version"])
	}
	if clonedData.ServerInfo["nested"].(map[string]interface{})["region"] != "us-east-1" {
		t.Fatal("expected nested server info map to be deep copied")
	}
}
