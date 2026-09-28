package services

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/manishiitg/mcpagent/oauth"
)

// Live auth probing for an MCP server found outside our own curated catalog
// (search_mcp_catalog's public registry results, or any URL a user gives).
//
// This automates the exact method PR #191's author ran by hand for each of
// the 67 catalog additions: call the server, and if it answers 401, follow
// WWW-Authenticate to its RFC 9728 protected-resource metadata, then its
// RFC 8414 authorization-server metadata. That chain is spec-compliant
// discovery, not a guess — it is what oauth.DiscoverFromResponse already
// implements in the mcpagent module (unused by this app until now). This is
// deliberately NOT the same thing as the naive "guess /.well-known/... from
// the host" probe oauth_routes.go's handleOAuthStart comments describe
// disabling: that one had no real 401 or WWW-Authenticate to anchor on and
// found unrelated website metadata. Being triggered by an actual 401 is
// what makes this trustworthy.

// MCPServerAuthProbe is the verdict of probing one server URL.
type MCPServerAuthProbe struct {
	// NoAuthRequired is true when the server answered without a 401 at all.
	NoAuthRequired bool
	// Endpoints is the discovered OAuth shape, set only when NoAuthRequired
	// is false and discovery succeeded.
	Endpoints *oauth.OAuthEndpoints
}

var mcpProbeHTTPClient = &http.Client{Timeout: 10 * time.Second}

// ProbeMCPServerAuth calls serverURL once and reports what it needs. An
// error means the probe itself couldn't reach a verdict (network failure, or
// a 401 with no usable discovery information — no WWW-Authenticate header,
// or metadata that doesn't resolve) — not a judgment call about whether to
// install. A server that needs auth but exposes no discoverable Dynamic
// Client Registration endpoint still returns successfully here (Endpoints
// non-nil, RegistrationEndpoint empty) — the caller decides what to do with
// that, since it may still support CIMD or a manually registered client.
func ProbeMCPServerAuth(ctx context.Context, serverURL string) (*MCPServerAuthProbe, error) {
	return ProbeMCPServerAuthWith(ctx, mcpProbeHTTPClient, serverURL)
}

// ProbeMCPServerAuthWith probes with client, which also follows the OAuth
// metadata links: a person's own server is probed with a public-only client
// (netguard) so neither the server nor its metadata can point the platform at
// its own network.
func ProbeMCPServerAuthWith(ctx context.Context, client *http.Client, serverURL string) (*MCPServerAuthProbe, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s: %w", serverURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		return &MCPServerAuthProbe{NoAuthRequired: true}, nil
	}

	endpoints, err := oauth.Discoverer{Client: client}.DiscoverFromResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("%s requires authentication but published no discoverable OAuth metadata (WWW-Authenticate / RFC 9728 / RFC 8414): %w", serverURL, err)
	}
	return &MCPServerAuthProbe{Endpoints: endpoints}, nil
}
