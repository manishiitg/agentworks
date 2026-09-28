package main

import (
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	sessionDuration      = 12 * time.Hour
	sessionRefreshWindow = 6 * time.Hour
	ssoSessionDuration   = 30 * 24 * time.Hour
	ssoFlowDuration      = 10 * time.Minute
	authRequiredHeader   = "X-AgentWorks-Login"
	requestIDHeader      = "X-Request-ID"
	slowRequestThreshold = time.Second
)

type gatewayResponseRecorder struct {
	http.ResponseWriter
	status        int
	bytes         int64
	firstWriteAt  time.Time
	firstWriteErr error
}

func (w *gatewayResponseRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.firstWriteAt = time.Now()
	w.ResponseWriter.WriteHeader(status)
}

func (w *gatewayResponseRecorder) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	if err != nil && w.firstWriteErr == nil {
		w.firstWriteErr = err
	}
	return n, err
}

func (w *gatewayResponseRecorder) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *gatewayResponseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *gatewayResponseRecorder) ReadFrom(src io.Reader) (int64, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	var (
		n   int64
		err error
	)
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err = readerFrom.ReadFrom(src)
	} else {
		n, err = io.Copy(struct{ io.Writer }{w.ResponseWriter}, src)
	}
	w.bytes += n
	if err != nil && w.firstWriteErr == nil {
		w.firstWriteErr = err
	}
	return n, err
}

func (w *gatewayResponseRecorder) Push(target string, options *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, options)
	}
	return http.ErrNotSupported
}

