package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// PLAT-746 review: a client that hangs up after a change succeeded must still get its
// receipt saved, or its retry with the same request_id repeats the change.
func TestExternalRetryReceiptSurvivesAClientHangUp(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "receipt-hang-up-test-secret")
	api := &StreamingAPI{}
	tool := externalTool{Name: "share_example", mutates: true, InputSchema: map[string]any{"properties": map[string]any{}}}
	args := map[string]any{"request_id": "hang-up-1", "mode": "private"}
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	ctx, hangUp := context.WithCancel(context.WithValue(context.Background(), UserContextKey, claims))
	request := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", nil).WithContext(ctx)

	recorder, rest, finish, answered := api.externalRetryReceipt(httptest.NewRecorder(), request, tool, args)
	if answered || rest["request_id"] != nil {
		t.Fatalf("first call: answered=%v args=%v", answered, rest)
	}
	recorder.WriteHeader(http.StatusOK)
	_, _ = recorder.Write([]byte(`{"shared":true}`))
	hangUp() // the connection drops after the change happened, before the receipt is written
	finish()

	again := httptest.NewRecorder()
	retry := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", nil).WithContext(context.WithValue(context.Background(), UserContextKey, claims))
	if _, _, _, answered := api.externalRetryReceipt(again, retry, tool, args); !answered || again.Header().Get("X-AgentWorks-Replayed") != "true" || again.Body.String() != `{"shared":true}` {
		t.Fatalf("retry after a hang-up did not replay the first result: answered=%v %q", answered, again.Body.String())
	}
}
