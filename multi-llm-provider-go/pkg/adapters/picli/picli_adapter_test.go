package picli

import (
	"encoding/json"

	"os"

	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type mockLogger struct{}

func (m *mockLogger) Debugf(format string, args ...interface{}) {}
func (m *mockLogger) Infof(format string, args ...interface{})  {}
func (m *mockLogger) Warnf(format string, args ...interface{})  {}
func (m *mockLogger) Errorf(format string, args ...interface{}) {}

func TestResolvePiProviderModel(t *testing.T) {
	tests := []struct {
		name             string
		modelID          string
		providerOverride string
		wantProvider     string
		wantModel        string
	}{
		{name: "default", wantProvider: "google", wantModel: "gemini-3.8-flash"},
		{name: "provider model", modelID: "google/gemini-3.5-flash", wantProvider: "google", wantModel: "gemini-3.5-flash"},
		{name: "openrouter nested model", modelID: "openrouter/minimax/minimax-m3-20260531", wantProvider: "openrouter", wantModel: "minimax/minimax-m3-20260531"},
		{name: "override", modelID: "gemini-3.5-flash", providerOverride: "google-vertex", wantProvider: "google-vertex", wantModel: "gemini-3.5-flash"},
		{name: "pi cli alias", modelID: "pi-cli", wantProvider: "google", wantModel: "gemini-3.8-flash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotProvider, gotModel := resolvePiProviderModel(tt.modelID, tt.providerOverride)
			if gotProvider != tt.wantProvider || gotModel != tt.wantModel {
				t.Fatalf("resolvePiProviderModel() = %q/%q, want %q/%q", gotProvider, gotModel, tt.wantProvider, tt.wantModel)
			}
		})
	}
}

func TestPiAPIKeyEnvUsesSelectedProvider(t *testing.T) {
	tests := []struct {
		provider string
		want     []string
	}{
		{provider: "google", want: []string{"GEMINI_API_KEY=test-key", "GOOGLE_API_KEY=test-key", "PI_API_KEY=test-key"}},
		{provider: "openrouter", want: []string{"OPENROUTER_API_KEY=test-key"}},
		{provider: "zai", want: []string{"ZAI_API_KEY=test-key"}},
		{provider: "zai-coding-cn", want: []string{"ZAI_CODING_CN_API_KEY=test-key"}},
		{provider: "kimi-coding", want: []string{"KIMI_API_KEY=test-key"}},
		{provider: "moonshotai", want: []string{"MOONSHOT_API_KEY=test-key"}},
		{provider: "minimax-cn", want: []string{"MINIMAX_CN_API_KEY=test-key"}},
		{provider: "deepseek", want: []string{"DEEPSEEK_API_KEY=test-key"}},
		{provider: "opencode-go", want: []string{"OPENCODE_API_KEY=test-key"}},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := piAPIKeyEnv(tt.provider, "test-key")
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Fatalf("piAPIKeyEnv(%q) = %#v, want %#v", tt.provider, got, tt.want)
			}
		})
	}
}

func TestPiNodeOptionsEnv(t *testing.T) {
	t.Run("default old space guard", func(t *testing.T) {
		t.Setenv(EnvPiNodeOptions, "")
		t.Setenv(EnvPiNodeMaxOldSpaceMB, "")
		t.Setenv("NODE_OPTIONS", "")
		got := strings.Join(piNodeOptionsEnv(), "\n")
		if got != "NODE_OPTIONS=--max-old-space-size=4096" {
			t.Fatalf("piNodeOptionsEnv() = %q, want default max old space", got)
		}
	})

	t.Run("preserves existing options", func(t *testing.T) {
		t.Setenv(EnvPiNodeOptions, "")
		t.Setenv(EnvPiNodeMaxOldSpaceMB, "2048")
		t.Setenv("NODE_OPTIONS", "--trace-warnings")
		got := strings.Join(piNodeOptionsEnv(), "\n")
		if got != "NODE_OPTIONS=--trace-warnings --max-old-space-size=2048" {
			t.Fatalf("piNodeOptionsEnv() = %q, want existing options plus old space", got)
		}
	})

	t.Run("does not override explicit old space", func(t *testing.T) {
		t.Setenv(EnvPiNodeOptions, "")
		t.Setenv(EnvPiNodeMaxOldSpaceMB, "")
		t.Setenv("NODE_OPTIONS", "--max-old-space-size=8192")
		if got := piNodeOptionsEnv(); len(got) != 0 {
			t.Fatalf("piNodeOptionsEnv() = %#v, want inherited explicit old space", got)
		}
	})

	t.Run("adapter override wins", func(t *testing.T) {
		t.Setenv(EnvPiNodeOptions, "--max-old-space-size=3072 --trace-gc")
		t.Setenv(EnvPiNodeMaxOldSpaceMB, "")
		t.Setenv("NODE_OPTIONS", "--trace-warnings")
		got := strings.Join(piNodeOptionsEnv(), "\n")
		if got != "NODE_OPTIONS=--max-old-space-size=3072 --trace-gc" {
			t.Fatalf("piNodeOptionsEnv() = %q, want adapter override", got)
		}
	})
}