func (w *gatewayResponseRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

type gateway struct {
	secret        []byte
	password      []byte
	frontendDir   string
	appName       string
	userID        string
	username      string
	sessionCookie string
	agent         *httputil.ReverseProxy
	// disablePasswordGate skips the shared-password session entirely,
	// relying solely on the inner app's own per-user auth. Zero value
	// (false) preserves the shared-password gate every existing deployment
	// (Video Studio, and every gateway{...} literal in this package's own
	// tests) already depends on -- this is opt-in per deployment via
	// GATEWAY_DISABLE_PASSWORD_GATE, never a default.
	disablePasswordGate bool
	// ssoOnly replaces the shared password with Google sign-in via Supabase,
	// while keeping this product's existing single family workspace.
	ssoOnly         bool
	ssoURL          string
	ssoAnonKey      string
	ssoRedirectURL  string
	ssoAllowedEmail map[string]bool
	ssoClient       *http.Client
}

// sessionCookieName derives a product-namespaced cookie name from the
// gateway's identity instead of a separate env var, so two gateways sharing
// a browser origin family never collide on cookie name. The default input
// "video-studio" reproduces the original hardcoded "video_studio_session"
// literal byte for byte, so an already-running Video Studio deployment's
// logged-in sessions survive a redeploy of this now-parameterized binary.
func sessionCookieName(userID string) string {
	return strings.ReplaceAll(userID, "-", "_") + "_session"
}

// gatewayClaims deliberately mirrors only the identity fields consumed by the
// agent API. The password session remains the public-facing authentication
// boundary; this signed, short-lived token is created server-side solely for
// the loopback hop from the gateway to the agent API.
type gatewayClaims struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Provider  string `json:"provider"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

func proxyFor(rawURL string) *httputil.ReverseProxy {
	target, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	return httputil.NewSingleHostReverseProxy(target)
}

func gatewayEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// gatewayBoolEnv defaults to false (unset means "off") so a new opt-in flag
// can never silently change behavior for a deployment that predates it.
func gatewayBoolEnv(name string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return value == "true" || value == "1"
}

func gatewayRequestLoggingEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("GATEWAY_REQUEST_LOG")))
	return value != "false" && value != "0" && value != "off"
}

func safeRequestID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func gatewayRequestID(r *http.Request) string {
	if existing := strings.TrimSpace(r.Header.Get(requestIDHeader)); safeRequestID(existing) {
		return existing
	}
	random := make([]byte, 12)
	if _, err := rand.Read(random); err == nil {
		return hex.EncodeToString(random)
	}
	return fmt.Sprintf("fallback-%x", time.Now().UnixNano())
}

func gatewayRoute(path string) string {
	switch {
	case path == "/login" || path == "/logout":
		return "gateway-auth"
	case strings.HasPrefix(path, "/api/wp"):
		return "workspace"
	case strings.HasPrefix(path, "/api"), strings.HasPrefix(path, "/ws"):
		return "agent"
	default:
		return "frontend"
	}
}

func gatewayStreamRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream")
}

func gatewayClientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
		return forwarded
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func newGateway() *gateway {
	secret := os.Getenv("AUTH_SECRET")
	password := os.Getenv("ACCESS_PASSWORD")
	ssoOnly := gatewayBoolEnv("GATEWAY_SSO_ONLY")
	if len(secret) < 32 || (!ssoOnly && password == "") {
		log.Fatal("AUTH_SECRET (32+ chars) and ACCESS_PASSWORD (unless GATEWAY_SSO_ONLY=true) are required")
	}
	allowed := make(map[string]bool)
	for _, email := range strings.Split(os.Getenv("GATEWAY_SSO_ALLOWED_EMAILS"), ",") {
		if email = strings.ToLower(strings.TrimSpace(email)); email != "" {
			allowed[email] = true
		}
	}
	ssoURL := strings.TrimRight(os.Getenv("GATEWAY_SSO_SUPABASE_URL"), "/")
	ssoRedirectURL := os.Getenv("GATEWAY_SSO_REDIRECT_URL")
	if ssoOnly && (gatewayBoolEnv("GATEWAY_DISABLE_PASSWORD_GATE") || len(allowed) == 0 || ssoURL == "" || os.Getenv("GATEWAY_SSO_SUPABASE_ANON_KEY") == "" || ssoRedirectURL == "") {
		log.Fatal("GATEWAY_SSO_ONLY needs its Supabase URL, anon key, redirect URL and allowed emails, and cannot use GATEWAY_DISABLE_PASSWORD_GATE")
	}
	if ssoOnly {
		providerURL, providerErr := url.Parse(ssoURL)
		callbackURL, callbackErr := url.Parse(ssoRedirectURL)
		if providerErr != nil || callbackErr != nil || providerURL.Scheme != "https" || providerURL.Host == "" || providerURL.User != nil || providerURL.Path != "" || providerURL.RawQuery != "" || providerURL.Fragment != "" || callbackURL.Scheme != "https" || callbackURL.Host == "" || callbackURL.User != nil || callbackURL.Path != "/auth/google/callback" || callbackURL.RawQuery != "" || callbackURL.Fragment != "" {
			log.Fatal("GATEWAY_SSO_SUPABASE_URL and GATEWAY_SSO_REDIRECT_URL must be valid HTTPS URLs, with the callback ending at /auth/google/callback")
		}
		password = ""
	}
	userID := gatewayEnv("GATEWAY_USER_ID", "video-studio")
	return &gateway{
		secret:              []byte(secret),
		password:            []byte(password),
		frontendDir:         os.Getenv("FRONTEND_DIR"),
		appName:             gatewayEnv("APP_NAME", "Video Studio"),
		userID:              userID,
		username:            gatewayEnv("GATEWAY_USERNAME", "video-studio"),
		sessionCookie:       sessionCookieName(userID),
		agent:               proxyFor(gatewayEnv("AGENT_API_URL", "http://127.0.0.1:8000")),
		disablePasswordGate: gatewayBoolEnv("GATEWAY_DISABLE_PASSWORD_GATE"),
		ssoOnly:             ssoOnly,
		ssoURL:              ssoURL,
		ssoAnonKey:          os.Getenv("GATEWAY_SSO_SUPABASE_ANON_KEY"),
		ssoRedirectURL:      ssoRedirectURL,
		ssoAllowedEmail:     allowed,
		ssoClient:           &http.Client{Timeout: 10 * time.Second},
	}
}

func (g *gateway) serveFrontend(w http.ResponseWriter, r *http.Request) {
	cleanPath := filepath.Clean("/" + r.URL.Path)
	path := filepath.Join(g.frontendDir, cleanPath)
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		// Release deployments swap the frontend directory behind a stable URL.
		// The HTML entry point selects hashed JS assets, so it must never be
		// served from a previous browser cache after a release.
		if filepath.Base(path) == "index.html" || filepath.Base(path) == "runtime-config.js" {
			w.Header().Set("Cache-Control", "no-cache")
		} else if strings.HasPrefix(cleanPath, "/assets/") {
			// Vite build assets are content-hashed (assets/index-<hash>.js), so a
			// URL never changes content: let browsers and any CDN keep it for a
			// year instead of re-fetching multi-MB bundles over slow links.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeFile(w, r, path)
		return
	}
	// A hashed asset from a previous release is gone, not a client route:
	// answering with index.html makes the browser (and a CDN in front) treat
	// HTML as the script, leaving a blank page after a deploy.
	if strings.HasPrefix(cleanPath, "/assets/") {
		w.Header().Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, filepath.Join(g.frontendDir, "index.html"))
}

func (g *gateway) signedSession(expires time.Time) string {
	payload := strconv.FormatInt(expires.Unix(), 10)
	return g.signCookiePayload(payload)
}

func (g *gateway) signCookiePayload(payload string) string {
	mac := hmac.New(sha256.New, g.secret)
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (g *gateway) signedSSOSession(expires time.Time, email string) string {
	return g.signCookiePayload("s." + base64.RawURLEncoding.EncodeToString([]byte(strings.ToLower(email))) + "." + strconv.FormatInt(expires.Unix(), 10))
}

func (g *gateway) sessionExpiry(r *http.Request) (time.Time, bool) {
	cookie, err := r.Cookie(g.sessionCookie)
	if err != nil {
		return time.Time{}, false
	}
	parts := strings.Split(cookie.Value, ".")
	if g.ssoOnly {
		if len(parts) != 4 || parts[0] != "s" {
			return time.Time{}, false
		}
		decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil || !g.ssoAllowedEmail[strings.ToLower(string(decoded))] {
			return time.Time{}, false
		}
		expires, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || !time.Now().Before(time.Unix(expires, 0)) {
			return time.Time{}, false
		}
		expiresAt := time.Unix(expires, 0)
		want := g.signedSSOSession(expiresAt, string(decoded))
		return expiresAt, subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(want)) == 1
	}
	if len(parts) != 2 {
		return time.Time{}, false
	}
	expires, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().After(time.Unix(expires, 0)) {
		return time.Time{}, false
	}
	expiresAt := time.Unix(expires, 0)
	want := g.signedSession(expiresAt)
	if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(want)) != 1 {
		return time.Time{}, false
	}
	return expiresAt, true
}

func (g *gateway) setSSOSessionCookie(w http.ResponseWriter, expires time.Time, email string) {
	http.SetCookie(w, &http.Cookie{
		Name:     g.sessionCookie,
		Value:    g.signedSSOSession(expires, email),
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	})
}

func (g *gateway) setSessionCookie(w http.ResponseWriter, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     g.sessionCookie,
		Value:    g.signedSession(expires),
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	})
}

func isGatewayAPIRoute(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/") || path == "/ws" || strings.HasPrefix(path, "/ws/")
}

func apiLoginURL(r *http.Request) string {
	next := "/"
	if referer, err := url.Parse(r.Referer()); err == nil && referer.Host == r.Host {
		// A protected API request can be made by the login screen itself while
		// an earlier authenticated request is still settling during logout.
		// Never use /login as the next destination or every 401 would wrap the
		// current login URL in another /login?next=... redirect.
		if referer.Path != "/login" && !strings.HasPrefix(referer.Path, "/login/") {
			next = safeNext(referer.RequestURI())
		}
	}
	return "/login?next=" + url.QueryEscape(next)
}

func (g *gateway) agentToken() (string, error) {
	now := time.Now()
	userID := g.userID
	if userID == "" {
		userID = "video-studio"
	}
	username := g.username
	if username == "" {
		username = "video-studio"
	}
	claims := gatewayClaims{
		UserID:    userID,
		Username:  username,
		Provider:  "gateway",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(15 * time.Minute).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := header + "." + body
	mac := hmac.New(sha256.New, g.secret)
	_, _ = mac.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// verifyAgentToken checks an HS256 JWT issued by the agent API (or by
// agentToken above — same secret) and returns its user id. Only what the
// gateway needs: signature, expiry, a non-empty user_id. The agent still
// runs its own full validation afterwards.
func (g *gateway) verifyAgentToken(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", false
	}
	mac := hmac.New(sha256.New, g.secret)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	var claims struct {
		UserID string `json:"user_id"`
		Exp    int64  `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.UserID == "" || claims.Exp <= time.Now().Unix() {
		return "", false
	}
	return claims.UserID, true
}

