package codexcli

import (
	"github.com/spf13/cobra"
)

var CodexCLICmd = &cobra.Command{
	Use:   "codex-cli",
	Short: "OpenAI Codex CLI provider tests",
}

func init() {
	CodexCLICmd.AddCommand(CodexCLIStreamingContentTestCmd)
	CodexCLICmd.AddCommand(CodexCLISearchWebTestCmd)
	CodexCLICmd.AddCommand(CodexCLIImageGenerateTestCmd)
}
