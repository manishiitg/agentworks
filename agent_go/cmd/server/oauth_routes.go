package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/mcpcache"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
)

// OAuthFlowState tracks ongoing OAuth flows
type OAuthFlowState struct {
	ServerName   string
	ConnectionID string
	Outcome      string // guarded by oauthFlowsMu
	State        string
	CodeChan     chan string
	ErrChan      chan error
	Manager      *oauth.Manager
	ServerConfig mcpclient.MCPServerConfig // The server config with OAuth settings to persist
}

var (
	oauthFlows   = make(map[string]*OAuthFlowState) // state -> flow
	oauthFlowsMu sync.RWMutex
)

// The legacy platform credential namespace is reserved for Vault. Ordinary
// product connections use the authenticated person's sealed private store.
const platformMCPTokenUserID = "_platform"

const platformMCPConnectionSessionID = "mcp-platform"

// These catalog providers issue confidential OAuth clients by hand. Other
// servers may use public clients even without Dynamic Client Registration.
func requiresRegisteredMCPClientSecret(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "github", "hubspot", "slack", "render", "box",
		"googlecalendar", "googlechat", "googledocs", "googledrive",
		"googlegmail", "googlepeople", "googlesheets", "googleslides":
		return true
	default:
		return false
	}
}

func hasRegisteredMCPClientSecret(cfg *oauth.OAuthConfig) bool {
	if cfg == nil {
		return false
	}
	if strings.TrimSpace(cfg.ClientSecret) != "" {
		return true
	}
	if strings.TrimSpace(cfg.ClientSecretFile) == "" {
		return false
	}
	secret, err := oauth.ReadClientSecretFile(cfg.ClientSecretFile)
	return err == nil && strings.TrimSpace(secret) != ""
}

func closePlatformMCPConnection(serverName string) {
	registry := mcpclient.GetSessionRegistry()
	connectionSessionID := registry.ResolveConnectionSessionID(platformMCPConnectionSessionID, serverName)
	registry.CloseSessionServer(connectionSessionID, serverName)
}

func deriveOAuthRedirectURI(r *http.Request) string {
	if publicURL := os.Getenv("PUBLIC_URL"); publicURL != "" {
		return fmt.Sprintf("%s/api/oauth/callback", strings.TrimRight(publicURL, "/"))
	}

	scheme := "http"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = strings.TrimSpace(strings.Split(proto, ",")[0])
	} else if r.TLS != nil {
		scheme = "https"
	}

	host := r.Host
	if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
		host = strings.TrimSpace(strings.Split(forwardedHost, ",")[0])
	}

	return fmt.Sprintf("%s://%s/api/oauth/callback", scheme, host)
}

// deriveOAuthRedirectURIFromEnv is deriveOAuthRedirectURI's PUBLIC_URL-only
// half, for a caller with no *http.Request to fall back on (the
// install_mcp_server agent tool). Empty means the caller should tell the
// user to connect from the UI instead — the request-based fallback exists
// specifically for local dev, which the tool path has no equivalent for.
func deriveOAuthRedirectURIFromEnv() string {
	publicURL := os.Getenv("PUBLIC_URL")
	if publicURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/api/oauth/callback", strings.TrimRight(publicURL, "/"))
}

// OAuthLoginRequest represents a request to start OAuth flow
type OAuthLoginRequest struct {
	SessionID    string `json:"session_id,omitempty"`
	Scope        string `json:"scope,omitempty"`
	ConnectionID string `json:"connection_id,omitempty"`
	ServerName   string `json:"server_name"`
	ClientID     string `json:"client_id,omitempty"` // User-provided client_id for servers without DCR
	// ClientSecret goes with ClientID for providers whose OAuth apps are
	// confidential clients (Google, GitHub).
	ClientSecret string `json:"client_secret,omitempty"`
}

// MCPConnectRequest represents a request to connect a server. APIKey is optional
// and only meaningful for servers with no oauth block.
type MCPConnectRequest struct {
	Scope        string `json:"scope,omitempty"`
	ConnectionID string `json:"connection_id,omitempty"`
	ServerName   string `json:"server_name"`
	APIKey       string `json:"api_key,omitempty"`
}

// OAuthDiscoveryResponse is returned when the server doesn't support DCR and needs a client_id
type OAuthDiscoveryResponse struct {
	// It deliberately carries no auth_url: clients open any auth_url they are
	// handed, and an authorize URL without a client_id only shows the person
	// the provider's "app ID is invalid" page (Vercel, PLAT-708).
	Status          string   `json:"status"` // "needs_client_id"
	ServerName      string   `json:"server_name"`
	Resource        string   `json:"resource,omitempty"`         // RFC 8707 resource indicator
	ScopesSupported []string `json:"scopes_supported,omitempty"` // Discovered scopes
	Message         string   `json:"message"`
	// RedirectURI is the callback to register on the OAuth app.
	RedirectURI       string `json:"redirect_uri,omitempty"`
	NeedsClientSecret bool   `json:"needs_client_secret,omitempty"`
}

// OAuthStartResponse represents the response when starting OAuth flow
type OAuthStartResponse struct {
	ServerName string `json:"server_name"`
	AuthURL    string `json:"auth_url"`
	State      string `json:"state"`
	Message    string `json:"message"`
}

// OAuthStatusResponse represents the OAuth token status
type OAuthStatusResponse struct {
	ServerName string `json:"server_name"`
	Valid      bool   `json:"valid"`
	ExpiresIn  string `json:"expires_in"`
	TokenPath  string `json:"token_path"`
}

// OAuthLogoutRequest represents a request to logout (remove token)
type OAuthLogoutRequest struct {
	Scope        string `json:"scope,omitempty"`
	ConnectionID string `json:"connection_id,omitempty"`
	ServerName   string `json:"server_name"`
}

// handleOAuthCallback handles GET /api/oauth/callback - receives OAuth authorization code
func (api *StreamingAPI) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Query().Get("state"), gmailSetupStatePrefix) {
		api.gmailSetupCallback(w, r)
		return
	}
	api.logger.Info(fmt.Sprintf("🔔 OAuth callback received: state=%s, code_present=%v, error=%s",
		r.URL.Query().Get("state"), r.URL.Query().Get("code") != "", r.URL.Query().Get("error")))

	query := r.URL.Query()

	// Get state parameter
	state := query.Get("state")
	if state == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head><title>OAuth Error</title></head>
