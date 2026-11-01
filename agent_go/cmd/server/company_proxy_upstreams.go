package server

// Where a company account used from the Mac app really goes (PLAT-856-441). A program whose server account is a key
// (Claude Code's token, Codex's OpenAI key) is passed straight to its provider with the key swapped in; Cursor's own
// traffic is relayed to Cursor with the server's Cursor session swapped in; a program whose server account is a
// browser login the server cannot share (Muse, or Claude/Codex without a key) goes through the model proxy's own
// login. In every case the Mac only ever holds a placeholder, and an admin turns the program on from its card.
import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
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
var companyKeyUpstreams = map[string]string{"claude-code": "https://api.anthropic.com/v1", "codex-cli": "https://api.openai.com/v1", "muse-cli": "https://api.meta.ai/v1"}

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
	if provider == "muse-cli" {
		// The server's own Muse login keeps a Meta API key in its login record, kept current by Muse itself: read
		// it each time, never refreshed here, so the server's login and the Mac's use never sign each other out.
		home, _ := os.UserHomeDir()
		configHome := os.Getenv("XDG_CONFIG_HOME")
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
		if raw, err := os.ReadFile(filepath.Join(configHome, "muse", "auth.json")); err == nil {
			var record struct {
				Providers map[string]struct {
					APIKey string `json:"api_key"`
				} `json:"providers"`
			}
			if json.Unmarshal(raw, &record) == nil {
				return strings.TrimSpace(record.Providers["meta"].APIKey)
			}
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
// The lend-<provider> file is the one on/off switch for every way of lending (a key, Cursor, the proxy's login).
func companyLendingLabel(ctx context.Context, provider string) string {
	if !lendingOn(provider) {
		return ""
	}
	switch companyLendsDirectly(ctx, provider) {
	case "key":
		if provider == "muse-cli" {
			return "the server's Muse account"
		}
		return "the server's key"
	case "cursor":
		return "the server's Cursor account"
	}
	return modelProxySignedInAs(provider)
}

// companyUpstream is where one request for a program goes and how it is signed: the provider itself with the
// server's key, or the model proxy with its login.
func companyUpstream(ctx context.Context, provider string) (base string, sign func(*http.Request), ok bool) {
	if !lendingOn(provider) {
		return "", nil, false
	}
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
	// source names the credential the token came from, so a replaced or removed key is not reused.
	source string
}{}

func digestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

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
	key := serverAccountKey(ctx, "cursor-cli")
	source := "login"
	if key != "" {
		source = digestString(key)
	}
	if cursorSession.token != "" && cursorSession.source == source && time.Until(cursorSession.expires) > 5*time.Minute {
		return cursorSession.token, nil
	}
	cursorSession.token, cursorSession.source = "", source
	if key != "" {
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

func copyRelayResponse(w http.ResponseWriter, resp *http.Response, tap io.Writer) {
	copyRelayHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if tap != nil {
				_, _ = tap.Write(buf[:n])
			}
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

// cursorRunCapture reads a Cursor agent run's reply (Connect frames) as it streams and keeps only the usage, so a
// long reply never has to be held.
type cursorRunCapture struct {
	pending                                         []byte
	input, output, cacheRead, cacheWrite, reasoning int
	counted, broken                                 bool
}

func (c *cursorRunCapture) Write(p []byte) (int, error) {
	if c.broken {
		return len(p), nil
	}
	c.pending = append(c.pending, p...)
	for len(c.pending) >= 5 {
		size := int(binary.BigEndian.Uint32(c.pending[1:5]))
		if size < 0 || size > 64<<20 {
			c.broken = true
			return len(p), nil
		}
		if len(c.pending) < 5+size {
			break
		}
		if in, out, read, write, reasoning, ok := cursorRunUsage(c.pending[:5+size]); ok {
			c.input, c.output, c.cacheRead, c.cacheWrite, c.reasoning, c.counted = in, out, read, write, reasoning, true
		}
		c.pending = append(c.pending[:0], c.pending[5+size:]...)
	}
	return len(p), nil
}

// cursorRunUsage reads the token usage Cursor puts at the end of an agent run: Connect frames (a flag byte and a
// 4-byte length; flag 1 = gzip, flag 2 = the end-of-stream trailer) carrying protobuf, where field 1 holds the
// run's updates and update 14 is the usage (1 input, 2 output, 3 cache read, 4 cache write, 5 reasoning).
// Matched against the Cursor SDK's own usage on a live run (1 Nov 2026).
func cursorRunUsage(data []byte) (input, output, cacheRead, cacheWrite, reasoning int, ok bool) {
	for len(data) >= 5 {
		flag, size := data[0], int(binary.BigEndian.Uint32(data[1:5]))
		if size < 0 || 5+size > len(data) {
			return
		}
		body := data[5 : 5+size]
		data = data[5+size:]
		if flag&2 != 0 {
			continue
		}
		if flag&1 != 0 {
			reader, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				return
			}
			body, err = io.ReadAll(io.LimitReader(reader, 16<<20))
			if err != nil {
				return
			}
		}
		for _, field := range protoFields(body) {
			if field.num != 1 || field.wire != 2 {
				continue
			}
			for _, update := range protoFields(field.bytes) {
				if update.num != 14 || update.wire != 2 {
					continue
				}
				values := map[int]int{}
				for _, item := range protoFields(update.bytes) {
					if item.wire == 0 {
						values[item.num] = int(item.value)
					}
				}
				input, output, cacheRead, cacheWrite, reasoning, ok = values[1], values[2], values[3], values[4], values[5], true
			}
		}
	}
	return
}

type protoField struct {
	num, wire int
	value     uint64
	bytes     []byte
}

// protoFields splits one protobuf message into its fields; a malformed message yields what was read so far.
func protoFields(b []byte) []protoField {
	var out []protoField
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			return out
		}
		b = b[n:]
		field := protoField{num: int(key >> 3), wire: int(key & 7)}
		switch field.wire {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return out
			}
			field.value, b = v, b[n:]
		case 1:
			if len(b) < 8 {
				return out
			}
			b = b[8:]
		case 2:
			size, n := binary.Uvarint(b)
			if n <= 0 || uint64(len(b)-n) < size {
				return out
			}
			field.bytes, b = b[n:n+int(size)], b[n+int(size):]
		case 5:
			if len(b) < 4 {
				return out
			}
			b = b[4:]
		default:
			return out
		}
		out = append(out, field)
	}
	return out
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
	// Cursor checks the requested model against the account's model list before a run (GET v1/models on its
	// public API, which needs a Cursor API key the server may not have). The server answers it: the models of its
	// own Cursor account, limited to the ones the account allows this person.
	if path == "v1/models" && r.Method == http.MethodGet {
		if _, err := api.admitProviderAccount(r.Context(), providerAccountScope{Principal: caller}, "cursor-cli", "global:cursor-cli"); err != nil {
			desktopJSONError(w, admissionStatus(err), err.Error())
			return
		}
		items, err := serverCursorModels(r.Context())
		if err != nil {
			log.Printf("[MODEL_PROXY] cursor model list unavailable: %v", err)
			desktopJSONError(w, 502, "The server could not list its Cursor models.")
			return
		}
		allowed, _, _ := accountAllowedModels(withModelLimitPerson(r.Context(), caller), "cursor-cli", "global:cursor-cli")
		kept := []json.RawMessage{}
		for _, item := range items {
			var model struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(item, &model) == nil && modelAllowed(allowed, model.ID) {
				kept = append(kept, item)
			}
		}
		desktopJSON(w, map[string]any{"items": kept})
		return
	}
	if !cursorRelayAllowed(path) {
		log.Printf("[MODEL_PROXY] %s cursor relay refused %s", caller, path)
		desktopJSONError(w, 403, "Not available through the company account.")
		return
	}
	if _, err := api.admitProviderAccount(r.Context(), providerAccountScope{Principal: caller}, "cursor-cli", "global:cursor-cli"); err != nil {
		desktopJSONError(w, admissionStatus(err), err.Error())
		return
	}
	if path == "aiserver.v1.BidiService/BidiAppend" {
		// A run starts with a BidiAppend carrying the run request and its model: refuse a model the account does not
		// allow this person here, on the server, whatever the Mac's own model list said (PLAT-856-474).
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
		if err != nil {
			desktopJSONError(w, 400, "Could not read the request.")
			return
		}
		model, readable := cursorAppendModelRead(body, r.Header.Get("Content-Encoding"))
		if !readable {
			// A body this relay cannot read (compressed, or another shape) could carry any model: refuse it.
			log.Printf("[MODEL_PROXY] %s cursor append refused: unreadable body (encoding %q)", caller, r.Header.Get("Content-Encoding"))
			desktopJSONError(w, 400, "This request could not be checked.")
			return
		}
		if model != "" {
			person := withModelLimitPerson(r.Context(), caller)
			if err := checkAccountModel(person, "cursor-cli", "global:cursor-cli", model); err != nil {
				log.Printf("[MODEL_PROXY] %s cursor run refused: model %s not allowed", caller, model)
				desktopJSONError(w, 403, err.Error())
				return
			}
			allowed, _, _ := accountAllowedModels(person, "cursor-cli", "global:cursor-cli")
			if _, capped, limitErr := applyModelTokenLimit(person, "cursor-cli", "global:cursor-cli", model, allowed); modelLimitRefuses(capped, limitErr) {
				desktopJSONError(w, 429, "You have used your token limit for "+model+" on this account. Pick another model.")
				return
			}
			rememberCursorRunModel(cursorAppendRequestID(body), model)
		}
		r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
	}
	session, err := serverCursorSession(r.Context())
	if err != nil {
		log.Printf("[MODEL_PROXY] cursor session unavailable: %v", err)
		desktopJSONError(w, 502, err.Error())
		return
	}
	var runBody []byte
	if path == "agent.v1.AgentService/RunSSE" {
		runBody, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
		r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(runBody)), int64(len(runBody))
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
	if path != "agent.v1.AgentService/RunSSE" {
		copyRelayResponse(w, resp, nil)
		return
	}
	// An agent run: its usage is read from the end of the reply and recorded to the person, with the model its run
	// start named (so per-model limits count it).
	// Cursor opens the reply stream before it sends the run start, so the model is looked up once the run is over.
	capture := &cursorRunCapture{}
	copyRelayResponse(w, resp, capture)
	runModel := takeCursorRunModel(runSSERequestID(runBody))
	input, output, cacheRead, cacheWrite, reasoning, counted := capture.input, capture.output, capture.cacheRead, capture.cacheWrite, capture.reasoning, capture.counted
	log.Printf("[MODEL_PROXY] %s used the server's cursor-cli account from the Mac: %s run -> %d (%s) tokens in=%d out=%d cached=%d counted=%v", caller, runModel, resp.StatusCode, time.Since(started).Round(time.Millisecond), input, output, cacheRead, counted)
	if ledger := api.tokenLimitLedger(); ledger != nil {
		if err := ledger.Append(costledger.Entry{
			Attribution: costledger.Attribution{AttributionVersion: costledger.AttributionVersion, ActorID: caller, Activity: "mac_app"},
			EventID:     uuid.NewString(), Timestamp: time.Now().UTC(), UserID: caller, Scope: "chat", Component: "mac_company_account",
			Provider: "cursor-cli", ModelID: runModel, AccountID: "global:cursor-cli", LLMCallCount: 1, BillingBasis: "unpriced",
			PromptTokens: input, CompletionTokens: output, CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite, ReasoningTokens: reasoning,
			LLMGenerationDurationMS: time.Since(started).Milliseconds(),
		}); err != nil {
			log.Printf("[COST_LEDGER] Mac use of the cursor-cli account by %s not recorded: %v", caller, err)
		}
	}
}

