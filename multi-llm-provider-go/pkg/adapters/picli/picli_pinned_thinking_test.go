package picli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A deployment whose gateway rejects tool calls together with reasoning pins the model to "off" in its template settings. The
// project default ("high") must not reach Pi's --thinking, and the model must not offer reasoning levels: Citymall's chats came
// back empty (the gateway's HTTP 400) until this held.
func TestPinnedThinkingBeatsTheChosenLevelAndHidesTheLevels(t *testing.T) {
	dir := t.TempDir()
	models := `{"providers":{"citymall":{"baseUrl":"https://example.test/v1","api":"openai-completions","apiKey":"$CITYMALL_API_KEY","models":[{"id":"gpt-6-luna","name":"Luna","input":["text"],"reasoning":true,"thinkingLevelMap":{"off":"none"}}]}}}`
	settings := `{"defaultThinkingLevel":"off","modelThinkingLevels":{"citymall/gpt-6-luna":"off"}}`
	for name, content := range map[string]string{"models.json": models, "settings.json": settings} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(EnvPiAgentTemplateDir, dir)
	if _, err := LoadPiAgentTemplate(); err != nil {
		t.Fatalf("test template must load: %v", err)
	}
	if got := piEffectiveThinkingLevel("citymall", "gpt-6-luna", &llmtypes.CallOptions{ReasoningEffort: "high"}); got != "off" {
		t.Fatalf("pinned model must run with thinking off, got %q", got)
	}
	if got := piEffectiveThinkingLevel("openai", "other", &llmtypes.CallOptions{ReasoningEffort: "high"}); got != "high" {
		t.Fatalf("an unpinned model keeps the chosen level, got %q", got)
	}
}