// bearerToken is the credential a request carries: an Authorization header,
// or ?token= for the places a browser cannot set a header (SSE, WebSocket
// upgrades, file links).
func bearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return strings.TrimSpace(r.URL.Query().Get("token"))
}

// accessTokenPrefix marks an AgentWorks personal access token
// (accesstokens.Prefix in the agent API). The gateway cannot verify one —
// only the app holds the token store — so PAT-bearing requests pass through
// for the app to authenticate, exactly like the webhook exception. The app
// fail-closes on unknown, expired, or revoked tokens.
const accessTokenPrefix = "aw_pat_"
const hostedMCPPath = "/api/external/v1/mcp"

func isMCPOAuthDiscoveryPath(path string) bool {
	return path == "/.well-known/oauth-protected-resource" || path == "/.well-known/oauth-protected-resource/api/external/v1/mcp" || path == "/.well-known/oauth-authorization-server"
}

func isMCPOAuthPublicAPIPath(path string) bool {
	return path == "/api/oauth/mcp/register" || path == "/api/oauth/mcp/authorize" || path == "/api/oauth/mcp/token" || path == hostedMCPPath || path == "/api/oauth/cli/device" || path == "/api/oauth/cli/token" || path == "/api/oauth/cli/revoke"
}

func isAccessTokenCredential(r *http.Request) bool {
	return strings.HasPrefix(bearerToken(r), accessTokenPrefix) || strings.HasPrefix(r.Header.Get("Authorization"), "Bearer aw_cli_")
}

