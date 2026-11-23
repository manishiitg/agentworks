package vertex

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestGoogleGenAIAdapterImplementsWebSearchModel(t *testing.T) {
	adapter := NewGoogleGenAIAdapter(nil, "gemini-3.5-flash", nil)
	if _, ok := interface{}(adapter).(llmtypes.WebSearchModel); !ok {
		t.Fatal("GoogleGenAIAdapter should implement llmtypes.WebSearchModel")
	}
}
