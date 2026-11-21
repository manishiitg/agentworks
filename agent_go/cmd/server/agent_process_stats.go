package server

import (
	"encoding/json"
	"net/http"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// GET /api/admin/agent-processes — today's native coding-agent process
// launches, reuses and exits per provider, with the top launch reasons
// (#2886). The provider library counts them as it logs [AGENT_PROCESS];
// counts restart at local midnight and when the server restarts. Names and
// counts only, never values.
func (api *StreamingAPI) handleAgentProcessStats(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(llmtypes.AgentProcessStatsToday(5))
}
