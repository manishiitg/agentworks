package picli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const stalePiProjectConfig = `{"mcpServers":{"api-bridge":{"command":"mcpbridge","env":{"MCP_API_URL":"http://127.0.0.1:18743","MCP_API_TOKEN":"stale-token"}}}}`

func TestPreparePiExclusiveMCPConfigRemovesStaleProjectConfig(t *testing.T) {
	workDir := t.TempDir()
	legacyPath := filepath.Join(workDir, ".pi", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatalf("create legacy config dir: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte(stalePiProjectConfig), 0o600); err != nil {
		t.Fatalf("write stale project config: %v", err)
	}
	legacyAgentDir, _ := piSessionRuntimeDirs(workDir, "mlp-pi-test-123")
	if err := os.MkdirAll(legacyAgentDir, 0o700); err != nil {
		t.Fatalf("create legacy agent config dir: %v", err)
	}
	legacyAgentPath := filepath.Join(legacyAgentDir, "mcp-adapter.json")
	if err := os.WriteFile(legacyAgentPath, []byte(stalePiProjectConfig), 0o600); err != nil {
		t.Fatalf("write legacy agent config: %v", err)
	}

	fresh := `{"mcpServers":{"api-bridge":{"command":"mcpbridge","env":{"MCP_API_URL":"http://127.0.0.1:18743","MCP_API_TOKEN":"fresh-token"}}}}`
	opts := &llmtypes.CallOptions{}
	WithMCPConfig(fresh)(opts)

	agentDir, _, cleanup, err := preparePiNativeMCPConfig(workDir, "mlp-pi-test-123", opts, nil)
	if err != nil {
		t.Fatalf("preparePiNativeMCPConfig: %v", err)
	}
	if cleanup == nil {
		t.Fatalf("expected a session config cleanup func")
	}

	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected stale project config %s to be removed, stat err: %v", legacyPath, err)
	}
	if _, err := os.Stat(legacyAgentPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy agent config %s to be removed, stat err: %v", legacyAgentPath, err)
	}
	sessionConfig, err := os.ReadFile(piNativeMCPConfigPath(agentDir))
	if err != nil {
		t.Fatalf("read session config: %v", err)
	}
	if !strings.Contains(string(sessionConfig), "fresh-token") {
		t.Fatalf("expected session config to carry the fresh token, got: %s", sessionConfig)
	}
	if strings.Contains(string(sessionConfig), "stale-token") {
		t.Fatalf("session config must not contain the stale token, got: %s", sessionConfig)
	}
}

func TestPreparePiExclusiveMCPConfigPreservesForeignProjectConfig(t *testing.T) {
	cases := map[string]string{
		"other servers":   `{"mcpServers":{"user-server":{"command":"user-mcp"}}}`,
		"no token marker": `{"mcpServers":{"api-bridge":{"command":"mcpbridge"}}}`,
		"empty token":     `{"mcpServers":{"api-bridge":{"command":"mcpbridge","env":{"MCP_API_TOKEN":""}}}}`,
		"invalid json":    `{"mcpServers": oops`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			workDir := t.TempDir()
			legacyPath := filepath.Join(workDir, ".pi", "mcp.json")
			if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
				t.Fatalf("create legacy config dir: %v", err)
			}
			if err := os.WriteFile(legacyPath, []byte(content), 0o600); err != nil {
				t.Fatalf("write foreign project config: %v", err)
			}

			opts := &llmtypes.CallOptions{}
			WithMCPConfig(stalePiProjectConfig)(opts)
			if _, _, _, err := preparePiNativeMCPConfig(workDir, "mlp-pi-test-123", opts, nil); err != nil {
				t.Fatalf("preparePiNativeMCPConfig: %v", err)
			}

			preserved, err := os.ReadFile(legacyPath)
			if err != nil {
				t.Fatalf("expected foreign project config to be preserved, read err: %v", err)
			}
			if string(preserved) != content {
				t.Fatalf("foreign project config was modified, got: %s", preserved)
			}
		})
	}
}