<body style="font-family: Arial, sans-serif; text-align: center; padding: 50px;">
	<h1>❌ Invalid Request</h1>
	<p>Missing state parameter</p>
	<p>You can close this window.</p>
</body>
</html>`)
		return
	}

	// A Google account sign-in through the deployment's Google app returns here too (one
	// redirect URI registered on the client); hand it to the Gmail completion.
	if services.HasPendingGmailOAuthState(state) {
		gmailOAuthCallbackHandler(api)(w, r)
		return
	}

	// Find the OAuth flow
	oauthFlowsMu.RLock()
	flow, exists := oauthFlows[state]
	oauthFlowsMu.RUnlock()

	if !exists {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head><title>OAuth Error</title></head>
<body style="font-family: Arial, sans-serif; text-align: center; padding: 50px;">
	<h1>❌ Invalid or Expired State</h1>
	<p>OAuth flow not found or expired</p>
	<p>You can close this window and try again.</p>
</body>
</html>`)
		return
	}

	// Check for error from OAuth provider
	if errCode := query.Get("error"); errCode != "" {
		errDesc := query.Get("error_description")
		if errDesc == "" {
			errDesc = errCode
		}

		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head><title>Authentication Failed</title></head>
<body style="font-family: Arial, sans-serif; text-align: center; padding: 50px;">
	<h1>❌ Authentication Failed</h1>
	<p>%s</p>
	<p>You can close this window.</p>
</body>
</html>`, errDesc)

		// Send error to flow
		select {
		case flow.ErrChan <- fmt.Errorf("OAuth error: %s - %s", errCode, errDesc):
		default:
		}
		return
	}

	// Get authorization code
	code := query.Get("code")
	if code == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head><title>OAuth Error</title></head>
<body style="font-family: Arial, sans-serif; text-align: center; padding: 50px;">
	<h1>❌ Missing Authorization Code</h1>
	<p>No authorization code received</p>
	<p>You can close this window.</p>
</body>
</html>`)
		return
	}

	// Send code to flow
	select {
	case flow.CodeChan <- code:
		// Success - show nice page
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
	<title>Authentication Successful</title>
	<style>
		body {
			font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
			display: flex;
			align-items: center;
			justify-content: center;
			min-height: 100vh;
			margin: 0;
			background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%);
		}
		.container {
			background: white;
			border-radius: 16px;
			padding: 48px;
			box-shadow: 0 20px 60px rgba(0,0,0,0.3);
			text-align: center;
			max-width: 400px;
		}
		.success-icon {
			font-size: 64px;
			margin-bottom: 24px;
		}
		h1 {
			color: #2d3748;
			margin: 0 0 16px 0;
			font-size: 24px;
		}
		p {
			color: #718096;
			margin: 0;
			font-size: 16px;
		}
	</style>
</head>
<body>
	<div class="container">
		<div class="success-icon">✅</div>
		<h1>Authentication Successful!</h1>
		<p>You can close this window and return to the application.</p>
	</div>
</body>
</html>`)
	default:
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head><title>OAuth Error</title></head>
<body style="font-family: Arial, sans-serif; text-align: center; padding: 50px;">
	<h1>❌ Internal Error</h1>
	<p>Failed to process authorization code</p>
	<p>You can close this window and try again.</p>
</body>
</html>`)
	}
}

// handleOAuthStart handles POST /api/oauth/start - initiates OAuth flow
// oauthStartError carries the HTTP status handleOAuthStart historically used
// for each failure case, so the extraction below preserves exact prior
// response codes even though beginOAuthFlow itself is transport-agnostic.
type oauthStartError struct {
	status  int
	message string
}

func (e *oauthStartError) Error() string { return e.message }