func TestPreparePiExclusiveMCPConfigWritesOnlySessionConfigAndCleansUp(t *testing.T) {
	workDir := t.TempDir()
	ambientHome := t.TempDir()
	t.Setenv("HOME", ambientHome)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	globalMCPPath := filepath.Join(ambientHome, ".config", "mcp", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(globalMCPPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalMCPPath, []byte(`{"mcpServers":{"linear":{"command":"must-not-load"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	projectMCPPath := filepath.Join(workDir, ".pi", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(projectMCPPath), 0o755); err != nil {
		t.Fatal(err)
	}
	projectMCP := []byte(`{"mcpServers":{"project-global":{"command":"must-not-load"}}}`)
	if err := os.WriteFile(projectMCPPath, projectMCP, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := &llmtypes.CallOptions{}
	WithMCPConfig(`{"mcpServers":{"api-bridge":{"command":"node","args":["server.js"]}}}`)(opts)

	agentDir, sessionDir, cleanup, err := preparePiNativeMCPConfig(workDir, "mlp-pi-session-a", opts, nil)
	if err != nil {
		t.Fatalf("preparePiNativeMCPConfig() error = %v", err)
	}
	if cleanup == nil {
		t.Fatal("preparePiNativeMCPConfig() cleanup = nil, want cleanup")
	}
	if want := filepath.Join(agentDir, "sessions"); sessionDir != want {
		t.Fatalf("sessionDir = %q, want %q", sessionDir, want)
	}

	mcpPath := piNativeMCPConfigPath(agentDir)
	body, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("read Pi MCP config: %v", err)
	}
	var config struct {
		MCPServers map[string]struct {
			Command  string `json:"command"`
			Exposure string `json:"exposure"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatalf("Pi MCP config invalid JSON: %v\n%s", err, body)
	}
	bridge := config.MCPServers["api-bridge"]
	if bridge.Command != "node" || bridge.Exposure != "direct" {
		t.Fatalf("api-bridge config = %#v, want node with direct exposure", bridge)
	}
	if len(config.MCPServers) != 1 {
		t.Fatalf("exclusive config inherited ambient MCP servers: %#v", config.MCPServers)
	}

	cleanup()
	if _, err := os.Stat(mcpPath); !os.IsNotExist(err) {
		t.Fatalf("exclusive mcp.json should be removed after cleanup, err=%v", err)
	}
	if got, err := os.ReadFile(projectMCPPath); err != nil || string(got) != string(projectMCP) {
		t.Fatalf("project MCP config was mutated: got=%q err=%v", got, err)
	}
}

func TestPreparePiExclusiveMCPConfigReplacesStaleSessionConfig(t *testing.T) {
	workDir := t.TempDir()
	agentDir, _ := piSessionRuntimeDirs(workDir, "mlp-pi-session-b")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mcpPath := piNativeMCPConfigPath(agentDir)
	original := []byte(`{"mcpServers":{"existing":{"command":"old"}}}` + "\n")
	if err := os.WriteFile(mcpPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := &llmtypes.CallOptions{}
	WithMCPConfig(`{"mcpServers":{"api-bridge":{"command":"new"}}}`)(opts)

	_, _, cleanup, err := preparePiNativeMCPConfig(workDir, "mlp-pi-session-b", opts, nil)
	if err != nil {
		t.Fatalf("preparePiNativeMCPConfig() error = %v", err)
	}
	active, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("read active mcp.json: %v", err)
	}
	if !strings.Contains(string(active), `"api-bridge"`) {
		t.Fatalf("active mcp.json = %s, want session bridge config", active)
	}

	cleanup()
	if _, err := os.Stat(mcpPath); !os.IsNotExist(err) {
		t.Fatalf("stale session mcp.json must not be restored, err=%v", err)
	}
}
