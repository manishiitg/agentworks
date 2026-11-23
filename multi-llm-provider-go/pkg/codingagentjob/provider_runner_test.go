package codingagentjob

import (
	"encoding/json"
	"reflect"
	"testing"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestUnattendedProviderOptions(t *testing.T) {
	tests := []struct {
		name     string
		provider llmproviders.Provider
		want     map[string]any
	}{
		{
			name:     "codex",
			provider: llmproviders.ProviderCodexCLI,
			want: map[string]any{
				"codex_approval_policy": "never",
				"codex_sandbox":         "workspace-write",
			},
		},
		{
			name:     "cursor",
			provider: llmproviders.ProviderCursorCLI,
			want: map[string]any{
				"cursor_force":   true,
				"cursor_sandbox": "enabled",
			},
		},
		{
			name:     "claude",
			provider: llmproviders.ProviderClaudeCode,
			want: map[string]any{
				"claude_code_tools": "default",
			},
		},
		{
			name:     "pi",
			provider: llmproviders.ProviderPiCLI,
			want:     map[string]any{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options, err := unattendedProviderOptions(test.provider, "/tmp/trusted-project")
			if err != nil {
				t.Fatalf("unattendedProviderOptions() error = %v", err)
			}
			callOptions := &llmtypes.CallOptions{}
			for _, option := range options {
				option(callOptions)
			}
			custom := map[string]any{}
			if callOptions.Metadata != nil && callOptions.Metadata.Custom != nil {
				custom = callOptions.Metadata.Custom
			}
			for key, want := range test.want {
				if got := custom[key]; !reflect.DeepEqual(got, want) {
					t.Errorf("metadata[%q] = %#v, want %#v", key, got, want)
				}
			}
		})
	}
}

func TestClaudeUnattendedSettingsScopeToolsToWorkspace(t *testing.T) {
	settingsJSON, err := claudeUnattendedSettings("/tmp/trusted project")
	if err != nil {
		t.Fatalf("claudeUnattendedSettings() error = %v", err)
	}
	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
		Sandbox struct {
			Enabled                  bool `json:"enabled"`
			AutoAllowBashIfSandboxed bool `json:"autoAllowBashIfSandboxed"`
			AllowUnsandboxedCommands bool `json:"allowUnsandboxedCommands"`
			FailIfUnavailable        bool `json:"failIfUnavailable"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	wantRules := []string{
		"Bash",
		"Read(//tmp/trusted project/**)",
		"Edit(//tmp/trusted project/**)",
		"Write(//tmp/trusted project/**)",
		"WebFetch",
		"WebSearch",
	}
	if !reflect.DeepEqual(settings.Permissions.Allow, wantRules) {
		t.Fatalf("allow rules = %#v, want %#v", settings.Permissions.Allow, wantRules)
	}
	if !settings.Sandbox.Enabled || !settings.Sandbox.AutoAllowBashIfSandboxed || settings.Sandbox.AllowUnsandboxedCommands || !settings.Sandbox.FailIfUnavailable {
		t.Fatalf("unexpected sandbox settings: %#v", settings.Sandbox)
	}
}

func TestProgressFromStatusLineIncludesTmuxSession(t *testing.T) {
	update := progressFromChunk(llmtypes.StreamChunk{
		Type: llmtypes.StreamChunkTypeStatusLine,
		StatusLine: &llmtypes.StatusLine{
			Model: "composer-2.5",
			Metadata: map[string]interface{}{
				"tmux_session": "cursor-session-123",
			},
		},
	})
	if update.Message != "Coding agent is running with composer-2.5" {
		t.Fatalf("message = %q", update.Message)
	}
	if update.TmuxSession != "cursor-session-123" {
		t.Fatalf("tmux session = %q", update.TmuxSession)
	}
}

func TestProgressFromTerminalIncludesTmuxSession(t *testing.T) {
	update := progressFromChunk(llmtypes.StreamChunk{
		Type:    llmtypes.StreamChunkTypeTerminal,
		Content: "Running tests",
		Metadata: map[string]interface{}{
			"tmux_session": "cursor-session-456",
		},
	})
	if update.Message != "Running tests" {
		t.Fatalf("message = %q", update.Message)
	}
	if update.TmuxSession != "cursor-session-456" {
		t.Fatalf("tmux session = %q", update.TmuxSession)
	}
}
