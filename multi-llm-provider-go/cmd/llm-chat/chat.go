package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	vertexadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/vertex"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"

	"github.com/joho/godotenv"
)

// noopLogger is a no-op logger implementation for chat
type noopLogger struct{}

func (n *noopLogger) Infof(format string, v ...any)             {}
func (n *noopLogger) Errorf(format string, v ...any)            {}
func (n *noopLogger) Debugf(format string, args ...interface{}) {}

// safeCloseChannel safely closes a channel, recovering from panic if already closed
func safeCloseChannel(ch chan llmtypes.StreamChunk) {
	defer func() {
		if r := recover(); r != nil {
			// Channel already closed, ignore panic
			_ = r // Acknowledge the recovered value to satisfy linter
		}
	}()
	close(ch)
}

// SelectProvider interactively selects a provider
func SelectProvider() (llmproviders.Provider, error) {
	fmt.Println("\nSelect LLM Provider:")
	fmt.Println("1. OpenAI")
	fmt.Println("2. Anthropic")
	fmt.Println("3. Bedrock")
	fmt.Println("4. Vertex")
	fmt.Println("5. OpenRouter")
	fmt.Println("6. Z.AI")
	fmt.Print("\nEnter choice (1-6): ")

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}

	input = strings.TrimSpace(input)
	switch input {
	case "1":
		return llmproviders.ProviderOpenAI, nil
	case "2":
		return llmproviders.ProviderAnthropic, nil
	case "3":
		return llmproviders.ProviderBedrock, nil
	case "4":
		return llmproviders.ProviderVertex, nil
	case "5":
		return llmproviders.ProviderOpenRouter, nil
	case "6":
		return llmproviders.ProviderZAI, nil
	default:
		return "", fmt.Errorf("invalid choice: %s", input)
	}
}

