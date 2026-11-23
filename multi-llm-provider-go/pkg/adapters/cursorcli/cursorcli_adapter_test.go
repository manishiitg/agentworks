package cursorcli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type MockLogger struct{}

func (l *MockLogger) Infof(format string, args ...any)  { fmt.Printf("INFO: "+format+"\n", args...) }
func (l *MockLogger) Errorf(format string, args ...any) { fmt.Printf("ERROR: "+format+"\n", args...) }
func (l *MockLogger) Debugf(format string, args ...interface{}) {
	fmt.Printf("DEBUG: "+format+"\n", args...)
}

func TestCursorCLIAdapterImplementsWebSearchModel(t *testing.T) {
	adapter := NewCursorCLIAdapter("", "cursor-cli", &MockLogger{})
	if _, ok := interface{}(adapter).(llmtypes.WebSearchModel); !ok {
		t.Fatal("CursorCLIAdapter should implement llmtypes.WebSearchModel")
	}
}

// TestPrepareCursorProjectFilesRemovesGeneratedFilesOnCleanup verifies that
// cleanup removes what the session generated and restores a project's own
// .cursor/cli.json byte-for-byte (it is never lost).
func TestPrepareCursorProjectFilesRemovesGeneratedFilesOnCleanup(t *testing.T) {
	workDir := t.TempDir()
	cursorDir := filepath.Join(workDir, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cliPath := filepath.Join(cursorDir, "cli.json")
	original := `{"permissions":{"allow":["Shell(ls)"],"deny":[]}}`
	if err := os.WriteFile(cliPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := &llmtypes.CallOptions{}
	WithProjectConfig(`{"permissions":{"allow":[],"deny":["Shell(rm)"]}}`)(opts)
	WithMCPConfig(`{"mcpServers":{"api-bridge":{"command":"node","args":["server.js"]}}}`)(opts)

	cleanup, err := prepareCursorProjectFiles(workDir, "System text", opts, "test-session-restore")
	if err != nil {
		t.Fatalf("prepareCursorProjectFiles error = %v", err)
	}
	if got, err := os.ReadFile(cliPath); err != nil || !strings.Contains(string(got), "Shell(rm)") {
		t.Fatalf("mid-session cli.json = %q err=%v, want orchestrator override", string(got), err)
	}
	if _, err := os.Stat(filepath.Join(cursorDir, "mcp.json")); err != nil {
		t.Fatalf("mcp.json not written: %v", err)
	}

	cleanup()

	if got, err := os.ReadFile(cliPath); err != nil || string(got) != original {
		t.Fatalf("project's own cli.json after cleanup = %q err=%v, want it restored byte-for-byte", string(got), err)
	}
	for _, generated := range []string{"mcp.json", filepath.Join("rules", "mlp-system.mdc")} {
		if _, err := os.Stat(filepath.Join(cursorDir, generated)); !os.IsNotExist(err) {
			t.Fatalf("cleanup must remove generated %s; stat err = %v", generated, err)
		}
	}
	if _, err := os.Stat(filepath.Join(workDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("cleanup must remove the generated .git marker; stat err = %v", err)
	}
}

func TestPrepareCursorProjectFilesWritesMCPAllowlistCLIConfig(t *testing.T) {
	workDir := t.TempDir()
	opts := &llmtypes.CallOptions{}
	WithMCPConfig(`{"mcpServers":{"api-bridge":{"command":"node","args":["server.js"]}}}`)(opts)

	cleanup, err := prepareCursorProjectFiles(workDir, "System text", opts, "test-session-mcp-allow")
	if err != nil {
		t.Fatalf("prepareCursorProjectFiles error = %v", err)
	}
	defer cleanup()

	cursorDir := filepath.Join(workDir, ".cursor")
	mcpBody, err := os.ReadFile(filepath.Join(cursorDir, "mcp.json"))
	if err != nil {
		t.Fatalf("read mcp.json: %v", err)
	}
	if !strings.Contains(string(mcpBody), `"type":"stdio"`) {
		t.Fatalf("mcp.json = %s, want stdio type normalization", string(mcpBody))
	}
	cliBody, err := os.ReadFile(filepath.Join(cursorDir, "cli.json"))
	if err != nil {
		t.Fatalf("read cli.json: %v", err)
	}
	if !strings.Contains(string(cliBody), `Mcp(api-bridge:*)`) {
		t.Fatalf("cli.json = %s, want MCP allowlist", string(cliBody))
	}
}

func TestBuildCursorPromptResumeSendsOnlyLatestHuman(t *testing.T) {
	messages := []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "first"}}},
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "old answer"}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "latest"}}},
	}
	if got := buildCursorPrompt(messages, true); got != "latest" {
		t.Fatalf("resume prompt = %q, want latest", got)
	}
}

