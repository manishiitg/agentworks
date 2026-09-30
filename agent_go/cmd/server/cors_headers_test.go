package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Local dev calls the agent cross-origin (Vite on another port), so every
// custom header the chat send path attaches must pass the CORS preflight or
// the browser drops the request as a bare "Network Error".
func TestCORSPreflightAllowsChatSendHeaders(t *testing.T) {
	api := &StreamingAPI{config: ServerConfig{CORSOrigins: []string{"http://127.0.0.1:51734"}}}
	handler := api.corsMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("preflight must not reach the handler")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/query", nil)
	req.Header.Set("Origin", "http://127.0.0.1:51734")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("preflight status = %d, want 200", rec.Code)
	}
	allowed := map[string]bool{}
	for _, name := range strings.Split(rec.Header().Get("Access-Control-Allow-Headers"), ",") {
		allowed[strings.ToLower(strings.TrimSpace(name))] = true
	}
	for _, header := range []string{"X-Session-ID", "X-Conversation-Continuation", "X-Queued-Chat-Delivery", "X-Client-Submitted-At", attendedChatHeader,
		// The workspace client sends X-User-ID on /api/wp; without it the local Crew list failed.
		"X-User-ID"} {
		if !allowed[strings.ToLower(header)] {
			t.Errorf("preflight does not allow %s", header)
		}
	}
}