// SelectModel prompts for model ID
func SelectModel(provider llmproviders.Provider) (string, error) {
	// For OpenAI, show a menu with two options
	if provider == llmproviders.ProviderOpenAI {
		fmt.Println("\nSelect OpenAI Model:")
		fmt.Println("1. gpt-4.1")
		fmt.Println("2. gpt-5-mini")
		fmt.Print("\nEnter choice (1-2): ")

		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("failed to read input: %w", err)
		}

		input = strings.TrimSpace(input)
		switch input {
		case "1":
			return "gpt-4.1", nil
		case "2":
			return "gpt-5-mini", nil
		default:
			return "", fmt.Errorf("invalid choice: %s (must be 1 or 2)", input)
		}
	}

	// For Anthropic, show a menu with two options
	if provider == llmproviders.ProviderAnthropic {
		fmt.Println("\nSelect Anthropic Model:")
		fmt.Println("1. claude-haiku-4-5-20251001 (Haiku 4.5)")
		fmt.Println("2. claude-sonnet-4-5-20251001 (Sonnet 4.5)")
		fmt.Print("\nEnter choice (1-2): ")

		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("failed to read input: %w", err)
		}

		input = strings.TrimSpace(input)
		switch input {
		case "1":
			return "claude-haiku-4-5-20251001", nil
		case "2":
			return "claude-sonnet-4-5-20251001", nil
		default:
			return "", fmt.Errorf("invalid choice: %s (must be 1 or 2)", input)
		}
	}

	// For Bedrock, show a menu with model options
	if provider == llmproviders.ProviderBedrock {
		fmt.Println("\nSelect Bedrock Model:")
		fmt.Println("1. global.anthropic.claude-sonnet-4-5-20250929-v1:0 (Claude Sonnet 4.5)")
		fmt.Println("2. us.anthropic.claude-sonnet-4-20250514-v1:0 (Claude Sonnet 4)")
		fmt.Println("3. us.anthropic.claude-3-haiku-20240307-v1:0 (Claude Haiku 3)")
		fmt.Print("\nEnter choice (1-3): ")

		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("failed to read input: %w", err)
		}

		input = strings.TrimSpace(input)
		switch input {
		case "1":
			return "global.anthropic.claude-sonnet-4-5-20250929-v1:0", nil
		case "2":
			return "us.anthropic.claude-sonnet-4-20250514-v1:0", nil
		case "3":
			return "us.anthropic.claude-3-haiku-20240307-v1:0", nil
		default:
			return "", fmt.Errorf("invalid choice: %s (must be 1, 2, or 3)", input)
		}
	}

	// For Vertex, show a menu with model options
	if provider == llmproviders.ProviderVertex {
		fmt.Println("\nSelect Vertex AI Model:")
		fmt.Println("1. gemini-3.8-flash (Gemini 3.8 Flash — GA default)")
		fmt.Println("2. gemini-3.5-flash-lite (Gemini 3.5 Flash-Lite — GA)")
		fmt.Println("3. gemini-3.1-pro-preview (Gemini 3.1 Pro Preview)")
		fmt.Print("\nEnter choice (1-3): ")

		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("failed to read input: %w", err)
		}

		input = strings.TrimSpace(input)
		switch input {
		case "1":
			return vertexadapter.ModelGemini38Flash, nil
		case "2":
			return vertexadapter.ModelGemini35FlashLite, nil
		case "3":
			return vertexadapter.ModelGemini31ProPreview, nil
		default:
			return "", fmt.Errorf("invalid choice: %s (must be 1, 2, or 3)", input)
		}
	}

	// For OpenRouter, show a menu with model options
	if provider == llmproviders.ProviderOpenRouter {
		fmt.Println("\nSelect OpenRouter Model:")
		fmt.Println("1. x-ai/grok-4-fast (Grok 4 Fast)")
		fmt.Println("2. x-ai/grok-code-fast-1 (Grok Code Fast)")
		fmt.Print("\nEnter choice (1-2): ")

		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("failed to read input: %w", err)
		}

		input = strings.TrimSpace(input)
		switch input {
		case "1":
			return "x-ai/grok-4-fast", nil
		case "2":
			return "x-ai/grok-code-fast-1", nil
		default:
			return "", fmt.Errorf("invalid choice: %s (must be 1 or 2)", input)
		}
	}

	if provider == llmproviders.ProviderZAI {
		fmt.Println("\nSelect Z.AI Model:")
		fmt.Println("1. glm-5.1")
		fmt.Println("2. glm-5")
		fmt.Println("3. glm-5-turbo")
		fmt.Print("\nEnter choice (1-3): ")

		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("failed to read input: %w", err)
		}

		input = strings.TrimSpace(input)
		switch input {
		case "1":
			return "glm-5.1", nil
		case "2":
			return "glm-5", nil
		case "3":
			return "glm-5-turbo", nil
		default:
			return "", fmt.Errorf("invalid choice: %s (must be 1, 2, or 3)", input)
		}
	}

	return "", fmt.Errorf("unsupported provider: %s", provider)
}

// ChatMode represents the different chat modes
type ChatMode int

const (
	ChatModeStreaming ChatMode = iota
	ChatModeNonStreaming
	ChatModeImageUnderstanding
)

// SelectChatMode asks the user to select a chat mode
func SelectChatMode() (ChatMode, error) {
	fmt.Println("\nSelect Mode:")
	fmt.Println("1. Streaming (real-time token output)")
	fmt.Println("2. Non-streaming (wait for complete response)")
	fmt.Println("3. Image Understanding (analyze images)")
	fmt.Print("\nEnter choice (1-3): ")

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return ChatModeStreaming, fmt.Errorf("failed to read input: %w", err)
	}

	input = strings.TrimSpace(input)
	switch input {
	case "1":
		return ChatModeStreaming, nil
	case "2":
		return ChatModeNonStreaming, nil
	case "3":
		return ChatModeImageUnderstanding, nil
	default:
		return ChatModeStreaming, fmt.Errorf("invalid choice: %s (must be 1, 2, or 3)", input)
	}
}

