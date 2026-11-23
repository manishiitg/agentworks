package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/netguard"
)

// Manager orchestrates the OAuth 2.1 authentication flow
type Manager struct {
	config       *OAuthConfig
	logger       loggerv2.Logger
	oauth2Config *oauth2.Config
	tokenStore   *TokenStore

	// PKCE state
	verifier  string
	challenge string
	state     string // CSRF protection
}

// NewManager creates a new OAuth manager with the given configuration
func NewManager(cfg *OAuthConfig, logger loggerv2.Logger) *Manager {
	if logger == nil {
		logger = loggerv2.NewNoop()
	}

	// Set defaults
	cfg.SetDefaults()

	// Create token store
	tokenStore := NewTokenStore(cfg.TokenFile)

	clientSecret := cfg.ClientSecret
	if clientSecret == "" && cfg.ClientSecretFile != "" {
		secret, err := ReadClientSecretFile(cfg.ClientSecretFile)
		if err != nil {
			logger.Warn(fmt.Sprintf("OAuth client secret unavailable (%v); continuing without it", err))
		}
		clientSecret = secret
	}

	// Create oauth2 config (may be incomplete if using auto-discovery)
	oauth2Cfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: clientSecret,
		RedirectURL:  cfg.RedirectURL,
		Scopes:       cfg.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  cfg.AuthURL,
			TokenURL: cfg.TokenURL,
		},
	}

	return &Manager{
		config:       cfg,
		logger:       logger,
		oauth2Config: oauth2Cfg,
		tokenStore:   tokenStore,
	}
}

// requestContext is ctx with the HTTP client every token request must use:
// the public-only client for a PublicOnly config, else ctx unchanged.
func (m *Manager) requestContext(ctx context.Context) (context.Context, error) {
	if !m.config.PublicOnly {
		return ctx, nil
	}
	if err := netguard.CheckURL(m.oauth2Config.Endpoint.TokenURL, false); err != nil {
		return nil, fmt.Errorf("token endpoint refused: %w", err)
	}
	return context.WithValue(ctx, oauth2.HTTPClient, netguard.Client(30*time.Second)), nil
}

// UpdateEndpoints updates the OAuth endpoints (used after auto-discovery)
func (m *Manager) UpdateEndpoints(authURL, tokenURL string) {
	m.config.AuthURL = authURL
	m.config.TokenURL = tokenURL
	m.oauth2Config.Endpoint.AuthURL = authURL
	m.oauth2Config.Endpoint.TokenURL = tokenURL
}

// GenerateAuthURL generates the authorization URL and state for the OAuth flow
func (m *Manager) GenerateAuthURL() (state string, authURL string, err error) {
	// Validate configuration
	if err := m.config.Validate(); err != nil {
		return "", "", err
	}
	// A user-supplied server's authorization URL is opened in the person's
	// browser: https only, never javascript:, data: or an internal host.
	if m.config.PublicOnly {
		if err := netguard.CheckURL(m.oauth2Config.Endpoint.AuthURL, true); err != nil {
			return "", "", fmt.Errorf("authorization URL refused: %w", err)
		}
	}

	// Generate state for CSRF protection
	m.state = generateRandomState()

	// Generate PKCE challenge if enabled
	var authOptions []oauth2.AuthCodeOption
	if m.config.UsePKCE {
		m.verifier, m.challenge = GeneratePKCEPair()
		authOptions = append(authOptions,
			oauth2.SetAuthURLParam("code_challenge", m.challenge),
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		)
	}

	// Add RFC 8707 resource parameter if set
	if m.config.Resource != "" {
		authOptions = append(authOptions, oauth2.SetAuthURLParam("resource", m.config.Resource))
	}
	authOptions = append(authOptions, m.extraAuthOptions()...)

	// Build authorization URL
	authURL = m.oauth2Config.AuthCodeURL(m.state, authOptions...)

	return m.state, authURL, nil
}

