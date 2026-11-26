package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// A provider's sign-in page can be finished in anyone's browser. On a server with several people, a sign-in link
// started by one person and finished by another would put the second person's account (their Slack, their Google
// mailbox) into the first person's connection. So on such a server the provider's return only keeps the one-time
// code and sends the browser into the app, which finishes the sign-in signed in, and only for the person who started
// it. This is the GitHub connection's rule (github_vault_oauth.go), shared by MCP app and Google sign-ins.
// A single-person installation finishes at the callback as before.

const (
	oauthReturnParam = "oauth_connect"
	oauthReturnTTL   = 15 * time.Minute

	oauthReturnMCP   = "mcp"
	oauthReturnGmail = "gmail"
)

type oauthReturn struct {
	person  string
	kind    string
	code    string
	created time.Time
}

var oauthReturns = struct {
	sync.Mutex
	m map[string]*oauthReturn
}{m: make(map[string]*oauthReturn)}

func oauthReturnPerson(person string) string {
	person = strings.TrimSpace(person)
	if person == "" {
		return ""
	}
	return sanitizeUserIDForPath(person)
}

// oauthReturnBound reports whether a sign-in started by person must be finished by them in the app.
func oauthReturnBound(person string) bool {
	return IsMultiUserMode() && oauthReturnPerson(person) != ""
}

// holdOAuthReturn keeps the provider's code for the sign-in state names and sends the browser into the app.
func holdOAuthReturn(w http.ResponseWriter, r *http.Request, state, person, kind, code string) {
	oauthReturns.Lock()
	for key, held := range oauthReturns.m {
		if time.Since(held.created) > oauthReturnTTL {
			delete(oauthReturns.m, key)
		}
	}
	if _, already := oauthReturns.m[state]; already {
		oauthReturns.Unlock()
		writeOAuthPage(w, http.StatusConflict, "Sign-in already received", "Return to AgentWorks to finish it.")
		return
	}
	oauthReturns.m[state] = &oauthReturn{person: oauthReturnPerson(person), kind: kind, code: code, created: time.Now()}
	oauthReturns.Unlock()
	log.Printf("[OAUTH_RETURN] kind=%s event=held", kind)
	http.Redirect(w, r, "/?"+oauthReturnParam+"="+url.QueryEscape(state), http.StatusFound)
}

// takeOAuthReturn hands the held code to the person who started the sign-in. Anyone else is refused and the code
// stays for the rightful person.
func takeOAuthReturn(state, person string) (*oauthReturn, error) {
	person = oauthReturnPerson(person)
	oauthReturns.Lock()
	defer oauthReturns.Unlock()
	held := oauthReturns.m[state]
	if held != nil && time.Since(held.created) > oauthReturnTTL {
		delete(oauthReturns.m, state)
		held = nil
	}
	if held == nil {
		return nil, errors.New("This sign-in has expired or was already finished. Start it again.")
	}
	if person == "" || held.person != person {
		name := held.person
		if claims := userClaimsForDirectoryID(held.person); claims != nil && claims.Username != "" {
			name = claims.Username
		}
		log.Printf("[OAUTH_RETURN] kind=%s event=refused reason=other_person", held.kind)
		return nil, fmt.Errorf("This sign-in was started by %s. Sign in to AgentWorks as %s in this browser and open the link again.", name, name)
	}
	delete(oauthReturns.m, state)
	return held, nil
}

// handleOAuthReturnComplete finishes a held sign-in for the signed-in person: POST /api/oauth/complete {state}.
func (api *StreamingAPI) handleOAuthReturnComplete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if claims == nil || strings.TrimSpace(claims.UserID) == "" || claims.AccessToken != nil {
		writeAgentProfileError(w, http.StatusForbidden, "Sign in to AgentWorks in this browser to finish connecting.")
		return
	}
	var body struct {
		State string `json:"state"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body) != nil || strings.TrimSpace(body.State) == "" {
		writeAgentProfileError(w, http.StatusBadRequest, "This sign-in link is not valid. Start it again.")
		return
	}
	held, err := takeOAuthReturn(body.State, claims.UserID)
	if err != nil {
		writeAgentProfileError(w, http.StatusForbidden, err.Error())
		return
	}
	switch held.kind {
	case oauthReturnGmail:
		email, err := api.finishGmailOAuth(r.Context(), body.State, held.code)
		if err != nil {
			writeAgentProfileError(w, http.StatusBadRequest, "Google sign-in could not finish: "+err.Error())
			return
		}
		message := "Google account connected."
		if email != "" {
			message = "Google account " + email + " connected."
		}
		writeAgentProfileJSON(w, http.StatusOK, map[string]any{"connected": true, "message": message})
	default:
		oauthFlowsMu.RLock()
		flow := oauthFlows[body.State]
		oauthFlowsMu.RUnlock()
		if flow == nil {
			writeAgentProfileError(w, http.StatusGone, "This sign-in took too long and expired. Start it again.")
			return
		}
		result := deliverOAuthFlowCode(r.Context(), flow, held.code)
		switch {
		case result.failure != "":
			writeAgentProfileError(w, http.StatusBadRequest, "Sign-in could not finish: "+result.failure)
		case !result.accepted:
			writeAgentProfileError(w, http.StatusConflict, "This sign-in was already finished. Start it again if the app still needs a sign-in.")
		case result.checking:
			writeAgentProfileJSON(w, http.StatusAccepted, map[string]any{"connected": false, "message": "Still checking this account. Its status updates in the app when it is done."})
		default:
			writeAgentProfileJSON(w, http.StatusOK, map[string]any{"connected": true, "message": "Signed in. The app's tools are being set up."})
		}
	}
}

type oauthFlowDelivery struct {
	accepted bool   // the flow took the code
	checking bool   // the account check was still running when we stopped waiting
	failure  string // the account check refused the sign-in
}

// deliverOAuthFlowCode gives the waiting flow its code. A flow that checks the account (ValidatesIdentity) is waited
// for briefly, so a refused account is reported where the person is looking.
func deliverOAuthFlowCode(ctx context.Context, flow *OAuthFlowState, code string) oauthFlowDelivery {
	select {
	case flow.CodeChan <- code:
	default:
		return oauthFlowDelivery{}
	}
	if !flow.ValidatesIdentity {
		return oauthFlowDelivery{accepted: true}
	}
	select {
	case <-flow.Done:
		oauthFlowsMu.RLock()
		failure := flow.Failure
		oauthFlowsMu.RUnlock()
		return oauthFlowDelivery{accepted: true, failure: failure}
	case <-time.After(45 * time.Second):
	case <-ctx.Done():
	}
	return oauthFlowDelivery{accepted: true, checking: true}
}

// writeOAuthPage renders the small page a person lands on after a provider sign-in. Title and detail are text:
// both are escaped, and the page runs no script.
func writeOAuthPage(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>%s</title></head>
<body style="font-family: system-ui, -apple-system, sans-serif; text-align: center; padding: 48px;">
	<h1 style="font-size: 1.25rem;">%s</h1>
	<p>%s</p>
</body></html>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(detail))
}