// PromptForImageURL asks the user for an image URL
func PromptForImageURL() (string, error) {
	fmt.Print("\nEnter image URL: ")
	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}

	url := strings.TrimSpace(input)
	if url == "" {
		return "", fmt.Errorf("image URL cannot be empty")
	}

	return url, nil
}

// RunChat is the main chat loop
func RunChat() error {
	// Load .env files
	_ = godotenv.Load("agent_go/.env")
	_ = godotenv.Load(".env")
	_ = godotenv.Load("../.env")

	// Welcome message
	fmt.Println("=== LLM Chat CLI ===")
	fmt.Println("Interactive chat with LLM models, tools, and streaming")

	// Select chat mode
	chatMode, err := SelectChatMode()
	if err != nil {
		return fmt.Errorf("chat mode selection failed: %w", err)
	}

	// Select provider
	provider, err := SelectProvider()
	if err != nil {
		return fmt.Errorf("provider selection failed: %w", err)
	}

	// Select model
	modelID, err := SelectModel(provider)
	if err != nil {
		return fmt.Errorf("model selection failed: %w", err)
	}

	fmt.Printf("\nInitializing %s with model %s...\n", provider, modelID)

	// Initialize LLM with a no-op logger
	llm, err := llmproviders.InitializeLLM(llmproviders.Config{
		Provider:    provider,
		ModelID:     modelID,
		Temperature: 0.7,
		Logger:      interfaces.Logger(&noopLogger{}), // No-op logger for chat
	})
	if err != nil {
		return fmt.Errorf("failed to initialize LLM: %w", err)
	}

	// Initialize system prompt and conversation
	systemPrompt := GetSystemPrompt()
	conv := NewConversation(systemPrompt)

	// Initialize tools
	toolRegistry := InitializeTools()
	tools := GetDefaultTools()

	var modeStr string
	switch chatMode {
	case ChatModeStreaming:
		modeStr = "streaming"
	case ChatModeNonStreaming:
		modeStr = "non-streaming"
	case ChatModeImageUnderstanding:
		modeStr = "image understanding"
	}
	fmt.Printf("\nChat started (%s mode)! Type '/help' for commands, '/exit' to quit\n", modeStr)
	if chatMode == ChatModeImageUnderstanding {
		fmt.Println("You will be prompted for an image URL when you send a message. The image will be described automatically.")
	}
	fmt.Println("=" + strings.Repeat("=", 50))

	// Main chat loop
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("\n> ")
		input, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read input: %w", err)
		}

		text := strings.TrimSpace(input)
		if text == "" {
			continue
		}

		// Handle commands
		if strings.HasPrefix(text, "/") {
			if err := handleCommand(text, conv, tools); err != nil {
				if errors.Is(err, errExit) {
					fmt.Println("\nGoodbye!")
					return nil
				}
				fmt.Printf("Error: %v\n", err)
			}
			continue
		}

		// Handle image understanding mode
		if chatMode == ChatModeImageUnderstanding {
			imageURL, err := PromptForImageURL()
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				continue
			}

			// Always use "Describe the image" as the question
			question := "Describe the image"

			// Create and add user message with image and question to conversation
			imageMessage := llmtypes.MessageContent{
				Role: llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{
					llmtypes.ImageContent{
						SourceType: "url",
						MediaType:  "",
						Data:       imageURL,
					},
					llmtypes.TextContent{
						Text: question,
					},
				},
			}
			conv.messages = append(conv.messages, imageMessage)

			// Add user message with image and question
			ctx := context.Background()
			var toolCalls []llmtypes.ToolCall
			var content string
			_, toolCalls, content, err = handleImageUnderstandingRequest(ctx, llm, modelID, imageURL, question, conv)
			if err != nil {
				fmt.Printf("\n[Error: %v]\n", err)
				// Remove the message we just added if there was an error
				conv.messages = conv.messages[:len(conv.messages)-1]
				continue
			}

			// Add assistant message
			conv.AddAssistantMessage(content, toolCalls)
			fmt.Println() // Empty line after response
			continue
		}

		// Add user message
		conv.AddUserMessage(text)

		ctx := context.Background()
		var toolCalls []llmtypes.ToolCall
		var content string

		if chatMode == ChatModeStreaming {
			// Streaming mode
			_, toolCalls, content, err = handleStreamingRequest(ctx, llm, modelID, tools, conv)
		} else {
			// Non-streaming mode
			_, toolCalls, content, err = handleNonStreamingRequest(ctx, llm, modelID, tools, conv)
		}

		// Check for errors
		if err != nil {
			fmt.Printf("\n[Error: %v]\n", err)
			// Don't add to conversation if there was an error
			continue
		}

		// If we got no content and no tool calls, something might be wrong
		if content == "" && len(toolCalls) == 0 {
			fmt.Println("\n[Warning: Received empty response]")
		}

		// Add assistant message
		conv.AddAssistantMessage(content, toolCalls)

		// Handle tool calls in a loop (support multiple levels of tool calls)
		maxToolCallIterations := 5 // Prevent infinite loops
		for iteration := 0; len(toolCalls) > 0 && iteration < maxToolCallIterations; iteration++ {
			fmt.Printf("\n[Executing %d tool call(s)...]\n", len(toolCalls))
			toolResults := ExecuteToolCallsParallel(ctx, toolCalls, toolRegistry)

			// Display tool results
			for _, tr := range toolResults {
				if tr.Error != nil {
					fmt.Printf("[Tool %s error: %v]\n", tr.Name, tr.Error)
				} else {
					fmt.Printf("[Tool %s completed (mocked)]\n", tr.Name)
				}
			}

			// Add tool results to conversation
			conv.AddToolResults(toolResults)

			// Continue conversation with tool results
			fmt.Println("\n[Continuing conversation with tool results...]")

			var err2 error

			if chatMode == ChatModeStreaming {
				// Streaming mode
				_, toolCalls, content, err2 = handleStreamingRequest(ctx, llm, modelID, tools, conv)
			} else {
				// Non-streaming mode
				_, toolCalls, content, err2 = handleNonStreamingRequest(ctx, llm, modelID, tools, conv)
			}

			// Check for errors
			if err2 != nil {
				fmt.Printf("\n[Error: %v]\n", err2)
				break // Exit tool call loop on error
			}

			conv.AddAssistantMessage(content, toolCalls)
		}

		if len(toolCalls) > 0 && maxToolCallIterations > 0 {
			fmt.Printf("\n[Warning: Reached maximum tool call iterations (%d)]\n", maxToolCallIterations)
		}

		fmt.Println() // Empty line after response
	}
}

