package codexcli

import (
	"fmt"
	"log"
	"strings"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/internal/testing"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var CodexCLISearchWebTestCmd = &cobra.Command{
	Use:   "codex-cli-search-web",
	Short: "Test Codex CLI native web search",
	Run:   runCodexCLISearchWebTest,
}

func init() {
	CodexCLISearchWebTestCmd.Flags().String("model", llmproviders.DefaultCodexCLIModel, "Codex CLI model to test")
	CodexCLISearchWebTestCmd.Flags().String("query", "", "Web search query to run")
	_ = CodexCLISearchWebTestCmd.MarkFlagRequired("query")
}

func runCodexCLISearchWebTest(cmd *cobra.Command, args []string) {
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

	query, _ := cmd.Flags().GetString("query")
	query = strings.TrimSpace(query)
	if query == "" {
		log.Fatal("query is required")
	}

	llmInstance, err := llmproviders.InitializeLLM(llmproviders.Config{
		Provider: llmproviders.ProviderCodexCLI,
		ModelID:  modelID,
		Logger:   logger,
	})
	if err != nil {
		log.Fatalf("Failed to initialize Codex CLI LLM: %v", err)
	}

	result, err := llmproviders.SearchWeb(cmd.Context(), llmInstance, query)
	if err != nil {
		log.Fatalf("Codex CLI web search failed: %v", err)
	}

	fmt.Println(result)
}