// ExchangeCodeForToken exchanges an authorization code for an access token
func (m *Manager) ExchangeCodeForToken(ctx context.Context, code string) (*oauth2.Token, error) {
	m.logger.Info("ExchangeCodeForToken starting",
		loggerv2.String("token_url", m.oauth2Config.Endpoint.TokenURL),
		loggerv2.String("redirect_uri", m.oauth2Config.RedirectURL),
		loggerv2.String("client_id", m.oauth2Config.ClientID),
		loggerv2.String("use_pkce", fmt.Sprintf("%v", m.config.UsePKCE)),
		loggerv2.String("code_length", fmt.Sprintf("%d", len(code))))

	// Exchange authorization code for token
	tokenOptions := []oauth2.AuthCodeOption{}
	if m.config.UsePKCE {
		tokenOptions = append(tokenOptions, oauth2.SetAuthURLParam("code_verifier", m.verifier))
		m.logger.Info("Using PKCE code_verifier",
			loggerv2.String("verifier_length", fmt.Sprintf("%d", len(m.verifier))))
	}

	// Add RFC 8707 resource parameter if set
	if m.config.Resource != "" {
		tokenOptions = append(tokenOptions, oauth2.SetAuthURLParam("resource", m.config.Resource))
	}

	reqCtx, reqErr := m.requestContext(ctx)
	if reqErr != nil {
		return nil, reqErr
	}
	token, err := m.oauth2Config.Exchange(reqCtx, code, tokenOptions...)
	if err != nil {
		m.logger.Error("Token exchange HTTP request failed", err,
			loggerv2.String("token_url", m.oauth2Config.Endpoint.TokenURL))
		return nil, fmt.Errorf("failed to exchange authorization code: %w", err)
	}

	m.logger.Info("Token exchange successful",
		loggerv2.String("token_type", token.TokenType),
		loggerv2.String("expiry", token.Expiry.String()),
		loggerv2.String("has_refresh", fmt.Sprintf("%v", token.RefreshToken != "")))

	// Save token to disk
	m.logger.Info("Saving token to disk",
		loggerv2.String("file_path", m.tokenStore.GetFilePath()))
	if err := m.tokenStore.Save(token); err != nil {
		m.logger.Warn("Failed to save token to disk",
			loggerv2.Error(err),
			loggerv2.String("file_path", m.tokenStore.GetFilePath()))
		// Don't fail the whole flow if we can't save - token is still valid
	} else {
		m.logger.Info("Token saved to disk successfully",
			loggerv2.String("file_path", m.tokenStore.GetFilePath()))
	}

	return token, nil
}

// GetAccessToken retrieves a valid access token from cache or refreshes it
func (m *Manager) GetAccessToken(ctx context.Context) (string, error) {
	m.logger.Info("GetAccessToken called",
		loggerv2.String("token_url", m.oauth2Config.Endpoint.TokenURL),
		loggerv2.String("auth_url", m.oauth2Config.Endpoint.AuthURL))

	// Try to load cached token
	token, err := m.tokenStore.Load()
	if err != nil {
		m.logger.Info("Failed to load token from store", loggerv2.Error(err))
		return "", ErrNoValidToken
	}

	if token.Valid() {
		m.logger.Info("Using cached OAuth token (still valid)",
			loggerv2.String("expires_in", m.tokenStore.ExpiresIn().String()))
		return token.AccessToken, nil
	}

	hasRefreshToken := "no"
	if token.RefreshToken != "" {
		hasRefreshToken = "yes"
	}
	m.logger.Info("Token expired or invalid",
		loggerv2.String("expiry", token.Expiry.String()),
		loggerv2.String("has_refresh_token", hasRefreshToken))

	// Token expired or not found - try to refresh
	if token.RefreshToken != "" {
		m.logger.Info("OAuth token expired, attempting refresh...",
			loggerv2.String("token_url", m.oauth2Config.Endpoint.TokenURL))
		newToken, err := m.refreshToken(ctx, token)
		if err == nil {
			m.logger.Info("Token refreshed successfully")
			return newToken.AccessToken, nil
		}
		m.logger.Warn("Token refresh failed", loggerv2.Error(err))
	}

	// No valid token available
	return "", ErrNoValidToken
}