// beginOAuthFlow is the transport-independent core of starting an MCP OAuth
// connection: resolve the server's OAuth config, run DCR when needed,
// generate the authorization URL, register the pending flow, and start the
// background goroutine that completes it on callback (handleOAuthCallback).
// Both handleOAuthStart (HTTP, used by the connector-directory UI) and the
// install_mcp_server agent tool call this, so the two paths cannot diverge.
//
// Returns exactly one of: a discovery response (the server needs a manually
// registered client_id — no Dynamic Client Registration support), a start
// response (authURL to open), or an error.
//
// onInstalled, if set, runs after the credential is actually persisted —
// i.e. once the user has completed the browser consent step, not when this
// function returns the authURL. The connector-directory UI already refreshes
// itself on its own button-click handler, so handleOAuthStart passes nil;
// install_mcp_server passes a callback that nudges the mcp workspace view,
// since chat has no other way to learn the flow finished.
// sessionID is the chat session that triggered this install, if any — used
// only to report completion/failure back into that conversation once the
// background wait below resolves (empty when this was started from the
// connector-directory UI instead of chat, which has no synthetic-turn target).
func (api *StreamingAPI) beginOAuthFlow(userID, sessionID, serverName, redirectURI, clientID, clientSecret string, onInstalled func()) (*OAuthStartResponse, *OAuthDiscoveryResponse, error) {
	api.logger.Info(fmt.Sprintf("🔐 Platform OAuth start for server %s, initiated_by %s", serverName, userID))

	if _, err := ensureUserTokenDir(platformMCPTokenUserID); err != nil {
		api.logger.Error(fmt.Sprintf("Failed to create platform token directory: %v", err), err)
		return nil, nil, &oauthStartError{http.StatusInternalServerError, "Failed to create token directory"}
	}

	// Load server config
	config, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		api.logger.Error(fmt.Sprintf("Failed to load config: %v", err), err)
		return nil, nil, &oauthStartError{http.StatusInternalServerError, "Failed to load server config"}
	}

	canonicalName, serverConfig, err := config.ResolveServer(serverName)
	if err != nil {
		return nil, nil, &oauthStartError{http.StatusNotFound, fmt.Sprintf("Server '%s' not found", serverName)}
	}

	serverName = canonicalName

	platformTokenFile := getUserTokenFilePath(platformMCPTokenUserID, serverName)

	// Apply user-provided client_id if present (from the "needs_client_id" flow)
	if clientID != "" {
		if serverConfig.OAuth == nil {
			return nil, nil, &oauthStartError{http.StatusBadRequest, fmt.Sprintf("Server '%s' has no oauth configuration; a client_id is not applicable", serverName)}
		}
		if clientID != serverConfig.OAuth.ClientID {
			serverConfig.OAuth.ClientSecret = ""
			serverConfig.OAuth.ClientSecretFile = ""
		}
		serverConfig.OAuth.ClientID = clientID
		if strings.TrimSpace(clientSecret) != "" {
			serverConfig.OAuth.ClientSecret = strings.TrimSpace(clientSecret)
		}
		api.logger.Info(fmt.Sprintf("Using user-provided client_id for %s: %s", serverName, clientID))
	}

	// No client entered and none configured: use the deployment's sign-in app
	// for this provider (Google, GitHub, Slack, ...) when an admin set one up,
	// so the shared connect is one click like a personal one.
	if clientID == "" && serverConfig.OAuth != nil && strings.TrimSpace(serverConfig.OAuth.ClientID) == "" {
		if key := mcpAppKeyIndex(config.MCPServers)[serverName]; key != "" {
			if app, appErr := readMCPApp(key); appErr == nil && app != nil {
				serverConfig.OAuth.ClientID, serverConfig.OAuth.ClientSecret, serverConfig.OAuth.ClientSecretFile = app.ClientID, app.ClientSecret, ""
				api.logger.Info(fmt.Sprintf("Using the %s sign-in app for platform server %s", key, serverName))
			}
		}
	}

	// The oauth block is the sole authority. Its absence means the server is
	// open, not that endpoints need discovering. Probing here is what used to
	// report false positives: a well-known lookup built from scheme+host alone
	// found the *website's* OAuth metadata for servers that have none.
	if serverConfig.OAuth == nil {
		return nil, nil, &oauthStartError{http.StatusBadRequest, fmt.Sprintf("Server '%s' is not an OAuth server", serverName)}
	}

	serverConfig.OAuth.TokenFile = platformTokenFile

	// Always use the current callback URL for a new flow. User configs can
	// contain stale localhost ports from previous runs, which otherwise get
	// sent to the OAuth provider and make the callback unreachable.
	serverConfig.OAuth.RedirectURL = redirectURI

	// Endpoints come from config. Fail loudly rather than falling back to a probe.
	if serverConfig.OAuth.AuthURL == "" || serverConfig.OAuth.TokenURL == "" {
		api.logger.Error(fmt.Sprintf("Server %s is missing auth_url or token_url in config", serverName), nil)
		return nil, nil, &oauthStartError{http.StatusInternalServerError, fmt.Sprintf("Server '%s' is missing auth_url or token_url in its oauth config. Endpoints are not discovered at runtime; copy authorization_endpoint and token_endpoint from the provider's /.well-known/oauth-authorization-server metadata into the MCP config.", serverName)}
	}
	if requiresRegisteredMCPClientSecret(serverName) && (serverConfig.OAuth.ClientID == "" || !hasRegisteredMCPClientSecret(serverConfig.OAuth)) {
		return nil, &OAuthDiscoveryResponse{
			Status: "needs_client_id", ServerName: serverName,
			Resource: serverConfig.OAuth.Resource, NeedsClientSecret: true,
			RedirectURI: redirectURI,
			Message:     "Create an OAuth app with this AgentWorks server's /api/oauth/callback redirect URL, then enter its client ID and secret.",
		}, nil
	}

	return api.runOAuthFlow(sessionID, redirectURI, oauthFlowTarget{
		Name:       serverName,
		Config:     serverConfig,
		ClientFile: expandPath(getUserClientFilePath(platformMCPTokenUserID, serverName)),
		OnSuccess: func(flow *OAuthFlowState) {
			// Persist the platform OAuth config so every AgentWorks product resolves it.
			api.logger.Info(fmt.Sprintf("💾 Persisting OAuth config for %s", serverName))
			if err := api.persistOAuthConfig(serverName, flow.ServerConfig); err != nil {
				api.logger.Error(fmt.Sprintf("Failed to persist OAuth config for %s: %v", serverName, err), err)
			} else {
				api.logger.Info(fmt.Sprintf("✅ OAuth config persisted for %s", serverName))
				if onInstalled != nil {
					onInstalled()
				}
			}

			// Reauthorization replaces the retained platform connection.
			closePlatformMCPConnection(serverName)

			// Invalidate cache for this server so tools are re-discovered with OAuth token
			api.logger.Info(fmt.Sprintf("🔄 Invalidating cache for %s to refresh tools with OAuth", serverName))
			cacheManager := mcpcache.GetCacheManager(api.logger)
			if err := cacheManager.InvalidateByServer(api.mcpConfigPath, serverName); err != nil {
				api.logger.Warn(fmt.Sprintf("Failed to invalidate cache for %s: %v", serverName, err))
			} else {
				api.logger.Info(fmt.Sprintf("✅ Cache invalidated for %s - tools will be refreshed on next request", serverName))
			}

			// Also invalidate the in-memory tool status cache
			api.toolStatusMux.Lock()
			delete(api.toolStatus, serverName)
			api.toolStatusMux.Unlock()
			api.logger.Info(fmt.Sprintf("✅ In-memory tool status cleared for %s", serverName))

			// OAuth success means prior auth-related discovery failures are no
			// longer permanent. Clear the skip marker and rediscover tools now,
			// otherwise /api/tools returns "loading" forever and the frontend keeps
			// polling.
			api.clearDiscoveryFailure(serverName)
			api.appendServerLog(serverName, "info", "Authentication succeeded, rediscovering tools...")
			api.startServerDiscovery(userID, serverName)
		},
	})
}

// oauthFlowTarget is what one OAuth connect needs besides the shared flow: the
// server (its config already carries the token file), where a dynamic client
// registration is cached, the HTTP client for discovery and registration
// (public-only for a personal server), and what to do once connected.
type oauthFlowTarget struct {
	Name           string
	DisplayName    string // what a person calls the app ("Vercel"); Name when empty
	Config         mcpclient.MCPServerConfig
	ClientFile     string
	Discoverer     oauth.Discoverer
	OnSuccess      func(flow *OAuthFlowState)
	ConnectionID   string
	LockKey        string
	BeforeExchange func(*OAuthFlowState) error
	AfterExchange  func(*OAuthFlowState) error
	Notify         func(bool, string)
}

