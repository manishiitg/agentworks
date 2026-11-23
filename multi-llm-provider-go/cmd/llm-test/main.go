package main

import (
	"os"

	"github.com/spf13/cobra"

	claudecodecmd "github.com/manishiitg/multi-llm-provider-go/internal/testing/commands/claudecode"
	codexclicmd "github.com/manishiitg/multi-llm-provider-go/internal/testing/commands/codexcli"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "llm-test",
		Short: "LLM Provider Testing Tool",
		Long:  "Test tool for llm-providers module",
	}

	// Register all test commands
	rootCmd.AddCommand(claudecodecmd.ClaudeCodeCmd)
	rootCmd.AddCommand(claudecodecmd.ClaudeCodeStreamingContentTestCmd)
	rootCmd.AddCommand(claudecodecmd.ClaudeCodeStreamingMultiTurnTestCmd)
	rootCmd.AddCommand(claudecodecmd.ClaudeCodePermissionTestCmd)
	rootCmd.AddCommand(claudecodecmd.ClaudeCodeSearchWebTestCmd)
	rootCmd.AddCommand(codexclicmd.CodexCLICmd)
	rootCmd.AddCommand(codexclicmd.CodexCLIStreamingContentTestCmd)
	rootCmd.AddCommand(codexclicmd.CodexCLIStreamingMultiTurnTestCmd)
	rootCmd.AddCommand(codexclicmd.CodexCLISearchWebTestCmd)
	rootCmd.AddCommand(codexclicmd.CodexCLIImageGenerateTestCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