// ANY /api/desktop-model-proxy/{account}/cursor/{path} — on the Mac: Cursor's own traffic from the company Cursor
// account, presented with that account's placeholder login, forwarded to the server's relay over the connection.
func (api *StreamingAPI) handleDesktopCursorRelay(w http.ResponseWriter, r *http.Request) {
	// Cursor on a Mac may sign in from the Keychain (the person's own Cursor login) rather than the account's
	// placeholder file, so the Cursor path is admitted by place and account, not by the presented login: a program on
	// this Mac (loopback, no browser Origin) addressing an existing company Cursor account, whose id is the secret.
	// Whatever Cursor presents never leaves the Mac: the connection's sign-in replaces it, and the server swaps in its
	// own Cursor session. Cursor's sign-in call is answered here with the placeholder and never forwarded.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	records, loadErr := loadProviderConnections(r.Context())
	record, found := companyAccountOnMac(records, mux.Vars(r)["account"])
	if err != nil || !net.ParseIP(host).IsLoopback() || r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" || loadErr != nil || !found || record.Provider != "cursor-cli" {
		desktopJSONError(w, 401, "Unknown account.")
		return
	}
	if mux.Vars(r)["path"] == "auth/exchange_user_api_key" {
		desktopJSON(w, map[string]string{"accessToken": record.Credential, "refreshToken": record.Credential})
		return
	}
	c, err := desktopConnectionFor(record.OwnerUserID, record.ViaConnection)
	if err != nil {
		desktopJSONError(w, 502, "The server connection is gone. Connect to the server again in the Mac app.")
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
	copyRelayResponse(w, resp, nil)
}

