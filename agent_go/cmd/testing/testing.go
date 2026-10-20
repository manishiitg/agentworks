package testing

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// TestingCmd groups coding-CLI, browser and workspace integration checks.
var TestingCmd = &cobra.Command{
	Use:   "test",
	Short: "Coding CLI and workspace integration checks",
	Long: `Run coding-CLI, browser and workspace integration checks.

Coding-CLI commands exercise real provider processes and the current chat,
background work, sandbox and step contracts. Workspace image tools and browser
commands check their existing execution paths.`,
}

func init() {
	// Don't initialize logger here - let individual commands handle it
	// The logger will be initialized in each test command based on the log-file parameter

	// Add common flags for all testing commands
	TestingCmd.PersistentFlags().String("model", "", "model ID override (e.g., gpt-5.4-mini)")
	TestingCmd.PersistentFlags().String("timeout", "5m", "test timeout duration")

	// Remove duplicate logging flag definitions - let them inherit from root command
	// The root command already defines and binds these flags:
	// --log-file, --log-level, --log-format, --test.log-file

	// Bind to viper for configuration
	viper.BindPFlag("test.timeout", TestingCmd.PersistentFlags().Lookup("timeout"))
	viper.BindPFlag("test.model", TestingCmd.PersistentFlags().Lookup("model"))
	// Remove duplicate viper bindings for logging flags - they're already bound in root command

	// Initialize all subcommands
	initTestingCommands()
}

// initTestingCommands initializes all testing subcommands
func initTestingCommands() {
	// Don't initialize logger here - let individual commands handle it
	// The logger will be initialized in each test command based on the log-file parameter

	// Add subcommands explicitly to ensure they're registered
	TestingCmd.AddCommand(readImageProvidersTestCmd)
	TestingCmd.AddCommand(imageGenProvidersTestCmd)
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
