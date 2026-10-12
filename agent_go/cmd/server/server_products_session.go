package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

const serverProductUIScopePrefix = "server-product-ui:"
const serverProductUISessionLifetime = 10 * time.Minute

func serverProductUIScope(product string) string      { return serverProductUIScopePrefix + product }
func serverProductUICookieName(product string) string { return "agentworks_product_" + product }

type serverProductBrowserSession struct {
	product, parentToken string
	expires              time.Time
}

// Ephemeral opaque cookies never contain the app JWT. Only this process can
// resolve them, so logout and a backend restart invalidate them immediately.
var serverProductBrowserSessions = struct {
	sync.Mutex
	entries map[[32]byte]serverProductBrowserSession
}{entries: make(map[[32]byte]serverProductBrowserSession)}

func revokeServerProductUISessions(parentToken string) {
	serverProductBrowserSessions.Lock()
	defer serverProductBrowserSessions.Unlock()
	for key, session := range serverProductBrowserSessions.entries {
		if session.parentToken == parentToken || !session.expires.After(time.Now()) {
			delete(serverProductBrowserSessions.entries, key)
		}
	}
}

// This purpose token authenticates iframe navigation, static assets and the
// product's UI API only. It cannot reach inference, another product, or any
// platform API. Current directory/role/entitlement checks still run per request.
func serverProductUIScopeAllowsPath(scope, requestPath string) bool {
	product := strings.TrimPrefix(scope, serverProductUIScopePrefix)
	if scope == product || !registeredServerProducts[product] {
		return false
	}
	prefix := "/api/server-products/" + product + "/"
	if !strings.HasPrefix(requestPath, prefix) {
		return false
	}
	tail := strings.TrimPrefix(requestPath, prefix)
	switch tail {
	case "me", "models", "usage", "admin/models", "admin/audit":
		return true
	}
	return strings.HasPrefix(tail, "ui/") && !strings.Contains(tail, "..") && !strings.Contains(tail, "\\")
}

func serverProductUICookie(r *http.Request) (token, scope string) {
	for product := range registeredServerProducts {
		scope = serverProductUIScope(product)
		if serverProductUIScopeAllowsPath(scope, r.URL.Path) {
			if cookie, err := r.Cookie(serverProductUICookieName(product)); err == nil {
				key := sha256.Sum256([]byte(cookie.Value))
				serverProductBrowserSessions.Lock()
				session, ok := serverProductBrowserSessions.entries[key]
				if ok && !session.expires.After(time.Now()) {
					delete(serverProductBrowserSessions.entries, key)
					ok = false
				}
				serverProductBrowserSessions.Unlock()
				if ok && session.product == product {
					return session.parentToken, scope
				}
			}
		}
	}
	return "", ""
}

// Fetch Metadata is browser-owned and survives a local Vite reverse proxy.
// Only read methods are accepted; same-site sibling origins cannot use this
// ambient cookie to read the product. Bootstrap requires the app Bearer header.
func serverProductUIRequestAllowed(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin"
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		return err == nil && u.User == nil && u.Host == r.Host && (u.Scheme == "http" || u.Scheme == "https") && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
	}
	return r.Method == http.MethodGet || r.Method == http.MethodHead
}

func issueServerProductUISession(w http.ResponseWriter, r *http.Request, product string, claims *UserClaims) {
	// Only a browser's original session can bootstrap/renew this cookie. Never
	// upgrade a PAT, CLI/OAuth credential, query token, or another purpose token.
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if r.Method != http.MethodPost || claims.Scope != "" || claims.AccessToken != nil || claims.ExpiresAt == nil ||
		!strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || strings.Count(auth, ".") != 2 ||
		r.URL.RawQuery != "" || r.Header.Get("X-AgentWorks-Product-Session") != "1" {
		http.Error(w, "browser session required", http.StatusForbidden)
		return
	}
	now := time.Now()
	expires := now.Add(serverProductUISessionLifetime)
	if claims.ExpiresAt.Time.Before(expires) {
		expires = claims.ExpiresAt.Time
	}
	if !expires.After(now) {
		http.Error(w, "browser session expired", http.StatusUnauthorized)
		return
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		http.Error(w, "browser session unavailable", http.StatusServiceUnavailable)
		return
	}
	token := hex.EncodeToString(random)
	serverProductBrowserSessions.Lock()
	for key, session := range serverProductBrowserSessions.entries {
		// An iframe request may already carry the previous cookie when the
		// renewal response arrives. Preserve it until its original expiry;
		// logout still revokes every session bound to the parent JWT.
		if !session.expires.After(now) {
			delete(serverProductBrowserSessions.entries, key)
		}
	}
	if len(serverProductBrowserSessions.entries) >= 10000 {
		serverProductBrowserSessions.Unlock()
		http.Error(w, "browser session capacity reached", http.StatusServiceUnavailable)
		return
	}
	serverProductBrowserSessions.entries[sha256.Sum256([]byte(token))] = serverProductBrowserSession{product: product, parentToken: auth, expires: expires}
	serverProductBrowserSessions.Unlock()
	host := (&url.URL{Host: r.Host}).Hostname()
	secure := !productpolicy.Local() || (host != "localhost" && host != "127.0.0.1" && host != "::1")
	http.SetCookie(w, &http.Cookie{Name: serverProductUICookieName(product), Value: token,
		Path: "/api/server-products/" + product + "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: int(time.Until(expires).Seconds()),
	})
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
