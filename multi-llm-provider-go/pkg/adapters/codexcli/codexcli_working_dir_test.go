package codexcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCodexWorkingDirFromOptionsFallsBackToGetwd is a regression
// guard for a real production bug surfaced by the multi-turn chat
// e2e: when callers don't pass MetadataKeyProjectDirID, this helper
// used to return "" with no fallback. That empty value flowed into
// readCodexTranscriptUsage/readCodexTranscriptMessages, which both
// short-circuit their session_meta.cwd filter on an empty
// expectedWorkingDir — and the sidecar parser would then happily
// pick up the freshest rollout from a parallel codex process
// (Codex Desktop, VS Code Codex), leaking that other process's
// conversation + tokens + cost into the current session's ledger.
//
// The fix mirrors cursor's `cursorMustGetwd()` fallback. This test
// exercises every branch:
//
//  1. opts == nil → process cwd
//  2. opts with no Metadata → process cwd
//  3. opts with empty MetadataKeyProjectDirID → process cwd
//  4. opts with whitespace-only MetadataKeyProjectDirID → process cwd
//  5. opts with a real MetadataKeyProjectDirID → that value verbatim
//
// Critical invariant: the function must NEVER return an empty
// string when os.Getwd() succeeds. An empty result is the bug.
func TestCodexWorkingDirFromOptionsFallsBackToGetwd(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}

	cases := []struct {
		name string
		opts *llmtypes.CallOptions
		want string
	}{
		{
			name: "nil_opts_falls_back_to_getwd",
			opts: nil,
			want: wd,
		},
		{
			name: "opts_no_metadata_falls_back_to_getwd",
			opts: &llmtypes.CallOptions{},
			want: wd,
		},
		{
			name: "opts_no_custom_map_falls_back_to_getwd",
			opts: &llmtypes.CallOptions{Metadata: &llmtypes.Metadata{}},
			want: wd,
		},
		{
			name: "empty_metadata_value_falls_back_to_getwd",
			opts: &llmtypes.CallOptions{Metadata: &llmtypes.Metadata{Custom: map[string]interface{}{
				MetadataKeyProjectDirID: "",
			}}},
			want: wd,
		},
		{
			name: "whitespace_metadata_value_falls_back_to_getwd",
			opts: &llmtypes.CallOptions{Metadata: &llmtypes.Metadata{Custom: map[string]interface{}{
				MetadataKeyProjectDirID: "   ",
			}}},
			want: wd,
		},
		{
			name: "explicit_metadata_value_wins",
			opts: &llmtypes.CallOptions{Metadata: &llmtypes.Metadata{Custom: map[string]interface{}{
				MetadataKeyProjectDirID: filepath.Join(wd, "subdir"),
			}}},
			want: filepath.Join(wd, "subdir"),
		},
		{
			name: "explicit_metadata_value_is_trimmed",
			opts: &llmtypes.CallOptions{Metadata: &llmtypes.Metadata{Custom: map[string]interface{}{
				MetadataKeyProjectDirID: "  /tmp/codex-explicit  ",
			}}},
			want: "/tmp/codex-explicit",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := codexWorkingDirFromOptions(tc.opts)
			if got != tc.want {
				t.Fatalf("codexWorkingDirFromOptions(%+v) = %q, want %q", tc.opts, got, tc.want)
			}
			// The invariant: never return empty when os.Getwd works.
			// Even the "explicit value" branches return a non-empty
			// string. If the function ever returns "" here, the
			// codex sidecar parser will skip its cwd filter and
			// leak rollouts from parallel codex processes.
			if strings.TrimSpace(got) == "" {
				t.Fatalf("codexWorkingDirFromOptions returned empty — the cwd-filter bypass bug is back")
			}
		})
	}
}