var errExit = fmt.Errorf("exit requested")

// handleStreamingRequest handles a streaming LLM request
func handleStreamingRequest(ctx context.Context, llm llmtypes.Model, modelID string, tools []llmtypes.Tool, conv *Conversation) (*llmtypes.ContentResponse, []llmtypes.ToolCall, string, error) {
	// Create streaming channel
	streamChan := make(chan llmtypes.StreamChunk, 100)

	var streamErr error
	responseChan := make(chan *llmtypes.ContentResponse, 1)
	requestStartTime := time.Now() // Start timing right before request
	go func() {
		defer func() {
			// Ensure channel is closed even if GenerateContent panics
			// The adapter should close it via defer, but we add this as a safety net
			// safeCloseChannel will recover if the channel is already closed
			safeCloseChannel(streamChan)
		}()
		resp, err := llm.GenerateContent(ctx, conv.GetMessages(),
			llmtypes.WithModel(modelID),
			llmtypes.WithTools(tools),
			llmtypes.WithToolChoiceString("auto"),
			llmtypes.WithStreamingChan(streamChan),
		)
		if err != nil {
			streamErr = err
			responseChan <- nil
		} else {
			responseChan <- resp
		}
	}()

	// Handle streaming (this will block until channel is closed)
	toolCalls, content, ttft := HandleStreaming(streamChan, requestStartTime)

	// Get response from channel (should be ready since HandleStreaming blocks until channel closes)
	response := <-responseChan

	// Check for streaming errors
	if streamErr != nil {
		return nil, nil, "", streamErr
	}

	// Display token usage and time to first token if available
	if response != nil && len(response.Choices) > 0 && response.Choices[0].GenerationInfo != nil {
		displayTokenUsage(response.Choices[0].GenerationInfo, ttft)
	} else if ttft >= 0 {
		// Display TTFT even if token usage is not available
		fmt.Printf("\n⏱️  Time to first token: %d ms\n", ttft)
	}

	return response, toolCalls, content, nil
}

