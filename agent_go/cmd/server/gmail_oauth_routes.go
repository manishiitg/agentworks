package server

import (
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"

	"github.com/gorilla/mux"
)

// Browser-driven Gmail sign-in.
//
// Two endpoints: one hands the browser a Google URL, one receives Google's
// redirect. Between them the user consents in their own browser, so adding a
// mailbox needs no shell access on the host.

// gmailOAuthCallbackPath is registered on the OAuth client as a redirect URI.
const gmailOAuthCallbackPath = "/api/human-feedback/gmail/auth/callback"

// GmailOAuthStartResponse carries the URL the browser must open.
type GmailOAuthStartResponse struct {
	AuthURL string `json:"auth_url"`
	// RedirectURI is echoed so the UI can show exactly what must be registered
	// in the Google Cloud console when Google rejects it.
	RedirectURI string `json:"redirect_uri"`
}

// GmailOAuthRoutes wires the sign-in endpoints.
func GmailOAuthRoutes(router *mux.Router, api *StreamingAPI) {
	router.HandleFunc("/api/human-feedback/gmail/connections/{id}/auth/start",
		requireGmailConnectionManager(startGmailOAuthHandler(api))).Methods("POST", "OPTIONS")
	router.HandleFunc(gmailOAuthCallbackPath,
		gmailOAuthCallbackHandler(api)).Methods("GET")
}

// gmailOAuthRedirectURI derives the callback from the request that reached us,
// so the flow works on whatever host and port the server is actually serving —
// no configured base URL to drift out of sync.
func gmailOAuthRedirectURI(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		scheme = forwarded
	}
	return fmt.Sprintf("%s://%s%s", scheme, r.Host, gmailOAuthCallbackPath)
}

func startGmailOAuthHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		svc, id, ok := gmailConnectionService(w, r)
		if !ok {
			return
		}
		conn, found := svc.GetConnection(id)
		if !found {
			http.Error(w, fmt.Sprintf("gmail connection %q not found", id), http.StatusNotFound)
			return
		}
		conn, err := svc.EnsureConnectionOAuthClient(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		redirectURI := gmailOAuthRedirectURI(r)
		if conn.ClientName == services.PlatformGoogleClientName {
			// The deployment's Google app: keep the named client in step with the stored app
			// and return through the callback that app already registers.
			clientID, clientSecret, ok := platformGoogleApp()
			if !ok {
				http.Error(w, "This server's Google app is no longer configured. Ask an administrator.", http.StatusConflict)
				return
			}
			if _, err := services.EnsurePlatformOAuthClient(r.Context(), clientID, clientSecret); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			redirectURI = gmailGoogleAppReturnURI(r)
		}
		extraScopes := services.GoogleServiceScopeURIs(conn.Services)
		authURL, err := services.BeginGmailOAuth(id, conn.ClientName, redirectURI, conn.AllowReadAccess, conn.AllowAgentWriteAccess, extraScopes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(GmailOAuthStartResponse{AuthURL: authURL, RedirectURI: redirectURI})
	}
}

// gmailOAuthCallbackHandler receives Google's redirect, stores the credential,
// and resolves which mailbox was actually authorized.
//
// It renders HTML rather than JSON because a person is looking at it: this page
// opens in their browser, not in a fetch.
func gmailOAuthCallbackHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if authErr := strings.TrimSpace(query.Get("error")); authErr != "" {
			// A user who clicks Cancel is not an error condition worth a stack
			// trace — say what happened and let them close the tab.
			writeGmailOAuthPage(w, false, "Sign-in cancelled", html.EscapeString(authErr))
			return
		}
		state, code := query.Get("state"), query.Get("code")
		if state == "" || code == "" {
			writeGmailOAuthPage(w, false, "Sign-in failed", "Google did not return an authorization code.")
			return
		}

		connectionID, email, scopes, err := services.CompleteGmailOAuth(r.Context(), state, code)
		if err != nil {
			log.Printf("[GMAIL] OAuth callback failed: %v", err)
			writeGmailOAuthPage(w, false, "Sign-in failed", html.EscapeString(err.Error()))
			return
		}

		svc, svcErr := ensureGmailService()
		if svcErr != nil {
			writeGmailOAuthPage(w, false, "Sign-in failed", html.EscapeString(svcErr.Error()))
			return
		}

		if err := svc.CompleteGogConnection(r.Context(), connectionID, email, scopes); err != nil {
			writeGmailOAuthPage(w, false, "Sign-in failed", html.EscapeString(err.Error()))
			return
		}

		// Signing the same Google account in again in this Code (to change its access) replaces
		// the older connection instead of listing the account twice.
		for _, old := range svc.PrivateDuplicatesOf(connectionID) {
			if err := svc.DeleteConnection(r.Context(), old.ID); err != nil {
				log.Printf("[GMAIL] Could not remove replaced connection %s: %v", old.ID, err)
			} else {
				log.Printf("[GMAIL] Connection %s replaced by %s (%s)", old.ID, connectionID, email)
			}
		}
		log.Printf("[GMAIL] Connection %s authorized as %s", connectionID, email)
		detail := "You can close this tab and return to the app."
		if email != "" {
			detail = fmt.Sprintf("Connected as %s. You can close this tab and return to the app.", html.EscapeString(email))
		}
		writeGmailOAuthPage(w, true, "Gmail connected", detail)
	}
}

// writeGmailOAuthPage renders the small end-of-flow page the user lands on.
func writeGmailOAuthPage(w http.ResponseWriter, success bool, title, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !success {
		w.WriteHeader(http.StatusBadRequest)
	}
	accent := "#b91c1c"
	if success {
		accent = "#15803d"
	}
	fmt.Fprintf(w, `<!doctype html>
<html><head><meta charset="utf-8"><title>%s</title></head>
<body style="font-family:system-ui,-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;background:#fafaf9">
  <div style="max-width:26rem;padding:2rem;text-align:center">
    <h1 style="color:%s;font-size:1.25rem;margin:0 0 .5rem">%s</h1>
    <p style="color:#57534e;font-size:.9rem;line-height:1.5;margin:0">%s</p>
  </div>
  <script>setTimeout(function(){ try { window.close() } catch (e) {} }, 2500)</script>
</body></html>`, html.EscapeString(title), accent, html.EscapeString(title), detail)
}
