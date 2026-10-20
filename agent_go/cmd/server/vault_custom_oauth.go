package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/netguard"
	"github.com/manishiitg/mcpagent/oauth"
)

// A custom Vault connection (a name and an MCP URL, not a catalog app) that answers "authorization required" signs in
// with the server's own published sign-in details, the same discovery a project connection uses (addPlaceMCP): its
// protected-resource and authorization-server metadata, with dynamic client registration and PKCE. The gateway marks
// such a connection with OAuthServer "custom:<provider key>"; the URL that is signed in to is the connection's own
// upstream URL, so a custom connection can never borrow a catalog app's sign-in. Discovered endpoints are checked
// against private networks and kept per connection for the life of the process.

const customVaultOAuthPrefix = "custom:"

func isCustomVaultOAuthServer(name string) bool {
	return strings.HasPrefix(name, customVaultOAuthPrefix) && len(name) > len(customVaultOAuthPrefix)
}

var customVaultOAuthEndpoints sync.Map // connection ID + "\x00" + URL -> oauth.OAuthConfig

func customVaultOAuthTemplate(ctx context.Context, id, upstreamURL string) (mcpclient.MCPServerConfig, error) {
	key := id + "\x00" + upstreamURL
	var discovered oauth.OAuthConfig
	if cached, ok := customVaultOAuthEndpoints.Load(key); ok {
		discovered = cached.(oauth.OAuthConfig)
	} else {
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		probe, err := services.ProbeMCPServerAuthWith(probeCtx, netguard.Client(15*time.Second), upstreamURL)
		if err != nil {
			return mcpclient.MCPServerConfig{}, errors.New("could not read this server's sign-in details")
		}
		if probe.NoAuthRequired || probe.Endpoints == nil || probe.Endpoints.AuthURL == "" || probe.Endpoints.TokenURL == "" {
			return mcpclient.MCPServerConfig{}, errors.New("this server does not publish standard MCP sign-in details")
		}
		for _, endpoint := range []string{probe.Endpoints.AuthURL, probe.Endpoints.TokenURL, probe.Endpoints.RegistrationEndpoint} {
			if endpoint == "" {
				continue
			}
			if err := netguard.CheckURL(endpoint, true); err != nil {
				return mcpclient.MCPServerConfig{}, errors.New("the server's sign-in endpoint was refused")
			}
		}
		discovered = oauth.OAuthConfig{
			AuthURL: probe.Endpoints.AuthURL, TokenURL: probe.Endpoints.TokenURL,
			RegistrationEndpoint: probe.Endpoints.RegistrationEndpoint, Resource: probe.Endpoints.Resource,
			Scopes: probe.Endpoints.ScopesSupported, UsePKCE: true,
		}
		customVaultOAuthEndpoints.Store(key, discovered)
	}
	discovered.TokenFile = getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(id))
	return mcpclient.MCPServerConfig{URL: upstreamURL, OAuth: &discovered}, nil
}
