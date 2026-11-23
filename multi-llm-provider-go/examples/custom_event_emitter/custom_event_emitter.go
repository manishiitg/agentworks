package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// FileEventEmitter is a custom event emitter that writes events to a file
type FileEventEmitter struct {
	file *os.File
	mu   sync.Mutex
}

// NewFileEventEmitter creates a new file event emitter that writes to the specified file
func NewFileEventEmitter(filename string) (*FileEventEmitter, error) {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open event file: %w", err)
	}

	return &FileEventEmitter{
		file: file,
	}, nil
}

// Close closes the event file
func (e *FileEventEmitter) Close() error {
	if e.file != nil {
		return e.file.Close()
	}
	return nil
}

// writeEvent writes an event to the file with timestamp
func (e *FileEventEmitter) writeEvent(eventType string, data interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05")

	// Format event data as JSON for readability
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		jsonData = []byte(fmt.Sprintf("%+v", data))
	}

	eventEntry := fmt.Sprintf("[%s] [EVENT: %s]\n%s\n\n", timestamp, eventType, string(jsonData))

	_, err = e.file.WriteString(eventEntry)
	if err != nil {
		// Fallback to stderr if file write fails
		fmt.Fprintf(os.Stderr, "Failed to write to event file: %v\n", err)
		fmt.Fprint(os.Stderr, eventEntry)
	}
}

// EmitLLMInitializationStart emits an LLM initialization start event
func (e *FileEventEmitter) EmitLLMInitializationStart(provider string, modelID string, temperature float64, traceID interfaces.TraceID, metadata interfaces.LLMMetadata) {
	eventData := map[string]interface{}{
		"event_type":  "LLM_INITIALIZATION_START",
		"provider":    provider,
		"model_id":    modelID,
		"temperature": temperature,
		"trace_id":    string(traceID),
		"metadata":    metadata,
	}
	e.writeEvent("INIT_START", eventData)
}

// EmitLLMInitializationSuccess emits an LLM initialization success event
func (e *FileEventEmitter) EmitLLMInitializationSuccess(provider string, modelID string, capabilities string, traceID interfaces.TraceID, metadata interfaces.LLMMetadata) {
	eventData := map[string]interface{}{
		"event_type":   "LLM_INITIALIZATION_SUCCESS",
		"provider":     provider,
		"model_id":     modelID,
		"capabilities": capabilities,
		"trace_id":     string(traceID),
		"metadata":     metadata,
	}
	e.writeEvent("INIT_SUCCESS", eventData)
}

// EmitLLMInitializationError emits an LLM initialization error event
func (e *FileEventEmitter) EmitLLMInitializationError(provider string, modelID string, operation string, err error, traceID interfaces.TraceID, metadata interfaces.LLMMetadata) {
	eventData := map[string]interface{}{
		"event_type": "LLM_INITIALIZATION_ERROR",
		"provider":   provider,
		"model_id":   modelID,
		"operation":  operation,
		"error":      err.Error(),
		"trace_id":   string(traceID),
		"metadata":   metadata,
	}
	e.writeEvent("INIT_ERROR", eventData)
}

// EmitLLMGenerationSuccess emits an LLM generation success event
func (e *FileEventEmitter) EmitLLMGenerationSuccess(provider string, modelID string, operation string, messages int, temperature float64, messageContent string, responseLength int, choicesCount int, traceID interfaces.TraceID, metadata interfaces.LLMMetadata) {
	eventData := map[string]interface{}{
		"event_type":      "LLM_GENERATION_SUCCESS",
		"provider":        provider,
		"model_id":        modelID,
		"operation":       operation,
		"messages_count":  messages,
		"temperature":     temperature,
		"message_content": messageContent,
		"response_length": responseLength,
		"choices_count":   choicesCount,
		"trace_id":        string(traceID),
		"metadata":        metadata,
	}
	e.writeEvent("GENERATION_SUCCESS", eventData)
}

