package events

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestNativeActivityOmitsMessageContent(t *testing.T) {
	e := NewCodingAgentNativeActivity(llmtypes.NativeRecord{ID: "record1", Provider: "claude-code", Kind: "reply", Text: "PRIVATE_REPLY"})
	raw, err := json.Marshal(CloneAgentEvent(NewAgentEvent(e)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "PRIVATE_REPLY") {
		t.Fatal("operational event copied private message content")
	}
}
