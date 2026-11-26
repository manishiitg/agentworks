package musecli

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The chat's plan line read Muse's "Window" and "Weekly" and showed neither:
// it knows only "5-hour limit" and "Weekly limit".
func TestMuseAccountUsageNamesThePlanLineWindows(t *testing.T) {
	raw := []byte(`{"allowance":{"tier":"example","window":{"usedPercent":6,"resetsAtMs":1795676448000},"weekly":{"usedPercent":67,"resetsAtMs":1795996800000},"asOfMs":1795662388419}}`)
	usage, err := parseMuseAccountUsage(raw, llmtypes.AccountUsage{})
	if err != nil || len(usage.Windows) != 2 || usage.Windows[0].Label != "5-hour limit" || usage.Windows[0].UsedPercent != 6 || usage.Windows[1].Label != "Weekly limit" || usage.Windows[1].UsedPercent != 67 {
		t.Fatalf("windows = %+v, err = %v", usage.Windows, err)
	}
}
