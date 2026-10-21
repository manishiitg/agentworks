package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// AuthProvider defines the interface for authentication providers
type AuthProvider interface {
	// Name returns the provider identifier (e.g., "simple", "cognito", "supabase")
	Name() string
	// Type returns the auth type: "credentials" for username/password, "oauth" for OAuth flow
	Type() string
	// GetAuthURL returns the OAuth authorization URL (for OAuth providers)
	// state parameter should be included for CSRF protection
	GetAuthURL(state, redirectURI string) string
	// ExchangeCode exchanges an authorization code for user information (for OAuth providers).
	// state is the CSRF state this flow was started with; providers whose
	// exchange needs flow-scoped secrets (PKCE verifiers) look them up by it.
	ExchangeCode(ctx context.Context, code, redirectURI, state string) (*ExternalUser, error)
	// ValidateCredentials validates username/password (for credentials providers)
	ValidateCredentials(username, password string) (*ExternalUser, error)
	// IsConfigured returns true if the provider has all required configuration
	IsConfigured() bool
}

// ExternalUser represents a user authenticated by an external provider
type ExternalUser struct {
	EmailVerified bool   // Provider-confirmed email, never user-editable display metadata.
	ExternalID    string // ID from external provider
	Email         string
	Username      string
	Provider      string // "cognito", "supabase", "simple"
}

// ProviderInfo represents provider information returned to frontend
type ProviderInfo struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // "credentials" or "oauth"
	AuthURL string `json:"auth_url,omitempty"`
}

// --- Provider Registry ---

var (
	authProviders     map[string]AuthProvider
	authProvidersOnce sync.Once
)

// GetAuthProviders returns all configured auth providers
func GetAuthProviders() map[string]AuthProvider {
	authProvidersOnce.Do(func() {
		authProviders = initializeProviders()
	})
	return authProviders
}

// GetEnabledProviders returns providers that are both configured and enabled
func GetEnabledProviders() []AuthProvider {
	providers := GetAuthProviders()
	enabledNames := getEnabledProviderNames()

	var enabled []AuthProvider
	for _, name := range enabledNames {
		if p, ok := providers[name]; ok && p.IsConfigured() {
			enabled = append(enabled, p)
		}
	}
	return enabled
}

// GetProviderInfoList returns provider info for the frontend
func GetProviderInfoList(baseURL string) []ProviderInfo {
	providers := GetEnabledProviders()
	infos := make([]ProviderInfo, 0, len(providers))

	for _, p := range providers {
		info := ProviderInfo{
			Name: p.Name(),
			Type: p.Type(),
		}
		// For OAuth providers, the frontend will initiate the flow
		// We don't pre-generate auth URLs here since they need state parameter
		infos = append(infos, info)
	}
	return infos
}

// GetProvider returns a specific provider by name
func GetProvider(name string) (AuthProvider, bool) {
	providers := GetAuthProviders()
	p, ok := providers[name]
	return p, ok
}

func getEnabledProviderNames() []string {
	providersEnv := os.Getenv("AUTH_PROVIDERS")
	if providersEnv == "" {
		// Default: if AUTH_USERS is set, enable simple provider
		// Password login is on whenever there is anyone to log in: AUTH_USERS
		// in the environment, or accounts in config/users.json.
		if os.Getenv("AUTH_USERS") != "" || userDirectoryHasPasswordUsers() {
			return []string{"simple"}
		}
		return nil
	}

	names := strings.Split(providersEnv, ",")
	var result []string
	for _, name := range names {
		name = strings.TrimSpace(strings.ToLower(name))
		if name != "" {
			result = append(result, name)
		}
	}
	return result
}

func initializeProviders() map[string]AuthProvider {
	providers := make(map[string]AuthProvider)

	// Initialize Simple provider
	simple := NewSimpleProvider()
	providers["simple"] = simple

	// Initialize Cognito provider
	cognito := NewCognitoProvider()
	providers["cognito"] = cognito

	// Initialize Supabase provider
	supabase := NewSupabaseProvider()
	providers["supabase"] = supabase

	// Initialize Google-via-Supabase social login provider
	supabaseGoogle := NewSupabaseGoogleProvider()
	providers[supabaseGoogle.Name()] = supabaseGoogle

	return providers
}

// --- Simple Provider (AUTH_USERS) ---

// SimpleProvider implements AuthProvider using AUTH_USERS environment variable
type SimpleProvider struct{}

