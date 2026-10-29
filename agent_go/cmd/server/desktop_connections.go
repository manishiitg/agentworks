package server

// Desktop connections use ordinary remote website sessions. The local server
// owns the credentials; Electron proves that the caller is its top-level app.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

type desktopConnection struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Server string   `json:"server"`
	User   UserInfo `json:"user"`
	Token  string   `json:"token,omitempty"` // Only in the encrypted file, never in a response.
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

func desktopConnectionGate(w http.ResponseWriter, r *http.Request) bool {
	secret, host := os.Getenv("AGENTWORKS_DESKTOP_SECRET"), os.Getenv("AGENTWORKS_DESKTOP_HOST")
	u := GetUserFromContext(r.Context())
	// Single-user JWTs alone aren't a desktop proof: localhost login issues them
	// without a password. Never admit PAT/MCP callers to these saved web sessions.
	if secret == "" || host == "" || r.Host != host || u == nil || u.AccessToken != nil || r.Header.Get("Authorization") == "" ||
		subtle.ConstantTimeCompare([]byte(secret), []byte(r.Header.Get("X-AgentWorks-Desktop"))) != 1 {
		http.Error(w, `{"error":"Open this connection in the Mac app."}`, http.StatusForbidden)
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+host {
		http.Error(w, `{"error":"Use the app window."}`, http.StatusForbidden)
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
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
	return os.WriteFile(path, []byte(sealed), 0600)
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
		w.WriteHeader(http.StatusNoContent)
		return
	}
	for i := range records {
		records[i].Token = ""
	}
	desktopJSON(w, map[string]any{"connections": records})
}

func desktopServerURL(value string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(value), "/"))
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" ||
		(u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"))) {
		return nil, fmt.Errorf("Use the server's HTTPS address, without a path.")
	}
	return u, nil
}
func desktopHTTPClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func (api *StreamingAPI) beginDesktopSignIn(w http.ResponseWriter, r *http.Request, owner string) {
	var input struct {
		Server string `json:"server"`
		Name   string `json:"name"`
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
			var auth AuthResponse
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
				name := strings.TrimSpace(input.Name)
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
	if strings.HasPrefix(path, "/api/connections") || strings.HasPrefix(path, "/api/desktop-connections") || r.URL.Query().Has("token") {
		http.Error(w, "Invalid server path", 400)
		return
	}
	target, _ := url.Parse(c.Server)
	proxy := &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = path
			pr.Out.URL.RawPath = ""
			pr.Out.Host = target.Host
			for key := range pr.Out.Header {
				lower := strings.ToLower(key)
				if lower == "cookie" || lower == "origin" || lower == "referer" || strings.HasPrefix(lower, "x-") || strings.HasPrefix(lower, "sec-") {
					pr.Out.Header.Del(key)
				}
			}
			pr.Out.Header.Set("Authorization", "Bearer "+c.Token)
			// Session id is a request selector, validated against the remote user.
			for _, key := range []string{"X-Session-ID", "X-Client-Submitted-At", "X-Conversation-Continuation", "X-Queued-Chat-Delivery"} {
				if value := r.Header.Get(key); value != "" {
					pr.Out.Header.Set(key, value)
				}
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			if token := resp.Header.Get("X-Renewed-Token"); token != "" {
				desktopConnectionsMu.Lock()
				defer desktopConnectionsMu.Unlock()
				records, e := readDesktopConnections(owner)
				if e != nil {
					return e
				}
				for i := range records {
					if records[i].ID == c.ID && records[i].Token == c.Token {
						records[i].Token = token
					}
				}
				if e = writeDesktopConnections(owner, records); e != nil {
					return e
				}
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
		ErrorHandler: func(out http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(out, `{"error":"Cannot reach this server. Your other connections are still available."}`, 502)
		},
	}
	proxy.ServeHTTP(w, r)
}

type desktopProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func (api *StreamingAPI) handleDesktopConnectionInventory(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	id := mux.Vars(r)["connection"]
	base, token := "http://"+os.Getenv("AGENTWORKS_DESKTOP_HOST"), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if id != "local" {
		c, err := desktopConnectionFor(GetUserFromContext(r.Context()).UserID, id)
		if err != nil {
			http.Error(w, `{"error":"Connection is unavailable."}`, 404)
			return
		}
		base, token = c.Server, c.Token
	}
	type result struct {
		projects []desktopProject
		status   int
	}
	results := make(chan result, 2)
	for _, kind := range []string{"crew", "goal"} {
		go func(kind string) {
			path, method, body := "/api/workflows/manifests", "GET", ""
			if kind == "crew" {
				path, method, body = "/api/workspaces", "POST", `{"action":"list","detail":"navigation"}`
			}
			req, err := http.NewRequestWithContext(r.Context(), method, base+path, strings.NewReader(body))
			if err != nil {
				results <- result{status: 502}
				return
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			resp, err := desktopHTTPClient().Do(req)
			if err != nil {
				results <- result{status: 502}
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				results <- result{status: resp.StatusCode}
				return
			}
			var inventory struct {
				Workspaces []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"workspaces"`
				Workflows []struct {
					Manifest struct {
						ID    string `json:"id"`
						Label string `json:"label"`
						Kind  string `json:"kind"`
					} `json:"manifest"`
				} `json:"workflows"`
			}
			if json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&inventory) != nil {
				results <- result{status: 502}
				return
			}
			projects := []desktopProject{}
			for _, p := range inventory.Workspaces {
				projects = append(projects, desktopProject{p.ID, p.Name, "crew"})
			}
			for _, p := range inventory.Workflows {
				if p.Manifest.Kind != "relay" {
					projects = append(projects, desktopProject{p.Manifest.ID, p.Manifest.Label, "goal"})
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
	desktopJSON(w, map[string]any{"projects": projects, "status": status})
}
