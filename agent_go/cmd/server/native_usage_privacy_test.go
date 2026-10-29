package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// PLAT-872: sharing a conversation does not share its provider account's quota.
// The owner still receives the warning, and ordinary product data is untouched.
func TestNativeUsageWarningsStayWithAccountOwner(t *testing.T) {
	store := events.NewEventStore(10)
	defer store.Stop()
	store.SetSessionOwner("native-private-example", "alice")
	api := &StreamingAPI{eventStore: store}
	payload := map[string]interface{}{"events": []interface{}{map[string]interface{}{"type": "status_line", "data": map[string]interface{}{"text": "private quota text", "metadata": map[string]interface{}{"context_used_tokens": 4200, "context_window_tokens": 10000, "rate_limit_windows": []interface{}{map[string]interface{}{"name": "private-window", "used_percent": 95, "resets_at": 12345}}, "status_extras": []interface{}{"ctx 42%", "private-window 95%"}}}}, map[string]interface{}{"type": "tool_call_end", "data": map[string]interface{}{"result": map[string]interface{}{"credits": 3, "plan": "Product plan"}}}}}
	// Native Codex token_count and Claude status line shapes, with sample
	// numbers: private windows/credits never survive shared replay.
	var codex map[string]interface{}
	if err := json.Unmarshal([]byte(`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":60,"output_tokens":4},"last_token_usage":{"total_tokens":104},"model_context_window":10000},"rate_limits":{"primary":{"used_percent":95,"window_minutes":300,"resets_at":12345},"secondary":{"used_percent":5,"window_minutes":10080,"resets_at":12346},"credits":{"has_credits":true,"balance":"private-credit"},"limit_id":"private-limit"}}}`), &codex); err != nil {
		t.Fatal(err)
	}
	payload["events"] = append(payload["events"].([]interface{}), codex)
	for _, user := range []string{"alice", "bob"} {
		req := httptest.NewRequest("GET", "/api/sessions/native-private-example/events", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user}))
		out, err := json.Marshal(api.nativeUsageResponse(req, "native-private-example", payload))
		if err != nil {
			t.Fatal(err)
		}
		s := string(out)
		if user == "alice" && !strings.Contains(s, "private-window") {
			t.Fatal("owner lost private warning")
		}
		if user == "bob" && (strings.Contains(s, "private-window") || strings.Contains(s, "private quota text") || strings.Contains(s, "resets_at") || strings.Contains(s, "private-credit") || strings.Contains(s, "private-limit")) {
			t.Fatal("shared response exposed private account limits")
		}
		if !strings.Contains(s, "context_used_tokens") || !strings.Contains(s, `"cached_input_tokens":60`) || !strings.Contains(s, "model_context_window") || !strings.Contains(s, "Product plan") || !strings.Contains(s, `"credits":3`) {
			t.Fatal("privacy projection removed project data")
		}
	}
}