// cursorPlaceholderLogin is the company Cursor account's login on the Mac: shaped like Cursor's own token so the
// Cursor SDK accepts it, worthless anywhere but this Mac's backend.
func cursorPlaceholderLogin(secret string) string {
	part := func(v any) string { raw, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(raw) }
	return part(map[string]string{"alg": "none", "typ": "JWT"}) + "." + part(map[string]any{"sub": "agentworks-company", "exp": 4102444800}) + "." + secret
}

// cursorModelList caches the server's Cursor model catalog as the Cursor SDK itself reports it.
var cursorModelList = struct {
	sync.Mutex
	items []json.RawMessage
	at    time.Time
}{}

// serverCursorModels lists the server's Cursor account's models with the Cursor SDK (Cursor.models.list), in the
// form the SDK expects back from its model list; cached for five minutes.
func serverCursorModels(ctx context.Context) ([]json.RawMessage, error) {
	cursorModelList.Lock()
	defer cursorModelList.Unlock()
	if cursorModelList.items != nil && time.Since(cursorModelList.at) < 5*time.Minute {
		return cursorModelList.items, nil
	}
	// The SDK sits next to the cursor-agent install, beside the bin folder on the path (not the versioned folder
	// that bin entry links to).
	sdk := strings.TrimSpace(os.Getenv("CURSOR_SDK_PATH"))
	if sdk == "" {
		if agent, err := exec.LookPath("cursor-agent"); err == nil {
			candidates := []string{filepath.Join(filepath.Dir(agent), "..", "lib", "node_modules", "@cursor", "sdk")}
			if resolved, err := filepath.EvalSymlinks(agent); err == nil {
				candidates = append(candidates, filepath.Join(filepath.Dir(resolved), "..", "lib", "node_modules", "@cursor", "sdk"))
			}
			for _, candidate := range candidates {
				if _, err := os.Stat(candidate); err == nil {
					sdk = candidate
					break
				}
			}
		}
	}
	if sdk == "" {
		return nil, fmt.Errorf("the Cursor SDK is not installed")
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	script := `const { Cursor } = require(process.argv[1]); const key = process.env.CURSOR_API_KEY || undefined;
Cursor.models.list({ apiKey: key }).then(m => { process.stdout.write(JSON.stringify(m), () => process.exit(0)) }).catch(e => { process.stderr.write(String(e && e.message || e), () => process.exit(1)) })`
	cmd := exec.CommandContext(runCtx, "node", "-e", script, sdk)
	cmd.Env = append(minimalChildEnv(), passthroughChildEnv("CURSOR_API_KEY")...)
	if key := serverAccountKey(ctx, "cursor-cli"); key != "" {
		cmd.Env = append(cmd.Env, "CURSOR_API_KEY="+key)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing Cursor models with %s: %w: %s", sdk, err, strings.TrimSpace(stderr.String()))
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, fmt.Errorf("listing Cursor models with %s: no answer: %s", sdk, strings.TrimSpace(stderr.String()))
	}
	var items []json.RawMessage
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("reading Cursor models: %w", err)
	}
	cursorModelList.items, cursorModelList.at = items, time.Now()
	return items, nil
}

