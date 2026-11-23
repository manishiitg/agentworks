package tmuxstartup

import (
	"context"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Publish exposes a newly-created tmux pane before the CLI finishes booting.
// Hosts use this status event to register the terminal immediately; later
// terminal snapshots and usage status lines update the same pane.
func Publish(
	ctx context.Context,
	streamChan chan<- llmtypes.StreamChunk,
	provider, model, tmuxSession, workingDir string,
	extra map[string]interface{},
) bool {
	tmuxSession = strings.TrimSpace(tmuxSession)
	if streamChan == nil || tmuxSession == "" {
		return false
	}

	metadata := make(map[string]interface{}, len(extra)+3)
	for key, value := range extra {
		metadata[key] = value
	}
	metadata["tmux_session"] = tmuxSession
	metadata["step_transport"] = "tmux"
	if workingDir = strings.TrimSpace(workingDir); workingDir != "" {
		metadata["working_dir"] = workingDir
	}

	chunk := llmtypes.StreamChunk{
		Type: llmtypes.StreamChunkTypeStatusLine,
		StatusLine: &llmtypes.StatusLine{
			Provider: strings.TrimSpace(provider),
			Model:    strings.TrimSpace(model),
			Metadata: metadata,
		},
		Metadata: metadata,
	}
	select {
	case streamChan <- chunk:
		return true
	case <-ctx.Done():
		return false
	default:
		return false
	}
}
