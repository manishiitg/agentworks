package codexcli

import (
	"log"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/internal/testing"
	"github.com/manishiitg/multi-llm-provider-go/internal/testing/commands/shared"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var CodexCLIStreamingContentTestCmd = &cobra.Command{
	Use:   "codex-cli-streaming-content",
	Short: "Test Codex CLI streaming content (no tool calls)",
	Run:   runCodexCLIStreamingContentTest,
}

func init() {
	CodexCLIStreamingContentTestCmd.Flags().String("model", llmproviders.DefaultCodexCLIModel, "Codex CLI model to test")
}

func runCodexCLIStreamingContentTest(cmd *cobra.Command, args []string) {
	_ = godotenv.Load("agent_go/.env")
	_ = godotenv.Load(".env")
	_ = godotenv.Load("../.env")

	logFile := viper.GetString("log-file")
	logLevel := viper.GetString("log-level")
	testing.InitTestLogger(logFile, logLevel)
	logger := testing.GetTestLogger()

	modelID, _ := cmd.Flags().GetString("model")
	if modelID == "" {
		modelID = llmproviders.DefaultCodexCLIModel
	}

	llmInstance, err := llmproviders.InitializeLLM(llmproviders.Config{
		Provider:    llmproviders.ProviderCodexCLI,
		ModelID:     modelID,
		Temperature: 0.7,
		Logger:      logger,
	})
	if err != nil {
		log.Fatalf("Failed to initialize Codex CLI LLM: %v", err)
	}

	shared.RunStreamingContentTest(llmInstance, modelID)
}
