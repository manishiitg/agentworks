package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// FileLogger is a custom logger implementation that writes logs to a file
type FileLogger struct {
	file *os.File
	mu   sync.Mutex
}

// NewFileLogger creates a new file logger that writes to the specified file
func NewFileLogger(filename string) (*FileLogger, error) {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	return &FileLogger{
		file: file,
	}, nil
}

// Close closes the log file
func (l *FileLogger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// log writes a formatted log entry with timestamp and level
func (l *FileLogger) log(level string, format string, v ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	message := fmt.Sprintf(format, v...)
	logEntry := fmt.Sprintf("[%s] [%s] %s\n", timestamp, level, message)

	_, err := l.file.WriteString(logEntry)
	if err != nil {
		// Fallback to stderr if file write fails
		fmt.Fprintf(os.Stderr, "Failed to write to log file: %v\n", err)
		fmt.Fprint(os.Stderr, logEntry)
	}
}

// Infof logs an info message
func (l *FileLogger) Infof(format string, v ...any) {
	l.log("INFO", format, v...)
}

// Errorf logs an error message
func (l *FileLogger) Errorf(format string, v ...any) {
	l.log("ERROR", format, v...)
}

// Debugf logs a debug message
func (l *FileLogger) Debugf(format string, args ...interface{}) {
	l.log("DEBUG", format, args...)
}

func main() {
	// Check for API key
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "Error: OPENAI_API_KEY environment variable is required\n")
		fmt.Fprintf(os.Stderr, "Set it with: export OPENAI_API_KEY=your-api-key\n")
		os.Exit(1)
	}

	// Create a custom file logger
	logger, err := NewFileLogger("test.log")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Close()

	// Log that we're starting
	logger.Infof("Starting custom logger example")
	logger.Infof("Log file: test.log")

	// Configure the OpenAI provider with custom logger
	config := llmproviders.Config{
		Provider:     llmproviders.ProviderOpenAI,
		ModelID:      "gpt-4.1-mini", // Use a cost-effective model for examples
		Temperature:  0.7,
		Logger:       logger, // Use our custom file logger
		EventEmitter: nil,    // nil is allowed - uses no-op event emitter
	}

	// Initialize the LLM
	logger.Infof("Initializing OpenAI provider with model: %s", config.ModelID)
	llm, err := llmproviders.InitializeLLM(config)
	if err != nil {
		logger.Errorf("Failed to initialize LLM: %v", err)
		fmt.Fprintf(os.Stderr, "Failed to initialize LLM: %v\n", err)
		os.Exit(1)
	}

	logger.Infof("Successfully initialized LLM")

	// Create context
	ctx := context.Background()

	// Prepare the message
	messages := []llmtypes.MessageContent{
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "Hello! Can you introduce yourself in one sentence?"),
	}

	// Generate content
	logger.Infof("Sending request to OpenAI...")
	response, err := llm.GenerateContent(ctx, messages)
	if err != nil {
		logger.Errorf("Failed to generate content: %v", err)
		fmt.Fprintf(os.Stderr, "Failed to generate content: %v\n", err)
		os.Exit(1)
	}

	logger.Infof("Successfully received response from OpenAI")

	// Display the response
	if len(response.Choices) > 0 {
		choice := response.Choices[0]
		fmt.Println("\n📝 Response:")
		fmt.Println(choice.Content)
		fmt.Println()

		// Log the response length
		logger.Infof("Response received: %d characters", len(choice.Content))

		// Display token usage if available
		if choice.GenerationInfo != nil {
			fmt.Println("📊 Token Usage:")
			if choice.GenerationInfo.InputTokens != nil {
				fmt.Printf("  Input tokens:  %d\n", *choice.GenerationInfo.InputTokens)
				logger.Infof("Input tokens: %d", *choice.GenerationInfo.InputTokens)
			}
			if choice.GenerationInfo.OutputTokens != nil {
				fmt.Printf("  Output tokens: %d\n", *choice.GenerationInfo.OutputTokens)
				logger.Infof("Output tokens: %d", *choice.GenerationInfo.OutputTokens)
			}
			if choice.GenerationInfo.TotalTokens != nil {
				fmt.Printf("  Total tokens:  %d\n", *choice.GenerationInfo.TotalTokens)
				logger.Infof("Total tokens: %d", *choice.GenerationInfo.TotalTokens)
			}
		}
	} else {
		fmt.Println("No response received")
		logger.Errorf("No response received from LLM")
	}

	logger.Infof("Example completed successfully")
	fmt.Println("\n✅ Check test.log for detailed logs!")
}
