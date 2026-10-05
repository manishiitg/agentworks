package testing

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// TestingCmd represents the testing command group
var TestingCmd = &cobra.Command{
	Use:   "test",
	Short: "Testing framework for MCP Agent with comprehensive validation",
	Long: `Testing framework for MCP Agent with comprehensive validation.

Features:
- Agent conversation testing (with all LLM providers)
- MCP server connection testing
- SSE streaming testing
- Langfuse trace retrieval
- Connection pooling validation
- Context cancellation testing

Note: For comprehensive LLM provider testing (tool calls, provider-specific response formats,
streaming, embeddings, etc.), use the multi-llm-provider-go test suite:
  See: https://github.com/manishiitg/multi-llm-provider-go

Examples:
  # Max tokens flexibility testing
  orchestrator test max-tokens-flexibility --provider bedrock --verbose`,
}

// Common flags for all testing commands
var (
	verbose  bool
	provider string
	// showOutput, timeout, and config are accessed via viper, not directly
)

func init() {
	// Don't initialize logger here - let individual commands handle it
	// The logger will be initialized in each test command based on the log-file parameter

	// Add common flags for all testing commands
	TestingCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "enable verbose test output")
	TestingCmd.PersistentFlags().StringVar(&provider, "provider", "bedrock", "LLM provider for tests")
	TestingCmd.PersistentFlags().String("model", "", "model ID override (e.g., gpt-5.4-mini)")
	// show-output, timeout, and config flags are defined but accessed via viper
	TestingCmd.PersistentFlags().Bool("show-output", true, "show detailed test output")
	TestingCmd.PersistentFlags().String("timeout", "5m", "test timeout duration")
	TestingCmd.PersistentFlags().String("config", "mcp.yaml", "MCP config file to use for tests")

	// Remove duplicate logging flag definitions - let them inherit from root command
	// The root command already defines and binds these flags:
	// --log-file, --log-level, --log-format, --test.log-file

	// Bind to viper for configuration
	viper.BindPFlag("test.verbose", TestingCmd.PersistentFlags().Lookup("verbose"))
	viper.BindPFlag("test.show-output", TestingCmd.PersistentFlags().Lookup("show-output"))
	viper.BindPFlag("test.timeout", TestingCmd.PersistentFlags().Lookup("timeout"))
	viper.BindPFlag("test.provider", TestingCmd.PersistentFlags().Lookup("provider"))
	viper.BindPFlag("test.model", TestingCmd.PersistentFlags().Lookup("model"))
	viper.BindPFlag("config", TestingCmd.PersistentFlags().Lookup("config"))
	// Remove duplicate viper bindings for logging flags - they're already bound in root command

	// Initialize all subcommands
	initTestingCommands()
}

// initTestingCommands initializes all testing subcommands
func initTestingCommands() {
	// Don't initialize logger here - let individual commands handle it
	// The logger will be initialized in each test command based on the log-file parameter

	// Add subcommands explicitly to ensure they're registered
	// Note: LLM provider tests (anthropic, bedrock, openai, vertex, etc.) are now in github.com/manishiitg/multi-llm-provider-go
	// Use the llm-test tool from multi-llm-provider-go for comprehensive provider testing
	TestingCmd.AddCommand(sseCmd)
	TestingCmd.AddCommand(maxTokensFlexibilityCmd)
	TestingCmd.AddCommand(customToolsTestCmd)
	TestingCmd.AddCommand(readImageTestCmd)
	TestingCmd.AddCommand(readImageProvidersTestCmd)
	TestingCmd.AddCommand(imageGenProvidersTestCmd)
	TestingCmd.AddCommand(searchWebLLMProvidersTestCmd)
	TestingCmd.AddCommand(readSecureAccessTestCmd)
	TestingCmd.AddCommand(workspaceDiffJSONTestCmd)
	TestingCmd.AddCommand(shellSecurityTestCmd)
	TestingCmd.AddCommand(shellOutputTestCmd)

	TestingCmd.AddCommand(claudeExperimentalTestCmd)
	TestingCmd.AddCommand(claudeResumeAfterCancelTestCmd)
	TestingCmd.AddCommand(codexResumeAfterCancelTestCmd)
	TestingCmd.AddCommand(codexMCPToolCallTestCmd)
	TestingCmd.AddCommand(codingAgentChatE2ECmd)
	TestingCmd.AddCommand(agentBrowseCDPDiagnoseCmd)
	TestingCmd.AddCommand(agentBrowseE2ECmd)
	TestingCmd.AddCommand(agentBrowseStressE2ECmd)
	TestingCmd.AddCommand(agentBrowseAPIStressE2ECmd)
	TestingCmd.AddCommand(codingAgentBackgroundE2ECmd)
	TestingCmd.AddCommand(workflowAutoNotificationE2ECmd)
	TestingCmd.AddCommand(providerSwitchE2ECmd)
	TestingCmd.AddCommand(cliSandboxContractCmd)
	TestingCmd.AddCommand(cliStepContractCmd)
}
