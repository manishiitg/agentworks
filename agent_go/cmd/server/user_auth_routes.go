package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// LoginRequest represents a user login request
type LoginRequest struct {
	Provider string `json:"provider,omitempty"` // Provider name: "simple", "cognito", "supabase"
	Username string `json:"username"`
	Password string `json:"password"`
}

// AuthResponse represents the response for login/register operations
type AuthResponse struct {
	Token string   `json:"token"`
	User  UserInfo `json:"user"`
}

// UserInfo represents public user information
type UserInfo struct {
	ID                      string   `json:"id"`
	Username                string   `json:"username"`
	Email                   string   `json:"email,omitempty"`
	Provider                string   `json:"provider,omitempty"`
	Role                    string   `json:"role,omitempty"`
	WorkflowAccess          string   `json:"workflow_access,omitempty"`
	CanRunWorkflows         bool     `json:"can_run_workflows"`
	CanWriteWorkflows       bool     `json:"can_write_workflows"`
	CanManageWorkflowAccess bool     `json:"can_manage_workflow_access"`
	AllowedProducts         []string `json:"allowed_products,omitempty"`
	AllowedWorkflowIDs      []string `json:"allowed_workflow_ids,omitempty"`
	IsAdmin                 bool     `json:"is_admin"`
	IsCodeReviewer          bool     `json:"is_code_reviewer,omitempty"`
	IsVaultReader           bool     `json:"is_vault_reader,omitempty"`
	CanCreate               bool     `json:"can_create"`
}

// AuthModeResponse represents the response for GET /api/auth/mode
type AuthModeResponse struct {
	MultiUserMode       bool           `json:"multi_user_mode"`
	RegistrationEnabled bool           `json:"registration_enabled"`
	Providers           []ProviderInfo `json:"providers"`
}

// MultiProviderOAuthStartRequest represents a request to start OAuth flow for multi-provider auth
type MultiProviderOAuthStartRequest struct {
	Provider    string `json:"provider"`
	RedirectURI string `json:"redirect_uri"`
}

// MultiProviderOAuthStartResponse represents the response for starting OAuth flow
type MultiProviderOAuthStartResponse struct {
	AuthURL string `json:"auth_url"`
	State   string `json:"state"`
}

type DesktopConnectResponse struct {
	ConnectURL string `json:"connect_url"`
	ServerURL  string `json:"server_url"`
	ExpiresAt  string `json:"expires_at"`
}

type DesktopConnectExchangeRequest struct {
	Code string `json:"code"`
}

// OAuth state management for CSRF protection
var (
	oauthStates     = make(map[string]*oauthStateEntry)
	oauthStatesMu   sync.RWMutex
	stateExpiration = 10 * time.Minute

	desktopConnectCodes      = make(map[string]*desktopConnectEntry)
	desktopConnectCodesMu    sync.Mutex
	desktopConnectExpiration = 5 * time.Minute
)

type oauthStateEntry struct {
	Provider    string
	RedirectURI string
	CreatedAt   time.Time
}

type desktopConnectEntry struct {
	User      UserClaims
	CreatedAt time.Time
	ExpiresAt time.Time
}

func init() {
	// Start cleanup goroutine for expired states
	go cleanupExpiredStates()
	go cleanupExpiredDesktopConnectCodes()
}

func cleanupExpiredStates() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		oauthStatesMu.Lock()
		now := time.Now()
		for state, entry := range oauthStates {
			if now.Sub(entry.CreatedAt) > stateExpiration {
				delete(oauthStates, state)
			}
		}
		oauthStatesMu.Unlock()
	}
}

func cleanupExpiredDesktopConnectCodes() {
	ticker := time.NewTicker(1 * time.Minute)
	for range ticker.C {
		desktopConnectCodesMu.Lock()
		now := time.Now()
		for code, entry := range desktopConnectCodes {
			if now.After(entry.ExpiresAt) {
				delete(desktopConnectCodes, code)
			}
		}
		desktopConnectCodesMu.Unlock()
	}
}

