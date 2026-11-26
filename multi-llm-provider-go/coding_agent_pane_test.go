package llmproviders

import "testing"

func TestCodingAgentPaneReadyUsesProviderContract(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		pane     string
		want     bool
	}{
		{name: "claude idle", provider: ProviderClaudeCode, pane: "answer\n\n❯\n", want: true},
		{name: "claude running", provider: ProviderClaudeCode, pane: "Working… esc to interrupt\n❯\n", want: false},
		{name: "codex idle", provider: ProviderCodexCLI, pane: "Completed\n›\n", want: true},
		{name: "codex running", provider: ProviderCodexCLI, pane: "• Working (1s • esc to interrupt)\n›\n", want: false},
		{name: "unsupported", provider: ProviderOpenAI, pane: "›\n", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CodingAgentPaneReady(tt.provider, tt.pane); got != tt.want {
				t.Fatalf("CodingAgentPaneReady(%q, %q) = %v, want %v", tt.provider, tt.pane, got, tt.want)
			}
		})
	}
}
