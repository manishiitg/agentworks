package agycli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingready"
)

// TestAgyExecDoesNotBlockOnMissingMCPReadyFileBeforeLaunch guards against a
// regression where agycli waited on MCPReadyFile before spawning the agy
// process (which is what spawns mcpbridge to create the file), causing every
// cold turn with MCP to stall for the full MCPReadyWait duration (30s).
func TestAgyExecDoesNotBlockOnMissingMCPReadyFileBeforeLaunch(t *testing.T) {
	binDir := t.TempDir()
	stub := filepath.Join(binDir, "agy")
	script := "#!/bin/sh\n" +
		"echo '{\"event\":\"result\",\"result\":{\"status\":\"SUCCESS\",\"response\":\"ok\",\"conversation_id\":\"c1\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}'\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(codingready.EnvMCPReadyWaitSeconds, "5")

	missingReady := filepath.Join(t.TempDir(), "never-created.marker")
	opts := &llmtypes.CallOptions{
		Model: DefaultModelID,
		Metadata: &llmtypes.Metadata{
			Custom: map[string]interface{}{
				MetadataKeyWorkingDir:               t.TempDir(),
				MetadataKeyMCPConfig:                `{"mcpServers":{"bridge":{"command":"true"}}}`,
				codingready.MetadataKeyMCPReadyFile: missingReady,
			},
		},
	}

	adapter := NewAgyCLIAdapter("", DefaultModelID, nil)
	start := time.Now()
	resp, err := adapter.generateContentExec(
		context.Background(),
		[]llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "hi")},
		opts,
	)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("generateContentExec failed: %v", err)
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Content != "ok" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("generateContentExec stalled %v waiting on pre-launch MCPReadyFile", elapsed)
	}
}