// cursorAppendModel reads the model of a run request from a Cursor BidiAppend body: field 1 holds the client message
// as hex; its run request (field 1) names the model in field 9, field 1. Empty when the append is not a run start.
func cursorAppendModel(body []byte) string {
	for _, outer := range protoFields(body) {
		if outer.num != 1 || outer.wire != 2 {
			continue
		}
		message, err := hex.DecodeString(string(outer.bytes))
		if err != nil {
			return ""
		}
		for _, run := range protoFields(message) {
			if run.num != 1 || run.wire != 2 {
				continue
			}
			for _, details := range protoFields(run.bytes) {
				if details.num != 9 || details.wire != 2 {
					continue
				}
				for _, id := range protoFields(details.bytes) {
					if id.num == 1 && id.wire == 2 {
						return strings.TrimSpace(string(id.bytes))
					}
				}
			}
		}
	}
	return ""
}

// cursorAppendModelRead is cursorAppendModel for a body as it arrived: a gzip body is read uncompressed; readable is
// false when the body cannot be decoded at all (then it is not checked, so it must be refused).
func cursorAppendModelRead(body []byte, encoding string) (model string, readable bool) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return "", false
		}
		plain, err := io.ReadAll(io.LimitReader(reader, 64<<20))
		if err != nil {
			return "", false
		}
		body = plain
	default:
		return "", false
	}
	for _, outer := range protoFields(body) {
		if outer.num == 1 && outer.wire == 2 {
			if _, err := hex.DecodeString(string(outer.bytes)); err != nil {
				return "", false
			}
			return cursorAppendModel(body), true
		}
	}
	return "", len(body) == 0
}

