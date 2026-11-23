package codexcli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Replay the cold-start race: task_started exists, but the user row used to
// bind THIS session's transcript is written after the pane looks idle.
func TestCodexColdCompletionWaitsForNativePromptAndFinal(t *testing.T) {
	for _, marker := range []string{"", "STATUS: COMPLETED"} {
		t.Run("pane_"+marker, func(t *testing.T) {
			workingDir, accountRoot := t.TempDir(), t.TempDir()
			day := filepath.Join(accountRoot, "sessions", "2026", "10", "24")
			if err := os.MkdirAll(day, 0o700); err != nil {
				t.Fatal(err)
			}
			rollout := filepath.Join(day, "rollout-cold-thread.jsonl")
			turnStart := time.Now().UTC()
			initial := fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":\"cold-thread\",\"cwd\":%q}}\n{\"timestamp\":%q,\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\",\"turn_id\":\"cold-turn\"}}\n", workingDir, turnStart.Format(time.RFC3339Nano))
			if err := os.WriteFile(rollout, []byte(initial), 0o600); err != nil {
				t.Fatal(err)
			}
			fakeBin := t.TempDir()
			script := "#!/bin/sh\nprintf '%s\\n' '/tmp/runtime' '" + marker + "' '›' 'gpt-6-luna low · /tmp/runtime'\n"
			if err := os.WriteFile(filepath.Join(fakeBin, "tmux"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv(EnvCodexInteractiveStalePaneBackstopSeconds, "1")
			const prompt = "Show the sample request chart."
			session := &codexInteractiveSession{workingDir: workingDir, accountRoot: accountRoot, bindPrompt: prompt, bindSince: turnStart}
			resolver := codexRolloutResolverForSession(session)
			if got := resolver(turnStart); got != "" {
				t.Fatalf("rollout bound before its user row: %q", got)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			type result struct {
				source string
				err    error
			}
			done := make(chan result, 1)
			go func() {
				source := ""
				_, err := waitForCodexInteractiveResponse(ctx, "cold-pane", "Starting Codex", nil, turnStart, workingDir, false, false, resolver, &codexCompletionDiagnosticHooks{completedBy: func(s string) { source = s }})
				done <- result{source, err}
			}()
			select {
			case early := <-done:
				t.Fatalf("returned before native prompt/final: source=%q err=%v", early.source, early.err)
			case <-time.After(2500 * time.Millisecond):
			}
			stamp := time.Now().UTC().Format(time.RFC3339Nano)
			final := fmt.Sprintf("{\"timestamp\":%q,\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":%q}]}}\n{\"timestamp\":%q,\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final_answer\",\"content\":[{\"type\":\"output_text\",\"text\":\"The sample total is 45.\"}]}}\n{\"timestamp\":%q,\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\",\"turn_id\":\"cold-turn\",\"last_agent_message\":\"The sample total is 45.\"}}\n", stamp, prompt, stamp, stamp)
			f, err := os.OpenFile(rollout, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteString(final)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			got := <-done
			if got.err != nil || got.source != codexCompletionSourceRollout {
				t.Fatalf("completion source=%q err=%v, want native completion", got.source, got.err)
			}
			if answer, _ := readCodexRolloutFinalAssistantText(rollout, turnStart); answer != "The sample total is 45." {
				t.Fatalf("native answer = %q", answer)
			}
		})
	}
}

func TestCodexMissingNativeTurnEndsWithReplyError(t *testing.T) {
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "tmux"), []byte("#!/bin/sh\nprintf '%s\\n' '/tmp/runtime' '›'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(EnvCodexInteractivePromptMaxWaitSeconds, "1")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	source := ""
	_, err := waitForCodexInteractiveResponse(ctx, "missing-native", "Starting Codex", nil, time.Now().UTC(), t.TempDir(), false, false, func(time.Time) string { return "" }, &codexCompletionDiagnosticHooks{completedBy: func(s string) { source = s }})
	if err == nil || !strings.Contains(err.Error(), "Codex did not return a reply") || source != "native_turn_timeout" {
		t.Fatalf("source=%q err=%v, want bounded missing-native error", source, err)
	}
}
