package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestAgentProcessStatsReportsTodaysCounts(t *testing.T) {
	llmtypes.NewAgentProcess("example-provider", "chat-1").Launched(nil, "launch_changed:option:mcp_config")
	w := httptest.NewRecorder()
	(&StreamingAPI{}).handleAgentProcessStats(w, httptest.NewRequest(http.MethodGet, "/api/admin/agent-processes", nil))
	var got llmtypes.AgentProcessStats
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	for _, row := range got.Providers {
		if row.Provider == "example-provider" {
			if row.Launches != 1 || len(row.LaunchReasons) != 1 || row.LaunchReasons[0].Reason != "launch_changed:option:mcp_config" {
				t.Fatalf("row = %+v", row)
			}
			return
		}
	}
	t.Fatalf("provider missing from %+v", got)
}