func generateState() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

func generateDesktopConnectCode() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func storeState(state, provider, redirectURI string) {
	oauthStatesMu.Lock()
	defer oauthStatesMu.Unlock()
	oauthStates[state] = &oauthStateEntry{
		Provider:    provider,
		RedirectURI: redirectURI,
		CreatedAt:   time.Now(),
	}
}

func validateAndConsumeState(state string) (*oauthStateEntry, bool) {
	oauthStatesMu.Lock()
	defer oauthStatesMu.Unlock()
	entry, ok := oauthStates[state]
	if !ok {
		return nil, false
	}
	if time.Since(entry.CreatedAt) > stateExpiration {
		delete(oauthStates, state)
		return nil, false
	}
	delete(oauthStates, state)
	return entry, true
}

func storeDesktopConnectCode(code string, user *UserClaims) time.Time {
	expiresAt := time.Now().Add(desktopConnectExpiration)
	desktopConnectCodesMu.Lock()
	defer desktopConnectCodesMu.Unlock()
	desktopConnectCodes[code] = &desktopConnectEntry{
		User:      *user,
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
	}
	return expiresAt
}

func consumeDesktopConnectCode(code string) (*desktopConnectEntry, bool) {
	desktopConnectCodesMu.Lock()
	defer desktopConnectCodesMu.Unlock()
	entry, ok := desktopConnectCodes[code]
	if !ok {
		return nil, false
	}
	delete(desktopConnectCodes, code)
	if time.Now().After(entry.ExpiresAt) {
		return nil, false
	}
	return entry, true
}

// UserAuthRoutes registers authentication routes
func UserAuthRoutes(router http.Handler, api *StreamingAPI) {
	// Routes are registered in server.go using the handler functions below
}

// handleRegister handles user registration
// Registration is always disabled - users are managed via AUTH_USERS env var
func (api *StreamingAPI) handleRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	http.Error(w, `{"error": "Registration is disabled - users are configured via AUTH_USERS environment variable"}`, http.StatusForbidden)
}

