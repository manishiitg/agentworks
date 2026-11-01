package server

// Where a company account used from the Mac app really goes (PLAT-856-441). A program whose server account is a key
// (Claude Code's token, Codex's OpenAI key) is passed straight to its provider with the key swapped in; Cursor's own
// traffic is relayed to Cursor with the server's Cursor session swapped in; a program whose server account is a
// browser login the server cannot share (Muse, or Claude/Codex without a key) goes through the model proxy's own
// login. In every case the Mac only ever holds a placeholder, and an admin turns the program on from its card.
import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// companyKeyUpstreams is each program the server can pass straight to its provider when its account is a key.
var companyKeyUpstreams = map[string]string{"claude-code": "https://api.anthropic.com/v1", "codex-cli": "https://api.openai.com/v1"}

const cursorBackend = "https://api2.cursor.sh"

// serverAccountKey is the server account's own key for a program, or "" when the server signs in with a login.
func serverAccountKey(ctx context.Context, provider string) string {
	if stored, _ := LoadProviderKeys(ctx); stored != nil {
		switch provider {
		case "codex-cli":
			if key := strings.TrimSpace(stored.CodexCLI); key != "" {
				return key
			}
		case "cursor-cli":
			if key := strings.TrimSpace(stored.CursorCLI); key != "" {
				return key
			}
		}
	}
	for _, name := range serverAccountEnvNames[provider] {
		if key := strings.TrimSpace(os.Getenv(name)); key != "" {
			return key
		}
	}
	return ""
}

// lendFile marks a program turned on for the Mac app when no proxy login is involved (a key, or Cursor).
func lendFile(provider string) string {
	dir, err := modelProxyDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "lend-"+provider)
}

func lendingOn(provider string) bool {
	file := lendFile(provider)
	_, err := os.Stat(file)
	return file != "" && err == nil
}

// companyLendsDirectly reports how a program is lent without the proxy's login: "key", "cursor" or "".
func companyLendsDirectly(ctx context.Context, provider string) string {
	switch {
	case provider == "cursor-cli":
		return "cursor"
	case companyKeyUpstreams[provider] != "" && serverAccountKey(ctx, provider) != "":
		return "key"
	}
	return ""
}

// companyLendingLabel is what the admin card says the program is lent with, or "" when it is off.
func companyLendingLabel(ctx context.Context, provider string) string {
	switch companyLendsDirectly(ctx, provider) {
	case "key":
		if lendingOn(provider) {
			return "the server's key"
		}
		return ""
	case "cursor":
		if lendingOn(provider) {
			return "the server's Cursor account"
		}
		return ""
	}
	return modelProxySignedInAs(provider)
}

// companyUpstream is where one request for a program goes and how it is signed: the provider itself with the
// server's key, or the model proxy with its login.
func companyUpstream(ctx context.Context, provider string) (base string, sign func(*http.Request), ok bool) {
	if companyLendsDirectly(ctx, provider) == "key" {
		key := serverAccountKey(ctx, provider)
		return companyKeyUpstreams[provider], func(req *http.Request) {
			req.Header.Del("X-Api-Key")
			if provider == "claude-code" && !strings.HasPrefix(key, "sk-ant-oat") {
				// A plain Anthropic API key: x-api-key, and no OAuth beta the Mac's Claude Code asked for.
				req.Header.Del("Authorization")
				req.Header.Set("X-Api-Key", key)
				req.Header.Set("Anthropic-Beta", strings.Join(withoutOAuthBeta(req.Header.Get("Anthropic-Beta")), ","))
				return
			}
			req.Header.Set("Authorization", "Bearer "+key)
		}, true
	}
	base, key, ok := companyModelProxyUpstream()
	return base + "/v1", func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+key) }, ok
}

func withoutOAuthBeta(betas string) []string {
	out := []string{}
	for _, beta := range strings.Split(betas, ",") {
		if beta = strings.TrimSpace(beta); beta != "" && !strings.HasPrefix(beta, "oauth-") {
			out = append(out, beta)
		}
	}
	return out
}

