package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A server that refuses the first call says why in its answer; the connect error carries that sentence, not a transport error.
func TestDialShowsTheServersOwnRefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"App is not enabled for this server"}}`))
	}))
	defer server.Close()
	_, err := DialWithOptions(context.Background(), server.URL, DialOptions{AllowPrivate: true})
	if err == nil || !strings.Contains(err.Error(), "refused the connection: App is not enabled for this server") {
		t.Fatalf("the server's reason was not in the error: %v", err)
	}
}
