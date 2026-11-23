package claudecode

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

var ClaudeCodeSearchWebTestCmd = &cobra.Command{
	Use:   "claude-code-search-web",
	Short: "Test Claude Code native web search",
	Run:   runClaudeCodeSearchWebTest,
}

func init() {
	ClaudeCodeSearchWebTestCmd.Flags().String("model", "claude-code", "Claude Code model to test")
	ClaudeCodeSearchWebTestCmd.Flags().String("query", "", "Web search query to run")
	_ = ClaudeCodeSearchWebTestCmd.MarkFlagRequired("query")
}

func runClaudeCodeSearchWebTest(cmd *cobra.Command, args []string) {
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

	query, _ := cmd.Flags().GetString("query")
	query = strings.TrimSpace(query)
	if query == "" {
		log.Fatal("query is required")
	}

	llmInstance, err := llmproviders.InitializeLLM(llmproviders.Config{
		Provider: llmproviders.ProviderClaudeCode,
		ModelID:  modelID,
		Logger:   logger,
	})
	if err != nil {
		log.Fatalf("Failed to initialize Claude Code LLM: %v", err)
	}

	result, err := llmproviders.SearchWeb(cmd.Context(), llmInstance, query)
	if err != nil {
		log.Fatalf("Claude Code web search failed: %v", err)
	}

	fmt.Println(result)
}
