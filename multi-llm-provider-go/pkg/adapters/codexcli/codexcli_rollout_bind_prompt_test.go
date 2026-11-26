package codexcli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeBindPromptRollout(t *testing.T, dir, name, workingDir, prompt string, mod time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	body := fmt.Sprintf("%s\n%s\n",
		fmt.Sprintf(`{"type":"session_meta","payload":{"cwd":%q}}`, workingDir),
		fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}}`, now, prompt))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	return path
}

// Two sessions started together in one directory: the newest rollout belongs
// to the OTHER session. Recency (and exclusion, before either has claimed)
// picks it. The bind prompt must pick this session's own rollout.
func TestFindCodexRolloutForSessionScanUsesBindPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	workingDir := filepath.Join(t.TempDir(), "shared-workspace")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dayDir := filepath.Join(home, ".codex", "sessions", "2026", "09", "23")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	turnStart := time.Now().UTC().Add(-time.Second)
	alphaPrompt := "Call the api-bridge write_contract MCP tool with token TOKEN_ALPHA_1. Then reply exactly with the tool result text."
	betaPrompt := "Call the api-bridge write_contract MCP tool with token TOKEN_BETA_2. Then reply exactly with the tool result text."
	alpha := writeBindPromptRollout(t, dayDir, "rollout-a-11111111-1111-4111-8111-111111111111.jsonl", workingDir, alphaPrompt, time.Now().Add(-2*time.Second))
	beta := writeBindPromptRollout(t, dayDir, "rollout-b-22222222-2222-4222-8222-222222222222.jsonl", workingDir, betaPrompt, time.Now())

	if got := findCodexRolloutByWorkingDirExcluding(turnStart, workingDir, nil); got != beta {
		t.Fatalf("legacy scan = %q; fixture expects it to pick the newest (other session's) rollout %q", got, beta)
	}
	if got := findCodexRolloutForSessionScan(turnStart, workingDir, nil, alphaPrompt, time.Time{}); got != alpha {
		t.Fatalf("alpha bound to %q, want its own rollout %q", got, alpha)
	}
	if got := findCodexRolloutForSessionScan(turnStart, workingDir, nil, betaPrompt, time.Time{}); got != beta {
		t.Fatalf("beta bound to %q, want %q", got, beta)
	}
	if got := findCodexRolloutForSessionScan(turnStart, workingDir, nil, "a prompt no rollout has recorded yet", time.Time{}); got != "" {
		t.Fatalf("unrecorded prompt bound to %q, want no binding yet", got)
	}

	// Two long prompts sharing a >200-byte opening (same workflow system text):
	// the strict prefix+suffix rule still tells them apart.
	opening := strings.Repeat("Shared workflow instructions for every run. ", 8)
	longAlpha := opening + "Now handle request ALPHA and reply ALPHA_DONE."
	longBeta := opening + "Now handle request BETA and reply BETA_DONE."
	la := writeBindPromptRollout(t, dayDir, "rollout-c-33333333-3333-4333-8333-333333333333.jsonl", workingDir, longAlpha, time.Now().Add(time.Second))
	lb := writeBindPromptRollout(t, dayDir, "rollout-d-44444444-4444-4444-8444-444444444444.jsonl", workingDir, longBeta, time.Now().Add(2*time.Second))
	if got := findCodexRolloutForSessionScan(turnStart, workingDir, nil, longAlpha, time.Time{}); got != la {
		t.Fatalf("long alpha bound to %q, want %q", got, la)
	}
	if got := findCodexRolloutForSessionScan(turnStart, workingDir, nil, longBeta, time.Time{}); got != lb {
		t.Fatalf("long beta bound to %q, want %q", got, lb)
	}
}

// A fresh session sends its first prompt as a launch argument, so Codex can
// record it well before the turn's promptSentAt (TestCodexCLIRealInteractive
// WorkspaceTrustPromptContract, codex 0.156.1). bindSince, taken before launch,
// must still find it; without it the row is outside the scan window.
func TestFindCodexRolloutForSessionScanUsesBindSince(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	workingDir := filepath.Join(t.TempDir(), "fresh-workspace")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dayDir := filepath.Join(home, ".codex", "sessions", "2026", "09", "23")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prompt := "Reply exactly: trusted TRUST_REAL_c26d1a20"
	launchedAt := time.Now().UTC().Add(-20 * time.Second)
	rowAt := launchedAt.Add(time.Second).Format(time.RFC3339Nano)
	path := filepath.Join(dayDir, "rollout-2026-09-23T18-44-35-55555555-5555-4555-8555-555555555555.jsonl")
	body := fmt.Sprintf("%s\n%s\n",
		fmt.Sprintf(`{"type":"session_meta","payload":{"cwd":%q}}`, workingDir),
		fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}}`, rowAt, prompt))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	turnStart := time.Now().UTC() // promptSentAt: long after the launch-time row

	if got := findCodexRolloutForSessionScan(turnStart, workingDir, nil, prompt, time.Time{}); got != "" {
		t.Fatalf("without bindSince the launch-time row should be outside the window, got %q", got)
	}
	if got := findCodexRolloutForSessionScan(turnStart, workingDir, nil, prompt, launchedAt); got != path {
		t.Fatalf("with bindSince = %q, want %q", got, path)
	}
}