// handleNonStreamingRequest handles a non-streaming LLM request
func handleNonStreamingRequest(ctx context.Context, llm llmtypes.Model, modelID string, tools []llmtypes.Tool, conv *Conversation) (*llmtypes.ContentResponse, []llmtypes.ToolCall, string, error) {
	requestStartTime := time.Now()

	// Call LLM without streaming
	response, err := llm.GenerateContent(ctx, conv.GetMessages(),
		llmtypes.WithModel(modelID),
		llmtypes.WithTools(tools),
		llmtypes.WithToolChoiceString("auto"),
	)
	if err != nil {
		return nil, nil, "", err
	}

	// Extract content and tool calls from response
	var content string
	var toolCalls []llmtypes.ToolCall

	if len(response.Choices) > 0 {
		choice := response.Choices[0]
		content = choice.Content
		toolCalls = choice.ToolCalls

		// Display the response
		if content != "" {
			fmt.Println(content)
		}

		// Display tool calls if any
		for _, tc := range toolCalls {
			if tc.FunctionCall != nil {
				args := tc.FunctionCall.Arguments
				if args == "" {
					args = "{}"
				}
				fmt.Printf("\n[Tool call: %s, args: %s]\n", tc.FunctionCall.Name, args)
			}
		}

		// Calculate and display time to complete (since there's no first token in non-streaming)
		totalTime := time.Since(requestStartTime).Milliseconds()

		// Display token usage and total time
		if choice.GenerationInfo != nil {
			displayTokenUsage(choice.GenerationInfo, -1) // -1 means no TTFT for non-streaming
			fmt.Printf("⏱️  Total response time: %d ms\n", totalTime)
		} else {
			fmt.Printf("\n⏱️  Total response time: %d ms\n", totalTime)
		}
	}

	return response, toolCalls, content, nil
}

// handleImageUnderstandingRequest handles an image understanding request
// Note: The image message should already be added to the conversation before calling this function
func handleImageUnderstandingRequest(ctx context.Context, llm llmtypes.Model, modelID string, imageURL string, question string, conv *Conversation) (*llmtypes.ContentResponse, []llmtypes.ToolCall, string, error) {
	requestStartTime := time.Now()

	// Get all messages (image message should already be in the conversation)
	messages := conv.GetMessages()

	// Call LLM without streaming (image understanding typically works better non-streaming)
	response, err := llm.GenerateContent(ctx, messages,
		llmtypes.WithModel(modelID),
		// Note: We don't pass tools here, but you could add them if needed
	)
	if err != nil {
		return nil, nil, "", err
	}

	// Extract content and tool calls from response
	var content string
	var toolCalls []llmtypes.ToolCall

	if len(response.Choices) > 0 {
		choice := response.Choices[0]
		content = choice.Content
		toolCalls = choice.ToolCalls

		// Display the response
		if content != "" {
			fmt.Println("\n" + content)
		}

		// Display tool calls if any
		for _, tc := range toolCalls {
			if tc.FunctionCall != nil {
				args := tc.FunctionCall.Arguments
				if args == "" {
					args = "{}"
				}
				fmt.Printf("\n[Tool call: %s, args: %s]\n", tc.FunctionCall.Name, args)
			}
		}

		// Calculate and display time to complete
		totalTime := time.Since(requestStartTime).Milliseconds()

		// Display token usage and total time
		if choice.GenerationInfo != nil {
			displayTokenUsage(choice.GenerationInfo, -1) // -1 means no TTFT for non-streaming
			fmt.Printf("⏱️  Total response time: %d ms\n", totalTime)
		} else {
			fmt.Printf("\n⏱️  Total response time: %d ms\n", totalTime)
		}
	}

	return response, toolCalls, content, nil
}

