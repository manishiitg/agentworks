package mcpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/netguard"
	"github.com/manishiitg/mcpagent/oauth"
)

// A user-supplied (public-only) server is remote with a public https URL;
// nothing else connects.
func TestPublicOnlyServersAreRemoteAndPublic(t *testing.T) {
	for name, cfg := range map[string]MCPServerConfig{
		"stdio":      {Command: "/bin/sh", Args: []string{"-c", "true"}, PublicOnly: true},
		"plain http": {URL: "http://example.com/mcp", Protocol: ProtocolHTTP, PublicOnly: true},
		"loopback":   {URL: "https://127.0.0.1/mcp", Protocol: ProtocolHTTP, PublicOnly: true},
		"metadata":   {URL: "https://169.254.169.254/latest", Protocol: ProtocolHTTP, PublicOnly: true},
		"localhost":  {URL: "https://localhost:8443/mcp", Protocol: ProtocolSSE, PublicOnly: true},
	} {
		err := New(cfg, loggerv2.NewNoop()).Connect(context.Background())
		if err == nil || !(strings.Contains(err.Error(), "must be remote") || strings.Contains(err.Error(), "refused")) {
			t.Errorf("%s: connected or wrong error: %v", name, err)
		}
	}
}

// PublicOnly OAuth auto-discovery must never bypass netguard by following a 401
// resource_metadata URL pointing at loopback or private addresses.
func TestPublicOnlyOAuthDiscoveryRefusesPrivateResourceMetadata(t *testing.T) {
	var hits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer internal.Close()

	// DiscoverOAuthEndpoints must refuse a private URL up front when PublicOnly.
	cli := New(MCPServerConfig{URL: internal.URL + "/mcp", Protocol: ProtocolHTTP, PublicOnly: true}, loggerv2.NewNoop())
	if _, err := cli.DiscoverOAuthEndpoints(context.Background()); err == nil {
		t.Fatal("DiscoverOAuthEndpoints accepted private URL under PublicOnly")
	}
	if hits.Load() != 0 {
		t.Fatalf("private server was contacted %d times under PublicOnly", hits.Load())
	}

	// Following a 401 with Discoverer{Client: netguard.Client(...)} must block
	// attacker-supplied loopback resource_metadata without making any request.
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header: http.Header{
			"Www-Authenticate": []string{`Bearer resource_metadata="` + internal.URL + `/.well-known/oauth-protected-resource"`},
		},
	}
	if _, err := (oauth.Discoverer{Client: netguard.Client(5 * time.Second)}).DiscoverFromResponse(resp); err == nil {
		t.Fatal("netguard Discoverer accepted loopback resource_metadata")
	}
	if hits.Load() != 0 {
		t.Fatalf("loopback resource_metadata server was hit %d times", hits.Load())
	}
}
