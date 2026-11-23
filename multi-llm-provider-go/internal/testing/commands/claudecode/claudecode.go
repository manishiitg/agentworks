package claudecode

import (
	"github.com/spf13/cobra"
)

var ClaudeCodeCmd = &cobra.Command{
	Use:   "claude-code",
	Short: "Claude Code CLI provider tests",
}

func init() {
	ClaudeCodeCmd.AddCommand(ClaudeCodeStreamingContentTestCmd)
	ClaudeCodeCmd.AddCommand(ClaudeCodeSearchWebTestCmd)
}
