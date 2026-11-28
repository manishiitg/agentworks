package server

// Desktop connections use ordinary remote website sessions. The local server
// owns the credentials; Electron proves that the caller is its top-level app.
import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Read once before startup can launch children. Never put these credentials in
// the initial process environment: macOS exposes it even after os.Unsetenv.
var desktopStartup = consumeDesktopBootstrap()
var desktopBootstrapSecret, desktopBootstrapHost = desktopStartup.Secret, desktopStartup.Host

type desktopBootstrap struct {
	Secret     string `json:"secret"`
	Host       string `json:"host"`
	AuthSecret string `json:"auth_secret"`
	AppOrigin  string `json:"app_origin"`
	// NotifySecret admits the Mac app's own notification stream (notifications.go).
	NotifySecret string `json:"notify_secret,omitempty"`
}

func consumeDesktopBootstrap() desktopBootstrap {
	var pipe *os.File
	switch os.Getenv("AGENTWORKS_BOOTSTRAP_FD") {
	case "3":
		pipe = os.NewFile(3, "desktop-bootstrap")
	case "stdin": // Windows: an inherited fd 3 is not a Go file there.
		pipe = os.Stdin
	default:
		return desktopBootstrap{}
	}
	_ = os.Unsetenv("AGENTWORKS_BOOTSTRAP_FD")
	if pipe == nil {
		panic("Desktop bootstrap pipe unavailable")
	}
	defer pipe.Close()
	var value desktopBootstrap
	if json.NewDecoder(io.LimitReader(pipe, 16384)).Decode(&value) != nil || value.Secret == "" || value.Host == "" || ValidateAuthSecretValue(value.AuthSecret) != nil {
		panic("Invalid desktop bootstrap")
	}
	if value.AppOrigin != "" {
		origin, err := url.Parse(value.AppOrigin)
		if err != nil || origin.Scheme != "http" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" || (origin.Hostname() != "127.0.0.1" && origin.Hostname() != "localhost" && origin.Hostname() != "::1") {
			panic("Invalid desktop app origin")
		}
	}
	value.AuthSecret = strings.TrimSpace(value.AuthSecret)
	return value
}

type desktopConnection struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Server string   `json:"server"`
	User   UserInfo `json:"user"`
	Token  string   `json:"token,omitempty"` // Only in the encrypted file, never in a response.
	// DeviceToken connects this Mac to the server as the person's computer (desktop_this_mac.go). Encrypted file only.
	DeviceToken string `json:"device_token,omitempty"`
}

var desktopConnectionsMu sync.Mutex
var desktopSignIns = struct {
	sync.Mutex
	pending map[string]*desktopSignIn
}{pending: make(map[string]*desktopSignIn)}

type desktopSignIn struct {
	Owner        string
	Expires      time.Time
	ConnectionID string
	Error        string
}

// Query credentials are already verified by AuthMiddleware. Only the streams a server project opens in place, its
// Terminal and its live browser view (#3383), and an account's sign-in window may use them, with the normal app proof.
func desktopProjectStreamUpgrade(r *http.Request) bool {
	parts := strings.Split(mux.Vars(r)["path"], "/")
	if !websocket.IsWebSocketUpgrade(r) || len(parts) < 1 || parts[0] != "api" {
		return false
	}
	terminal := len(parts) == 7 && parts[1] == "agent-profiles" && parts[2] != "" && parts[3] == "projects" &&
		parts[4] != "" && parts[5] == "shell" && parts[6] == "stream"
	browser := len(parts) == 5 && parts[1] == "browser" && parts[2] == "live" && parts[3] != "" && parts[4] == "stream"
	// The sign-in window of your own AI account on that server, added from the Mac app (#3807).
	signIn := len(parts) == 5 && parts[1] == "provider-setup" && parts[2] == "sessions" && parts[3] != "" && parts[4] == "stream"
	return terminal || browser || signIn
}

