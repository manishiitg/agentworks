// Package musecli is the Muse (Muse Code, `muse` CLI) adapter.
//
// Recon: multi-llm-provider-go/docs/MUSE_CLI_CODING_AGENT_CONTRACT.md.
// GenerateContent runs the interactive tmux lane by default, like every
// other coding provider; WithMuseStructuredTransport selects the exec
// --json lane (per-turn process, no live pane).
package musecli

import (
	"context"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// MuseCLIAdapter implements llmtypes.Model for the Muse CLI.
type MuseCLIAdapter struct {
	apiKey  string
	modelID string
	logger  interfaces.Logger
}

// NewMuseCLIAdapter creates a new MuseCLIAdapter. apiKey is META_API_KEY
// (may be empty; the CLI falls back to its stored `muse login`).
func NewMuseCLIAdapter(apiKey string, modelID string, logger interfaces.Logger) *MuseCLIAdapter {
	return &MuseCLIAdapter{apiKey: apiKey, modelID: modelID, logger: logger}
}

// GetModelID returns the configured model id.
func (a *MuseCLIAdapter) GetModelID() string { return a.modelID }

// GetModelMetadata returns metadata for a Muse model id.
func (a *MuseCLIAdapter) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	return GetMuseModelMetadata(modelID)
}

// GenerateContent runs one turn through the interactive tmux lane by
// default (bounded TUI per turn, or a persistent pooled session when the
// persistent option is set), or the exec --json lane when structured is
// explicitly requested via WithMuseStructuredTransport. WithTmuxTransport
// is retained as an explicit tmux pin for direct callers.
func (a *MuseCLIAdapter) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	opts := &llmtypes.CallOptions{}
	for _, opt := range options {
		opt(opts)
	}
	// Every Muse lane (tmux, persistent, exec) starts under the Landlock
	// launcher when the policy confines the CLI; any other strict policy is
	// refused rather than run unconfined.
	if err := llmtypes.ValidateCLISecurityLaunch(opts, llmtypes.ConfinedModes(opts)...); err != nil {
		return nil, err
	}

	if museStructuredTransportRequested(opts) {
		return a.generateContentExec(ctx, messages, options...)
	}
	// `muse serve` is the default; tmux stays for a server that selects it, or a
	// caller that pins it.
	if MuseMSPSelected() && !museTmuxTransportRequested(opts) {
		return a.generateContentMSP(ctx, messages, opts)
	}
	return a.generateContentTmux(ctx, messages, opts)
}