// runOAuthFlow is the OAuth connect shared by platform and personal servers:
// dynamic client registration, the authorization URL, and the callback wait
// and token exchange in the background.
func (api *StreamingAPI) runOAuthFlow(sessionID, redirectURI string, target oauthFlowTarget) (*OAuthStartResponse, *OAuthDiscoveryResponse, error) {
	serverName := target.Name
	serverConfig := target.Config
	outcome := func(success bool, detail string) {
		if target.Notify != nil {
			target.Notify(success, detail)
		} else {
			api.notifyOAuthFlowOutcome(sessionID, serverName, success, detail)
		}
	}

	// A server with no client_id in config either issues one through Dynamic
	// Client Registration or needs one registered by hand. Try DCR first, so
	// only the genuinely manual servers reach the prompt below.
	var regErr error
	if serverConfig.OAuth.ClientID == "" && serverConfig.OAuth.RegistrationEndpoint != "" {
		var client *registeredClient
		client, regErr = api.ensureRegisteredClientAt(target.ClientFile, target.Discoverer, serverName, serverConfig.OAuth.RegistrationEndpoint, redirectURI)
		if regErr != nil {
			// Fall through to the prompt: a hand-registered client_id still works.
			api.logger.Error(fmt.Sprintf("Dynamic client registration failed for %s (redirect %s): %v", serverName, redirectURI, regErr), regErr)
			// A provider's rejection (not a network error) marks the app "Needs admin setup" in Vault's Add app.
			var rejected *oauth.RegistrationError
			if errors.As(regErr, &rejected) {
				setRegistrationFailure(serverConfig.OAuth.RegistrationEndpoint, redirectURI, rejected.Reason())
			}
		} else {
			setRegistrationFailure(serverConfig.OAuth.RegistrationEndpoint, redirectURI, "")
			serverConfig.OAuth.ClientID = client.ClientID
			serverConfig.OAuth.ClientSecret = client.ClientSecret
			api.logger.Info(fmt.Sprintf("🪪 Using DCR client_id for %s: %s", serverName, client.ClientID))
		}
	}

	// No client: never build an authorize URL. Return a prompt for a
	// hand-registered client_id, saying why automatic setup failed.
	if serverConfig.OAuth.ClientID == "" {
		api.logger.Info(fmt.Sprintf("No client_id for %s, returning needs_client_id response", serverName))
		message := fmt.Sprintf("Server '%s' does not support Dynamic Client Registration. Please provide your OAuth App client ID (and client secret, if the provider issued one).", serverName)
		if regErr != nil {
			display := target.DisplayName
			if display == "" {
				display = serverName
			}
			message = registrationFailedMessage(display, regErr)
		}
		return nil, &OAuthDiscoveryResponse{
			Status:            "needs_client_id",
			ServerName:        serverName,
			Resource:          serverConfig.OAuth.Resource,
			ScopesSupported:   serverConfig.OAuth.Scopes,
			Message:           message,
			RedirectURI:       redirectURI,
			NeedsClientSecret: requiresRegisteredMCPClientSecret(serverName),
		}, nil
	}

	// Create OAuth manager with the fully configured OAuth settings
	oauthMgr := oauth.NewManager(serverConfig.OAuth, api.logger)

	// Generate state and authorization URL using the manager's helper
	state, authURL, err := oauthMgr.GenerateAuthURL()
	if err != nil {
		api.logger.Error(fmt.Sprintf("Failed to generate auth URL for %s: %v", serverName, err), err)
		return nil, nil, &oauthStartError{http.StatusInternalServerError, fmt.Sprintf("Failed to generate authorization URL: %v", err)}
	}

	// Log the config being stored in flow
	api.logger.Info(fmt.Sprintf("🔐 Storing OAuth config in flow for %s: AuthURL=%s, TokenURL=%s, TokenFile=%s",
		serverName, serverConfig.OAuth.AuthURL, serverConfig.OAuth.TokenURL, serverConfig.OAuth.TokenFile))

	// Register the OAuth flow state
	flow := &OAuthFlowState{
		ConnectionID: target.ConnectionID,
		ServerName:   serverName,
		State:        state,
		CodeChan:     make(chan string, 1),
		ErrChan:      make(chan error, 1),
		Manager:      oauthMgr,
		ServerConfig: serverConfig, // Store server config for persistence after OAuth success
	}

	oauthFlowsMu.Lock()
	oauthFlows[state] = flow
	oauthFlowsMu.Unlock()

	// Clean up flow state after timeout
	go func() {
		time.Sleep(5 * time.Minute)
		oauthFlowsMu.Lock()
		delete(oauthFlows, state)
		oauthFlowsMu.Unlock()
	}()

	finish := func(success bool, detail string) {
		oauthFlowsMu.Lock()
		if success {
			flow.Outcome = "completed"
		} else {
			flow.Outcome = "failed"
		}
		oauthFlowsMu.Unlock()
		outcome(success, detail)
	}
	// Start OAuth flow in background goroutine
	go func() {
		// Recover from panics so the goroutine doesn't die silently
		defer func() {
			if r := recover(); r != nil {
				api.logger.Error(fmt.Sprintf("🔥 PANIC in OAuth goroutine for %s: %v", serverName, r), fmt.Errorf("%v", r))
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		api.logger.Info(fmt.Sprintf("⏳ Waiting for OAuth callback for %s (state: %s)", serverName, state))

		// Wait for authorization code from callback
		var code string
		select {
		case code = <-flow.CodeChan:
			api.logger.Info(fmt.Sprintf("📥 Received authorization code for %s (code length: %d)", serverName, len(code)))
		case err := <-flow.ErrChan:
			api.logger.Error(fmt.Sprintf("OAuth flow failed for %s: %v", serverName, err), err)
			finish(false, err.Error())
			return
		case <-ctx.Done():
			api.logger.Error(fmt.Sprintf("OAuth flow timed out for %s", serverName), ctx.Err())
			finish(false, "the user did not complete authorization within 5 minutes")
			return
		}

		// Exchange code for token
		api.logger.Info(fmt.Sprintf("🔄 Exchanging authorization code for token for %s (redirect_uri: %s, token_url: %s)",
			serverName, oauthMgr.GetRedirectURI(), oauthMgr.GetTokenURL()))
		lockKey := target.LockKey
		if lockKey == "" {
			lockKey = serverName
		}
		mutex := platformMCPOAuthMutex(lockKey)
		mutex.Lock()
		if target.BeforeExchange != nil {
			if err := target.BeforeExchange(flow); err != nil {
				mutex.Unlock()
				finish(false, err.Error())
				return
			}
		}
		token, err := oauthMgr.ExchangeCodeForToken(ctx, code)
		mutex.Unlock()
		if err != nil {
			api.logger.Error(fmt.Sprintf("❌ Failed to exchange code for token for %s: %v", serverName, err), err)
			finish(false, fmt.Sprintf("token exchange failed: %v", err))
			return
		}

		api.logger.Info(fmt.Sprintf("✅ OAuth token obtained for %s, expires: %s, has_refresh: %v",
			serverName, token.Expiry, token.RefreshToken != ""))

		if target.AfterExchange != nil {
			if err := target.AfterExchange(flow); err != nil {
				finish(false, err.Error())
				return
			}
		}
		if target.OnSuccess != nil {
			target.OnSuccess(flow)
		}
		finish(true, "")
	}()

	return &OAuthStartResponse{
		ServerName: serverName,
		AuthURL:    authURL,
		State:      state,
		Message:    "Please authorize in your browser",
	}, nil, nil
}

// notifyOAuthFlowOutcome closes the gap where an OAuth install that started
// from chat finished (or failed/timed out) minutes later, in a background
// goroutine, with no way to tell the conversation: install_mcp_server had
// already returned "give the user this link" long before this runs. Injects
// a synthetic turn so the resident agent for sessionID actually tells the
// user, instead of the outcome only ever being visible in server logs.
// No-op when sessionID is "" (the caller did not bind a chat session).
func (api *StreamingAPI) notifyOAuthFlowOutcome(sessionID, serverName string, success bool, detail string) {
	if sessionID == "" {
		return
	}
	status := "completed"
	message := fmt.Sprintf("The OAuth connection to MCP server %q finished successfully and the token was saved. Tool discovery is running; verify discovery or a live tool call before saying it is ready. It still needs project selection: use update_workflow_config(add_servers=[%q]) for a workflow, or update_project_mcp_server_selection(action=select, server=%q) for an active Work project.", serverName, serverName, serverName)
	if !success {
		status = "failed"
		message = fmt.Sprintf("The OAuth connection to MCP server %q did not complete: %s. Tell the user and offer to retry (they can ask you to start the connection again).", serverName, detail)
	}
	api.emitSyntheticTurnReady(sessionID, "oauth:"+serverName, serverName, status, message)
	if api.executeSyntheticTurn(sessionID, message) {
		return
	}
	// The session was mid-turn (or its agent hadn't been stored yet) at the
	// exact moment this fired. One bounded retry covers the common case of a
	// user's own message still being processed when authorization completes;
	// this is a one-shot event, not a durable queue, so no further retries.
	time.AfterFunc(10*time.Second, func() {
		if !api.executeSyntheticTurn(sessionID, message) {
			api.logger.Warn(fmt.Sprintf("Could not deliver OAuth %s notification for %s into session %s: no reachable synthetic-turn target", status, serverName, sessionID))
		}
	})
}

func (api *StreamingAPI) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	var req OAuthLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.ServerName == "" {
		http.Error(w, "server_name is required", http.StatusBadRequest)
		return
	}

	if req.Scope == "" || req.Scope == "private" {
		api.startPrivateOAuth(w, r, req)
		return
	}
	if !vaultOAuthScope(w, r, req.Scope) {
		return
	}

	// The caller identity is audit provenance; the saved connection is shared.
	userID := GetUserIDFromContext(r.Context())
	// Derive redirect URI from PUBLIC_URL env var (for production) or incoming request (for local)
	redirectURI := deriveOAuthRedirectURI(r)

	// UI sign-in binds completion to its initiating conversation. Never accept
	// another user's session as a notification target.
	sessionID, sessionErr := api.oauthNotificationSession(r, req.SessionID)
	if sessionErr != nil {
		http.Error(w, "chat session not found or access denied", http.StatusForbidden)
		return
	}
	var startResp *OAuthStartResponse
	var discoveryResp *OAuthDiscoveryResponse
	var err error
	if req.ConnectionID != "" {
		startResp, discoveryResp, err = api.beginVaultConnectionOAuth(r.Context(), userID, sessionID, req.ConnectionID, req.ServerName, redirectURI, req.ClientID, req.ClientSecret)
	} else {
		startResp, discoveryResp, err = api.beginOAuthFlow(userID, sessionID, req.ServerName, redirectURI, req.ClientID, req.ClientSecret, nil)
	}
	if err != nil {
		status := http.StatusInternalServerError
		var se *oauthStartError
		if errors.As(err, &se) {
			status = se.status
		}
		http.Error(w, err.Error(), status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if discoveryResp != nil {
		json.NewEncoder(w).Encode(discoveryResp)
		return
	}
	json.NewEncoder(w).Encode(startResp)
}

// handleOAuthStatus handles GET /api/oauth/status/:server_name - get token status
func (api *StreamingAPI) handleOAuthStatus(w http.ResponseWriter, r *http.Request) {
	serverName := r.URL.Query().Get("server_name")
	if serverName == "" {
		http.Error(w, "server_name query parameter is required", http.StatusBadRequest)
		return
	}

	scope := r.URL.Query().Get("scope")
	if scope == "" || scope == "private" {
		privateOAuthStatus(w, r, serverName)
		return
	}
	if !vaultOAuthScope(w, r, scope) {
		return
	}

	if id := r.URL.Query().Get("connection_id"); id != "" {
		api.vaultConnectionOAuthStatus(w, r, id, serverName)
		return
	}
	api.logger.Info(fmt.Sprintf("🔍 Platform OAuth status check for server %s", serverName))

	// Load server config
	config, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		api.logger.Error(fmt.Sprintf("Failed to load config: %v", err), err)
		http.Error(w, "Failed to load server config", http.StatusInternalServerError)
		return
	}

	serverConfig, err := config.GetServer(serverName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Server '%s' not found", serverName), http.StatusNotFound)
		return
	}

	// A server with no oauth block is open, not undiscovered. Report it as such
	// instead of probing — this is what made open servers like Exa return 500.
	if serverConfig.OAuth == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"server_name":   serverName,
			"has_oauth":     false,
			"valid":         false,
			"authenticated": false,
		})
		return
	}

	// Existing overlay entries retain their saved path, promoting current
	// connections without copying secrets. New connections use _platform.
	if strings.TrimSpace(serverConfig.OAuth.TokenFile) == "" {
		serverConfig.OAuth.TokenFile = getUserTokenFilePath(platformMCPTokenUserID, serverName)
	}

	// Token refresh uses the configured token_url; there is no discovery fallback.

	// Log the OAuth config for debugging
	api.logger.Info(fmt.Sprintf("📋 OAuth status check for %s - Config: AuthURL=%s, TokenURL=%s, TokenFile=%s",
		serverName, serverConfig.OAuth.AuthURL, serverConfig.OAuth.TokenURL, serverConfig.OAuth.TokenFile))

	mutex := platformMCPOAuthMutex(serverName)
	mutex.Lock()
	defer mutex.Unlock()

	// Get token status - this also attempts token refresh if expired
	oauthMgr := oauth.NewManager(serverConfig.OAuth, api.logger)

	// Try to get a valid access token (this will attempt refresh if expired)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = oauthMgr.GetAccessToken(ctx)
	tokenRefreshed := err == nil

	if err != nil {
		api.logger.Info(fmt.Sprintf("⚠️ OAuth token refresh failed for %s: %v", serverName, err))
	} else {
		api.logger.Info(fmt.Sprintf("✅ OAuth token valid/refreshed for %s", serverName))
	}

	valid, expiresIn, _ := oauthMgr.GetTokenStatus()

	// If token refresh succeeded, the status should now be valid
	if tokenRefreshed && !valid {
		// Re-check status after refresh
		valid, expiresIn, _ = oauthMgr.GetTokenStatus()
	}

	api.logger.Info(fmt.Sprintf("📊 OAuth status result for %s: valid=%v, expiresIn=%s", serverName, valid, expiresIn))

	response := OAuthStatusResponse{
		ServerName: serverName,
		Valid:      valid,
		ExpiresIn:  expiresIn,
		// Never disclose a deployment credential path to ordinary users.
		TokenPath: "",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleOAuthLogout handles POST /api/oauth/logout - remove OAuth token
func (api *StreamingAPI) handleOAuthLogout(w http.ResponseWriter, r *http.Request) {
	var req OAuthLogoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.ServerName == "" {
		http.Error(w, "server_name is required", http.StatusBadRequest)
		return
	}

	if req.Scope == "" || req.Scope == "private" {
		disconnectPrivateCatalog(w, r, req.ServerName)
		return
	}
	if !vaultOAuthScope(w, r, req.Scope) {
		return
	}

	if req.ConnectionID != "" {
		if err := api.logoutVaultConnection(r.Context(), GetUserIDFromContext(r.Context()), req.ConnectionID, req.ServerName); err != nil {
			writeUsersError(w, 400, err.Error())
			return
		}
		writeUsersJSON(w, 200, map[string]string{"status": "disconnected"})
		return
	}
	userID := GetUserIDFromContext(r.Context())
	api.logger.Info(fmt.Sprintf("🔐 Platform OAuth logout for server %s, initiated_by %s", req.ServerName, userID))

	// Load server config
	config, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		api.logger.Error(fmt.Sprintf("Failed to load config: %v", err), err)
		http.Error(w, "Failed to load server config", http.StatusInternalServerError)
		return
	}

	serverConfig, err := config.GetServer(req.ServerName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Server '%s' not found", req.ServerName), http.StatusNotFound)
		return
	}

	// No oauth block means there is no token to remove.
	if serverConfig.OAuth == nil {
		http.Error(w, fmt.Sprintf("Server '%s' is not an OAuth server", req.ServerName), http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(serverConfig.OAuth.TokenFile) == "" {
		serverConfig.OAuth.TokenFile = getUserTokenFilePath(platformMCPTokenUserID, req.ServerName)
	}

	// Logout removes the shared platform token.
	oauthMgr := oauth.NewManager(serverConfig.OAuth, api.logger)
	mutex := platformMCPOAuthMutex(req.ServerName)
	mutex.Lock()
	err = oauthMgr.Logout()
	mutex.Unlock()
	if err != nil {
		api.logger.Error(fmt.Sprintf("Failed to logout from %s: %v", req.ServerName, err), err)
		http.Error(w, fmt.Sprintf("Failed to logout: %v", err), http.StatusInternalServerError)
		return
	}

	api.logger.Info(fmt.Sprintf("Successfully disconnected platform MCP %s by user %s", req.ServerName, userID))

	// Removing the token alone would leave the overlay entry behind, which under
	// the connection rule (§3) still reads as connected. Drop both.
	if err := api.removeOverlayEntry(req.ServerName); err != nil {
		api.logger.Warn(fmt.Sprintf("Failed to remove overlay entry for %s: %v", req.ServerName, err))
	}

	closePlatformMCPConnection(req.ServerName)
	api.invalidateServerDiscovery(req.ServerName, "Disconnected — token removed, rediscovering tools...")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": fmt.Sprintf("Successfully logged out from %s", req.ServerName),
	})
}