// EmitLLMGenerationError emits an LLM generation error event
func (e *FileEventEmitter) EmitLLMGenerationError(provider string, modelID string, operation string, messages int, temperature float64, messageContent string, err error, traceID interfaces.TraceID, metadata interfaces.LLMMetadata) {
	eventData := map[string]interface{}{
		"event_type":      "LLM_GENERATION_ERROR",
		"provider":        provider,
		"model_id":        modelID,
		"operation":       operation,
		"messages_count":  messages,
		"temperature":     temperature,
		"message_content": messageContent,
		"error":           err.Error(),
		"trace_id":        string(traceID),
		"metadata":        metadata,
	}
	e.writeEvent("GENERATION_ERROR", eventData)
}

// EmitToolCallDetected emits a tool call detected event
func (e *FileEventEmitter) EmitToolCallDetected(provider string, modelID string, toolCallID string, toolName string, arguments string, traceID interfaces.TraceID, metadata interfaces.LLMMetadata) {
	eventData := map[string]interface{}{
		"event_type":   "TOOL_CALL_DETECTED",
		"provider":     provider,
		"model_id":     modelID,
		"tool_call_id": toolCallID,
		"tool_name":    toolName,
		"arguments":    arguments,
		"trace_id":     string(traceID),
		"metadata":     metadata,
	}
	e.writeEvent("TOOL_CALL_DETECTED", eventData)
}

func main() {
	// Check for API key
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "Error: OPENAI_API_KEY environment variable is required\n")
		fmt.Fprintf(os.Stderr, "Set it with: export OPENAI_API_KEY=your-api-key\n")
		os.Exit(1)
	}

	// Create a custom event emitter
	eventEmitter, err := NewFileEventEmitter("events.log")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create event emitter: %v\n", err)
		os.Exit(1)
	}
	defer eventEmitter.Close()

	fmt.Println("Starting custom event emitter example")
	fmt.Println("Event file: events.log")
	fmt.Println()

	// Configure the OpenAI provider with custom event emitter
	config := llmproviders.Config{
		Provider:     llmproviders.ProviderOpenAI,
		ModelID:      "gpt-4.1-mini", // Use a cost-effective model for examples
		Temperature:  0.7,
		Logger:       nil,          // nil is allowed - uses no-op logger
		EventEmitter: eventEmitter, // Use our custom event emitter
	}

	// Initialize the LLM
	fmt.Println("Initializing OpenAI provider with model:", config.ModelID)
	fmt.Println("(Check events.log for initialization events)")
	llm, err := llmproviders.InitializeLLM(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize LLM: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Successfully initialized LLM")
	fmt.Println()

	// Create context
	ctx := context.Background()

	// Prepare the message
	messages := []llmtypes.MessageContent{
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "Hello! Can you introduce yourself in one sentence?"),
	}

	// Generate content
	fmt.Println("Sending request to OpenAI...")
	fmt.Println("(Check events.log for generation events)")
	response, err := llm.GenerateContent(ctx, messages)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to generate content: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Successfully received response from OpenAI")
	fmt.Println()

	// Display the response
	if len(response.Choices) > 0 {
		choice := response.Choices[0]
		fmt.Println("📝 Response:")
		fmt.Println(choice.Content)
		fmt.Println()

		// Display token usage if available
		if choice.GenerationInfo != nil {
			fmt.Println("📊 Token Usage:")
			if choice.GenerationInfo.InputTokens != nil {
				fmt.Printf("  Input tokens:  %d\n", *choice.GenerationInfo.InputTokens)
			}
			if choice.GenerationInfo.OutputTokens != nil {
				fmt.Printf("  Output tokens: %d\n", *choice.GenerationInfo.OutputTokens)
			}
			if choice.GenerationInfo.TotalTokens != nil {
				fmt.Printf("  Total tokens:  %d\n", *choice.GenerationInfo.TotalTokens)
			}
		}
	} else {
		fmt.Println("No response received")
	}

	fmt.Println()
	fmt.Println("✅ Check events.log for detailed event information!")
}
