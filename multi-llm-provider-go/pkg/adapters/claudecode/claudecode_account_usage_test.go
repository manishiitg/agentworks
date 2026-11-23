package claudecode

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-872-52: get_usage percentages are already 0-100 (stream events are 0-1).
func TestClaudeAccountUsagePercentAndUnavailable(t *testing.T) {
	raw := []byte(`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":4.2,"resets_at":"2026-10-27T20:00:00Z"},"seven_day":{"utilization":null},"model_scoped":[{"display_name":"Example","utilization":0}]}}`)
	usage, err := parseClaudeAccountUsage(raw, llmtypes.AccountUsage{})
	if err != nil || len(usage.Windows) != 2 || usage.Windows[0].UsedPercent != 4.2 || usage.Windows[0].ResetsAt == nil || usage.Windows[1].UsedPercent != 0 {
		t.Fatalf("wrong quotas: %#v %v", usage, err)
	}
	usage, err = parseClaudeAccountUsage([]byte(`{"rate_limits_available":false,"rate_limits":{"five_hour":{"utilization":0}}}`), llmtypes.AccountUsage{})
	if err != nil || len(usage.Windows) != 0 {
		t.Fatal("unavailable became zero usage")
	}
}