// removeOverlayEntry deletes a server from the platform config overlay.
// Overlay membership defines connection state, so this is what disconnects it.
func (api *StreamingAPI) removeOverlayEntry(serverName string) error {
	userConfigPath := api.getUserConfigPath()
	userConfig, err := mcpclient.LoadConfig(userConfigPath, api.logger)
	if err != nil {
		// Nothing to remove — an absent overlay already means "not connected".
		api.logger.Debug(fmt.Sprintf("No user config overlay to remove %s from: %v", serverName, err))
		return nil
	}
	if _, exists := userConfig.MCPServers[serverName]; !exists {
		return nil
	}
	delete(userConfig.MCPServers, serverName)
	if err := saveUserMCPOverlay(userConfigPath, userConfig); err != nil {
		return fmt.Errorf("failed to save user config after removing %s: %w", serverName, err)
	}
	api.logger.Info(fmt.Sprintf("🗑️ Removed %s from user config overlay", serverName))
	return nil
}

// invalidateServerDiscovery drops cached discovery without opening connections.
// /api/tools answers from the discovery cache, so a connection change
// that skips this leaves the connector reporting its previous state.
func (api *StreamingAPI) invalidateServerDiscovery(serverName, logMessage string) {
	cacheManager := mcpcache.GetCacheManager(api.logger)
	if err := cacheManager.InvalidateByServer(api.mcpConfigPath, serverName); err != nil {
		api.logger.Warn(fmt.Sprintf("Failed to invalidate cache for %s: %v", serverName, err))
	} else {
		api.logger.Info(fmt.Sprintf("✅ Cache invalidated for %s", serverName))
	}

	api.toolStatusMux.Lock()
	delete(api.toolStatus, serverName)
	api.toolStatusMux.Unlock()

	api.clearDiscoveryFailure(serverName)
	api.appendServerLog(serverName, "info", logMessage)

}

