package executor

import (
	"net/http"
	"net/http/httptest"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestExecutorCORSAllowsLoopbackOrigin(t *testing.T) {
	handler := NewExecutorHandlers("", loggerv2.NewNoop())
	req := httptest.NewRequest(http.MethodOptions, "/api/custom/execute", nil)
	req.Header.Set("Origin", "http://127.0.0.1:5173")
	rec := httptest.NewRecorder()

	handler.HandleCustomExecute(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want loopback origin", got)
	}
}

func TestExecutorCORSRejectsCrossSiteOrigin(t *testing.T) {
	handler := NewExecutorHandlers("", loggerv2.NewNoop())
	req := httptest.NewRequest(http.MethodOptions, "/api/custom/execute", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()

	handler.HandleCustomExecute(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty", got)
	}
}

func TestExecutorCORSNoOriginKeepsServerToServerCalls(t *testing.T) {
	handler := NewExecutorHandlers("", loggerv2.NewNoop())
	req := httptest.NewRequest(http.MethodOptions, "/api/custom/execute", nil)
	rec := httptest.NewRecorder()

	handler.HandleCustomExecute(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty for no-Origin request", got)
	}
}
