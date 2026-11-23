package llmproviders

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type providerAwareLoggingTestModel struct {
	called bool
}

func (m *providerAwareLoggingTestModel) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	m.called = true
	return &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{
			{Content: "ok"},
		},
	}, nil
}

func (m *providerAwareLoggingTestModel) GetModelID() string { return "test-model" }

func (m *providerAwareLoggingTestModel) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	return nil, nil
}

type providerAwareCaptureLogger struct {
	lines []string
}

func (l *providerAwareCaptureLogger) Infof(format string, v ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, v...))
}

func (l *providerAwareCaptureLogger) Errorf(format string, v ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, v...))
}

func (l *providerAwareCaptureLogger) Debugf(format string, args ...interface{}) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *providerAwareCaptureLogger) String() string {
	return strings.Join(l.lines, "\n")
}

func TestProviderAwareRequestPayloadLoggingDisabledByDefault(t *testing.T) {
	t.Setenv("MULTI_LLM_REQUEST_LOGS", "")
	t.Setenv("MULTI_LLM_VERBOSE_REQUEST_LOGS", "")

	promptTail := "PROMPT_TAIL_SHOULD_NOT_APPEAR"
	toolTail := "TOOL_SCHEMA_TAIL_SHOULD_NOT_APPEAR"
	longPrompt := strings.Repeat("p", providerAwarePromptLogMaxChars+100) + promptTail
	longToolDescription := strings.Repeat("d", providerAwareToolSchemaLogMaxChars+100) + toolTail

	model := &providerAwareLoggingTestModel{}
	logger := &providerAwareCaptureLogger{}
	wrapped := NewProviderAwareLLM(model, ProviderClaudeCode, "claude-test", nil, "", logger)

	_, err := wrapped.GenerateContent(
		context.Background(),
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: longPrompt}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "hello"}}},
		},
		llmtypes.WithTools([]llmtypes.Tool{
			{
				Type: "function",
				Function: &llmtypes.FunctionDefinition{
					Name:        "large_tool",
					Description: longToolDescription,
					Parameters:  &llmtypes.Parameters{Type: "object"},
				},
			},
		}),
	)
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}
	if !model.called {
		t.Fatal("underlying model was not called")
	}

	logs := logger.String()
	if strings.Contains(logs, promptTail) {
		t.Fatalf("bounded request logs included full prompt tail %q", promptTail)
	}
	if strings.Contains(logs, toolTail) {
		t.Fatalf("default request logs included full tool schema tail %q", toolTail)
	}
	if strings.Contains(logs, "large_tool") || strings.Contains(logs, "SYSTEM PROMPTS") || strings.Contains(logs, "TOOLS") {
		t.Fatalf("default request logs included payload details:\n%s", logs)
	}
	if !strings.Contains(logs, "LLM REQUEST START") {
		t.Fatalf("request timing log missing:\n%s", logs)
	}
}

func TestProviderAwareRequestPayloadLoggingIsOptInAndBounded(t *testing.T) {
	t.Setenv("MULTI_LLM_REQUEST_LOGS", "1")
	t.Setenv("MULTI_LLM_VERBOSE_REQUEST_LOGS", "")

	promptTail := "PROMPT_TAIL_SHOULD_NOT_APPEAR"
	toolTail := "TOOL_SCHEMA_TAIL_SHOULD_NOT_APPEAR"
	longPrompt := strings.Repeat("p", providerAwarePromptLogMaxChars+100) + promptTail
	longToolDescription := strings.Repeat("d", providerAwareToolSchemaLogMaxChars+100) + toolTail

	model := &providerAwareLoggingTestModel{}
	logger := &providerAwareCaptureLogger{}
	wrapped := NewProviderAwareLLM(model, ProviderClaudeCode, "claude-test", nil, "", logger)

	_, err := wrapped.GenerateContent(
		context.Background(),
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: longPrompt}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "hello"}}},
		},
		llmtypes.WithTools([]llmtypes.Tool{
			{
				Type: "function",
				Function: &llmtypes.FunctionDefinition{
					Name:        "large_tool",
					Description: longToolDescription,
					Parameters:  &llmtypes.Parameters{Type: "object"},
				},
			},
		}),
	)
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}

	logs := logger.String()
	if strings.Contains(logs, promptTail) {
		t.Fatalf("opt-in request logs included full prompt tail %q", promptTail)
	}
	if strings.Contains(logs, toolTail) {
		t.Fatalf("opt-in request logs included full tool schema tail %q", toolTail)
	}
	if !strings.Contains(logs, "large_tool") {
		t.Fatalf("tool name was not logged when request logs enabled:\n%s", logs)
	}
	if !strings.Contains(logs, "truncated") {
		t.Fatalf("prompt truncation was not visible in opt-in logs:\n%s", logs)
	}
	if strings.Contains(logs, "schema preview") {
		t.Fatalf("schema preview was logged without verbose request logs:\n%s", logs)
	}
}

func TestProviderAwareVerboseToolSchemaLoggingIsStillBounded(t *testing.T) {
	t.Setenv("MULTI_LLM_REQUEST_LOGS", "")
	t.Setenv("MULTI_LLM_VERBOSE_REQUEST_LOGS", "1")

	toolTail := "VERBOSE_TOOL_SCHEMA_TAIL_SHOULD_NOT_APPEAR"
	longToolDescription := strings.Repeat("d", providerAwareToolSchemaLogMaxChars+100) + toolTail

	model := &providerAwareLoggingTestModel{}
	logger := &providerAwareCaptureLogger{}
	wrapped := NewProviderAwareLLM(model, ProviderClaudeCode, "claude-test", nil, "", logger)

	_, err := wrapped.GenerateContent(
		context.Background(),
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "hello"}}},
		},
		llmtypes.WithTools([]llmtypes.Tool{
			{
				Type: "function",
				Function: &llmtypes.FunctionDefinition{
					Name:        "large_tool",
					Description: longToolDescription,
					Parameters:  &llmtypes.Parameters{Type: "object"},
				},
			},
		}),
	)
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}

	logs := logger.String()
	if !strings.Contains(logs, "schema preview") {
		t.Fatalf("verbose schema preview was not logged:\n%s", logs)
	}
	if strings.Contains(logs, toolTail) {
		t.Fatalf("verbose request logs included full tool schema tail %q", toolTail)
	}
}