// handleLogin handles user login (for credentials-based providers)
func (api *StreamingAPI) handleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// Password login can be internet-facing (the Video Studio gateway with
	// its shared-password gate off), so an address that keeps failing is
	// held off for a while instead of being allowed to grind on argon2.
	if !loginLimiter.allow(clientIPOf(r)) {
		http.Error(w, `{"error": "Too many failed logins from this address; try again later"}`, http.StatusTooManyRequests)
		return
	}

	// In single-user mode, return a token for the default user (no login required)
	if !IsMultiUserMode() {
		token, err := GenerateJWT(GetDefaultUserID(), singleUserUsername, "")
		if err != nil {
			log.Printf("[AUTH] Failed to generate single-user token: %v", err)
			http.Error(w, `{"error": "Authentication is not configured"}`, http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(AuthResponse{
			Token: token,
			User: userInfoWithWorkflowPermissions(UserInfo{
				ID:       GetDefaultUserID(),
				Username: singleUserUsername,
			}),
		})
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "Invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" {
		http.Error(w, `{"error": "Username and password are required"}`, http.StatusBadRequest)
		return
	}

	// Determine which provider to use
	providerName := req.Provider
	if providerName == "" {
		providerName = "simple" // Default to simple provider
	}

	provider, ok := GetProvider(providerName)
	if !ok {
		http.Error(w, `{"error": "Unknown authentication provider"}`, http.StatusBadRequest)
		return
	}

	if !provider.IsConfigured() {
		log.Printf("[AUTH] Provider %s is not configured", providerName)
		http.Error(w, fmt.Sprintf(`{"error": "Provider %s is not configured"}`, providerName), http.StatusInternalServerError)
		return
	}

	// Validate credentials using the provider
	extUser, err := provider.ValidateCredentials(req.Username, req.Password)
	if err != nil {
		log.Printf("[AUTH] Login failed for user %s via provider %s: %v", req.Username, providerName, err)
		loginLimiter.fail(clientIPOf(r))
		http.Error(w, `{"error": "Invalid credentials"}`, http.StatusUnauthorized)
		return
	}
	loginLimiter.reset(clientIPOf(r))

	// Generate a deterministic user ID if not provided
	userID := extUser.ExternalID
	if userID == "" {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%s", extUser.Provider, extUser.Username)))
		userID = hex.EncodeToString(hash[:16])
	}

	if rec := directoryUserFor(userID, extUser.Username, extUser.Email); rec != nil && rec.Disabled {
		log.Printf("[AUTH] Login refused for disabled user %s", extUser.Username)
		http.Error(w, `{"error": "This account is disabled"}`, http.StatusForbidden)
		return
	}
	// Generate JWT token with provider information
	token, err := GenerateJWTWithProvider(userID, extUser.Username, extUser.Email, extUser.Provider)
	if err != nil {
		log.Printf("[AUTH] Failed to generate token: %v", err)
		http.Error(w, `{"error": "Failed to generate token"}`, http.StatusInternalServerError)
		return
	}

	log.Printf("[AUTH] User %s logged in successfully via provider %s", extUser.Username, extUser.Provider)
	json.NewEncoder(w).Encode(AuthResponse{
		Token: token,
		User: userInfoWithWorkflowPermissions(UserInfo{
			ID:       userID,
			Username: extUser.Username,
			Email:    extUser.Email,
			Provider: extUser.Provider,
		}),
	})
}

// handleAuthStart initiates OAuth flow for a provider
func (api *StreamingAPI) handleAuthStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !IsMultiUserMode() {
		http.Error(w, `{"error": "OAuth not available in single-user mode"}`, http.StatusBadRequest)
		return
	}

	var req MultiProviderOAuthStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "Invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Provider == "" {
		http.Error(w, `{"error": "Provider is required"}`, http.StatusBadRequest)
		return
	}

	if req.RedirectURI == "" {
		http.Error(w, `{"error": "Redirect URI is required"}`, http.StatusBadRequest)
		return
	}

	provider, ok := GetProvider(req.Provider)
	if !ok {
		http.Error(w, `{"error": "Unknown authentication provider"}`, http.StatusBadRequest)
		return
	}

	if provider.Type() != "oauth" {
		http.Error(w, `{"error": "Provider does not support OAuth flow"}`, http.StatusBadRequest)
		return
	}

	if !provider.IsConfigured() {
		http.Error(w, fmt.Sprintf(`{"error": "Provider %s is not configured"}`, req.Provider), http.StatusInternalServerError)
		return
	}

	// Generate and store state for CSRF protection
	state := generateState()
	storeState(state, req.Provider, req.RedirectURI)

	// Get the OAuth authorization URL
	authURL := provider.GetAuthURL(state, req.RedirectURI)
	if authURL == "" {
		http.Error(w, `{"error": "Failed to generate authorization URL"}`, http.StatusInternalServerError)
		return
	}

	log.Printf("[AUTH] OAuth flow started for provider %s", req.Provider)
	json.NewEncoder(w).Encode(MultiProviderOAuthStartResponse{
		AuthURL: authURL,
		State:   state,
	})
}

