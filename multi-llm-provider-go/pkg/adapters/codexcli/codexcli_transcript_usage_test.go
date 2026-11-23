package codexcli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestReadCodexTranscriptUsageTakesLatestEventInTurn proves the
// extractor picks the freshest rollout file in ~/.codex/sessions and
// returns the LAST token_count event whose timestamp is at-or-after
// turnStart. Prior-turn token_count events are ignored.
func TestReadCodexTranscriptUsageTakesLatestEventInTurn(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dayDir := filepath.Join(tmpHome, ".codex", "sessions", "2026", "05", "19")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	rollout := filepath.Join(dayDir, "rollout-2026-05-19T12-00-00-aaaabbbb-cccc-dddd-eeee-ffff00001111.jsonl")

	turnStart := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	beforeTurn := turnStart.Add(-1 * time.Hour).Format(time.RFC3339Nano)
	earlyTurn := turnStart.Add(2 * time.Second).Format(time.RFC3339Nano)
	lateTurn := turnStart.Add(4 * time.Second).Format(time.RFC3339Nano)

	lines := []string{
		// session_meta before the turn still carries the session id we need for resume.
		`{"type":"session_meta","timestamp":"` + beforeTurn + `","payload":{"id":"aaaabbbb-cccc-dddd-eeee-ffff00001111","cwd":"/x"}}`,
		// prior-turn token snapshot — must be ignored
		`{"type":"event_msg","timestamp":"` + beforeTurn + `","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":999,"cached_input_tokens":999,"output_tokens":999,"reasoning_output_tokens":999,"total_tokens":1}}}}`,
		// turn_context: in-turn, captures model name
		`{"type":"turn_context","timestamp":"` + earlyTurn + `","payload":{"model":"gpt-5.4","reasoning_effort":"high"}}`,
		// turn-iteration #1 (early, NOT what we want)
		`{"type":"event_msg","timestamp":"` + earlyTurn + `","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":300,"cached_input_tokens":50,"output_tokens":40,"reasoning_output_tokens":10,"total_tokens":340}}}}`,
		// non-token noise
		`{"type":"response_item","timestamp":"` + earlyTurn + `","payload":{"role":"assistant","content":"hello"}}`,
		// turn-iteration #2 (LATEST — what we want)
		`{"type":"event_msg","timestamp":"` + lateTurn + `","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":500,"cached_input_tokens":120,"output_tokens":80,"reasoning_output_tokens":20,"total_tokens":580}}}}`,
	}
	if err := os.WriteFile(rollout, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	// Set mtime AFTER turnStart so the (turnStart-30s) freshness cutoff
	// in the extractor admits the file. Using time.Now() would fail
	// when the test's frozen turnStart is in the future relative to the
	// real clock.
	mtime := turnStart.Add(5 * time.Second)
	if err := os.Chtimes(rollout, mtime, mtime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	gi, model, threadID := readCodexTranscriptUsage(turnStart, "/x")
	if gi == nil {
		t.Fatal("expected non-nil GenerationInfo")
	}
	// Expected from the LATE event: input_tokens=500, cached=120,
	// output=80, reasoning=20, total=580.
	// PromptTokens = uncached prompt = 500 - 120 = 380
	if gi.PromptTokens == nil || *gi.PromptTokens != 380 {
		t.Fatalf("PromptTokens = %v, want 380", gi.PromptTokens)
	}
	if gi.CompletionTokens == nil || *gi.CompletionTokens != 80 {
		t.Fatalf("CompletionTokens = %v, want 80", gi.CompletionTokens)
	}
	if gi.CachedContentTokens == nil || *gi.CachedContentTokens != 120 {
		t.Fatalf("CachedContentTokens = %v, want 120", gi.CachedContentTokens)
	}
	// Cache hit count must also surface in Additional under the raw
	// Anthropic-style key the cost ledger's extractCacheTokens looks
	// for. Without this mirror, claude-code-style ledger entries got
	// the cache breakdown but codex entries showed cache_read=0.
	if gi.Additional == nil {
		t.Fatalf("gi.Additional must surface raw cache_read_input_tokens key; got nil")
	}
	if v, ok := gi.Additional["cache_read_input_tokens"]; !ok || v.(int) != 120 {
		t.Fatalf("gi.Additional[cache_read_input_tokens] = %v, want 120", v)
	}
	if includes, ok := gi.Additional["prompt_tokens_include_cache"].(bool); !ok || includes {
		t.Fatalf("prompt_tokens_include_cache = %#v, want explicit false", gi.Additional["prompt_tokens_include_cache"])
	}
	if known, ok := gi.Additional["context_window_usage_known"].(bool); !ok || known {
		t.Fatalf("context_window_usage_known = %#v, want false for aggregate rollout accounting", gi.Additional["context_window_usage_known"])
	}
	if gi.ReasoningTokens == nil || *gi.ReasoningTokens != 20 {
		t.Fatalf("ReasoningTokens = %v, want 20", gi.ReasoningTokens)
	}
	if gi.TotalTokens == nil || *gi.TotalTokens != 580 {
		t.Fatalf("TotalTokens = %v, want 580", gi.TotalTokens)
	}
	if model != "gpt-5.4" {
		t.Fatalf("model = %q, want gpt-5.4", model)
	}
	if threadID != "aaaabbbb-cccc-dddd-eeee-ffff00001111" {
		t.Fatalf("threadID = %q, want rollout session id", threadID)
	}
}

func TestReadCodexTranscriptUsageReturnsNilWhenMissing(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	if gi, model, threadID := readCodexTranscriptUsage(time.Now(), ""); gi != nil || model != "" || threadID != "" {
		t.Fatalf("expected nil/empty for missing rollout dir; got gi=%+v model=%q threadID=%q", gi, model, threadID)
	}
}

func TestSameCodexWorkingDirUsesFilesystemIdentity(t *testing.T) {
	dir := t.TempDir()
	physical := filepath.Join(dir, "agentworks")
	configured := filepath.Join(dir, "AgentWorks")
	if err := os.WriteFile(physical, []byte("same inode"), 0o600); err != nil {
		t.Fatalf("write physical path: %v", err)
	}
	// A case-insensitive filesystem already exposes configured as the same
	// object. On a case-sensitive filesystem, use a hard link to exercise the
	// same filesystem-identity contract portably.
	if _, err := os.Stat(configured); os.IsNotExist(err) {
		if err := os.Link(physical, configured); err != nil {
			t.Fatalf("link configured spelling: %v", err)
		}
	} else if err != nil {
		t.Fatalf("stat configured spelling: %v", err)
	}

	if !sameCodexWorkingDir(configured, physical) {
		t.Fatalf("filesystem aliases %q and %q must identify the same Codex working directory", configured, physical)
	}
}

func TestReadCodexTranscriptUsageFiltersByWorkingDir(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dayDir := filepath.Join(tmpHome, ".codex", "sessions", "2026", "05", "21")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	turnStart := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	inTurn := turnStart.Add(2 * time.Second).Format(time.RFC3339Nano)
	beforeTurn := turnStart.Add(-1 * time.Minute).Format(time.RFC3339Nano)

	writeRollout := func(name, id, cwd string, inputTokens int, mtime time.Time) {
		t.Helper()
		path := filepath.Join(dayDir, name)
		lines := []string{
			`{"type":"session_meta","timestamp":"` + beforeTurn + `","payload":{"id":"` + id + `","cwd":"` + cwd + `"}}`,
			`{"type":"turn_context","timestamp":"` + inTurn + `","payload":{"model":"gpt-5.4"}}`,
			`{"type":"event_msg","timestamp":"` + inTurn + `","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":` + inputTokensString(inputTokens) + `,"cached_input_tokens":0,"output_tokens":10,"total_tokens":` + inputTokensString(inputTokens+10) + `}}}}`,
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write rollout: %v", err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}

	writeRollout(
		"rollout-2026-05-21T12-00-00-11111111-1111-4111-8111-111111111111.jsonl",
		"11111111-1111-4111-8111-111111111111",
		"/wrong",
		900,
		turnStart.Add(10*time.Second),
	)
	writeRollout(
		"rollout-2026-05-21T12-00-00-22222222-2222-4222-8222-222222222222.jsonl",
		"22222222-2222-4222-8222-222222222222",
		"/wanted",
		500,
		turnStart.Add(5*time.Second),
	)

	gi, _, threadID := readCodexTranscriptUsage(turnStart, "/wanted")
	if threadID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("threadID = %q, want working-dir-matched session", threadID)
	}
	if gi == nil || gi.PromptTokens == nil || *gi.PromptTokens != 500 {
		t.Fatalf("PromptTokens = %#v, want 500 from matched rollout", gi)
	}

	_, _, newestThreadID := readCodexTranscriptUsage(turnStart, "")
	if newestThreadID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unscoped threadID = %q, want freshest rollout", newestThreadID)
	}
}

func inputTokensString(v int) string {
	return strconv.Itoa(v)
}

// TestReadCodexTranscriptUsageSurfacesRateLimitExtras proves the rollout's
// token_count rate_limits block (primary ≈ 5h, secondary ≈ 7d) is parsed into
// display-ready usage extras under Additional[StatusExtrasMetaKey], labelled
// from window_minutes.
func TestReadCodexTranscriptUsageSurfacesRateLimitExtras(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dayDir := filepath.Join(tmpHome, ".codex", "sessions", "2026", "06", "04")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	rollout := filepath.Join(dayDir, "rollout-2026-06-04T12-00-00-aaaabbbb-cccc-4ddd-8eee-ffff00001111.jsonl")

	turnStart := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	ts := turnStart.Add(2 * time.Second).Format(time.RFC3339Nano)
	lines := []string{
		`{"type":"session_meta","timestamp":"` + ts + `","payload":{"id":"aaaabbbb-cccc-4ddd-8eee-ffff00001111","cwd":"/x"}}`,
		`{"type":"event_msg","timestamp":"` + ts + `","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":500,"cached_input_tokens":120,"output_tokens":80,"reasoning_output_tokens":20,"total_tokens":580}},"rate_limits":{"primary":{"used_percent":8.0,"window_minutes":300,"resets_at":0},"secondary":{"used_percent":1.0,"window_minutes":10080,"resets_at":0}}}}`,
	}
	if err := os.WriteFile(rollout, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	mtime := turnStart.Add(5 * time.Second)
	if err := os.Chtimes(rollout, mtime, mtime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	gi, _, _ := readCodexTranscriptUsage(turnStart, "/x")
	if gi == nil || gi.Additional == nil {
		t.Fatalf("expected GenerationInfo with Additional, got %#v", gi)
	}
	extras, ok := gi.Additional[llmtypes.StatusExtrasMetaKey].([]string)
	if !ok {
		t.Fatalf("Additional[%q] = %#v, want []string", llmtypes.StatusExtrasMetaKey, gi.Additional[llmtypes.StatusExtrasMetaKey])
	}
	want := []string{"5h 8%", "7d 1%"}
	if len(extras) != len(want) || extras[0] != want[0] || extras[1] != want[1] {
		t.Fatalf("extras = %v, want %v", extras, want)
	}
}

// TestBuildCodexStatusLineSurfacesRateLimitExtras exercises the full adapter-side
// path (readCodexTranscriptUsage → gi.Additional → buildCodexStatusLine →
// SetStatusExtras), proving rate-limit usage lands on StatusLine.Metadata under
// the status_extras key the UI renders — not just inside GenerationInfo.
func TestBuildCodexStatusLineSurfacesRateLimitExtras(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dayDir := filepath.Join(tmpHome, ".codex", "sessions", "2026", "06", "04")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	rollout := filepath.Join(dayDir, "rollout-2026-06-04T13-00-00-bbbbcccc-dddd-4eee-8fff-000011112222.jsonl")
	now := time.Now()
	ts := now.Add(-2 * time.Second).Format(time.RFC3339Nano)
	lines := []string{
		`{"type":"session_meta","timestamp":"` + ts + `","payload":{"id":"bbbbcccc-dddd-4eee-8fff-000011112222","cwd":"` + tmpHome + `"}}`,
		`{"type":"turn_context","timestamp":"` + ts + `","payload":{"model":"gpt-5.4"}}`,
		`{"type":"event_msg","timestamp":"` + ts + `","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":15000,"cached_input_tokens":2000,"output_tokens":273,"reasoning_output_tokens":40,"total_tokens":15273}},"rate_limits":{"primary":{"used_percent":8.0,"window_minutes":300,"resets_at":0},"secondary":{"used_percent":1.0,"window_minutes":10080,"resets_at":0}}}}`,
	}
	if err := os.WriteFile(rollout, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	status := buildCodexStatusLine("codex-sess-1", tmpHome)
	if status == nil {
		t.Fatal("buildCodexStatusLine returned nil")
	}
	if status.Provider != "codex-cli" {
		t.Fatalf("Provider = %q, want codex-cli", status.Provider)
	}
	extras, ok := status.Metadata[llmtypes.StatusExtrasMetaKey].([]string)
	if !ok {
		t.Fatalf("Metadata[%q] = %#v, want []string", llmtypes.StatusExtrasMetaKey, status.Metadata[llmtypes.StatusExtrasMetaKey])
	}
	want := []string{"5h 8%", "7d 1%"}
	if len(extras) != len(want) || extras[0] != want[0] || extras[1] != want[1] {
		t.Fatalf("extras = %v, want %v", extras, want)
	}
	// tmux_session must still be tagged alongside the new extras.
	if got, _ := status.Metadata["tmux_session"].(string); got != "codex-sess-1" {
		t.Fatalf("tmux_session = %q, want codex-sess-1", got)
	}
}

// TestReadCodexTranscriptUsageSurfacesAllExtras proves the full extras set —
// rate limits (5h/7d), context fill (ctx%), reasoning effort, and plan type —
// is parsed in footer order from token_count + turn_context.
func TestReadCodexTranscriptUsageSurfacesAllExtras(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dayDir := filepath.Join(tmpHome, ".codex", "sessions", "2026", "06", "06")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	rollout := filepath.Join(dayDir, "rollout-2026-06-06T12-00-00-ccccdddd-eeee-4fff-8000-111122223333.jsonl")
	turnStart := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	ts := turnStart.Add(2 * time.Second).Format(time.RFC3339Nano)
	lines := []string{
		`{"type":"session_meta","timestamp":"` + ts + `","payload":{"id":"ccccdddd-eeee-4fff-8000-111122223333","cwd":"/x"}}`,
		`{"type":"turn_context","timestamp":"` + ts + `","payload":{"model":"gpt-5.5","effort":"xhigh"}}`,
		`{"type":"event_msg","timestamp":"` + ts + `","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":25840,"cached_input_tokens":0,"output_tokens":80,"total_tokens":25920},"model_context_window":258400},"rate_limits":{"primary":{"used_percent":8.0,"window_minutes":300,"resets_at":1},"secondary":{"used_percent":1.0,"window_minutes":10080,"resets_at":2},"plan_type":"pro"}}}`,
	}
	if err := os.WriteFile(rollout, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	mtime := turnStart.Add(5 * time.Second)
	if err := os.Chtimes(rollout, mtime, mtime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	gi, _, _ := readCodexTranscriptUsage(turnStart, "/x")
	if gi == nil || gi.Additional == nil {
		t.Fatalf("expected GenerationInfo with Additional, got %#v", gi)
	}
	extras, _ := gi.Additional[llmtypes.StatusExtrasMetaKey].([]string)
	want := []string{"5h 8%", "7d 1%", "ctx 10%", "xhigh", "pro"}
	if len(extras) != len(want) {
		t.Fatalf("extras = %v, want %v", extras, want)
	}
	for i := range want {
		if extras[i] != want[i] {
			t.Fatalf("extras[%d] = %q, want %q (full: %v)", i, extras[i], want[i], extras)
		}
	}
}