// agentPublicPath mirrors the agent API's own unauthenticated routes
// (auth_middleware.go shouldSkipAuth): what a browser needs before it has a
// token. Everything else needs one when the password gate is off.
func agentPublicPath(path string) bool {
	if isMCPOAuthDiscoveryPath(path) || isMCPOAuthPublicAPIPath(path) {
		return true
	}
	for _, p := range []string{
		"/api/auth/login", "/api/auth/register", "/api/auth/mode", "/api/auth/start", "/api/auth/callback",
		"/api/auth/desktop/exchange", "/api/auth/providers", "/api/health", "/api/capabilities",
		"/api/oauth/callback",
		// Google navigates the browser here after Gmail consent. It cannot carry
		// the AgentWorks JWT, and the inner API validates the random, single-use
		// OAuth state before it writes any credential.
		"/api/human-feedback/gmail/auth/callback",
	} {
		if path == p {
			return true
		}
	}
	return strings.HasPrefix(path, "/api/shared/") || strings.HasPrefix(path, "/api/downloads/")
}

// requireUserToken is the per-user gate used when the shared password gate
// is off: the request must carry a valid app JWT, whose user id is stamped
// onto X-User-ID so the workspace API never trusts a client-chosen header.
// Returns false after writing the 401.
func (g *gateway) requireUserToken(w http.ResponseWriter, r *http.Request) bool {
	// CLI/MCP clients authenticate with a PAT, not an app JWT. Pass them to
	// the agent API — never the workspace API, which keys off X-User-ID —
	// and let the app verify the token; strip any client-chosen user id.
	if isAccessTokenCredential(r) && !strings.HasPrefix(r.URL.Path, "/api/wp") {
		if r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", "Bearer "+bearerToken(r))
		}
		r.Header.Del("X-User-ID")
		return true
	}
	token := bearerToken(r)
	userID, ok := g.verifyAgentToken(token)
	if !ok {
		w.Header().Set(authRequiredHeader, apiLoginURL(r))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":"authentication_required"}`)
		return false
	}
	if r.Header.Get("Authorization") == "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("X-User-ID", userID)
	return true
}