// NewSimpleProvider creates a new SimpleProvider
func NewSimpleProvider() *SimpleProvider {
	return &SimpleProvider{}
}

func (p *SimpleProvider) Name() string {
	return "simple"
}

func (p *SimpleProvider) Type() string {
	return "credentials"
}

func (p *SimpleProvider) GetAuthURL(state, redirectURI string) string {
	// Not applicable for credentials-based auth
	return ""
}

func (p *SimpleProvider) ExchangeCode(ctx context.Context, code, redirectURI, state string) (*ExternalUser, error) {
	return nil, fmt.Errorf("simple provider does not support OAuth flow")
}

func (p *SimpleProvider) ValidateCredentials(username, password string) (*ExternalUser, error) {
	// The user directory (config/users.json) is checked first; AUTH_USERS
	// remains as the bootstrap fallback until its users are imported.
	if rec := validateDirectoryCredentials(username, password); rec != nil {
		return &ExternalUser{
			ExternalID: rec.ID,
			Username:   rec.Username,
			Email:      rec.Email,
			Provider:   "simple",
		}, nil
	}
	user := ValidateHardcodedUser(username, password)
	if user == nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	return &ExternalUser{
		ExternalID: user.UserID,
		Username:   user.Username,
		Email:      "",
		Provider:   "simple",
	}, nil
}

func (p *SimpleProvider) IsConfigured() bool {
	return IsHardcodedUserMode() || userDirectoryHasPasswordUsers()
}

// --- Cognito Provider ---

// CognitoProvider implements AuthProvider for AWS Cognito
type CognitoProvider struct {
	UserPoolID string
	ClientID   string
	Domain     string
	Region     string
}

// NewCognitoProvider creates a new CognitoProvider from environment variables
func NewCognitoProvider() *CognitoProvider {
	return &CognitoProvider{
		UserPoolID: os.Getenv("COGNITO_USER_POOL_ID"),
		ClientID:   os.Getenv("COGNITO_CLIENT_ID"),
		Domain:     os.Getenv("COGNITO_DOMAIN"),
		Region:     os.Getenv("AWS_REGION"),
	}
}

func (p *CognitoProvider) Name() string {
	return "cognito"
}

func (p *CognitoProvider) Type() string {
	return "oauth"
}

func (p *CognitoProvider) GetAuthURL(state, redirectURI string) string {
	if !p.IsConfigured() {
		return ""
	}

	params := url.Values{}
	params.Set("client_id", p.ClientID)
	params.Set("response_type", "code")
	params.Set("scope", "openid email profile")
	params.Set("redirect_uri", redirectURI)
	params.Set("state", state)

	return fmt.Sprintf("https://%s/oauth2/authorize?%s", p.Domain, params.Encode())
}

