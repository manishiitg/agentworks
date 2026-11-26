package claudecode

import (
	"fmt"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type MockLogger struct{}

func (l *MockLogger) Infof(format string, args ...any)  { fmt.Printf("INFO: "+format+"\n", args...) }
func (l *MockLogger) Errorf(format string, args ...any) { fmt.Printf("ERROR: "+format+"\n", args...) }
func (l *MockLogger) Debugf(format string, args ...interface{}) {
	fmt.Printf("DEBUG: "+format+"\n", args...)
}

func TestClaudeCodeNewModelMetadataIsExplicit(t *testing.T) {
	interactive := NewClaudeCodeInteractiveAdapter("claude-code", &MockLogger{})
	for _, tt := range []struct {
		modelID     string
		wantName    string
		wantInput   float64
		wantOutput  float64
		wantContext int
	}{
		{
			modelID:     "claude-fable-5-2",
			wantName:    "Claude Fable 5.2",
			wantInput:   8,
			wantOutput:  40,
			wantContext: 1000000,
		},
		{
			modelID:     "claude-fable-5-1",
			wantName:    "Claude Fable 5.1",
			wantInput:   10,
			wantOutput:  50,
			wantContext: 1000000,
		},
		{
			modelID:     "claude-opus-5",
			wantName:    "Claude Opus 5",
			wantInput:   5,
			wantOutput:  25,
			wantContext: 200000,
		},
	} {
		t.Run(tt.modelID, func(t *testing.T) {
			meta, err := interactive.GetModelMetadata(tt.modelID)
			if err != nil {
				t.Fatalf("interactive metadata error: %v", err)
			}
			if meta.ModelName != tt.wantName || meta.InputCostPer1MTokens != tt.wantInput || meta.OutputCostPer1MTokens != tt.wantOutput || meta.ContextWindow != tt.wantContext {
				t.Fatalf("interactive metadata = %+v, want name=%q input=%v output=%v context=%d", meta, tt.wantName, tt.wantInput, tt.wantOutput, tt.wantContext)
			}

			compat := NewClaudeCodeAdapter("", tt.modelID, &MockLogger{})
			meta, err = compat.GetModelMetadata(tt.modelID)
			if err != nil {
				t.Fatalf("compat metadata error: %v", err)
			}
			if meta.ModelName != tt.wantName || meta.InputCostPer1MTokens != tt.wantInput || meta.OutputCostPer1MTokens != tt.wantOutput || meta.ContextWindow != tt.wantContext {
				t.Fatalf("compat metadata = %+v, want name=%q input=%v output=%v context=%d", meta, tt.wantName, tt.wantInput, tt.wantOutput, tt.wantContext)
			}
		})
	}
}

func TestClaudeCodeAdapterImplementsWebSearchModel(t *testing.T) {
	adapter := NewClaudeCodeAdapter("", "test-model", &MockLogger{})
	if _, ok := interface{}(adapter).(llmtypes.WebSearchModel); !ok {
		t.Fatal("ClaudeCodeAdapter should implement llmtypes.WebSearchModel")
	}
}