func (g *gateway) serveAgent(w http.ResponseWriter, r *http.Request) {
	// With the shared password gate off, every user signs in to the app
	// itself and arrives with their own JWT. The gateway then never mints
	// its service identity: an unauthenticated request is refused here
	// (apart from the login/mode/health routes the app needs beforehand),
	// so nobody can act as the fixed "video-studio" user any more.
	if g.disablePasswordGate {
		if !agentPublicPath(r.URL.Path) && !g.requireUserToken(w, r) {
			return
		}
		if isMCPOAuthPublicAPIPath(r.URL.Path) || isMCPOAuthDiscoveryPath(r.URL.Path) {
			r.Header.Del("X-User-ID")
		}
		g.agent.ServeHTTP(w, r)
		return
	}
	// An explicit bearer token wins. Public file links and browser SSE cannot
	// attach a custom header, so the agent API also supports ?token=. Preserve
	// that token as a bearer credential instead of replacing it with the
	// gateway's service identity; the token determines which user's workspace
	// the public file handler reads from.
	//
	// Browser users without either token authenticate through the HttpOnly
	// password-session cookie, so the gateway supplies the signed internal
	// token required by the agent API on their behalf.
	if r.Header.Get("Authorization") == "" {
		if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		} else if r.URL.Path == hostedMCPPath || isMCPOAuthPublicAPIPath(r.URL.Path) || isMCPOAuthDiscoveryPath(r.URL.Path) {
			// Let the agent issue its OAuth challenge or handle the public
			// authorization endpoints without a gateway service identity.
			r.Header.Del("X-User-ID")
			g.agent.ServeHTTP(w, r)
			return
		} else {
			token, err := g.agentToken()
			if err != nil {
				http.Error(w, "Unable to authenticate gateway request", http.StatusInternalServerError)
				return
			}
			r.Header.Set("Authorization", "Bearer "+token)
		}
	}
	g.agent.ServeHTTP(w, r)
}

func safeNext(next string) string {
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && !strings.ContainsAny(next, "\\\r\n") {
		return next
	}
	return "/"
}