// handleConnectServer connects a server by writing it into the user config
// overlay. OAuth servers are redirected to the authorization flow instead — the
// overlay write happens on callback success.
func (api *StreamingAPI) handleConnectServer(w http.ResponseWriter, r *http.Request) {

	var req MCPConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}
	if req.ServerName == "" {
		http.Error(w, "server_name is required", http.StatusBadRequest)
		return
	}

	if req.Scope == "" || req.Scope == "private" {
		api.connectPrivateCatalog(w, r, req)
		return
	}
	if !vaultOAuthScope(w, r, req.Scope) {
		return
	}
	// The lock protects the shared catalog; private account stores are separate.
	if isMCPConfigLocked() {
		http.Error(w, "MCP configuration is locked by administrator", http.StatusForbidden)
		return
	}

	config, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		api.logger.Error(fmt.Sprintf("Failed to load config: %v", err), err)
		http.Error(w, "Failed to load server config", http.StatusInternalServerError)
		return
	}

	serverConfig, err := config.GetServer(req.ServerName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Server '%s' not found", req.ServerName), http.StatusNotFound)
		return
	}

	// OAuth servers cannot be connected by a config write alone; the caller must
	// run the authorization flow, which persists the overlay entry on success.
	if serverConfig.OAuth != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":      "oauth_required",
			"server_name": req.ServerName,
			"message":     fmt.Sprintf("Server '%s' uses OAuth; start the authorization flow via /api/oauth/start", req.ServerName),
		})
		return
	}

	// An API key is optional for open servers. Stored as a bearer header, which
	// the transport layer already forwards.
	if req.APIKey != "" {
		if serverConfig.Headers == nil {
			serverConfig.Headers = make(map[string]string)
		}
		serverConfig.Headers["Authorization"] = "Bearer " + req.APIKey
		api.logger.Info(fmt.Sprintf("🔑 Stored API key header for %s", req.ServerName))
	}

	// persistOAuthConfig is misnamed but generic: it upserts a whole server entry
	// into the overlay. Writing the merged entry keeps base fields intact, since
	// overlay entries replace base entries wholesale rather than field-by-field.
	if err := api.persistOAuthConfig(req.ServerName, serverConfig); err != nil {
		api.logger.Error(fmt.Sprintf("Failed to persist config for %s: %v", req.ServerName, err), err)
		http.Error(w, fmt.Sprintf("Failed to save connection: %v", err), http.StatusInternalServerError)
		return
	}

	api.invalidateServerDiscovery(req.ServerName, "Connected — discovering tools...")
	api.startServerDiscovery(GetUserIDFromContext(r.Context()), req.ServerName)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":      "connected",
		"server_name": req.ServerName,
		"message":     fmt.Sprintf("Connected to %s", req.ServerName),
	})
}