// displayTokenUsage displays token usage information from GenerationInfo and time to first token
func displayTokenUsage(genInfo *llmtypes.GenerationInfo, ttft int64) {
	if genInfo == nil {
		// Still display TTFT if available
		if ttft >= 0 {
			fmt.Printf("\n⏱️  Time to first token: %d ms\n", ttft)
		}
		return
	}

	var inputTokens, outputTokens, totalTokens int
	hasTokens := false

	if genInfo.InputTokens != nil {
		inputTokens = *genInfo.InputTokens
		hasTokens = true
	}
	if genInfo.OutputTokens != nil {
		outputTokens = *genInfo.OutputTokens
		hasTokens = true
	}
	if genInfo.TotalTokens != nil {
		totalTokens = *genInfo.TotalTokens
	} else if inputTokens > 0 && outputTokens > 0 {
		// Calculate total if not provided
		totalTokens = inputTokens + outputTokens
		hasTokens = true
	}

	// Display time to first token if available
	if ttft >= 0 {
		fmt.Printf("\n⏱️  Time to first token: %d ms\n", ttft)
	}

	if hasTokens {
		fmt.Printf("📊 Token Usage:\n")
		if inputTokens > 0 {
			fmt.Printf("   Input tokens:  %d\n", inputTokens)
		}
		if outputTokens > 0 {
			fmt.Printf("   Output tokens: %d\n", outputTokens)
		}
		if totalTokens > 0 {
			fmt.Printf("   Total tokens:  %d\n", totalTokens)
		}
	}
}

// handleCommand handles special commands
func handleCommand(cmd string, conv *Conversation, tools []llmtypes.Tool) error {
	cmd = strings.TrimSpace(cmd)
	switch cmd {
	case "/help":
		fmt.Println("\nAvailable commands:")
		fmt.Println("  /help     - Show this help message")
		fmt.Println("  /tools    - List available tools")
		fmt.Println("  /clear    - Clear conversation history")
		fmt.Println("  /history  - Show conversation summary")
		fmt.Println("  /exit     - Exit chat")
		fmt.Println("  /quit     - Exit chat")
	case "/tools":
		fmt.Println("\nAvailable tools:")
		for _, tool := range tools {
			if tool.Function != nil {
				fmt.Printf("  - %s: %s\n", tool.Function.Name, tool.Function.Description)
			}
		}
	case "/clear":
		conv.Clear()
		fmt.Println("\nConversation cleared (system prompt retained)")
	case "/history":
		messages := conv.GetMessages()
		fmt.Printf("\nConversation history (%d messages):\n", len(messages))
		for i, msg := range messages {
			role := string(msg.Role)
			preview := ""
			if len(msg.Parts) > 0 {
				if text, ok := msg.Parts[0].(llmtypes.TextContent); ok {
					preview = text.Text
					if len(preview) > 50 {
						preview = preview[:50] + "..."
					}
				}
			}
			fmt.Printf("  %d. [%s] %s\n", i+1, role, preview)
		}
	case "/exit", "/quit":
		return errExit
	default:
		fmt.Printf("Unknown command: %s (type /help for available commands)\n", cmd)
	}
	return nil
}
