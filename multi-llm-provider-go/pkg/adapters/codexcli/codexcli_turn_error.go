package codexcli

import "strings"

// codexErrorInfoServerOverloaded is the structured code Codex records on a failed turn
// (task_complete.error.codex_error_info) when the selected model is at capacity.
const codexErrorInfoServerOverloaded = "server_overloaded"

// CodexTurnError is a turn Codex itself failed (its API refused or could not serve the
// request). It is read from Codex's structured records only: the rollout's task_complete
// error object (message and codex_error_info) and the `turn.failed` event on `exec --json`
// stdout. Terminal text is never matched.
type CodexTurnError struct {
	Message string // the provider's message, unwrapped
	Info    string // codex_error_info when the rollout carries it ("" otherwise)
}

func (e *CodexTurnError) Error() string {
	if e.Capacity() {
		return "codex-cli model at capacity: " + e.Message + " (try again shortly or pick a different model)"
	}
	return "codex-cli turn failed: " + e.Message
}

// Capacity reports whether Codex classified the failure as the model being at capacity.
func (e *CodexTurnError) Capacity() bool {
	return strings.TrimSpace(e.Info) == codexErrorInfoServerOverloaded
}
