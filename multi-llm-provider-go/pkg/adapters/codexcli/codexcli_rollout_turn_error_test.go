package codexcli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Captured from a real rollout: Codex 0.147 with a ChatGPT account asked to
// run a model the account cannot use. The turn "completes" with no agent
// message and the API error on task_complete.
const codexFailedTurnRollout = `{"timestamp":"2026-09-06T06:17:13.100Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1788675433,"model_context_window":258400}}
{"timestamp":"2026-09-06T06:17:13.200Z","type":"response_item","payload":{"type":"message","id":"m1","role":"user","content":[{"type":"input_text","text":"hi"}]}}
{"timestamp":"2026-09-06T06:17:20.500Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"error":{"message":"{\"type\":\"error\",\"status\":400,\"error\":{\"type\":\"invalid_request_error\",\"message\":\"The 'gpt-5.4' model is not supported when using Codex with a ChatGPT account.\"}}"}}}
`

const codexSucceededTurnRollout = `{"timestamp":"2026-09-06T06:17:13.100Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}
{"timestamp":"2026-09-06T06:17:20.500Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":"Hello there!"}}
`

func writeRolloutFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout-2026-09-06T11-47-13-thread.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadCodexRolloutTurnErrorUnwrapsAPIError(t *testing.T) {
	path := writeRolloutFixture(t, codexFailedTurnRollout)
	turnStart := time.Date(2026, 9, 6, 6, 17, 0, 0, time.UTC)
	got := readCodexRolloutTurnError(path, turnStart)
	want := "The 'gpt-5.4' model is not supported when using Codex with a ChatGPT account."
	if got != want {
		t.Fatalf("turn error = %q, want %q", got, want)
	}
	if final, _ := readCodexRolloutFinalAssistantText(path, turnStart); final != "" {
		t.Fatalf("a failed turn must have no final answer, got %q", final)
	}
}

func TestReadCodexRolloutTurnErrorIsEmptyForNormalCompletionAndPriorTurns(t *testing.T) {
	path := writeRolloutFixture(t, codexSucceededTurnRollout)
	if got := readCodexRolloutTurnError(path, time.Date(2026, 9, 6, 6, 17, 0, 0, time.UTC)); got != "" {
		t.Fatalf("a normal completion must not report an error, got %q", got)
	}
	// The failure belongs to an earlier turn than the one being read.
	failed := writeRolloutFixture(t, codexFailedTurnRollout)
	if got := readCodexRolloutTurnError(failed, time.Date(2026, 9, 6, 6, 30, 0, 0, time.UTC)); got != "" {
		t.Fatalf("an error before turnStart must be ignored, got %q", got)
	}
	if got := readCodexRolloutTurnError("", time.Time{}); got != "" {
		t.Fatalf("no rollout must read as no error, got %q", got)
	}
}

func TestCodexTurnErrorMessageShapes(t *testing.T) {
	cases := map[string]string{
		`{"message":"plain failure"}`:                     "plain failure",
		`"bare string"`:                                   "bare string",
		`{"message":"{\"error\":{\"message\":\"nested\"}}"}`: "nested",
		`{"message":"{\"message\":\"top-level\"}"}`:        "top-level",
		`null`: "",
		``:     "",
	}
	for raw, want := range cases {
		if got := codexTurnErrorMessage([]byte(raw)); got != want {
			t.Fatalf("codexTurnErrorMessage(%s) = %q, want %q", raw, got, want)
		}
	}
}

// Captured from a real rollout on 2026-10-05 (Codex 0.160, gpt-6.1-sol): the model was at capacity.
// The machine-readable code is error.codex_error_info; no terminal text is involved.
const codexCapacityTurnRollout = `{"timestamp":"2026-10-05T05:01:21.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}
{"timestamp":"2026-10-05T05:01:41.077Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"error":{"message":"Selected model is at capacity. Please try a different model.","codex_error_info":"server_overloaded"},"duration_ms":19445}}
`

func TestModelAtCapacityIsReadFromTheRolloutAsACapacityError(t *testing.T) {
	path := writeRolloutFixture(t, codexCapacityTurnRollout)
	message, info := readCodexRolloutTurnFailure(path, time.Date(2026, 10, 5, 5, 0, 0, 0, time.UTC))
	err := &CodexTurnError{Message: message, Info: info}
	if message != "Selected model is at capacity. Please try a different model." || !err.Capacity() {
		t.Fatalf("message=%q info=%q capacity=%v", message, info, err.Capacity())
	}
	if got := err.Error(); got != "codex-cli model at capacity: Selected model is at capacity. Please try a different model. (try again shortly or pick a different model)" {
		t.Fatalf("error text = %q", got)
	}
	if other := (&CodexTurnError{Message: "x"}).Error(); other != "codex-cli turn failed: x" {
		t.Fatalf("non-capacity text changed: %q", other)
	}
}
