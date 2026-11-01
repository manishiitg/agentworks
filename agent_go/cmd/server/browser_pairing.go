package server

import (
	_ "embed"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
)

const browserPairingSetupPath = "/api/browser/extension/setup"
const browserPairingPeekPath = "/api/browser/extension/pairing/peek"
const browserPairingRedeemPath = "/api/browser/extension/pairing/redeem"

//go:embed browser_pairing.html
var browserPairingHTML string
var browserPairingPage = template.Must(template.New("browser-pairing").Parse(browserPairingHTML))
var browserPairingLimiter = &loginRateLimiter{failures: map[string]*loginFailures{}, limit: 30, window: time.Minute, lockout: time.Minute, now: time.Now}

func (api *StreamingAPI) handleBrowserPairingSetup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = browserPairingPage.Execute(w, r.URL.Query().Get("state"))
}

func (api *StreamingAPI) handleBrowserPairingExchange(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	// Extension host permission permits the request. Ordinary web pages get no CORS grant.
	for _, header := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers"} {
		w.Header().Del(header)
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	ip := clientIPOf(r)
	if !browserPairingLimiter.allow(ip) {
		http.Error(w, "Too many connection attempts. Wait a minute and try again.", 429)
		return
	}
	browserPairingLimiter.fail(ip)
	var req struct {
		Handoff string `json:"handoff"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 2048)).Decode(&req) != nil || len(req.Handoff) != 43 {
		http.Error(w, "Open a new connection from AgentWorks.", 400)
		return
	}
	consume := r.URL.Path == browserPairingRedeemPath
	grant, err := browserrelay.Default.AccountPairing(req.Handoff, consume)
	if err != nil {
		http.Error(w, err.Error(), 410)
		return
	}
	response := map[string]any{"account_id": grant.User, "account": grant.Account, "expires_at": grant.Expires}
	if consume {
		token, err := browserrelay.Default.PairAccount(grant.User, false)
		if err != nil {
			http.Error(w, "Could not connect your browser. Open a new connection from AgentWorks.", 503)
			return
		}
		response["token"] = token
	}
	json.NewEncoder(w).Encode(response)
}
