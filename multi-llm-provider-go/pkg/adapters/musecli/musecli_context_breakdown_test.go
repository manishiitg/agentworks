package musecli

import (
	"os"
	"path/filepath"
	"testing"
)

// A Muse chat's Context panel showed only a total: Muse's request trace splits it into its own rules, the skills
// list and other context it adds, the person's messages, the replies and tool results. The latest trace wins.
func TestMuseContextBreakdownReadsTheLatestRequestTrace(t *testing.T) {
	data := t.TempDir()
	dir := filepath.Join(data, "muse", "sessions", "2026", "01", "02", "s1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := `{"payload":{"kind":"run","event":{"kind":"context_block_diagnostic","block_id":"skills_catalog","text_bytes":300}}}
{"payload":{"kind":"run","event":{"kind":"model_input_trace_recorded","bounded":{"total_lane_bytes":10,"aggregates":[]}}}}
{"payload":{"kind":"run","event":{"kind":"model_input_trace_recorded","bounded":{"total_lane_bytes":2000,"aggregates":[` +
		`{"logical_lane":{"value":"system_base"},"source":{"value":"provider_default"},"byte_count":500},` +
		`{"logical_lane":{"value":"context"},"source":{"value":"run_context"},"byte_count":400},` +
		`{"logical_lane":{"value":"history"},"source":{"value":"current_user"},"byte_count":100},` +
		`{"logical_lane":{"value":"history"},"source":{"value":"history"},"byte_count":600},` +
		`{"logical_lane":{"value":"tool_result"},"source":{"value":"tool_result"},"byte_count":400}]}}}}
`
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadContextBreakdown("s1", data)
	want := ContextBreakdown{Total: 2000, Setup: 500, Skills: 300, Context: 100, Messages: 100, Replies: 600, ToolResults: 400}
	if !ok || got != want {
		t.Fatalf("breakdown = %+v (ok %v), want %+v", got, ok, want)
	}
}