// cursorSession is the server's Cursor session token: exchanged from the server's Cursor key, else the server's
// own Cursor login. It is used only on the server, never sent to a Mac.
var cursorSession = struct {
	sync.Mutex
	token   string
	expires time.Time
}{}

func jwtExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

func serverCursorSession(ctx context.Context) (string, error) {
	cursorSession.Lock()
	defer cursorSession.Unlock()
	if cursorSession.token != "" && time.Until(cursorSession.expires) > 5*time.Minute {
		return cursorSession.token, nil
	}
	if key := serverAccountKey(ctx, "cursor-cli"); key != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cursorBackend+"/auth/exchange_user_api_key", strings.NewReader("{}"))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		var answer struct {
			AccessToken string `json:"accessToken"`
		}
		if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&answer) != nil || answer.AccessToken == "" {
			return "", fmt.Errorf("Cursor did not accept the server's key (%d)", resp.StatusCode)
		}
		cursorSession.token, cursorSession.expires = answer.AccessToken, jwtExpiry(answer.AccessToken)
		return cursorSession.token, nil
	}
	home, _ := os.UserHomeDir()
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	raw, err := os.ReadFile(filepath.Join(configHome, "cursor", "auth.json"))
	if err != nil {
		return "", fmt.Errorf("the server is not signed in to Cursor")
	}
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	if json.Unmarshal(raw, &login) != nil || login.AccessToken == "" {
		return "", fmt.Errorf("the server's Cursor login cannot be read")
	}
	expires := jwtExpiry(login.AccessToken)
	if !expires.IsZero() && time.Until(expires) < time.Minute {
		return "", fmt.Errorf("the server's Cursor login has expired; sign in to Cursor on the server again")
	}
	// The server's own Cursor renews this file; read it again shortly.
	cursorSession.token, cursorSession.expires = login.AccessToken, time.Now().Add(10*time.Minute)
	return cursorSession.token, nil
}

// cursorRelayMethods are the exact Cursor calls an agent turn makes (seen live 1 Nov 2026), and nothing else: every
// Mac user shares the server's Cursor identity, so any call that lists or reads stored conversations, cloud agents
// or account history would show one person's Cursor chats to another. Refusals are logged; grow the list from them.
var cursorRelayMethods = map[string]bool{
	"agent.v1.AgentService/RunSSE":                    true,
	"aiserver.v1.BidiService/BidiAppend":              true,
	"aiserver.v1.DashboardService/GetUserPrivacyMode": true,
}

var cursorRelayPathShape = regexp.MustCompile(`^[A-Za-z0-9_]+(\.[A-Za-z0-9_]+)*/[A-Za-z0-9_]+$`)

func cursorRelayAllowed(path string) bool {
	return cursorRelayPathShape.MatchString(path) && cursorRelayMethods[path]
}