// StartAuthFlow initiates the interactive OAuth authorization flow
func (m *Manager) StartAuthFlow(ctx context.Context) (*oauth2.Token, error) {
	// Validate configuration
	if err := m.config.Validate(); err != nil {
		return nil, err
	}

	// Generate state for CSRF protection
	m.state = generateRandomState()

	// Generate PKCE challenge if enabled
	var authOptions []oauth2.AuthCodeOption
	if m.config.UsePKCE {
		m.verifier, m.challenge = GeneratePKCEPair()
		authOptions = append(authOptions,
			oauth2.SetAuthURLParam("code_challenge", m.challenge),
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		)
		m.logger.Debug("Generated PKCE challenge")
	}

	// Add RFC 8707 resource parameter if set
	if m.config.Resource != "" {
		authOptions = append(authOptions, oauth2.SetAuthURLParam("resource", m.config.Resource))
	}
	authOptions = append(authOptions, m.extraAuthOptions()...)

	// Extract port from redirect URL, default to 3333 if not specified
	port := 3333
	if m.config.RedirectURL != "" {
		// Parse the redirect URL to extract the port
		if u, err := url.Parse(m.config.RedirectURL); err == nil && u.Port() != "" {
			if p, err := strconv.Atoi(u.Port()); err == nil {
				port = p
			}
		}
	}

	// Start callback server
	callbackServer := NewCallbackServer(port, m.state)

	// Update redirect URL to match callback server
	m.oauth2Config.RedirectURL = callbackServer.GetCallbackURL()

	// Build authorization URL
	authURL := m.oauth2Config.AuthCodeURL(m.state, authOptions...)

	m.logger.Info("Starting OAuth authorization flow",
		loggerv2.String("auth_url", authURL))

	// Open browser
	if err := openBrowser(authURL); err != nil {
		m.logger.Warn("Failed to open browser automatically", loggerv2.Error(err))
		fmt.Printf("\n🔐 Please open this URL in your browser:\n%s\n\n", authURL)
	} else {
		m.logger.Info("Browser opened for authentication")
		fmt.Printf("\n🔐 Opening browser for authentication...\n")
		fmt.Printf("If browser doesn't open, visit:\n%s\n\n", authURL)
	}

	// Wait for callback
	m.logger.Info("Waiting for authorization callback...")
	code, err := callbackServer.WaitForCallback(ctx)
	if err != nil {
		return nil, fmt.Errorf("authorization callback failed: %w", err)
	}

	m.logger.Info("Authorization code received, exchanging for token...")

	// Exchange authorization code for token
	tokenOptions := []oauth2.AuthCodeOption{}
	if m.config.UsePKCE {
		tokenOptions = append(tokenOptions,
			oauth2.SetAuthURLParam("code_verifier", m.verifier),
		)
	}

	// Add RFC 8707 resource parameter if set
	if m.config.Resource != "" {
		tokenOptions = append(tokenOptions, oauth2.SetAuthURLParam("resource", m.config.Resource))
	}

	reqCtx, reqErr := m.requestContext(ctx)
	if reqErr != nil {
		return nil, reqErr
	}
	token, err := m.oauth2Config.Exchange(reqCtx, code, tokenOptions...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCodeExchange, err)
	}

	m.logger.Info("Token obtained successfully",
		loggerv2.String("expires", token.Expiry.String()))

	// Save token to cache
	if err := m.tokenStore.Save(token); err != nil {
		m.logger.Warn("Failed to cache token", loggerv2.Error(err))
	} else {
		m.logger.Debug("Token cached",
			loggerv2.String("file", m.tokenStore.GetFilePath()))
	}

	return token, nil
}

// refreshToken refreshes an expired token using the refresh token
func (m *Manager) refreshToken(ctx context.Context, token *oauth2.Token) (*oauth2.Token, error) {
	reqCtx, err := m.requestContext(ctx)
	if err != nil {
		return nil, err
	}
	tokenSource := m.oauth2Config.TokenSource(reqCtx, token)
	newToken, err := tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTokenExpired, err)
	}

	// Save refreshed token
	if err := m.tokenStore.Save(newToken); err != nil {
		m.logger.Warn("Failed to save refreshed token", loggerv2.Error(err))
	}

	return newToken, nil
}

// Logout removes the cached token
func (m *Manager) Logout() error {
	m.logger.Info("Removing cached OAuth token",
		loggerv2.String("file", m.tokenStore.GetFilePath()))

	if err := m.tokenStore.Delete(); err != nil {
		return fmt.Errorf("failed to logout: %w", err)
	}

	m.logger.Info("Token removed successfully")
	return nil
}

// GetRedirectURI returns the configured redirect URI (for debugging)
func (m *Manager) GetRedirectURI() string {
	return m.oauth2Config.RedirectURL
}

// GetTokenURL returns the configured token endpoint URL (for debugging)
func (m *Manager) GetTokenURL() string {
	return m.oauth2Config.Endpoint.TokenURL
}

// GetTokenStatus returns information about the cached token
func (m *Manager) GetTokenStatus() (valid bool, expiresIn string, filePath string) {
	filePath = m.tokenStore.GetFilePath()
	valid = m.tokenStore.IsValid()

	if valid {
		duration := m.tokenStore.ExpiresIn()
		// Check if this is a "never expires" token (100 years from ExpiresIn)
		if duration >= 99*365*24*time.Hour {
			expiresIn = "never expires"
		} else {
			expiresIn = duration.String()
		}
	} else {
		expiresIn = "expired or not found"
	}

	return valid, expiresIn, filePath
}

// generateRandomState generates a random state parameter for CSRF protection
func generateRandomState() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to simpler random (should never happen)
		return fmt.Sprintf("state-%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// openBrowser attempts to open a URL in the default browser
func openBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url) //nolint:gosec // opening the provider's authorization URL in the user's browser is the feature
	case "linux":
		cmd = exec.Command("xdg-open", url) //nolint:gosec // see above
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url) //nolint:gosec // see above
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return cmd.Start()
}

// extraAuthOptions carries OAuthConfig.ExtraAuthParams into the authorization
// URL, never overriding a parameter the flow itself sets.
func (m *Manager) extraAuthOptions() []oauth2.AuthCodeOption {
	var out []oauth2.AuthCodeOption
	for key, value := range m.config.ExtraAuthParams {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "state", "code_challenge", "code_challenge_method", "code_verifier", "redirect_uri", "client_id",
			"response_type", "response_mode", "scope", "resource", "request", "request_uri", "nonce":
			continue
		}
		out = append(out, oauth2.SetAuthURLParam(key, value))
	}
	return out
}