func hardWrapCursorDraftForTest(text string, width int) string {
	words := strings.Fields(text)
	if len(words) == 0 || width <= 0 {
		return text
	}
	lines := make([]string, 0, len(text)/width+1)
	current := words[0]
	for _, word := range words[1:] {
		if len([]rune(current))+1+len([]rune(word)) > width {
			lines = append(lines, current)
			current = word
			continue
		}
		current += " " + word
	}
	lines = append(lines, current)
	return strings.Join(lines, "\n  ")
}

func TestCursorFinalAnswerRecoveryAllowsIncompleteWorkToContinue(t *testing.T) {
	if !strings.Contains(cursorFinalAnswerRecoveryPrompt, "use tools as needed") {
		t.Fatalf("recovery prompt must allow unfinished tool work to continue: %q", cursorFinalAnswerRecoveryPrompt)
	}
	if strings.Contains(cursorFinalAnswerRecoveryPrompt, "Do not use tools") {
		t.Fatalf("recovery prompt still blocks the tools needed to finish the request: %q", cursorFinalAnswerRecoveryPrompt)
	}
	if !strings.Contains(cursorFinalAnswerRecoveryPrompt, "Do not repeat side effects") {
		t.Fatalf("recovery prompt must guard already-completed side effects: %q", cursorFinalAnswerRecoveryPrompt)
	}
}

func cursorArgsContain(args []string, value string) bool {
	for _, arg := range args {
		if arg == value {
			return true
		}
	}
	return false
}

func cursorArgsContainPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

// Auto has no fixed price (it bills the model each request is routed to), so
// its calls are priced at a labelled estimated average rather than left
// without any cost; Composer, with no rate we can verify, stays unpriced.
func TestCursorAutoHasAnEstimatedAveragePrice(t *testing.T) {
	adapter := &CursorCLIAdapter{}
	meta, err := adapter.GetModelMetadata("auto")
	if err != nil || meta == nil {
		t.Fatalf("metadata: %v %v", meta, err)
	}
	if !strings.Contains(meta.ModelName, "estimated") || meta.InputCostPer1MTokens != 1.25 || meta.OutputCostPer1MTokens != 6 || meta.CachedInputCostPer1MTokens != 0.25 {
		t.Fatalf("auto metadata = %+v", meta)
	}
	prompt, completion := 1_000_000, 100_000
	cost := llmtypes.ComputeUSDCostFromMetadata(meta, &llmtypes.GenerationInfo{PromptTokens: &prompt, CompletionTokens: &completion, Additional: map[string]interface{}{"prompt_tokens_include_cache": false}})
	if want := 1.25 + 0.6; cost < want-0.001 || cost > want+0.001 {
		t.Fatalf("1M in + 100K out = $%.4f, want $%.2f", cost, want)
	}
	composer, _ := adapter.GetModelMetadata("composer-2.5")
	if composer == nil || composer.InputCostPer1MTokens != 0 {
		t.Fatalf("composer gained an unverified price: %+v", composer)
	}
}
