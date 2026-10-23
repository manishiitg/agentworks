package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const agentServerLinkClientPrefix = "agent_link_"
const agentServerLinkCallbackPath = "/agentbridge/servers/callback"

var agentServerLinkScopes = []string{"messages:read", "messages:send"}

type namedAgentRelay struct {
	Origin       string    `json:"origin"`
	Provider     string    `json:"provider,omitempty"`
	DeliveryMode string    `json:"delivery_mode,omitempty"`
	Online       bool      `json:"online"`
	LastSeenAt   time.Time `json:"last_seen_at,omitempty"`
}

// A linked server is a messaging connection, not a person's general MCP login.
// The client ID comes from the verified OAuth grant, never request arguments.
func isAgentServerLinkClient(id string) bool {
	return strings.HasPrefix(id, agentServerLinkClientPrefix)
}

func agentServerLinkAPIPath(method, path string) bool {
	return method == http.MethodPost && (path == "/api/external/v1/call" || path == "/api/external/v1/agents/attachment" || path == "/api/external/v1/agents/link") ||
		method == http.MethodGet && (path == "/api/external/v1/tools" || path == "/api/external/v1/agents/events")
}

func agentServerNamespace(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" ||
		(u.Scheme != "https" && (u.Scheme != "http" || !cliOAuthLoopback(u.Hostname()))) {
		return "", errors.New("Use the server's HTTPS web address.")
	}
	// The origin, rather than branding shared by many installations, owns the
	// namespace. A person's short outbound labels are kept on their own links.
	name := strings.ToLower(u.Host)
	name = strings.NewReplacer(":", "-", "[", "", "]", "").Replace(name)
	return strings.Trim(name, "-"), nil
}

func agentServerLinkClientOrigin(client mcpOAuthClient) (string, error) {
	if !isAgentServerLinkClient(client.ID) || len(client.RedirectURIs) != 1 {
		return "", errors.New("This is not a linked-server connection.")
	}
	u, err := url.Parse(client.RedirectURIs[0])
	if err != nil || u.Path != agentServerLinkCallbackPath || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", errors.New("Invalid linked-server callback.")
	}
	origin := u.Scheme + "://" + u.Host
	_, err = agentServerNamespace(origin)
	return origin, err
}

func agentServerLinkOrigin(ctx context.Context, claims *UserClaims) (string, error) {
	if claims == nil || claims.AccessToken == nil || !isAgentServerLinkClient(claims.AccessToken.OAuthClientID) {
		return "", errors.New("A linked-server connection is required.")
	}
	store, err := openMCPOAuthStore()
	if err != nil {
		return "", err
	}
	defer store.Close()
	client, err := store.Client(ctx, claims.AccessToken.OAuthClientID)
	if err != nil {
		return "", err
	}
	return agentServerLinkClientOrigin(client)
}

func updateRelayPresence(agent *namedMessageAgent, args map[string]any) error {
	mode := externalArg(args, "home_delivery_mode")
	if !slices.Contains([]string{"live", "next_step", "inbox", "offline", "unknown"}, mode) {
		return errors.New("Report the home agent's delivery state.")
	}
	online, ok := args["home_online"].(bool)
	if !ok {
		return errors.New("Report whether the home agent is connected.")
	}
	seen, err := time.Parse(time.RFC3339Nano, externalArg(args, "home_last_seen_at"))
	if err != nil || seen.After(time.Now().Add(time.Minute)) {
		return fmt.Errorf("Report the home agent's last-seen time.")
	}
	agent.Relay.Provider = cleanAgentLabel(externalArg(args, "home_provider"))
	agent.Relay.DeliveryMode, agent.Relay.Online = mode, online
	agent.Relay.LastSeenAt, agent.LastOnlineAt = seen, seen
	return nil
}

// Reader restarts replace a derived child, not the person's server approval.
// Queued mail may use a current child of the same account, address and verified
// home origin. Never substitute general account authority or a native agent.
func currentRelaySenderClaims(ctx context.Context, source agentMessageEndpoint, connectionID string) (*UserClaims, error) {
	a, err := namedAgentByID(ctx, source.UserID, source.NamedAgentID)
	if err != nil || a.Relay == nil || !strings.HasPrefix(connectionID, "oauth-") {
		return nil, errors.New("Relayed sender unavailable.")
	}
	s, err := openMCPOAuthStore()
	if err != nil {
		return nil, err
	}
	var user, clientID string
	err = s.db.QueryRowContext(ctx, `SELECT user_id,client_id FROM tokens WHERE family_id=? LIMIT 1`, strings.TrimPrefix(connectionID, "oauth-")).Scan(&user, &clientID)
	var origin string
	if err == nil && user == source.UserID {
		var client mcpOAuthClient
		client, err = s.Client(ctx, clientID)
		if err == nil {
			origin, err = agentServerLinkClientOrigin(client)
		}
	}
	s.Close()
	if err != nil || origin != a.Relay.Origin {
		return nil, errors.New("Relayed sender origin changed.")
	}
	for _, id := range a.Connections {
		claims, e := agentMessageConnectionClaims(ctx, id)
		if e != nil || claims.AccessToken == nil || claims.AccessToken.BoundAgentID != a.ID || !isAgentServerLinkClient(claims.AccessToken.OAuthClientID) {
			continue
		}
		current, e := agentServerLinkOrigin(ctx, claims)
		if e == nil && current == origin && namedAgentConnectionAllowed(a, claims) {
			return claims, nil
		}
	}
	return nil, errors.New("The linked-server approval has ended.")
}
