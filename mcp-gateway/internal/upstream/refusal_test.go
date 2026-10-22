package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
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

// Exercise the real MCP transport: both HTTP refusals and JSON-RPC errors may
// repeat the authentication supplied during initialization or discovery.
func TestCredentialErrorsExcludeSentValues(t *testing.T) {
	for _, auth := range []string{"Bearer", "Basic", "Headers", "OAuth"} {
		for _, phase := range []string{"start", "initialize", "discover", "rpc-initialize", "rpc-discover", "plain"} {
			t.Run(auth+"/"+phase, func(t *testing.T) {
				key := `dummy-private-credential-"for-regression`
				other := "dummy-secondary-credential-for-regression"
				opts := DialOptions{AllowPrivate: true, AuthScheme: auth, BearerToken: key}
				if auth == "Headers" {
					opts.BearerToken = fmt.Sprintf(`{"X-API-Key":%q,"Authorization":%q}`, key, "Bearer "+other)
				}
				if auth == "OAuth" {
					opts.AuthScheme = "Bearer"
					opts.AccessToken = func(context.Context) (string, error) { return key, nil }
				}
				if phase == "start" {
					opts.Transport = "sse"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if auth == "Headers" {
						if r.Header.Get("X-API-Key") != key || r.Header.Get("Authorization") != "Bearer "+other {
							t.Error("configured credential headers were changed")
						}
					} else if r.Header.Get("Authorization") != map[bool]string{true: "Basic ", false: "Bearer "}[auth == "Basic"]+key {
						t.Error("configured authorization was changed")
					}
					var request struct {
						ID     json.RawMessage `json:"id"`
						Method string          `json:"method"`
					}
					if r.Method == http.MethodPost {
						_ = json.NewDecoder(r.Body).Decode(&request)
					}
					if (phase == "discover" || phase == "rpc-discover") && request.Method != "tools/list" {
						if request.Method == "initialize" {
							_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"protocolVersion": mcp.LATEST_PROTOCOL_VERSION, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fixture", "version": "1"}}})
						} else {
							w.WriteHeader(http.StatusAccepted)
						}
						return
					}
					if !strings.HasPrefix(phase, "rpc-") {
						w.WriteHeader(http.StatusForbidden)
					}
					if phase == "plain" {
						_, _ = fmt.Fprint(w, "Account needs setup. "+strings.Repeat("x", 260)+key)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32000, "message": "Account needs setup: " + key + " / " + other}})
				}))
				defer server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				client, err := DialWithOptions(ctx, server.URL, opts)
				if client != nil {
					defer client.Close()
					_, err = client.Discover(ctx)
				}
				if err == nil {
					t.Fatal("provider refusal was lost")
				}
				text := err.Error()
				for _, secret := range []string{key, other, "dummy-private-", "dummy-secondary-"} {
					// The secondary credential is only sent by the Headers case.
					if (secret == other || secret == "dummy-secondary-") && auth != "Headers" {
						continue
					}
					if strings.Contains(text, secret) {
						t.Fatalf("credential reached the returned error: %s", text)
					}
				}
				if !strings.Contains(text, "Account needs setup") {
					t.Fatalf("provider explanation was lost: %s", text)
				}
				if (phase == "initialize" || phase == "plain") && !strings.Contains(text, "403") {
					t.Fatalf("HTTP refusal status was lost: %s", text)
				}
			})
		}
	}
}
