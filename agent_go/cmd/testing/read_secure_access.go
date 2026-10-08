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

var readSecureAccessTestCmd = &cobra.Command{
	Use:   "read-secure-access",
	Short: "Test read_image tool with Downloads/secure_access_check.png",
	Long: `Test the read_image tool specifically with Downloads/secure_access_check.png.

This test:
1. Creates an agent with workspace tools (including read_image)
2. Invokes the agent to read Downloads/secure_access_check.png
3. Shows the full conversation flow, tool calls, and LLM responses
4. Helps debug the read_image tool integration

Example:
  orchestrator test read-secure-access --provider bedrock
  orchestrator test read-secure-access --provider vertex`,
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

		logger.Info(fmt.Sprintf("=== Read Secure Access Check Image Test ==="))

		// Hardcoded image path. read_image requires a full absolute path under workspace-docs.
		imagePath := defaultReadSecureAccessImagePath()
		if !filepath.IsAbs(imagePath) {
			return fmt.Errorf("secure access image path must be absolute, got %q", imagePath)
		}
		logger.Info(fmt.Sprintf("Testing with image: %s", imagePath))

		// Get provider and model from viper or use defaults
		provider := viper.GetString("test.provider")
		if provider == "" {
			provider = "bedrock"
		}
		model := viper.GetString("model")
		if model == "" {
			if provider == "openai" {
				model = "gpt-4o-mini"
			} else if provider == "bedrock" {
				model = "anthropic.claude-3-5-sonnet-20241022-v2:0"
			} else if provider == "vertex" {
				model = "claude-sonnet-4-5"
			} else {
				model = "claude-haiku-5-5"
			}
		}

		logger.Info(fmt.Sprintf("Provider: %s, Model: %s", provider, model))

		// Convert provider string to llm.Provider type
		llmProvider := llm.Provider(provider)

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

		logger.Info(fmt.Sprintf("Agent configuration created - provider: %s, model: %s", provider, model))

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

		// Test read_image tool with the specific image
		prompt := fmt.Sprintf("Please read the file '%s' and describe what you see in it.", imagePath)

		logger.Info(fmt.Sprintf("📝 Invoking agent with prompt: %s", prompt))
		logger.Info(fmt.Sprintf("🔍 This should trigger the read_image tool"))

		// Invoke the agent
		response, err := runAgentText(ctx, agent, prompt)
		if err != nil {
			logger.Error(fmt.Sprintf("❌ Read secure access image test failed: %v", err), nil)
			return fmt.Errorf("read secure access image test failed: %w", err)
		}

		logger.Info(fmt.Sprintf("✅ Read secure access image test successful"))
		logger.Info(fmt.Sprintf("Response length: %d characters", len(response)))

		// Show response
		fmt.Print("\n" + strings.Repeat("=", 80) + "\n")
		fmt.Printf("🖼️  Image: %s\n", imagePath)
		fmt.Printf("📝 LLM Response:\n")
		fmt.Print(strings.Repeat("-", 80) + "\n")
		fmt.Printf("%s\n", response)
		fmt.Print(strings.Repeat("=", 80) + "\n")

		// Show agent capabilities
		servers := mcpagent.ReadAgentDiagnostics(agent).ServerNames
		fmt.Printf("\n📊 Connected Servers: %v\n", servers)

		logger.Info(fmt.Sprintf("✅ Read secure access image test completed successfully"))
		fmt.Println("\n🎉 Read secure access image test completed successfully!")

		return nil
	},
}

func defaultReadSecureAccessImagePath() string {
	if path := firstExistingWorkspaceDocsAbsoluteTestPath(
		"_users/default/Downloads/secure_access_check.png",
		"Downloads/secure_access_check.png",
	); path != "" {
		return path
	}
	return workspaceDocsAbsoluteTestPath("_users/default/Downloads/secure_access_check.png")
}