func (p *CognitoProvider) ExchangeCode(ctx context.Context, code, redirectURI, state string) (*ExternalUser, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("cognito provider not configured")
	}

	// Exchange authorization code for tokens
	tokenURL := fmt.Sprintf("https://%s/oauth2/token", p.Domain)

	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("client_id", p.ClientID)
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[AUTH:Cognito] Token exchange failed: %s", string(body))
		return nil, fmt.Errorf("token exchange failed: %s", resp.Status)
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	// Get user info from Cognito
	userInfoURL := fmt.Sprintf("https://%s/oauth2/userInfo", p.Domain)
	userReq, err := http.NewRequestWithContext(ctx, "GET", userInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create userinfo request: %w", err)
	}
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)

	userResp, err := client.Do(userReq)
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}
	defer userResp.Body.Close()

	if userResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(userResp.Body)
		log.Printf("[AUTH:Cognito] UserInfo failed: %s", string(body))
		return nil, fmt.Errorf("failed to get user info: %s", userResp.Status)
	}

	var userInfo struct {
		Sub      string `json:"sub"`
		Email    string `json:"email"`
		Username string `json:"username"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&userInfo); err != nil {
		return nil, fmt.Errorf("failed to decode user info: %w", err)
	}

	// Determine username - prefer username, fall back to email, then sub
	username := userInfo.Username
	if username == "" {
		username = userInfo.Name
	}
	if username == "" {
		username = userInfo.Email
	}
	if username == "" {
		username = userInfo.Sub
	}

	log.Printf("[AUTH:Cognito] User authenticated: %s (sub: %s)", username, userInfo.Sub)

	return &ExternalUser{
		ExternalID: userInfo.Sub,
		Email:      userInfo.Email,
		Username:   username,
		Provider:   "cognito",
	}, nil
}

func (p *CognitoProvider) ValidateCredentials(username, password string) (*ExternalUser, error) {
	return nil, fmt.Errorf("cognito provider uses OAuth flow, not direct credentials")
}

func (p *CognitoProvider) IsConfigured() bool {
	return p.ClientID != "" && p.Domain != ""
}

// --- Supabase Provider ---

// SupabaseProvider implements AuthProvider for Supabase Auth
type SupabaseProvider struct {
	URL     string
	AnonKey string
}

// NewSupabaseProvider creates a new SupabaseProvider from environment variables
func NewSupabaseProvider() *SupabaseProvider {
	return &SupabaseProvider{
		URL:     os.Getenv("SUPABASE_URL"),
		AnonKey: os.Getenv("SUPABASE_ANON_KEY"),
	}
}

// StartSupabaseKeepalive launches a background goroutine that pings the
// configured Supabase project every 24h to prevent the free-tier auto-pause
// (kicks in after 7 days idle, removes the project subdomain from DNS, and
// breaks all logins until manually restored).
//
// No-op if Supabase isn't an active auth provider or URL/anon key are missing.
// Best-effort: failures are logged and the loop keeps running.
func StartSupabaseKeepalive(ctx context.Context) {
	enabled := false
	for _, name := range getEnabledProviderNames() {
		// supabase-google pings the same Supabase project as supabase, so
		// either provider keeps the keepalive relevant.
		if name == "supabase" || name == "supabase-google" {
			enabled = true
			break
		}
	}
	if !enabled {
		return
	}
	supabaseURL := strings.TrimSuffix(os.Getenv("SUPABASE_URL"), "/")
	anonKey := os.Getenv("SUPABASE_ANON_KEY")
	if supabaseURL == "" || anonKey == "" {
		log.Printf("[SUPABASE_KEEPALIVE] supabase enabled but URL/ANON_KEY missing — skipping")
		return
	}
	pingURL := supabaseURL + "/rest/v1/?apikey=" + anonKey
	client := &http.Client{Timeout: 15 * time.Second}
	ping := func() {
		req, err := http.NewRequestWithContext(ctx, "GET", pingURL, nil)
		if err != nil {
			log.Printf("[SUPABASE_KEEPALIVE] build request failed: %v", err)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[SUPABASE_KEEPALIVE] %s -> error: %v", supabaseURL, err)
			return
		}
		_ = resp.Body.Close()
		log.Printf("[SUPABASE_KEEPALIVE] %s -> HTTP %d", supabaseURL, resp.StatusCode)
	}
	go func() {
		// Initial 5min delay so a restart loop doesn't hammer the project.
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Minute):
		}
		ping()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ping()
			}
		}
	}()
	log.Printf("[SUPABASE_KEEPALIVE] enabled for %s (24h interval)", supabaseURL)
}

func (p *SupabaseProvider) Name() string {
	return "supabase"
}

func (p *SupabaseProvider) Type() string {
	return "credentials"
}

func (p *SupabaseProvider) GetAuthURL(state, redirectURI string) string {
	if !p.IsConfigured() {
		return ""
	}

	// Supabase uses its own OAuth endpoint
	// The redirect_to parameter tells Supabase where to redirect after auth
	params := url.Values{}
	params.Set("provider", "github") // Default to GitHub, could be configurable
	params.Set("redirect_to", redirectURI)

	return fmt.Sprintf("%s/auth/v1/authorize?%s", p.URL, params.Encode())
}

func (p *SupabaseProvider) ExchangeCode(ctx context.Context, code, redirectURI, state string) (*ExternalUser, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("supabase provider not configured")
	}

	// Exchange the code for a session via Supabase's token endpoint
	tokenURL := fmt.Sprintf("%s/auth/v1/token?grant_type=pkce", p.URL)

	data := map[string]string{
		"auth_code":     code,
		"code_verifier": "", // PKCE code verifier - should be stored and retrieved from session
	}
	jsonData, _ := json.Marshal(data)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", p.AnonKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[AUTH:Supabase] Token exchange failed: %s", string(body))
		return nil, fmt.Errorf("token exchange failed: %s", resp.Status)
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		TokenType    string `json:"token_type"`
		User         struct {
			ID          string `json:"id"`
			Email       string `json:"email"`
			AppMetadata struct {
				Provider string `json:"provider"`
			} `json:"app_metadata"`
			UserMetadata struct {
				Name      string `json:"name"`
				FullName  string `json:"full_name"`
				UserName  string `json:"user_name"`
				AvatarURL string `json:"avatar_url"`
			} `json:"user_metadata"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	// Determine username
	username := tokenResp.User.UserMetadata.UserName
	if username == "" {
		username = tokenResp.User.UserMetadata.Name
	}
	if username == "" {
		username = tokenResp.User.UserMetadata.FullName
	}
	if username == "" {
		username = tokenResp.User.Email
	}
	if username == "" {
		username = tokenResp.User.ID
	}

	log.Printf("[AUTH:Supabase] User authenticated: %s (id: %s)", username, tokenResp.User.ID)

	return &ExternalUser{
		ExternalID: tokenResp.User.ID,
		Email:      tokenResp.User.Email,
		Username:   username,
		Provider:   "supabase",
	}, nil
}

