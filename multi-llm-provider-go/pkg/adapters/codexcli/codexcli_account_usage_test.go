package codexcli

import (
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"testing"
)

// PLAT-872-52: per-pool quotas supersede legacy aggregate and null is unavailable.
func TestCodexAccountUsageKeepsPoolsWithoutDuplicateAggregate(t *testing.T) {
	raw := []byte(`{"rateLimits":{"primary":{"usedPercent":99}},"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":0,"windowDurationMins":300,"resetsAt":1793100000},"secondary":{"usedPercent":null}},"review":{"primary":{"usedPercent":42}}}}`)
	usage, err := parseCodexAccountUsage(raw, llmtypes.AccountUsage{})
	if err != nil || len(usage.Windows) != 2 || usage.Windows[0].UsedPercent != 0 || usage.Windows[0].ResetsAt.Unix() != 1793100000 || usage.Windows[1].UsedPercent != 42 {
		t.Fatalf("wrong quotas: %#v %v", usage, err)
	}
}