// relayHopHeaders are not passed through a relay.
var relayHopHeaders = map[string]bool{"Authorization": true, "Host": true, "Connection": true, "Content-Length": true, "Keep-Alive": true, "Proxy-Connection": true, "Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true, "X-Agentworks-Desktop": true, "Cookie": true, "Set-Cookie": true}

func copyRelayHeaders(to http.Header, from http.Header) {
	for name, values := range from {
		if relayHopHeaders[http.CanonicalHeaderKey(name)] {
			continue
		}
		for _, value := range values {
			to.Add(name, value)
		}
	}
}

func copyRelayResponse(w http.ResponseWriter, resp *http.Response) {
	copyRelayHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// ANY /api/model-proxy/cursor-cli/relay/{path} — on the server: a person's Cursor call, relayed to Cursor unchanged
// except that the server's Cursor session replaces the Mac's placeholder. Same access as the other company accounts.
func (api *StreamingAPI) handleCursorRelay(w http.ResponseWriter, r *http.Request) {
	caller, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	if u := GetUserFromContext(r.Context()); u == nil || u.AccessToken != nil {
		desktopJSONError(w, 403, "Use the Mac app's connection to this server.")
		return
	}
	path := mux.Vars(r)["path"]
	if !lendingOn("cursor-cli") || !providerEnabled("cursor-cli") {
		desktopJSONError(w, 404, "This server does not offer its Cursor account for use on your Mac.")
		return
	}
	if !cursorRelayAllowed(path) {
		log.Printf("[MODEL_PROXY] %s cursor relay refused %s", caller, path)
		desktopJSONError(w, 403, "Not available through the company account.")
		return
	}
	if _, err := api.admitProviderAccount(r.Context(), providerAccountScope{Principal: caller}, "cursor-cli", "global:cursor-cli"); err != nil {
		desktopJSONError(w, 403, err.Error())
		return
	}
	session, err := serverCursorSession(r.Context())
	if err != nil {
		log.Printf("[MODEL_PROXY] cursor session unavailable: %v", err)
		desktopJSONError(w, 502, err.Error())
		return
	}
	target := cursorBackend + "/" + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		desktopJSONError(w, 500, "Cannot reach Cursor.")
		return
	}
	copyRelayHeaders(req.Header, r.Header)
	req.Header.Set("Authorization", "Bearer "+session)
	req.ContentLength = r.ContentLength
	started := time.Now()
	resp, err := companyProxyClient.Do(req)
	if err != nil {
		desktopJSONError(w, 502, "Cannot reach Cursor.")
		return
	}
	defer resp.Body.Close()
	copyRelayResponse(w, resp)
	// One agent run is one recorded call: Cursor's own format carries the tokens, which are not read yet.
	if path == "agent.v1.AgentService/RunSSE" {
		log.Printf("[MODEL_PROXY] %s used the server's cursor-cli account from the Mac: run -> %d (%s)", caller, resp.StatusCode, time.Since(started).Round(time.Millisecond))
		if ledger := api.tokenLimitLedger(); ledger != nil {
			_ = ledger.Append(costledger.Entry{
				Attribution: costledger.Attribution{AttributionVersion: costledger.AttributionVersion, ActorID: caller, Activity: "mac_app"},
				EventID:     uuid.NewString(), Timestamp: time.Now().UTC(), UserID: caller, Scope: "chat", Component: "mac_company_account",
				Provider: "cursor-cli", AccountID: "global:cursor-cli", LLMCallCount: 1, BillingBasis: "unpriced",
			})
		}
	}
}

// ANY /api/desktop-model-proxy/{account}/cursor/{path} — on the Mac: Cursor's own traffic from the company Cursor
// account, presented with that account's placeholder login, forwarded to the server's relay over the connection.
func (api *StreamingAPI) handleDesktopCursorRelay(w http.ResponseWriter, r *http.Request) {
	record, c, ok := desktopCompanyCaller(w, r)
	if !ok {
		return
	}
	target := c.Server + "/api/model-proxy/cursor-cli/relay/" + mux.Vars(r)["path"]
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		desktopJSONError(w, 500, "Cannot reach the server.")
		return
	}
	copyRelayHeaders(req.Header, r.Header)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.ContentLength = r.ContentLength
	resp, err := companyProxyClient.Do(req)
	if err != nil {
		desktopJSONError(w, 502, "Cannot reach "+c.Name+".")
		return
	}
	defer resp.Body.Close()
	_ = record
	copyRelayResponse(w, resp)
}

// cursorPlaceholderLogin is the company Cursor account's login on the Mac: shaped like Cursor's own token so the
// Cursor SDK accepts it, worthless anywhere but this Mac's backend.
func cursorPlaceholderLogin(secret string) string {
	part := func(v any) string { raw, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(raw) }
	return part(map[string]string{"alg": "none", "typ": "JWT"}) + "." + part(map[string]any{"sub": "agentworks-company", "exp": 4102444800}) + "." + secret
}
