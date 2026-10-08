package testing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var readImageTestCmd = &cobra.Command{
	Use:   "read-image",
	Short: "Test read_image tool with workspace images",
	Long: `Test the read_image tool that reads images from workspace and processes them with LLM.

This test:
1. Creates an agent with workspace tools (including read_image)
2. Tests read_image tool with a specific image file
3. Verifies that the image is processed correctly and sent to LLM`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Load .env file if present
		if err := godotenv.Load("agent_go/.env"); err == nil {
			// Environment loaded successfully
		} else if err := godotenv.Load(".env"); err == nil {
			// Environment loaded successfully
		} else if err := godotenv.Load("../.env"); err == nil {
			// Environment loaded successfully
		}

		// Get logging configuration from viper
		logFile := viper.GetString("log-file")
		logLevel := viper.GetString("log-level")

		// Initialize test logger
		InitTestLogger(logFile, logLevel)
		logger := GetTestLogger()

		logger.Info(fmt.Sprintf("=== Read Image Tool Test ==="))

		// Get provider and model from viper or use defaults
		// The provider flag is bound as "test.provider" in testing.go
		provider := viper.GetString("test.provider")
		if provider == "" {
			provider = "openai"
		}
		model := viper.GetString("model")
		if model == "" {
			if provider == "openai" {
				model = "gpt-4o-mini"
			} else if provider == "bedrock" {
				model = "anthropic.claude-3-5-sonnet-20241022-v2:0"
			} else {
				model = "claude-haiku-5-5"
			}
		}

		// Get image path from flag or use default.
		// read_image requires a full absolute path under workspace-docs.
		imagePath := strings.TrimSpace(viper.GetString("image-path"))
		if imagePath == "" {
			imagePath = defaultReadImageTestPath()
		}
		if !filepath.IsAbs(imagePath) {
			return fmt.Errorf("--image-path must be a full absolute workspace-docs path, got %q", imagePath)
		}

		logger.Info(fmt.Sprintf("Using workspace image path: %s", imagePath))

		// Convert provider string to llm.Provider type
		llmProvider := llm.Provider(provider)

		logger.Info(fmt.Sprintf("Provider string: '%s', Provider type: '%s'", provider, string(llmProvider)))

		// Initialize LLM
		var llmModel llmtypes.Model
		var err error

		// Get API keys from environment
		apiKeys := &llm.ProviderAPIKeys{}
		if provider == "openai" {
			openAIKey := os.Getenv("OPENAI_API_KEY")
			if openAIKey == "" {
				return fmt.Errorf("OPENAI_API_KEY environment variable is not set")
			}
			apiKeys.OpenAI = &openAIKey
		} else if provider == "bedrock" {
			// Bedrock uses AWS credentials from environment
		} else if provider == "vertex" {
			// Vertex uses GCP credentials from environment
		}

		llmModel, err = llm.InitializeLLM(llm.Config{
			Provider:    llmProvider,
			ModelID:     model,
			Temperature: 0.7,
			Logger:      nil, // Use default logger
			APIKeys:     apiKeys,
		})
		if err != nil {
			return fmt.Errorf("failed to initialize LLM: %w", err)
		}

		logger.Info(fmt.Sprintf("Agent configuration created - provider: %s, model: %s, image: %s", provider, model, imagePath))

		// Create the agent
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		directTools, err := advancedWorkspaceToolDefinitions()
		if err != nil {
			return fmt.Errorf("build workspace tool definitions: %w", err)
		}
		agent, err := createTestingAgent(ctx, llmModel, "configs/mcp_servers_simple.json", "fileserver", 10, logger, directTools)
		if err != nil {
			return fmt.Errorf("failed to create agent: %w", err)
		}
		defer agent.Close()
		logger.Info(fmt.Sprintf("✅ Agent created successfully with %d workspace tools", len(directTools)))

		// Test read_image tool. The prompt includes the absolute path so the
		// agent uses the same path contract exposed by the tool metadata.
		prompt := fmt.Sprintf("Please read the file '%s' and describe what you see in it.", imagePath)

		logger.Info(fmt.Sprintf("Testing read_image tool - image_file: %s", imagePath))

		// Invoke the agent
		response, err := runAgentText(ctx, agent, prompt)
		if err != nil {
			logger.Error(fmt.Sprintf("❌ Read image test failed: %v", err), nil)
			return fmt.Errorf("read image test failed: %w", err)
		}

		logger.Info(fmt.Sprintf("✅ Read image test successful"))
		logger.Info(fmt.Sprintf("Response length: %d characters", len(response)))

		// Show response
		fmt.Printf("\n🖼️ Image: %s\n", imagePath)
		fmt.Printf("📝 Response: %s\n", response)

		// Show agent capabilities
		servers := mcpagent.ReadAgentDiagnostics(agent).ServerNames
		fmt.Printf("\n📊 Connected Servers: %v\n", servers)

		logger.Info(fmt.Sprintf("✅ Read image test completed successfully"))
		fmt.Println("\n🎉 Read image test completed successfully!")

		return nil
	},
}

func init() {
	readImageTestCmd.Flags().String("image-path", "", "Full absolute workspace-docs image path to test")
	viper.BindPFlag("image-path", readImageTestCmd.Flags().Lookup("image-path"))
}

func defaultReadImageTestPath() string {
	if path := firstExistingWorkspaceDocsAbsoluteTestPath(
		"_users/default/Downloads/hdfc_after_password_attempt_1.png",
		"Downloads/hdfc_after_password_attempt_1.png",
	); path != "" {
		return path
	}
	return workspaceDocsAbsoluteTestPath("_users/default/Downloads/hdfc_after_password_attempt_1.png")
}