func desktopConnectionGate(w http.ResponseWriter, r *http.Request) bool {
	secret, host := desktopBootstrapSecret, desktopBootstrapHost
	appOrigin := desktopStartup.AppOrigin
	if len(desktopSocketKey) != 0 {
		appOrigin = r.Header.Get("Origin")
		secret = desktopSocketProof(appOrigin)
	}
	u := GetUserFromContext(r.Context())
	// Single-user JWTs alone aren't a desktop proof: localhost login issues them
	// without a password. Never admit PAT/MCP callers to these saved web sessions.
	if secret == "" || host == "" || r.Host != host || u == nil || u.AccessToken != nil || (r.Header.Get("Authorization") == "" && !(desktopProjectStreamUpgrade(r) && r.URL.Query().Get("token") != "")) ||
		!hmac.Equal([]byte(secret), []byte(r.Header.Get("X-AgentWorks-Desktop"))) {
		http.Error(w, `{"error":"Open this connection in the Mac app."}`, http.StatusForbidden)
		return false
	}
	if appOrigin == "" {
		appOrigin = "http://" + host
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != appOrigin {
		http.Error(w, `{"error":"Use the app window."}`, http.StatusForbidden)
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" && r.Header.Get("Origin") != appOrigin {
		http.Error(w, `{"error":"Use the app window."}`, http.StatusForbidden)
		return false
	}
	return true
}

func desktopConnectionFile(owner string) (string, error) {
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(owner))
	return filepath.Join(root, "desktop-connections", hex.EncodeToString(hash[:])+".sealed"), nil
}
func readDesktopConnections(owner string) ([]desktopConnection, error) {
	path, err := desktopConnectionFile(owner)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []desktopConnection{}, nil
	}
	if err != nil {
		return nil, err
	}
	plain, err := decryptSecretValueWithAAD(string(raw), []byte("desktop-connections:"+owner))
	if err != nil {
		return nil, err
	}
	var records []desktopConnection
	err = json.Unmarshal([]byte(plain), &records)
	return records, err
}
func writeDesktopConnections(owner string, records []desktopConnection) error {
	defer forgetCompanySync(owner)
	path, err := desktopConnectionFile(owner)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(records)
	if err != nil {
		return err
	}
	sealed, err := encryptSecretValueWithAAD(string(raw), []byte("desktop-connections:"+owner))
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, []byte(sealed), 0600)
}
func desktopConnectionFor(owner, id string) (desktopConnection, error) {
	desktopConnectionsMu.Lock()
	defer desktopConnectionsMu.Unlock()
	records, err := readDesktopConnections(owner)
	if err != nil {
		return desktopConnection{}, err
	}
	for _, c := range records {
		if c.ID == id {
			return c, nil
		}
	}
	return desktopConnection{}, fmt.Errorf("connection not found")
}

// saveRenewedDesktopToken keeps the server's renewed sign-in for this saved connection (X-Renewed-Token); "" is a no-op.
func saveRenewedDesktopToken(owner string, c desktopConnection, token string) error {
	if token == "" {
		return nil
	}
	desktopConnectionsMu.Lock()
	defer desktopConnectionsMu.Unlock()
	records, err := readDesktopConnections(owner)
	if err != nil {
		return err
	}
	for i := range records {
		if records[i].ID == c.ID && records[i].Token == c.Token {
			records[i].Token = token
		}
	}
	return writeDesktopConnections(owner, records)
}

// desktopServerDo sends one request to a connected server with the connection's saved sign-in and keeps the renewed
// sign-in the server returns, so a Mac in daily use is never signed out after the session's 7 days. A failed save is
// only logged: the current sign-in still works. owner is the local person who saved the connection.
func desktopServerDo(client *http.Client, owner string, c desktopConnection, req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	keepRenewedDesktopToken(owner, c, resp.Header)
	return resp, nil
}

// keepRenewedDesktopToken saves a renewed sign-in from a server's reply headers and removes it from them, so a
// proxied reply never hands it on.
func keepRenewedDesktopToken(owner string, c desktopConnection, header http.Header) {
	renewed := header.Get(renewedTokenHeader)
	if renewed == "" {
		return
	}
	header.Del(renewedTokenHeader)
	if owner == "" {
		log.Printf("[DESKTOP_CONNECTION] connection=%s event=renewed_sign_in_not_kept reason=no_owner", c.ID)
		return
	}
	if err := saveRenewedDesktopToken(owner, c, renewed); err != nil {
		log.Printf("[DESKTOP_CONNECTION] connection=%s event=renewed_sign_in_not_kept err=%v", c.ID, err)
	}
}

func desktopJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (api *StreamingAPI) handleDesktopConnections(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	owner := GetUserFromContext(r.Context()).UserID
	if r.Method == http.MethodPost {
		api.beginDesktopSignIn(w, r, owner)
		return
	}
	desktopConnectionsMu.Lock()
	defer desktopConnectionsMu.Unlock()
	records, err := readDesktopConnections(owner)
	if err != nil {
		http.Error(w, `{"error":"Cannot read saved connections."}`, 500)
		return
	}
	if r.Method == http.MethodDelete {
		id := mux.Vars(r)["connection"]
		next := make([]desktopConnection, 0, len(records))
		for _, c := range records {
			if c.ID != id {
				next = append(next, c)
			}
		}
		if err := writeDesktopConnections(owner, next); err != nil {
			http.Error(w, `{"error":"Cannot remove connection."}`, 500)
			return
		}
		stopThisMac(id)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	for i := range records {
		records[i].Token, records[i].DeviceToken = "", ""
		// Every screen names a server the same way, as the Servers page does (owner, 10 Nov: the menu showed two
		// servers as "Agentworkshq" from their web address).
		records[i].Name = friendlyServerName(records[i].Name, records[i].Server)
	}
	desktopJSON(w, map[string]any{"connections": records})
}

func desktopServerURL(value string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(value), "/"))
	if err != nil || u == nil || u.Hostname() == "" || u.User != nil ||
		(u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"))) {
		return nil, fmt.Errorf("Paste an HTTPS link from the server you want to connect.")
	}
	// Browser links may include a project, query or fragment. Connections use only the origin.
	return &url.URL{Scheme: u.Scheme, Host: u.Host}, nil
}
func desktopHTTPClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func (api *StreamingAPI) beginDesktopSignIn(w http.ResponseWriter, r *http.Request, owner string) {
	var input struct {
		Server string `json:"server"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&input) != nil {
		http.Error(w, `{"error":"Enter the server address."}`, 400)
		return
	}
	server, err := desktopServerURL(input.Server)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	state, err := generateDesktopConnectCode()
	if err != nil {
		http.Error(w, "Cannot start sign-in", 500)
		return
	}
	verifier, err := generateDesktopConnectCode()
	if err != nil {
		http.Error(w, "Cannot start sign-in", 500)
		return
	}
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		http.Error(w, "Cannot start sign-in", 500)
		return
	}
	callback := "http://" + listener.Addr().String() + "/connected"
	pending := &desktopSignIn{Owner: owner, Expires: time.Now().Add(5 * time.Minute)}
	desktopSignIns.Lock()
	for key, p := range desktopSignIns.pending {
		if time.Now().After(p.Expires) {
			delete(desktopSignIns.pending, key)
		}
	}
	desktopSignIns.pending[state] = pending
	desktopSignIns.Unlock()
	var once sync.Once
	callbackServer := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	callbackServer.Handler = http.HandlerFunc(func(out http.ResponseWriter, req *http.Request) {
		out.Header().Set("Referrer-Policy", "no-referrer")
		out.Header().Set("Cache-Control", "no-store")
		if req.Method != "GET" || req.Host != listener.Addr().String() || req.URL.Path != "/connected" || req.URL.Query().Get("state") != state {
			http.Error(out, "Sign-in does not match this app.", 400)
			return
		}
		used := true
		once.Do(func() {
			used = false
			defer func() { go callbackServer.Shutdown(context.Background()) }()
			body, _ := json.Marshal(map[string]string{"code": req.URL.Query().Get("code"), "verifier": verifier})
			response, e := desktopHTTPClient().Post(server.String()+"/api/auth/desktop/exchange", "application/json", bytes.NewReader(body))
			var auth DesktopConnectExchangeResponse
			if e == nil {
				defer response.Body.Close()
				if response.StatusCode != 200 {
					e = fmt.Errorf("exchange refused")
				} else {
					e = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&auth)
				}
			}
			if e == nil && (auth.Token == "" || auth.User.ID == "") {
				e = fmt.Errorf("missing account")
			}
			id := ""
			if e == nil {
				digest := sha256.Sum256([]byte(owner + "\x00" + server.String() + "\x00" + auth.User.ID))
				id = hex.EncodeToString(digest[:16])
				nameRunes := []rune(strings.Map(func(r rune) rune {
					if unicode.IsControl(r) {
						return -1
					}
					return r
				}, auth.ServerName))
				if len(nameRunes) > 80 {
					nameRunes = nameRunes[:80]
				}
				name := strings.TrimSpace(string(nameRunes))
				if name == "" {
					name = server.Hostname()
				}
				c := desktopConnection{ID: id, Name: name, Server: server.String(), User: auth.User, Token: auth.Token}
				desktopConnectionsMu.Lock()
				records, readErr := readDesktopConnections(owner)
				e = readErr
				if e == nil {
					next := make([]desktopConnection, 0, len(records)+1)
					for _, old := range records {
						if old.ID != id {
							next = append(next, old)
						}
					}
					e = writeDesktopConnections(owner, append(next, c))
				}
				desktopConnectionsMu.Unlock()
			}
			desktopSignIns.Lock()
			if e != nil {
				pending.Error = "Sign-in could not finish. Return to the app and try again."
			} else {
				pending.ConnectionID = id
			}
			desktopSignIns.Unlock()
			if e != nil {
				http.Error(out, "Sign-in could not finish. Return to the app and try again.", 400)
				return
			}
			out.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(out, "Connected. You can close this tab and return to AgentWorks.")
		})
		if used {
			http.Error(out, "This sign-in has already been used.", 400)
		}
	})
	go func() { _ = callbackServer.Serve(listener) }()
	time.AfterFunc(5*time.Minute, func() { _ = callbackServer.Close() })
	query := url.Values{"state": {state}, "callback": {callback}, "challenge": {challenge}}
	desktopJSON(w, map[string]string{"state": state, "url": server.String() + "/desktop/connect?" + query.Encode()})
}
func (api *StreamingAPI) handleDesktopSignInStatus(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	desktopSignIns.Lock()
	defer desktopSignIns.Unlock()
	p := desktopSignIns.pending[mux.Vars(r)["state"]]
	if p == nil || p.Owner != GetUserFromContext(r.Context()).UserID || time.Now().After(p.Expires) {
		http.Error(w, `{"error":"Sign-in expired. Try connecting again."}`, 410)
		return
	}
	desktopJSON(w, map[string]string{"connection_id": p.ConnectionID, "error": p.Error})
}

var (
	desktopProxyDialTimeout           = 5 * time.Second
	desktopProxyResponseHeaderTimeout = 6 * time.Second
	desktopProxyCooldown              = 15 * time.Second
	desktopProxyMaxCooldown           = 60 * time.Second

	desktopProxyTransportMu sync.Mutex
	desktopProxyTransport   *http.Transport
	desktopProxyStreamTrans *http.Transport

	desktopProxyCooldownMu sync.Mutex
	desktopProxyUnreach    = map[string]time.Time{}
	desktopProxyFailures   = map[string]int{}
	desktopProxyProbing    = map[string]bool{}
)

func desktopProxyTransports() (normal, stream *http.Transport) {
	desktopProxyTransportMu.Lock()
	defer desktopProxyTransportMu.Unlock()
	if desktopProxyTransport != nil && desktopProxyStreamTrans != nil &&
		desktopProxyTransport.ResponseHeaderTimeout == desktopProxyResponseHeaderTimeout {
		return desktopProxyTransport, desktopProxyStreamTrans
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok || base == nil {
		base = &http.Transport{}
	}
	dialer := &net.Dialer{Timeout: desktopProxyDialTimeout, KeepAlive: 30 * time.Second}

	t := base.Clone()
	t.DialContext = dialer.DialContext
	t.TLSHandshakeTimeout = desktopProxyDialTimeout
	t.ResponseHeaderTimeout = desktopProxyResponseHeaderTimeout
	desktopProxyTransport = t

	s := base.Clone()
	s.DialContext = dialer.DialContext
	s.TLSHandshakeTimeout = desktopProxyDialTimeout
	s.ResponseHeaderTimeout = 0
	desktopProxyStreamTrans = s
	return desktopProxyTransport, desktopProxyStreamTrans
}

func isDesktopProxyStreamingRequest(r *http.Request, path string, streamUpgrade bool) bool {
	if streamUpgrade {
		return true
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
		return true
	}
	return strings.HasSuffix(path, "/events") || strings.HasSuffix(path, "/stream")
}

func desktopProxyInCooldown(connID string) bool {
	if desktopProxyCooldown <= 0 || connID == "" {
		return false
	}
	desktopProxyCooldownMu.Lock()
	defer desktopProxyCooldownMu.Unlock()
	until, ok := desktopProxyUnreach[connID]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(desktopProxyUnreach, connID)
		return false
	}
	return true
}

// desktopProxyTryAcquire enforces per-connection unreachable cooldown and
// half-open single-probe gating: after cooldown expires on a previously
// unreachable connection, only one caller probes at a time while concurrent
// callers fail fast instead of stampeding into full timeouts (#4515).
func desktopProxyTryAcquire(connID string) (bool, func()) {
	if desktopProxyCooldown <= 0 || connID == "" {
		return true, nil
	}
	desktopProxyCooldownMu.Lock()
	defer desktopProxyCooldownMu.Unlock()
	if until, ok := desktopProxyUnreach[connID]; ok {
		if time.Now().Before(until) {
			return false, nil
		}
		delete(desktopProxyUnreach, connID)
	}
	if desktopProxyFailures[connID] > 0 {
		if desktopProxyProbing[connID] {
			return false, nil
		}
		desktopProxyProbing[connID] = true
		return true, func() {
			desktopProxyCooldownMu.Lock()
			delete(desktopProxyProbing, connID)
			desktopProxyCooldownMu.Unlock()
		}
	}
	return true, nil
}

func markDesktopProxyUnreachable(connID string) {
	if desktopProxyCooldown <= 0 || connID == "" {
		return
	}
	desktopProxyCooldownMu.Lock()
	failures := desktopProxyFailures[connID] + 1
	desktopProxyFailures[connID] = failures
	shift := failures - 1
	if shift > 2 {
		shift = 2
	}
	backoff := desktopProxyCooldown << shift
	maxBackoff := desktopProxyMaxCooldown
	if maxBackoff < desktopProxyCooldown {
		maxBackoff = desktopProxyCooldown
	}
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	desktopProxyUnreach[connID] = time.Now().Add(backoff)
	delete(desktopProxyProbing, connID)
	desktopProxyCooldownMu.Unlock()
}

func clearDesktopProxyUnreachable(connID string) {
	desktopProxyCooldownMu.Lock()
	delete(desktopProxyUnreach, connID)
	delete(desktopProxyFailures, connID)
	delete(desktopProxyProbing, connID)
	desktopProxyCooldownMu.Unlock()
}

func (api *StreamingAPI) handleDesktopConnectionProxy(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	owner := GetUserFromContext(r.Context()).UserID
	c, err := desktopConnectionFor(owner, mux.Vars(r)["connection"])
	if err != nil {
		http.Error(w, `{"error":"Connection is unavailable. Connect to the server again."}`, 404)
		return
	}
	path := "/" + mux.Vars(r)["path"]
	// A saved origin plus API-only clean paths. Never forward local auth, cookies,
	// app credentials, identity assertions or URL credentials to a remote host.
	if !strings.HasPrefix(path, "/api/") || strings.ContainsAny(path, "\\%") || strings.Contains(path, "//") {
		http.Error(w, "Invalid server path", 400)
		return
	}
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." {
			http.Error(w, "Invalid server path", 400)
			return
		}
	}
	streamUpgrade := desktopProjectStreamUpgrade(r)
	if strings.HasPrefix(path, "/api/connections") || strings.HasPrefix(path, "/api/desktop-connections") || (r.URL.Query().Has("token") && !streamUpgrade) {
		http.Error(w, "Invalid server path", 400)
		return
	}
	isStream := isDesktopProxyStreamingRequest(r, path, streamUpgrade)
	if !isStream {
		allowed, releaseProbe := desktopProxyTryAcquire(c.ID)
		if !allowed {
			http.Error(w, `{"error":"Cannot reach this server. Your other connections are still available."}`, 502)
			return
		}
		if releaseProbe != nil {
			defer releaseProbe()
		}
	}
	normalTransport, streamTransport := desktopProxyTransports()
	transport := normalTransport
	if isStream {
		transport = streamTransport
	}
	target, _ := url.Parse(c.Server)
	proxy := &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = path
			pr.Out.URL.RawPath = ""
			pr.Out.Host = target.Host
			if streamUpgrade {
				// The renderer's token is for the local backend, never the server.
				query := pr.Out.URL.Query()
				query.Del("token")
				pr.Out.URL.RawQuery = query.Encode()
			}
			for key := range pr.Out.Header {
				lower := strings.ToLower(key)
				if lower == "cookie" || lower == "origin" || lower == "referer" || strings.HasPrefix(lower, "x-") || strings.HasPrefix(lower, "sec-") {
					pr.Out.Header.Del(key)
				}
			}
			if streamUpgrade {
				// Strip local/security headers above, then restore only the two
				// mandatory WebSocket handshake fields. No subprotocol credentials.
				pr.Out.Header.Set("Sec-WebSocket-Key", r.Header.Get("Sec-WebSocket-Key"))
				pr.Out.Header.Set("Sec-WebSocket-Version", r.Header.Get("Sec-WebSocket-Version"))
			}
			pr.Out.Header.Set("Authorization", "Bearer "+c.Token)
			setCrewMacPair(pr.Out.Header, owner, c, path)
			// Session id is a request selector, validated against the remote user.
			for _, key := range []string{"X-Chat-Input", "X-Session-ID", "X-Client-Submitted-At", "X-Conversation-Continuation", "X-Queued-Chat-Delivery"} {
				if value := r.Header.Get(key); value != "" {
					pr.Out.Header.Set(key, value)
				}
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			clearDesktopProxyUnreachable(c.ID)
			if e := saveRenewedDesktopToken(owner, c, resp.Header.Get("X-Renewed-Token")); e != nil {
				return e
			}
			resp.Header.Del("X-Renewed-Token")
			resp.Header.Del("Set-Cookie")
			resp.Header.Del("X-AgentWorks-Login")
			// Redirect responses are not followed with a saved credential.
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				return fmt.Errorf("remote redirect refused")
			}
			return nil
		},
		ErrorHandler: func(out http.ResponseWriter, req *http.Request, proxyErr error) {
			if !isStream && proxyErr != nil && req.Context().Err() == nil && !errors.Is(proxyErr, context.Canceled) {
				markDesktopProxyUnreachable(c.ID)
			}
			http.Error(out, `{"error":"Cannot reach this server. Your other connections are still available."}`, 502)
		},
	}
	proxy.ServeHTTP(w, r)
}

type desktopProject struct {
	TeamLive      *crewTeamLiveView `json:"team_live,omitempty"`
	OwnedByCaller *bool             `json:"owned_by_caller,omitempty"`
	OwnerName     string            `json:"owner_name,omitempty"`
	SharedLine    string            `json:"shared_line,omitempty"`
	Icon          string            `json:"icon,omitempty"`
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	Mode          string            `json:"mode,omitempty"`
	Role          string            `json:"role,omitempty"`
}

// desktopMemberCrew resolves only this person's active member copy on the named
// connection. The Team lookup remains the single source of the local Crew ID.
func desktopMemberCrew(owner, connection, project string) string {
	local := crewTeamMemberCopy(project)
	if local == "" || ownerOfCrewProjectID(local) != owner {
		return ""
	}
	b, err := crewroot.ReadSync(local)
	if err != nil || b == nil || b.Editing != "local" || (connection != "local" && b.ConnectionID != connection) {
		return ""
	}
	if _, err := desktopConnectionFor(owner, b.ConnectionID); err != nil {
		return ""
	}
	return local
}

func (api *StreamingAPI) handleDesktopMemberCrew(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	local := desktopMemberCrew(GetUserFromContext(r.Context()).UserID, mux.Vars(r)["connection"], mux.Vars(r)["project"])
	desktopJSON(w, map[string]string{"project_id": local})
}

var (
	desktopInventoryFreshTTL = 30 * time.Second
	desktopInventoryStaleTTL = 5 * time.Minute
	desktopInventoryTimeout  = 5 * time.Second

	desktopInventoryCacheMu    sync.Mutex
	desktopInventoryCache      = map[string]desktopInventoryCacheEntry{}
	desktopInventoryRefreshing = map[string]bool{}
)

type desktopInventoryCacheEntry struct {
	projects   []desktopProject
	status     string
	freshUntil time.Time
	staleUntil time.Time
}

func cloneDesktopProjects(projects []desktopProject) []desktopProject {
	if projects == nil {
		return []desktopProject{}
	}
	out := make([]desktopProject, len(projects))
	copy(out, projects)
	return out
}

func desktopInventoryHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       desktopInventoryTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (api *StreamingAPI) handleDesktopConnectionInventory(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	writeSharedRead(w, r, "inventory\x00"+GetUserFromContext(r.Context()).UserID+"\x00"+mux.Vars(r)["connection"], api.readDesktopConnectionInventory)
}

func (api *StreamingAPI) readDesktopConnectionInventory(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["connection"]
	user := GetUserFromContext(r.Context()).UserID
	var selfConnection desktopConnection
	base, token := "http://"+desktopBootstrapHost, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if id != "local" {
		c, err := desktopConnectionFor(user, id)
		if err != nil {
			http.Error(w, `{"error":"Connection is unavailable."}`, 404)
			return
		}
		base, token = c.Server, c.Token
		selfConnection = c
	}
	if id != "local" {
		cacheKey := user + "\x00" + id + "\x00" + base
		forceRefresh := r.URL.Query().Get("refresh") == "true"
		now := time.Now()
		if !forceRefresh {
			desktopInventoryCacheMu.Lock()
			entry, ok := desktopInventoryCache[cacheKey]
			if ok && now.Before(entry.staleUntil) {
				projects := cloneDesktopProjects(entry.projects)
				status := entry.status
				needRefresh := !now.Before(entry.freshUntil) && !desktopInventoryRefreshing[cacheKey] && !desktopProxyInCooldown(id)
				if needRefresh {
					desktopInventoryRefreshing[cacheKey] = true
				}
				desktopInventoryCacheMu.Unlock()
				if needRefresh {
					go func() {
						defer func() {
							desktopInventoryCacheMu.Lock()
							delete(desktopInventoryRefreshing, cacheKey)
							desktopInventoryCacheMu.Unlock()
						}()
						defer recoverDetachedGoroutine("desktop_inventory_refresh", cacheKey)
						refreshCtx, cancel := context.WithTimeout(context.Background(), desktopInventoryTimeout)
						defer cancel()
						refreshedProjects, refreshedStatus := api.fetchDesktopConnectionInventory(refreshCtx, id, user, base, token, selfConnection, "")
						api.storeDesktopInventoryCache(cacheKey, id, refreshedProjects, refreshedStatus)
					}()
				}
				desktopJSON(w, map[string]any{"projects": projects, "status": status})
				return
			}
			desktopInventoryCacheMu.Unlock()
		}
		if desktopProxyInCooldown(id) {
			desktopJSON(w, map[string]any{"projects": []desktopProject{}, "status": "unavailable"})
			return
		}
		fetchCtx, cancel := context.WithTimeout(r.Context(), desktopInventoryTimeout)
		defer cancel()
		projects, status := api.fetchDesktopConnectionInventory(fetchCtx, id, user, base, token, selfConnection, clientSwitchID(r))
		api.storeDesktopInventoryCache(cacheKey, id, projects, status)
		desktopJSON(w, map[string]any{"projects": projects, "status": status})
		return
	}
	projects, status := api.fetchDesktopConnectionInventory(r.Context(), id, user, base, token, selfConnection, clientSwitchID(r))
	desktopJSON(w, map[string]any{"projects": projects, "status": status})
}

func (api *StreamingAPI) storeDesktopInventoryCache(cacheKey, connID string, projects []desktopProject, status string) {
	now := time.Now()
	switch status {
	case "connected":
		clearDesktopProxyUnreachable(connID)
		desktopInventoryCacheMu.Lock()
		desktopInventoryCache[cacheKey] = desktopInventoryCacheEntry{
			projects:   cloneDesktopProjects(projects),
			status:     status,
			freshUntil: now.Add(desktopInventoryFreshTTL),
			staleUntil: now.Add(desktopInventoryStaleTTL),
		}
		desktopInventoryCacheMu.Unlock()
	case "sign_in":
		desktopInventoryCacheMu.Lock()
		delete(desktopInventoryCache, cacheKey)
		desktopInventoryCacheMu.Unlock()
	case "unavailable":
		markDesktopProxyUnreachable(connID)
	}
}

func (api *StreamingAPI) fetchDesktopConnectionInventory(ctx context.Context, id, user, base, token string, selfConnection desktopConnection, switchID string) ([]desktopProject, string) {
	type result struct {
		projects []desktopProject
		status   int
	}
	results := make(chan result, 2)
	failed := func(kind string, status int) {
		log.Printf("[DESKTOP_INVENTORY] connection=%s list=%s status=%d", id, kind, status)
	}
	client := desktopHTTPClient()
	if id != "local" {
		client = desktopInventoryHTTPClient()
	}
	for _, kind := range []string{"crew", "goal"} {
		go func(kind string) {
			started := time.Now()
			defer func() {
				if switchID != "" {
					log.Printf("[SWITCH_INVENTORY] switch=%q kind=%s duration=%s", switchID, kind, time.Since(started).Round(time.Millisecond))
				}
			}()
			path, method, body := "/api/workflows/manifests", "GET", ""
			if kind == "crew" {
				path, method, body = "/api/workspaces", "POST", `{"action":"list","detail":"navigation"}`
				if id != "local" {
					body = `{"action":"list"}`
				}
			}
			req, err := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(body))
			if err != nil {
				failed(kind, 502)
				results <- result{status: 502}
				return
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err != nil {
				failed(kind, 502)
				results <- result{status: 502}
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				failed(kind, resp.StatusCode)
				results <- result{status: resp.StatusCode}
				return
			}
			var inventory struct {
				Workspaces []struct {
					TeamLive      *crewTeamLiveView `json:"team_live"`
					OwnedByCaller *bool             `json:"owned_by_caller"`
					OwnerName     string            `json:"owner_name"`
					SharedLine    string            `json:"shared_line"`
					ID            string            `json:"id"`
					Name          string            `json:"name"`
					Mode          string            `json:"mode"`
					Role          string            `json:"role"`
				} `json:"workspaces"`
				Workflows []struct {
					Manifest struct {
						ID    string `json:"id"`
						Label string `json:"label"`
						Icon  string `json:"icon"`
						Kind  string `json:"kind"`
					} `json:"manifest"`
				} `json:"workflows"`
			}
			if json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&inventory) != nil {
				failed(kind, 502)
				results <- result{status: 502}
				return
			}
			projects := []desktopProject{}
			for _, p := range inventory.Workspaces {
				if id != "local" && desktopMemberCrew(user, id, p.ID) != "" {
					continue
				}
				// One row per Crew: skip the online copy of one of this person's own Mac Crews paired with this connection.
				if id != "local" {
					if local := crewLocalCopyOf(p.ID); local != "" && ownerOfCrewProjectID(local) == user {
						if b, err := crewroot.ReadSync(local); err == nil && b != nil && b.Side == "local" && b.ConnectionID == id {
							continue
						}
					}
				}
				if id != "local" {
					crewTeamLiveLocalSelf(p.TeamLive, selfConnection)
				}
				projects = append(projects, desktopProject{TeamLive: p.TeamLive, ID: p.ID, Name: p.Name, Kind: "crew", Mode: p.Mode, Role: p.Role, OwnedByCaller: p.OwnedByCaller, OwnerName: p.OwnerName, SharedLine: p.SharedLine})
			}
			for _, p := range inventory.Workflows {
				if p.Manifest.Kind != "relay" {
					projects = append(projects, desktopProject{ID: p.Manifest.ID, Name: p.Manifest.Label, Kind: "goal", Icon: p.Manifest.Icon})
				}
			}
			results <- result{projects: projects, status: 200}
		}(kind)
	}
	projects := []desktopProject{}
	status := "connected"
	for range 2 {
		result := <-results
		projects = append(projects, result.projects...)
		if result.status == 401 {
			status = "sign_in"
		} else if result.status != 200 && status != "sign_in" {
			status = "unavailable"
		}
	}
	return projects, status
}
