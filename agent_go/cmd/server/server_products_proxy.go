package server

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Separately built server products use the platform's current user and role
// checks. The private implementation and its service token never reach clients.
func (api *StreamingAPI) handleServerProduct(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	path := strings.TrimPrefix(r.URL.Path, "/api/server-products/")
	product, tail, ok := strings.Cut(path, "/")
	// Explicit product allowlist; adding a private build alone cannot activate it.
	if !ok || !registeredServerProducts[product] || !productEnabled(product) {
		http.NotFound(w, r)
		return
	}
	if claims == nil || claims.Scope != "" || claims.Provider == "bot_route" || !activeMCPPerson(claims.UserID) || !userAllowedProduct(claims, product) {
		http.Error(w, "product access required", 403)
		return
	}
	if claims.AccessToken != nil && (!claims.AccessToken.Allows("llm:use") || !serverProductTokenPath(r)) {
		http.Error(w, "llm:use token permission required", 403)
		return
	}
	if strings.Contains(tail, "..") || strings.Contains(tail, "\\") || strings.Contains(r.URL.EscapedPath(), "%") || !serverProductQueryAllowed(r) {
		http.Error(w, "unsupported product path", 400)
		return
	}
	if strings.HasPrefix(tail, "admin/") && !currentUserIsAdmin(r) {
		http.Error(w, "administrator role required", 403)
		return
	}
	// The platform directory is authoritative; bind existing accounts before
	// rendering the product's user permissions, without creating new accounts.
	if tail == "admin/users" && r.Method == http.MethodGet {
		users, err := vaultDirectoryUsers(claims)
		if err != nil {
			http.Error(w, "user directory unavailable", 503)
			return
		}
		out := make([]map[string]string, 0, len(users))
		for _, user := range users {
			if err := syncVaultDirectoryUser(r.Context(), &UserRecord{ID: user.ID, Email: user.Email}); err != nil {
				http.Error(w, "user directory synchronization unavailable", 503)
				return
			}
			out = append(out, map[string]string{"ID": user.ID, "Email": user.Email})
		}
		writeUsersJSON(w, 200, map[string]any{"users": out})
		return
	}
	target, secret, err := serverProductServiceConfig(product)
	if err != nil {
		http.Error(w, "server product unavailable", 503)
		return
	}
	admin := currentUserIsAdmin(r)
	proxy := &httputil.ReverseProxy{Transport: capLayerTransport, FlushInterval: -1, Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		pr.Out.URL.Path = strings.TrimRight(target.Path, "/") + "/api/admin/products/" + product + "/" + tail
		pr.Out.URL.RawPath = ""
		pr.Out.URL.RawQuery = r.URL.RawQuery
		pr.Out.Header = make(http.Header)
		for _, key := range []string{"Content-Type", "Accept", "Anthropic-Version", "Anthropic-Beta", "Openai-Beta"} {
			if value := r.Header.Get(key); value != "" {
				pr.Out.Header.Set(key, value)
			}
		}
		pr.Out.Header.Set("Authorization", "Bearer "+secret)
		pr.Out.Header.Set("X-AgentWorks-Actor", claims.UserID)
		pr.Out.Header.Set("X-AgentWorks-Client", "platform")
		if admin {
			pr.Out.Header.Set("X-AgentWorks-Admin", "1")
		}
	}, ModifyResponse: func(resp *http.Response) error {
		resp.Header.Del("Set-Cookie")
		if strings.HasPrefix(tail, "ui/") {
			// Reviewed first-party UI, intentionally using the person's session.
			// CSP sandbox cannot be removed by scripts inside the child document.
			resp.Header.Set("Content-Security-Policy", serverProductUICSP)
			resp.Header.Set("Referrer-Policy", "no-referrer")
			resp.Header.Set("X-Content-Type-Options", "nosniff")
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			return errors.New("unexpected server product redirect")
		}
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "server product unavailable", 502)
	}}
	proxy.ServeHTTP(w, r)
}

// Gateway tokens can only reach inference and discovery, never management/UI.
func serverProductTokenPath(r *http.Request) bool {
	const prefix = "/api/server-products/llm-gateway/v1/"
	if r.Method == "GET" {
		return r.URL.Path == prefix+"models"
	}
	if r.Method != "POST" {
		return false
	}
	switch r.URL.Path {
	case prefix + "messages", prefix + "messages/count_tokens", prefix + "responses", prefix + "responses/compact", prefix + "chat/completions":
		return true
	}
	return false
}

func serverProductQueryAllowed(r *http.Request) bool {
	if r.URL.RawQuery == "" {
		return true
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 1 {
		return false
	}
	if strings.HasSuffix(r.URL.Path, "/v1/models") && r.Method == "GET" && len(q["limit"]) == 1 {
		n, err := strconv.Atoi(q.Get("limit"))
		return err == nil && n > 0 && n <= 1000
	}
	return r.Method == "POST" && (strings.HasSuffix(r.URL.Path, "/v1/messages") || strings.HasSuffix(r.URL.Path, "/v1/messages/count_tokens")) && len(q["beta"]) == 1 && q.Get("beta") == "true"
}

// The registry is explicit: a credential or private binary cannot activate a product.
var registeredServerProducts = map[string]bool{"llm-gateway": true}

const serverProductUICSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; font-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'; sandbox allow-scripts allow-same-origin"

func serverProductServiceConfig(product string) (*url.URL, string, error) {
	if !registeredServerProducts[product] {
		return nil, "", errors.New("unsupported server product")
	}
	target, err := capLayerServiceURL()
	if err != nil {
		return nil, "", err
	}
	key := "AGENTWORKS_PRODUCT_" + strings.ToUpper(strings.ReplaceAll(product, "-", "_")) + "_SERVICE_TOKEN"
	secret := strings.TrimSpace(os.Getenv(key))
	if file := strings.TrimSpace(os.Getenv(key + "_FILE")); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, "", errors.New("product credential unavailable")
		}
		secret = strings.TrimSpace(string(data))
	}
	if len(secret) < 32 {
		return nil, "", errors.New("dedicated product credential required")
	}
	return target, secret, nil
}