// cursorAppendRequestID is the run's request id in a BidiAppend (field 2, its field 1).
func cursorAppendRequestID(body []byte) string {
	for _, outer := range protoFields(body) {
		if outer.num == 2 && outer.wire == 2 {
			for _, id := range protoFields(outer.bytes) {
				if id.num == 1 && id.wire == 2 {
					return string(id.bytes)
				}
			}
		}
	}
	return ""
}

// runSSERequestID is the request id a RunSSE call streams (a Connect frame around field 1).
func runSSERequestID(body []byte) string {
	if len(body) > 5 && (body[0] == 0 || body[0] == 1) {
		body = body[5:]
	}
	for _, field := range protoFields(body) {
		if field.num == 1 && field.wire == 2 {
			return string(field.bytes)
		}
	}
	return ""
}

// cursorRunModels links a run's start (BidiAppend, which names the model) to its reply (RunSSE, which carries the
// usage), by request id; entries are taken once and expire.
var cursorRunModels = struct {
	sync.Mutex
	byID map[string]cursorRunModel
}{byID: map[string]cursorRunModel{}}

type cursorRunModel struct {
	model string
	at    time.Time
}

func rememberCursorRunModel(id, model string) {
	if id == "" {
		return
	}
	cursorRunModels.Lock()
	defer cursorRunModels.Unlock()
	for key, entry := range cursorRunModels.byID {
		if time.Since(entry.at) > time.Hour {
			delete(cursorRunModels.byID, key)
		}
	}
	cursorRunModels.byID[id] = cursorRunModel{model: model, at: time.Now()}
}

func takeCursorRunModel(id string) string {
	cursorRunModels.Lock()
	defer cursorRunModels.Unlock()
	entry := cursorRunModels.byID[id]
	delete(cursorRunModels.byID, id)
	return entry.model
}