// handleDisconnectServer removes the caller's private connection by default.
// Explicit Vault scope removes shared credential metadata and requires admin access.
func (api *StreamingAPI) handleDisconnectServer(w http.ResponseWriter, r *http.Request) {

	var req MCPConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}
	if req.ServerName == "" {
		http.Error(w, "server_name is required", http.StatusBadRequest)
		return
	}

	if req.Scope == "" || req.Scope == "private" {
		disconnectPrivateCatalog(w, r, req.ServerName)
		return
	}
	if !vaultOAuthScope(w, r, req.Scope) {
		return
	}
	// The lock protects the shared catalog; private account stores are separate.
	if isMCPConfigLocked() {
		http.Error(w, "MCP configuration is locked by administrator", http.StatusForbidden)
		return
	}

	userID := GetUserIDFromContext(r.Context())
	api.logger.Info(fmt.Sprintf("🔌 Platform disconnect %s initiated_by %s", req.ServerName, userID))

	config, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		api.logger.Error(fmt.Sprintf("Failed to load config: %v", err), err)
		http.Error(w, "Failed to load server config", http.StatusInternalServerError)
		return
	}

	// Remove the token first. A server absent from config has no token to remove,
	// which is not an error — the overlay removal below still needs to run.
	if serverConfig, err := config.GetServer(req.ServerName); err == nil && serverConfig.OAuth != nil {
		if strings.TrimSpace(serverConfig.OAuth.TokenFile) == "" {
			serverConfig.OAuth.TokenFile = getUserTokenFilePath(platformMCPTokenUserID, req.ServerName)
		}
		oauthMgr := oauth.NewManager(serverConfig.OAuth, api.logger)
		if err := oauthMgr.Logout(); err != nil {
			api.logger.Warn(fmt.Sprintf("Failed to remove token for %s: %v", req.ServerName, err))
		}
	}

	if err := api.removeOverlayEntry(req.ServerName); err != nil {
		api.logger.Error(fmt.Sprintf("Failed to disconnect %s: %v", req.ServerName, err), err)
		http.Error(w, fmt.Sprintf("Failed to disconnect: %v", err), http.StatusInternalServerError)
		return
	}

	// Drop the shared live connection as well as the saved token.
	closePlatformMCPConnection(req.ServerName)
	api.invalidateServerDiscovery(req.ServerName, "Disconnected — rediscovering tools...")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":      "disconnected",
		"server_name": req.ServerName,
		"message":     fmt.Sprintf("Disconnected from %s", req.ServerName),
	})
}

// persistOAuthConfig saves the OAuth configuration to the user config file
// This ensures future MCP connections will use the OAuth token
func (api *StreamingAPI) persistOAuthConfig(serverName string, serverConfig mcpclient.MCPServerConfig) error {
	// Get the user config path
	userConfigPath := api.getUserConfigPath()
	api.logger.Info(fmt.Sprintf("💾 Persisting OAuth config to: %s", userConfigPath))

	// Log the OAuth config being persisted
	if serverConfig.OAuth != nil {
		api.logger.Info(fmt.Sprintf("💾 OAuth config for %s: AuthURL=%s, TokenURL=%s, TokenFile=%s, ClientID=%s, Resource=%s",
			serverName, serverConfig.OAuth.AuthURL, serverConfig.OAuth.TokenURL, serverConfig.OAuth.TokenFile, serverConfig.OAuth.ClientID, serverConfig.OAuth.Resource))
	}

	// Load existing user config
	userConfig, err := mcpclient.LoadConfig(userConfigPath, api.logger)
	if err != nil {
		api.logger.Info(fmt.Sprintf("💾 User config doesn't exist, creating new: %v", err))
		// File doesn't exist, create new config
		userConfig = &mcpclient.MCPConfig{
			MCPServers: make(map[string]mcpclient.MCPServerConfig),
		}
	} else {
		api.logger.Info(fmt.Sprintf("💾 Loaded existing user config with %d servers", len(userConfig.MCPServers)))
	}

	// The client secret goes to its sealed file; the overlay keeps a reference.
	if serverConfig.OAuth != nil {
		copied := *serverConfig.OAuth
		if err := sealPlatformClientSecret(serverName, &copied); err != nil {
			return err
		}
		serverConfig.OAuth = &copied
	}
	// Update or add the server with OAuth config
	userConfig.MCPServers[serverName] = serverConfig

	// Save back to user config file
	err = saveUserMCPOverlay(userConfigPath, userConfig)
	if err != nil {
		api.logger.Error(fmt.Sprintf("💾 Failed to save config: %v", err), err)
	} else {
		api.logger.Info(fmt.Sprintf("💾 Successfully saved OAuth config for %s", serverName))
	}
	return err
}