func (g *gateway) login(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	if g.ssoOnly {
		if r.Method != http.MethodGet {
			http.Error(w, "Password login is unavailable", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, "<!doctype html><html><head><meta name=viewport content='width=device-width,initial-scale=1'><title>%s</title><style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#101827;color:#eef2ff;font:16px system-ui}main{width:min(360px,calc(100%% - 48px));padding:32px;border:1px solid #334155;border-radius:16px;background:#172033}a{display:block;text-align:center;padding:12px;border-radius:8px;background:#38bdf8;color:#082f49;font-weight:700;text-decoration:none}a:hover{background:#7dd3fc}p{color:#cbd5e1}</style></head><body><main><h1>%s</h1><p>Sign in with an approved Google account.</p><a href='/auth/google/start?next=%s'>Continue with Google</a></main></body></html>", g.appName, g.appName, url.QueryEscape(next))
		return
	}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err == nil && subtle.ConstantTimeCompare([]byte(r.Form.Get("password")), g.password) == 1 {
			expires := time.Now().Add(sessionDuration)
			g.setSessionCookie(w, expires)
			http.Redirect(w, r, safeNext(r.Form.Get("next")), http.StatusSeeOther)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><html><head><meta name=viewport content='width=device-width,initial-scale=1'><title>%s</title><style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#101827;color:#eef2ff;font:16px system-ui}main{width:min(360px,calc(100%% - 48px));padding:32px;border:1px solid #334155;border-radius:16px;background:#172033}input,button{width:100%%;box-sizing:border-box;padding:12px;border-radius:8px;font:inherit}input{border:1px solid #475569;background:#0f172a;color:white;margin:16px 0}button{border:0;background:#38bdf8;color:#082f49;font-weight:700;cursor:pointer}.error{color:#fda4af}</style></head><body><main><h1>%s</h1><p>Enter the shared access password.</p>", g.appName, g.appName)
	if r.Method == http.MethodPost {
		fmt.Fprint(w, "<p class=error>Incorrect password. Try again.</p>")
	}
	fmt.Fprintf(w, "<form method=post><input type=hidden name=next value=%q><input type=password name=password autofocus required><button type=submit>Continue</button></form></main></body></html>", next)
}

type gatewaySSOFlow struct {
	Verifier string `json:"verifier"`
	Next     string `json:"next"`
	Expires  int64  `json:"expires"`
}

func randomGatewayToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (g *gateway) ssoFlowCookieName() string { return g.sessionCookie + "_oauth" }

func (g *gateway) startGoogleSSO(w http.ResponseWriter, r *http.Request) {
	if !g.ssoOnly || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	verifier, err := randomGatewayToken(32)
	if err != nil {
		http.Error(w, "Unable to start sign-in", http.StatusInternalServerError)
		return
	}
	flow := gatewaySSOFlow{Verifier: verifier, Next: safeNext(r.URL.Query().Get("next")), Expires: time.Now().Add(ssoFlowDuration).Unix()}
	data, err := json.Marshal(flow)
	if err != nil {
		http.Error(w, "Unable to start sign-in", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: g.ssoFlowCookieName(), Value: g.signCookiePayload(base64.RawURLEncoding.EncodeToString(data)), Path: "/auth/google/callback", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: int(ssoFlowDuration.Seconds())})
	challenge := sha256.Sum256([]byte(verifier))
	authURL, _ := url.Parse(g.ssoURL + "/auth/v1/authorize")
	params := authURL.Query()
	params.Set("provider", "google")
	params.Set("redirect_to", g.ssoRedirectURL)
	params.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	params.Set("code_challenge_method", "s256")
	authURL.RawQuery = params.Encode()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, authURL.String(), http.StatusFound)
}

func (g *gateway) googleSSOCallback(w http.ResponseWriter, r *http.Request) {
	if !g.ssoOnly || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	cookie, err := r.Cookie(g.ssoFlowCookieName())
	http.SetCookie(w, &http.Cookie{Name: g.ssoFlowCookieName(), Path: "/auth/google/callback", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	if err != nil || r.URL.Query().Get("code") == "" {
		http.Error(w, "Sign-in was cancelled or expired", http.StatusBadRequest)
		return
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(g.signCookiePayload(parts[0]))) != 1 {
		http.Error(w, "Sign-in state is invalid", http.StatusBadRequest)
		return
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	var flow gatewaySSOFlow
	if err != nil || json.Unmarshal(data, &flow) != nil || flow.Verifier == "" || !time.Now().Before(time.Unix(flow.Expires, 0)) {
		http.Error(w, "Sign-in state has expired", http.StatusBadRequest)
		return
	}
	requestBody, _ := json.Marshal(map[string]string{"auth_code": r.URL.Query().Get("code"), "code_verifier": flow.Verifier})
	tokenURL := g.ssoURL + "/auth/v1/token?grant_type=pkce"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, tokenURL, strings.NewReader(string(requestBody)))
	if err != nil {
		http.Error(w, "Unable to complete sign-in", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", g.ssoAnonKey)
	client := g.ssoClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Unable to complete sign-in", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "Google sign-in failed", http.StatusUnauthorized)
		return
	}
	var result struct {
		User struct {
			Email            string `json:"email"`
			EmailConfirmedAt string `json:"email_confirmed_at"`
		} `json:"user"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil || result.User.EmailConfirmedAt == "" {
		http.Error(w, "Google did not provide a verified email", http.StatusForbidden)
		return
	}
	email := strings.ToLower(strings.TrimSpace(result.User.Email))
	if !g.ssoAllowedEmail[email] {
		http.Error(w, "This account is not approved for SparkQuill", http.StatusForbidden)
		return
	}
	g.setSSOSessionCookie(w, time.Now().Add(ssoSessionDuration), email)
	http.Redirect(w, r, safeNext(flow.Next), http.StatusSeeOther)
}

// Webhook receivers authenticate the original request with their own secret.
// Keep management endpoints behind the normal user gate.
func isWebhookRequest(r *http.Request) bool {
	prefix := ""
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/hooks/workflow/"):
		prefix = "/api/hooks/workflow/"
	case strings.HasPrefix(r.URL.Path, "/api/hooks/product/"):
		prefix = "/api/hooks/product/"
	default:
		return false
	}
	id := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.Split(id, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	if r.Method == http.MethodPost {
		return len(parts) == 1
	}
	if prefix == "/api/hooks/product/" {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return (len(parts) == 3 && parts[1] == "runs" && r.Method == http.MethodGet) || (len(parts) == 4 && parts[1] == "runs" && parts[3] == "artifact")
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !gatewayRequestLoggingEnabled() {
		g.serveHTTP(w, r)
		return
	}

	start := time.Now()
	requestID := gatewayRequestID(r)
	r.Header.Set(requestIDHeader, requestID)
	w.Header().Set(requestIDHeader, requestID)
	recorder := &gatewayResponseRecorder{ResponseWriter: w}
	route := gatewayRoute(r.URL.Path)
	stream := gatewayStreamRequest(r)
	log.Printf("[GATEWAY] --> request_id=%s method=%s path=%q route=%s stream=%t client_ip=%q", requestID, r.Method, r.URL.EscapedPath(), route, stream, gatewayClientIP(r))

	defer func() {
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		duration := time.Since(start)
		ttfb := time.Duration(-1)
		ttfbMillis := int64(-1)
		if !recorder.firstWriteAt.IsZero() {
			ttfb = recorder.firstWriteAt.Sub(start)
			ttfbMillis = ttfb.Milliseconds()
		}
		writeError := "-"
		if recorder.firstWriteErr != nil {
			writeError = recorder.firstWriteErr.Error()
		}
		log.Printf("[GATEWAY] <-- request_id=%s method=%s path=%q route=%s status=%d bytes=%d ttfb_ms=%d duration_ms=%d ttfb=%s duration=%s stream=%t slow=%t write_error=%q",
			requestID,
			r.Method,
			r.URL.EscapedPath(),
			route,
			status,
			recorder.bytes,
			ttfbMillis,
			duration.Milliseconds(),
			ttfb.Round(time.Millisecond),
			duration.Round(time.Millisecond),
			stream,
			!stream && duration >= slowRequestThreshold,
			writeError,
		)
	}()

	g.serveHTTP(recorder, r)
}

func (g *gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if isWebhookRequest(r) {
		r.Header.Del("X-User-ID")
		g.agent.ServeHTTP(w, r)
		return
	}
	if g.disablePasswordGate {
		// No shared-password session to check or renew -- the inner app's
		// own per-user auth is the sole gate. /login and /logout have
		// nothing to do in this mode, so they fall through to routing like
		// any other path (serveFrontend's SPA fallback handles them).
		g.route(w, r)
		return
	}
	if r.URL.Path == "/auth/google/start" {
		g.startGoogleSSO(w, r)
		return
	}
	if r.URL.Path == "/auth/google/callback" {
		g.googleSSOCallback(w, r)
		return
	}
	if r.URL.Path == "/login" {
		g.login(w, r)
		return
	}
	if r.URL.Path == "/logout" {
		http.SetCookie(w, &http.Cookie{Name: g.sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	expiresAt, authenticated := g.sessionExpiry(r)
	if !authenticated {
		if isGatewayAPIRoute(r.URL.Path) {
			if isMCPOAuthPublicAPIPath(r.URL.Path) {
				r.Header.Del("X-User-ID")
				g.route(w, r)
				return
			}
			// PAT clients (CLI/MCP) carry no browser session; the app
			// verifies the token itself. Workspace routes stay cookie-only.
			if isAccessTokenCredential(r) && !strings.HasPrefix(r.URL.Path, "/api/wp") {
				r.Header.Del("X-User-ID")
				g.route(w, r)
				return
			}
			loginURL := apiLoginURL(r)
			w.Header().Set(authRequiredHeader, loginURL)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprint(w, `{"error":"authentication_required"}`)
			return
		}
		if isMCPOAuthDiscoveryPath(r.URL.Path) {
			r.Header.Del("X-User-ID")
			g.agent.ServeHTTP(w, r)
			return
		}
		loginURL := "/login?next=" + url.QueryEscape(r.URL.RequestURI())
		http.Redirect(w, r, loginURL, http.StatusSeeOther)
		return
	}
	// Renew an active browser session before it expires. This prevents regular
	// users from being asked for the shared password repeatedly while still
	// allowing an idle browser to expire normally.
	if !g.ssoOnly && time.Until(expiresAt) < sessionRefreshWindow {
		g.setSessionCookie(w, time.Now().Add(sessionDuration))
	}
	g.route(w, r)
}

func (g *gateway) route(w http.ResponseWriter, r *http.Request) {
	switch {
	case isMCPOAuthDiscoveryPath(r.URL.Path):
		r.Header.Del("X-User-ID")
		g.agent.ServeHTTP(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/wp"):
		// Workspace calls go through the agent API's /api/wp proxy, never to
		// the workspace service directly: the agent verifies the caller,
		// enforces per-user and per-workflow access, and holds the workspace
		// service token. Without the password gate the user's JWT is still
		// required here (never a CLI access token).
		if g.disablePasswordGate && !g.requireUserToken(w, r) {
			return
		}
		g.serveAgent(w, r)
	case strings.HasPrefix(r.URL.Path, "/api"), strings.HasPrefix(r.URL.Path, "/ws"):
		g.serveAgent(w, r)
	default:
		g.serveFrontend(w, r)
	}
}

func main() {
	server := &http.Server{Addr: gatewayEnv("GATEWAY_ADDR", ":8090"), Handler: newGateway(), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(server.ListenAndServe())
}