// handleAuthCallback handles OAuth callback from provider
func (api *StreamingAPI) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get query parameters
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	errorParam := r.URL.Query().Get("error")

	if errorParam != "" {
		errorDesc := r.URL.Query().Get("error_description")
		log.Printf("[AUTH] OAuth callback error: %s - %s", errorParam, errorDesc)
		http.Error(w, fmt.Sprintf(`{"error": "%s", "error_description": "%s"}`, errorParam, errorDesc), http.StatusBadRequest)
		return
	}

	if code == "" {
		http.Error(w, `{"error": "Authorization code is required"}`, http.StatusBadRequest)
		return
	}

	if state == "" {
		http.Error(w, `{"error": "State parameter is required"}`, http.StatusBadRequest)
		return
	}

	// Validate and consume state
	stateEntry, valid := validateAndConsumeState(state)
	if !valid {
		http.Error(w, `{"error": "Invalid or expired state parameter"}`, http.StatusBadRequest)
		return
	}

	provider, ok := GetProvider(stateEntry.Provider)
	if !ok {
		http.Error(w, `{"error": "Unknown authentication provider"}`, http.StatusBadRequest)
		return
	}

	// Exchange code for user info
	extUser, err := provider.ExchangeCode(r.Context(), code, stateEntry.RedirectURI, state)
	if err != nil {
		log.Printf("[AUTH] Failed to exchange code for provider %s: %v", stateEntry.Provider, err)
		http.Error(w, `{"error": "Failed to authenticate with provider"}`, http.StatusInternalServerError)
		return
	}
	if !externalAuthIdentityApproved(extUser.Email) {
		log.Printf("[AUTH] OAuth login refused: email is not in the user directory via provider %s", stateEntry.Provider)
		http.Error(w, `{"error": "This account is not approved for this workspace"}`, http.StatusForbidden)
		return
	}

	// Generate a deterministic user ID if not provided
	userID := extUser.ExternalID
	if userID == "" {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%s", extUser.Provider, extUser.Username)))
		userID = hex.EncodeToString(hash[:16])
	}

	// A sign-in never creates an account. The person must already be in the directory (added by an
	// administrator), or be a configured administrator bootstrapping their own record.
	rec := ensureDirectoryUserForExternal(userID, extUser)
	if rec == nil {
		log.Printf("[AUTH] OAuth login refused: %s has no account; an administrator must add them", extUser.Email)
		http.Error(w, `{"error": "This account has not been added by an administrator"}`, http.StatusForbidden)
		return
	}
	if rec.Disabled {
		log.Printf("[AUTH] OAuth login refused for disabled user %s", extUser.Username)
		http.Error(w, `{"error": "This account is disabled"}`, http.StatusForbidden)
		return
	}
	// A pre-provisioned account keeps its stable id, projects, history, and permissions when the
	// same email first signs in with SSO.
	userID = rec.ID
	// The token names the directory account, not the display name the person chose at the provider.
	extUser.Username = rec.Username
	// Generate JWT token with provider information
	token, err := GenerateJWTWithProvider(userID, extUser.Username, extUser.Email, extUser.Provider)
	if err != nil {
		log.Printf("[AUTH] Failed to generate token: %v", err)
		http.Error(w, `{"error": "Failed to generate token"}`, http.StatusInternalServerError)
		return
	}

	log.Printf("[AUTH] User %s authenticated via OAuth provider %s", extUser.Username, extUser.Provider)
	json.NewEncoder(w).Encode(AuthResponse{
		Token: token,
		User: userInfoWithWorkflowPermissions(UserInfo{
			ID:       userID,
			Username: extUser.Username,
			Email:    extUser.Email,
			Provider: extUser.Provider,
		}),
	})
}

func (api *StreamingAPI) handleDesktopConnect(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		http.Error(w, `{"error": "Not authenticated"}`, http.StatusUnauthorized)
		return
	}

	code, err := generateDesktopConnectCode()
	if err != nil {
		log.Printf("[AUTH] Failed to generate desktop connect code: %v", err)
		http.Error(w, `{"error": "Failed to generate connect code"}`, http.StatusInternalServerError)
		return
	}

	serverURL := getBaseURL(r)
	expiresAt := storeDesktopConnectCode(code, user)
	connectURL := fmt.Sprintf("runloop://connect?server=%s&code=%s", url.QueryEscape(serverURL), url.QueryEscape(code))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(DesktopConnectResponse{
		ConnectURL: connectURL,
		ServerURL:  serverURL,
		ExpiresAt:  expiresAt.UTC().Format(time.RFC3339),
	})
}