// OAuth client secrets and bearer headers can be present in the overlay.
// The shared mcpclient.SaveConfig writes 0644, so use a private atomic file.
func saveUserMCPOverlay(path string, config *mcpclient.MCPConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcp-oauth-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// getUserConfigPath returns the user config file path (derived from base config path)
func (api *StreamingAPI) getUserConfigPath() string {
	// Replace .json with _user.json
	if len(api.mcpConfigPath) > 5 && api.mcpConfigPath[len(api.mcpConfigPath)-5:] == ".json" {
		return api.mcpConfigPath[:len(api.mcpConfigPath)-5] + "_user.json"
	}
	return api.mcpConfigPath + "_user"
}

// expandPath expands ~ to the user's home directory
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// ensureUserTokenDir creates the user-specific token directory if it doesn't exist
// Returns the expanded path to the user's token directory
func ensureUserTokenDir(userID string) (string, error) {
	tokenDir := filepath.Join(mcpagentTokensRoot(), userID)
	if err := os.MkdirAll(tokenDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create user token directory: %w", err)
	}
	return tokenDir, nil
}

// getUserTokenFilePath returns the token file path for a specific user and server
func getUserTokenFilePath(userID, serverName string) string {
	return filepath.Join(mcpagentTokensRoot(), userID, serverName+".json")
}

// mcpagentTokensRoot is where MCP connector OAuth tokens live. New AgentWorks
// connections use the reserved _platform directory; legacy persisted paths are
// retained during upgrade so credentials do not need to be copied.
// honours XDG_CONFIG_HOME so a host whose ~/.config is not writable by the
// service user (RTS: root-owned, the bootstrap wrote the systemd units there)
// can point the agent at a writable directory -- the same knob cursor-agent
// needed. Falls back to the historical ~/.config/mcpagent/tokens.
func mcpagentTokensRoot() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "mcpagent", "tokens")
	}
	return expandPath("~/.config/mcpagent/tokens")
}

// registeredClient is a client_id issued by Dynamic Client Registration. It is
// cached per user and server so reconnecting reuses the existing registration
// instead of creating a new client on the provider every time.
type registeredClient struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	RedirectURI  string `json:"redirect_uri"`
}

// getUserClientFilePath returns the DCR client file path for a user and server.
// It sits beside the token file so removing a user's token directory clears the
// registration with it.
func getUserClientFilePath(userID, serverName string) string {
	// Same root as the token files (XDG_CONFIG_HOME-aware): on RTS ~/.config is
	// root-owned and only the XDG tree is writable, so a literal ~/.config path
	// would fail to persist the registration there.
	return filepath.Join(mcpagentTokensRoot(), userID, serverName+".client.json")
}

// ensureRegisteredClient returns the DCR client for a server, registering one on
// first use. The redirect URI forms part of a registration, so a callback URL
// that no longer matches forces a fresh registration rather than an
// invalid_redirect_uri failure later in the flow.
func (api *StreamingAPI) ensureRegisteredClient(userID, serverName, registrationEndpoint, redirectURI string) (*registeredClient, error) {
	return api.ensureRegisteredClientAt(expandPath(getUserClientFilePath(userID, serverName)), oauth.Discoverer{}, serverName, registrationEndpoint, redirectURI)
}

// registrationFailedMessage is what a person sees when a provider would not
// issue a client automatically: the provider's own reason and the two ways on.
func registrationFailedMessage(app string, err error) string {
	reason := err.Error()
	var rejected *oauth.RegistrationError
	if errors.As(err, &rejected) {
		reason = "the provider rejected the app registration: " + rejected.Reason()
	}
	return fmt.Sprintf("%s sign-in couldn't be set up automatically: %s. An admin can register an OAuth app for it, or store its API key as a Vault secret.", app, reason)
}

// ensureRegisteredClientAt caches a dynamic client registration in clientFile
// (sealed at rest when a token sealer claims the path, as for personal
// servers) and registers through disc's HTTP client.
func (api *StreamingAPI) ensureRegisteredClientAt(clientFile string, disc oauth.Discoverer, serverName, registrationEndpoint, redirectURI string) (*registeredClient, error) {
	if data, err := oauth.ReadTokenFile(clientFile); err == nil {
		var cached registeredClient
		if json.Unmarshal(data, &cached) == nil && cached.ClientID != "" && cached.RedirectURI == redirectURI {
			return &cached, nil
		}
	}

	resp, err := disc.RegisterClient(registrationEndpoint, redirectURI)
	if err != nil {
		return nil, fmt.Errorf("dynamic client registration failed: %w", err)
	}

	client := &registeredClient{
		ClientID:     resp.ClientID,
		ClientSecret: resp.ClientSecret,
		RedirectURI:  redirectURI,
	}

	data, err := json.Marshal(client)
	if err != nil {
		return nil, fmt.Errorf("failed to encode client registration: %w", err)
	}
	// 0600 (and sealed when claimed): the record can carry a client_secret.
	// WriteTokenFile also creates the directory, so a first connector for a
	// fresh root does not re-register on every connect.
	if err := oauth.WriteTokenFile(clientFile, data); err != nil {
		// The registration itself succeeded, so continue with it and accept
		// re-registering on the next connect.
		api.logger.Error(fmt.Sprintf("Failed to cache client registration for %s: %v", serverName, err), err)
	}

	return client, nil
}
