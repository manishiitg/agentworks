package claudecode

import (
	"log"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/internal/testing"
	"github.com/manishiitg/multi-llm-provider-go/internal/testing/commands/shared"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var ClaudeCodeStreamingMultiTurnTestCmd = &cobra.Command{
	Use:   "claude-code-streaming-multiturn",
	Short: "Test Claude Code streaming multi-turn conversation",
	Run:   runClaudeCodeStreamingMultiTurnTest,
}

func init() {
	ClaudeCodeStreamingMultiTurnTestCmd.Flags().String("model", "claude-code", "Claude Code model to test")
}

func runClaudeCodeStreamingMultiTurnTest(cmd *cobra.Command, args []string) {
	_ = godotenv.Load("agent_go/.env")
	_ = godotenv.Load(".env")
	_ = godotenv.Load("../.env")

	logFile := viper.GetString("log-file")
	logLevel := viper.GetString("log-level")
	testing.InitTestLogger(logFile, logLevel)
	logger := testing.GetTestLogger()

	modelID, _ := cmd.Flags().GetString("model")
	if modelID == "" {
		modelID = "claude-code"
	}

	llmInstance, err := llmproviders.InitializeLLM(llmproviders.Config{
		Provider:    llmproviders.ProviderClaudeCode,
		ModelID:     modelID,
		Temperature: 0.7,
		Logger:      logger,
	})
	if err != nil {
		log.Fatalf("Failed to initialize Claude Code LLM: %v", err)
	}

	shared.RunStreamingMultiTurnTest(llmInstance, modelID)
}
