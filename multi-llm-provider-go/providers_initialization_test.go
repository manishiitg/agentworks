package llmproviders

import (
	"testing"

	claudecodeadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

func TestInitializeClaudeCodeDefaultUsesSDKTransport(t *testing.T) {
	t.Setenv(EnvClaudeCodeTransport, "")

	llm, err := InitializeLLM(Config{
		Provider: ProviderClaudeCode,
		ModelID:  "claude-code",
	})
	if err != nil {
		t.Fatalf("InitializeLLM() error = %v", err)
	}

	wrapped, ok := llm.(*ProviderAwareLLM)
	if !ok {
		t.Fatalf("InitializeLLM() returned %T, want *ProviderAwareLLM", llm)
	}
	if _, ok := wrapped.Model.(*claudecodeadapter.ClaudeCodeInteractiveAdapter); !ok {
		t.Fatalf("Claude Code default transport = %T, want *ClaudeCodeInteractiveAdapter", wrapped.Model)
	}
}

func TestInitializeClaudeCodeRejectsPrintTransport(t *testing.T) {
	t.Setenv(EnvClaudeCodeTransport, "print")

	_, err := InitializeLLM(Config{
		Provider: ProviderClaudeCode,
		ModelID:  "claude-haiku-4-5-20251001",
	})
	if err == nil {
		t.Fatal("InitializeLLM() error = nil, want print transport rejection")
	}
}

func TestInitializeClaudeCodeConfigTransportOverridesEnv(t *testing.T) {
	t.Setenv(EnvClaudeCodeTransport, "print")

	llm, err := InitializeLLM(Config{
		Provider:            ProviderClaudeCode,
		ModelID:             "claude-code",
		ClaudeCodeTransport: ClaudeCodeTransportTmux,
	})
	if err != nil {
		t.Fatalf("InitializeLLM() error = %v", err)
	}

	wrapped, ok := llm.(*ProviderAwareLLM)
	if !ok {
		t.Fatalf("InitializeLLM() returned %T, want *ProviderAwareLLM", llm)
	}
	if _, ok := wrapped.Model.(*claudecodeadapter.ClaudeCodeInteractiveAdapter); !ok {
		t.Fatalf("Claude Code config transport override = %T, want *ClaudeCodeInteractiveAdapter", wrapped.Model)
	}
}

func TestNormalizeClaudeCodeTransportAliases(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "default", raw: "", want: ClaudeCodeTransportSDK},
		{name: "removed experimental alias", raw: "experimental", wantErr: true},
		{name: "tmux", raw: "tmux", want: ClaudeCodeTransportTmux},
		{name: "sdk", raw: "sdk", want: ClaudeCodeTransportSDK},
		{name: "print", raw: "print", wantErr: true},
		{name: "invalid", raw: "json", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeClaudeCodeTransport(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeClaudeCodeTransport(%q) error = nil, want error", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeClaudeCodeTransport(%q) error = %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeClaudeCodeTransport(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestGetDefaultModelCodexCLIUsesGPT55(t *testing.T) {
	t.Setenv("CODEX_CLI_PRIMARY_MODEL", "")

	if got := GetDefaultModel(ProviderCodexCLI); got != DefaultCodexCLIModel {
		t.Fatalf("GetDefaultModel(ProviderCodexCLI) = %q, want %q", got, DefaultCodexCLIModel)
	}
}

func TestGetDefaultModelCursorCLIUsesSentinel(t *testing.T) {
	t.Setenv("CURSOR_CLI_PRIMARY_MODEL", "")

	if got := GetDefaultModel(ProviderCursorCLI); got != DefaultCursorCLIModel {
		t.Fatalf("GetDefaultModel(ProviderCursorCLI) = %q, want %q", got, DefaultCursorCLIModel)
	}
}

// PLAT-872-55: selecting a terminal is explicit; no default may start tmux.
func TestNativeTransportDefaultsAndExplicitTerminals(t *testing.T) {
	for _, tc := range []struct {
		provider Provider
		env      string
		native   CodingAgentTransport
	}{
		{ProviderClaudeCode, EnvClaudeCodeTransport, CodingAgentTransportSDK},
		{ProviderCursorCLI, EnvCursorCLITransport, CodingAgentTransportSDK},
		{ProviderCodexCLI, EnvCodexCLITransport, CodingAgentTransportAppServer},
	} {
		t.Run(string(tc.provider), func(t *testing.T) {
			t.Setenv(tc.env, "")
			c, _ := GetCodingAgentProviderContract(tc.provider, "")
			if c.Transport != tc.native || c.SupportsTerminalStream {
				t.Fatalf("default contract: %+v", c)
			}
			t.Setenv(tc.env, "tmux")
			c, _ = GetCodingAgentProviderContract(tc.provider, "")
			if c.Transport != CodingAgentTransportTmux || !c.SupportsTerminalStream {
				t.Fatalf("terminal contract: %+v", c)
			}
			t.Setenv(tc.env, "invalid")
			if _, err := InitializeLLM(Config{Provider: tc.provider}); err == nil {
				t.Fatal("invalid selection must not silently start a different transport")
			}
		})
	}
}
