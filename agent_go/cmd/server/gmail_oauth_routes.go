package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
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
	router.HandleFunc("/human-feedback/gmail/connections/{id}/auth/start",
		requireGmailConnectionManager(func(w http.ResponseWriter, r *http.Request) {
			svc, err := ensureGmailService()
			if err != nil {
				http.Error(w, "Could not load Google accounts.", 500)
				return
			}
			if c, ok := svc.GetConnection(mux.Vars(r)["id"]); ok && c.IsPrivate() {
				http.Error(w, "Reconnect personal accounts in your project's Vault panel or in chat.", http.StatusForbidden)
				return
			}
			startGmailOAuthHandler(api)(w, r)
		})).Methods("POST", "OPTIONS")
	router.HandleFunc(strings.TrimPrefix(gmailOAuthCallbackPath, "/api"),
		gmailOAuthCallbackHandler(api)).Methods("GET")
}

// gmailOAuthRedirectURI prefers PUBLIC_URL when configured, and otherwise
// derives the callback from validated request headers so it works on local dev
// without trusting arbitrary forwarded schemes or hosts.
func gmailOAuthRedirectURI(r *http.Request) string {
	if publicURL := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_URL")), "/"); publicURL != "" {
		return publicURL + gmailOAuthCallbackPath
	}
	scheme := "http"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		candidate := strings.ToLower(strings.TrimSpace(strings.Split(proto, ",")[0]))
		if candidate == "http" || candidate == "https" {
			scheme = candidate
		}
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
		candidate := strings.TrimSpace(strings.Split(forwardedHost, ",")[0])
		if candidate != "" && !strings.ContainsAny(candidate, " \t\r\n/\\@?#") {
			host = candidate
		}
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, gmailOAuthCallbackPath)
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
		if conn.IsPrivate() && conn.OwnerID != personalConnectionActor(r.Context()) {
			http.Error(w, "Only the account owner can sign in again. Ask them to reconnect in Google apps.", http.StatusForbidden)
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
		authURL, err := services.BeginGmailOAuth(personalConnectionActor(r.Context()), id, conn.ClientName, redirectURI, conn.AllowReadAccess, conn.AllowAgentWriteAccess, extraScopes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if st := oauthURLStateParam(authURL); st != "" {
			bindOAuthStartBrowser(w, r, st)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(GmailOAuthStartResponse{AuthURL: authURL, RedirectURI: redirectURI})
	}
}

// gmailOAuthCallbackHandler receives Google's redirect, stores the credential,
// and resolves which mailbox was actually authorized. On a server with several
// people it only keeps Google's code: the person who started the sign-in finishes
// it in the app (oauth_return.go), so a link finished in someone else's browser
// cannot put their mailbox into this connection.
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
		if !verifyOAuthCallbackBrowser(r, state) {
			services.DiscardGmailOAuthState(state)
			writeGmailOAuthPage(w, false, "Sign-in started in another browser", "Start again from the same browser.")
			return
		}
		if person := services.GmailOAuthPendingPerson(state); oauthReturnBound(person) {
			holdOAuthReturn(w, r, state, person, oauthReturnGmail, code)
			return
		}
		email, err := api.finishGmailOAuth(r.Context(), nil, state, code)
		if err != nil {
			writeGmailOAuthPage(w, false, "Sign-in failed", html.EscapeString(err.Error()))
			return
		}
		detail := "You can close this tab and return to the app."
		if email != "" {
			detail = fmt.Sprintf("Connected as %s. You can close this tab and return to the app.", html.EscapeString(email))
		}
		writeGmailOAuthPage(w, true, "Gmail connected", detail)
	}
}

// finishGmailOAuth exchanges Google's code for the sign-in state names and saves the connection.
// When r carries an authenticated completing user, live authorization on the target connection
// is re-checked before exchanging the code so a demoted admin or non-owner cannot overwrite it.
func (api *StreamingAPI) finishGmailOAuth(ctx context.Context, r *http.Request, state, code string) (string, error) {
	if r != nil && GetUserFromContext(r.Context()) != nil {
		connID, ok := services.GmailOAuthPendingConnection(state)
		if !ok {
			return "", errors.New("this sign-in link has expired or was already used — start again from the Gmail settings")
		}
		svc, err := ensureGmailService()
		if err != nil {
			return "", err
		}
		conn, found := svc.GetConnection(connID)
		if !found {
			services.DiscardGmailOAuthState(state)
			return "", fmt.Errorf("gmail connection %q not found", connID)
		}
		if !canManageGoogleAccount(r, conn) || (conn.IsPrivate() && conn.OwnerID != personalConnectionActor(r.Context())) {
			services.DiscardGmailOAuthState(state)
			return "", errors.New("you are no longer allowed to manage this Google account")
		}
	}
	connectionID, email, scopes, err := services.CompleteGmailOAuth(ctx, state, code)
	if err != nil {
		log.Printf("[GMAIL] OAuth callback failed: %v", err)
		return "", err
	}
	svc, err := ensureGmailService()
	if err != nil {
		return "", err
	}
	if err := svc.CompleteGogConnection(ctx, connectionID, email, scopes); err != nil {
		return "", err
	}
	completeMCPURLSignIn(state, connectionID)

	// Signing the same Google account in again in this Code (to change its access) replaces
	// the older connection instead of listing the account twice.
	for _, old := range svc.PrivateDuplicatesOf(connectionID) {
		if err := svc.DeleteConnection(ctx, old.ID); err != nil {
			log.Printf("[GMAIL] Could not remove replaced connection %s: %v", old.ID, err)
		} else {
			log.Printf("[GMAIL] Connection %s replaced by %s (%s)", old.ID, connectionID, email)
		}
	}
	log.Printf("[GMAIL] Connection %s authorized as %s", connectionID, email)
	return email, nil
}

// writeGmailOAuthPage renders the small end-of-flow page the user lands on.
func writeGmailOAuthPage(w http.ResponseWriter, success bool, title, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
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
