package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientChatTelemetryAcceptsContentFreeTimeline(t *testing.T) {
	api := &StreamingAPI{}
	req := httptest.NewRequest(http.MethodPost, "/api/client-telemetry/chat-delivery", strings.NewReader(`{
		"page_id":"page-1",
		"events":[{
			"sequence":1,
			"phase":"sse_received",
			"session_id":"session-1",
			"event_id":"event-1",
			"event_type":"conversation_thinking",
			"transport":"sse",
			"client_time":"2026-09-22T07:10:13.700Z",
			"performance_ms":1234.5,
			"server_event_time":"2026-09-22T07:10:13.663Z"
		}]
	}`))
	w := httptest.NewRecorder()
	api.handleClientChatTelemetry(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestClientChatTelemetryRejectsUnknownFieldsOfMeaning(t *testing.T) {
	api := &StreamingAPI{}
	req := httptest.NewRequest(http.MethodPost, "/api/client-telemetry/chat-delivery", strings.NewReader(`{
		"page_id":"page-1",
		"events":[{"phase":"message_content","session_id":"s","client_time":"now"}]
	}`))
	w := httptest.NewRecorder()
	api.handleClientChatTelemetry(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestClientChatTelemetryRejectsMessageContent(t *testing.T) {
	api := &StreamingAPI{}
	req := httptest.NewRequest(http.MethodPost, "/api/client-telemetry/chat-delivery", strings.NewReader(`{
		"page_id":"page-1",
		"events":[{"phase":"painted","session_id":"s","client_time":"now","content":"must not be logged"}]
	}`))
	w := httptest.NewRecorder()
	api.handleClientChatTelemetry(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// A send is traced from the click: the decision and the staged row are accepted phases, so a message that never
// reaches the server can be found in the timeline.
func TestClientChatTelemetryAcceptsSendDecisionAndStagedPhases(t *testing.T) {
	api := &StreamingAPI{}
	for _, phase := range []string{"send_decision", "send_staged"} {
		req := httptest.NewRequest(http.MethodPost, "/api/client-telemetry/chat-delivery", strings.NewReader(`{
			"page_id":"page-1",
			"events":[{"sequence":1,"phase":"`+phase+`","session_id":"session-1","event_type":"chat_submission","transport":"server","submission_id":"sub-1","client_time":"2026-10-14T11:35:00.000Z","performance_ms":1.5}]
		}`))
		w := httptest.NewRecorder()
		api.handleClientChatTelemetry(w, req)
		if w.Code != http.StatusAccepted {
			t.Fatalf("%s: status = %d, body = %s", phase, w.Code, w.Body.String())
		}
	}
}