func (api *StreamingAPI) handleDesktopConnectExchange(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req DesktopConnectExchangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "Invalid request body"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Code) == "" {
		http.Error(w, `{"error": "Code is required"}`, http.StatusBadRequest)
		return
	}

	entry, ok := consumeDesktopConnectCode(strings.TrimSpace(req.Code))
	if !ok {
		http.Error(w, `{"error": "Invalid or expired connect code"}`, http.StatusUnauthorized)
		return
	}

	user := entry.User
	token, err := GenerateJWTWithProvider(user.UserID, user.Username, user.Email, user.Provider)
	if err != nil {
		log.Printf("[AUTH] Failed to generate desktop connect token: %v", err)
		http.Error(w, `{"error": "Failed to generate token"}`, http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(AuthResponse{
		Token: token,
		User: userInfoWithWorkflowPermissions(UserInfo{
			ID:       user.UserID,
			Username: user.Username,
			Email:    user.Email,
			Provider: user.Provider,
		}),
	})
}

// handleLogout handles user logout
// Since we use stateless JWTs, logout is handled client-side by clearing the token
func (api *StreamingAPI) handleLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Logged out successfully",
	})
}

// isBotManager checks if the given email is in the BOT_MANAGER_EMAILS list.
// If the env var is empty/unset, everyone is considered a bot manager (backwards compatible).
func isBotManager(email string) bool {
	envVal := os.Getenv("BOT_MANAGER_EMAILS")
	if envVal == "" {
		return true // No restriction — everyone is a bot manager
	}
	emails := strings.Split(envVal, ",")
	for _, e := range emails {
		if strings.TrimSpace(e) == email {
			return true
		}
	}
	return false
}

// handleGetCurrentUser returns the current authenticated user's info.
//
// The workflow_* capability flags are the GLOBAL account tier. Effective
// access varies per workflow (a global editor can be a viewer on one
// workflow and an owner of another), so the response also carries the
// resolved per-workflow map — clients must gate workflow actions on it,
// never on the global flags alone.
func (api *StreamingAPI) handleGetCurrentUser(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		http.Error(w, `{"error": "Not authenticated"}`, http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"id":             user.UserID,
		"username":       user.Username,
		"email":          user.Email,
		"provider":       user.Provider,
		"is_bot_manager": isBotManager(user.Email),
		"role":           roleForClaims(user),
	}
	for key, value := range workflowPermissionResponseFields(workflowPermissionInfoForClaims(user)) {
		response[key] = value
	}
	for key, value := range productAccessResponseFields(user) {
		response[key] = value
	}
	acc := userAccessForClaims(user)
	response["is_admin"] = acc.Admin
	response["can_create"] = acc.CanCreate
	response["can_create_in"] = canCreateInProducts(acc)
	if access, err := effectiveWorkflowAccessMap(r.Context(), user); err == nil {
		response["workflows"] = access
	}
	json.NewEncoder(w).Encode(response)
}

// handleGetAuthMode returns the current authentication mode and available providers
func (api *StreamingAPI) handleGetAuthMode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get base URL for generating OAuth URLs
	baseURL := getBaseURL(r)

	response := AuthModeResponse{
		MultiUserMode:       IsMultiUserMode(),
		RegistrationEnabled: false, // Registration is always disabled - use providers
		Providers:           GetProviderInfoList(baseURL),
	}

	json.NewEncoder(w).Encode(response)
}

// getBaseURL extracts the base URL from the request for generating callback URLs
func getBaseURL(r *http.Request) string {
	if publicURL := strings.TrimSpace(os.Getenv("PUBLIC_URL")); publicURL != "" {
		return strings.TrimRight(publicURL, "/")
	}

	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	// Check for X-Forwarded-Proto header (common behind reverse proxies)
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = strings.TrimSpace(strings.Split(proto, ",")[0])
	}

	host := r.Host
	if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
		host = strings.TrimSpace(strings.Split(forwardedHost, ",")[0])
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}