func (p *SupabaseProvider) ValidateCredentials(username, password string) (*ExternalUser, error) {
	// Supabase supports direct email/password auth via their API
	if !p.IsConfigured() {
		return nil, fmt.Errorf("supabase provider not configured")
	}

	signInURL := fmt.Sprintf("%s/auth/v1/token?grant_type=password", p.URL)

	data := map[string]string{
		"email":    username,
		"password": password,
	}
	jsonData, _ := json.Marshal(data)

	req, err := http.NewRequest("POST", signInURL, strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, fmt.Errorf("failed to create sign-in request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", p.AnonKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to sign in: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("invalid credentials")
	}

	var signInResp struct {
		User struct {
			ID           string `json:"id"`
			Email        string `json:"email"`
			UserMetadata struct {
				Name     string `json:"name"`
				FullName string `json:"full_name"`
			} `json:"user_metadata"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&signInResp); err != nil {
		return nil, fmt.Errorf("failed to decode sign-in response: %w", err)
	}

	username = signInResp.User.UserMetadata.Name
	if username == "" {
		username = signInResp.User.UserMetadata.FullName
	}
	if username == "" {
		username = signInResp.User.Email
	}

	return &ExternalUser{
		ExternalID: signInResp.User.ID,
		Email:      signInResp.User.Email,
		Username:   username,
		Provider:   "supabase",
	}, nil
}

func (p *SupabaseProvider) IsConfigured() bool {
	return p.URL != "" && p.AnonKey != ""
}

// --- Supabase Social Provider (Google sign-in via Supabase Auth) ---
//
// A second Supabase-backed provider, but OAuth instead of credentials: the
// browser goes to Supabase's /auth/v1/authorize?provider=google, Google
// authenticates, Supabase redirects back to our /auth/callback with a code,
// and we exchange it with grant_type=pkce. The Supabase user id becomes the
// stable ExternalID, so a person signing in with Google and (if enabled)
// Supabase email/password lands on the same user-directory record.
//
// Two Supabase-side gotchas, both setup not code:
//   - the Google provider must be enabled in the Supabase dashboard (with a
//     Google Cloud OAuth client whose redirect is
//     https://<project>.supabase.co/auth/v1/callback); and
//   - our redirect URI (<app origin>/auth/callback) must be allowlisted in
//     Supabase's Authentication -> URL Configuration -> Redirect URLs.
//
// Supabase does not echo our CSRF state back (unlike Cognito), so the
// frontend falls back to its session-stored state on callbacks without one.
// Flow binding still holds: the PKCE verifier created at start is required
// to exchange the code, and it never leaves this server.

// SupabaseSocialProvider implements AuthProvider for one Supabase-hosted
// social login. Today only Google is registered ("supabase-google"); adding
// another is a new constructor plus a frontend button.
type SupabaseSocialProvider struct {
	SocialProvider string // Supabase provider id, e.g. "google"
	URL            string
	AnonKey        string
}

// NewSupabaseGoogleProvider creates the Google-via-Supabase login provider.
func NewSupabaseGoogleProvider() *SupabaseSocialProvider {
	return &SupabaseSocialProvider{
		SocialProvider: "google",
		URL:            strings.TrimSuffix(os.Getenv("SUPABASE_URL"), "/"),
		AnonKey:        os.Getenv("SUPABASE_ANON_KEY"),
	}
}

func (p *SupabaseSocialProvider) Name() string {
	return "supabase-google"
}

func (p *SupabaseSocialProvider) Type() string {
	return "oauth"
}

func (p *SupabaseSocialProvider) GetAuthURL(state, redirectURI string) string {
	if !p.IsConfigured() {
		return ""
	}
	verifier, challenge := newSupabasePKCEPair()
	storeSupabasePKCEVerifier(state, verifier)

	params := url.Values{}
	params.Set("provider", p.SocialProvider)
	params.Set("redirect_to", redirectURI)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "s256")
	return fmt.Sprintf("%s/auth/v1/authorize?%s", p.URL, params.Encode())
}

func (p *SupabaseSocialProvider) ExchangeCode(ctx context.Context, code, redirectURI, state string) (*ExternalUser, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("supabase-google provider not configured")
	}
	verifier, ok := consumeSupabasePKCEVerifier(state)
	if !ok {
		return nil, fmt.Errorf("login session expired or already used; please sign in again")
	}

	tokenURL := fmt.Sprintf("%s/auth/v1/token?grant_type=pkce", p.URL)
	data := map[string]string{"auth_code": code, "code_verifier": verifier}
	jsonData, _ := json.Marshal(data)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", p.AnonKey)

	resp, err := supabaseSocialHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[AUTH:SupabaseGoogle] Token exchange failed: %s", string(body))
		return nil, fmt.Errorf("token exchange failed: %s", resp.Status)
	}

	// The PKCE token response already carries the provider-confirmed user.
	var tokenResp struct {
		User struct {
			ID               string `json:"id"`
			Email            string `json:"email"`
			EmailConfirmedAt string `json:"email_confirmed_at"`
			UserMetadata     struct {
				Name     string `json:"name"`
				FullName string `json:"full_name"`
			} `json:"user_metadata"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	username := tokenResp.User.UserMetadata.Name
	if username == "" {
		username = tokenResp.User.UserMetadata.FullName
	}
	if username == "" {
		username = tokenResp.User.Email
	}
	if username == "" {
		username = tokenResp.User.ID
	}

	log.Printf("[AUTH:SupabaseGoogle] User authenticated: %s (id: %s)", username, tokenResp.User.ID)

	return &ExternalUser{
		ExternalID:    tokenResp.User.ID,
		EmailVerified: tokenResp.User.EmailConfirmedAt != "",
		Email:         tokenResp.User.Email,
		Username:      username,
		Provider:      p.Name(),
	}, nil
}

func (p *SupabaseSocialProvider) ValidateCredentials(username, password string) (*ExternalUser, error) {
	return nil, fmt.Errorf("supabase-google provider uses OAuth flow, not direct credentials")
}

func (p *SupabaseSocialProvider) IsConfigured() bool {
	return p.URL != "" && p.AnonKey != ""
}

// supabaseSocialHTTPClient is a var so tests can stub Supabase's token
// endpoint without opening sockets.
var supabaseSocialHTTPClient = &http.Client{Timeout: 30 * time.Second}

// --- Supabase PKCE verifier store ---
//
// Verifiers live only as long as the login flow: keyed by our CSRF state,
// single-use, expired after the same window as the state itself.

type supabasePKCEEntry struct {
	Verifier  string
	CreatedAt time.Time
}

var (
	supabasePKCEMu        sync.Mutex
	supabasePKCEVerifiers = make(map[string]*supabasePKCEEntry)
)

func newSupabasePKCEPair() (verifier, challenge string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing means the process is out of entropy; a login
		// flow must not proceed with a weak verifier.
		panic(fmt.Sprintf("supabase pkce: crypto/rand failed: %v", err))
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge
}

func storeSupabasePKCEVerifier(state, verifier string) {
	supabasePKCEMu.Lock()
	defer supabasePKCEMu.Unlock()
	// Opportunistic sweep so abandoned flows don't accumulate.
	now := time.Now()
	for s, e := range supabasePKCEVerifiers {
		if now.Sub(e.CreatedAt) > stateExpiration {
			delete(supabasePKCEVerifiers, s)
		}
	}
	supabasePKCEVerifiers[state] = &supabasePKCEEntry{Verifier: verifier, CreatedAt: now}
}

func consumeSupabasePKCEVerifier(state string) (string, bool) {
	supabasePKCEMu.Lock()
	defer supabasePKCEMu.Unlock()
	entry, ok := supabasePKCEVerifiers[state]
	if !ok {
		return "", false
	}
	delete(supabasePKCEVerifiers, state)
	if time.Since(entry.CreatedAt) > stateExpiration {
		return "", false
	}
	return entry.Verifier, true
}
