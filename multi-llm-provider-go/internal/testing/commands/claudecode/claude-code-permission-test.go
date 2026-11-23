package claudecode

import (
	"context"
	"log"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/internal/testing"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var ClaudeCodePermissionTestCmd = &cobra.Command{
	Use:   "claude-code-permission",
	Short: "Test Claude Code permission denial handling",
	Run:   runClaudeCodePermissionTest,
}

func runClaudeCodePermissionTest(cmd *cobra.Command, args []string) {
	_ = godotenv.Load(".env")

	logFile := viper.GetString("log-file")
	logLevel := viper.GetString("log-level")
	testing.InitTestLogger(logFile, logLevel)
	logger := testing.GetTestLogger()

	llmInstance, err := llmproviders.InitializeLLM(llmproviders.Config{
		Provider:    llmproviders.ProviderClaudeCode,
		ModelID:     "claude-code",
		Temperature: 0.7,
		Logger:      logger,
	})
	if err != nil {
		log.Fatalf("Failed to initialize Claude Code LLM: %v", err)
	}

	ctx := context.Background()
	messages := []llmtypes.MessageContent{
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "Install 'wget' using brew. Use the Bash tool specifically."),
	}

	log.Printf("🚀 Requesting sensitive operation (brew install)...")
	resp, err := llmInstance.GenerateContent(ctx, messages)
	if err != nil {
		log.Fatalf("❌ Generation failed: %v", err)
	}

	log.Printf("✅ Response received")
	
	// Check for permission denials in GenerationInfo
	if len(resp.Choices) > 0 && resp.Choices[0].GenerationInfo != nil {
		if denials, ok := resp.Choices[0].GenerationInfo.Additional["permission_denials"]; ok {
			log.Printf("🎯 SUCCESS: Detected permission denials: %+v", denials)
		} else {
			log.Printf("⚠️ No permission denials found in Additional map. Model response: %s", resp.Choices[0].Content)
		}
	} else {
		log.Printf("❌ No GenerationInfo found in response")
	}
}
