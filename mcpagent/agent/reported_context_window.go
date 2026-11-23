package mcpagent

import (
	"encoding/json"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Native capacity is independent of whether a CLI exposed its current prompt
// occupancy. Never derive occupancy from aggregate billing/token totals.
func (a *Agent) applyReportedContextWindow(resp *llmtypes.ContentResponse) {
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil || resp.Choices[0].GenerationInfo == nil {
		return
	}
	raw := resp.Choices[0].GenerationInfo.Additional[llmtypes.ContextWindowTokensMetaKey]
	// Round-trip the scalar to enforce a positive integer and reject NaN,
	// fractions, overflow and strings, including after a JSON wire boundary.
	encoded, err := json.Marshal(raw)
	if err != nil {
		return
	}
	var capacity int
	if json.Unmarshal(encoded, &capacity) != nil || capacity <= 0 {
		return
	}
	a.tokenTrackingMutex.Lock()
	changed := a.modelContextWindow != capacity
	a.modelContextWindow = capacity
	a.tokenTrackingMutex.Unlock()
	if changed {
		getLogger(a).Info("[CLI_CONTEXT] native capacity applied", loggerv2.String("session", a.sessionID), loggerv2.String("model", a.modelID), loggerv2.Int("window", capacity))
	}
}
