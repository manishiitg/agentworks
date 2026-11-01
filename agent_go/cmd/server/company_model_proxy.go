package server

// Company AI accounts on the Mac (PLAT-856-441, part B): a person uses their server's AI login from the Mac app.
// The login never leaves the server. A model proxy on the server holds it (configured with
// AGENTWORKS_MODEL_PROXY_URL and AGENTWORKS_MODEL_PROXY_KEY_FILE); the server passes a person's requests to it when
// that person may use the server's account for the program. On the Mac, the program runs locally (its tools and
// files stay on the Mac) and sends its model requests to the Mac's own backend, which forwards them to the server
// over the saved connection. Muse only for now: it takes the address with --base-url.
import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// companyProxyModels is each program the server can lend through its model proxy, and the model names it may ask for.
var companyProxyModels = map[string]string{"muse-cli": "muse-"}

// companyProxyPaths are the only proxy routes a program needs.
var companyProxyPaths = map[string]bool{"responses": true, "models": true}

// companyProxyClient streams replies for as long as a turn runs.
var companyProxyClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func companyModelProxyUpstream() (string, string, bool) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("AGENTWORKS_MODEL_PROXY_URL")), "/")
	file := strings.TrimSpace(os.Getenv("AGENTWORKS_MODEL_PROXY_KEY_FILE"))
	if base == "" || file == "" {
		return "", "", false
	}
	raw, err := os.ReadFile(file)
	key := strings.TrimSpace(string(raw))
	return base, key, err == nil && key != ""
}

// copyProxyResponse passes a reply through as it arrives (streamed model output included). Everything written also
// goes to tap, when given.
func copyProxyResponse(w http.ResponseWriter, resp *http.Response, tap io.Writer) {
	for _, name := range []string{"Content-Type", "Cache-Control"} {
		if value := resp.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
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

// readProxyRequest reads a model request and checks it asks for one of the program's models.
func readProxyRequest(r *http.Request, provider string) ([]byte, string, error) {
	if r.Method != http.MethodPost {
		return nil, "", nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		return nil, "", err
	}
	var request struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &request) != nil || !strings.HasPrefix(request.Model, companyProxyModels[provider]) {
		return nil, "", fmt.Errorf("this account only runs %s models", providerAccountProductName(provider))
	}
	return body, request.Model, nil
}

// proxyUsage keeps the final usage of a Responses reply as it streams past: the whole body of a plain JSON reply, or
// the "response.completed" event of a stream.
type proxyUsage struct {
	stream  bool
	line    []byte
	final   []byte
	partial bool
}

func (u *proxyUsage) Write(p []byte) (int, error) {
	if !u.stream {
		if len(u.final)+len(p) <= 16<<20 {
			u.final = append(u.final, p...)
		} else {
			u.partial = true
		}
		return len(p), nil
	}
	for _, b := range p {
		if b != '\n' {
			if len(u.line) < 16<<20 {
				u.line = append(u.line, b)
			}
			continue
		}
		if data, ok := bytes.CutPrefix(u.line, []byte("data:")); ok && bytes.Contains(data, []byte(`"response.completed"`)) {
			u.final = append([]byte(nil), bytes.TrimSpace(data)...)
		}
		u.line = u.line[:0]
	}
	return len(p), nil
}

// tokens reads input, output, reasoning and cached-input tokens from the reply's usage.
func (u *proxyUsage) tokens() (input, output, reasoning, cached int, ok bool) {
	var reply struct {
		Response *struct {
			Usage json.RawMessage `json:"usage"`
		} `json:"response"`
		Usage json.RawMessage `json:"usage"`
	}
	if u.partial || json.Unmarshal(u.final, &reply) != nil {
		return 0, 0, 0, 0, false
	}
	raw := reply.Usage
	if reply.Response != nil {
		raw = reply.Response.Usage
	}
	var usage struct {
		Input   int `json:"input_tokens"`
		Output  int `json:"output_tokens"`
		Details struct {
			Cached int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		OutputDetails struct {
			Reasoning int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &usage) != nil {
		return 0, 0, 0, 0, false
	}
	return usage.Input, usage.Output, usage.OutputDetails.Reasoning, usage.Details.Cached, true
}

// GET/POST /api/model-proxy/{provider}/v1/{path} — on the server: a person's model request on the server's account,
// under the same rules as a chat on that account here: who may use it, its allowed models and the person's token
// limits; the tokens are recorded to the person.
func (api *StreamingAPI) handleCompanyModelProxy(w http.ResponseWriter, r *http.Request) {
	caller, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	// Only a person's own sign-in (the Mac app's connection), never an access token such as an MCP or project token,
	// which agents on this server hold.
	if u := GetUserFromContext(r.Context()); u == nil || u.AccessToken != nil {
		desktopJSONError(w, 403, "Use the Mac app's connection to this server.")
		return
	}
	provider, path := mux.Vars(r)["provider"], mux.Vars(r)["path"]
	base, key, configured := companyModelProxyUpstream()
	if _, offered := companyProxyModels[provider]; !offered || !configured || !providerEnabled(provider) {
		desktopJSONError(w, 404, "This server does not offer its "+providerAccountProductName(provider)+" account for use on your Mac.")
		return
	}
	if !companyProxyPaths[path] {
		desktopJSONError(w, 404, "Not found.")
		return
	}
	account := "global:" + provider
	if _, err := api.admitProviderAccount(r.Context(), providerAccountScope{Principal: caller}, provider, account); err != nil {
		desktopJSONError(w, 403, err.Error())
		return
	}
	body, model, err := readProxyRequest(r, provider)
	if err != nil {
		desktopJSONError(w, 400, err.Error())
		return
	}
	if err := checkAccountModel(withModelLimitPerson(r.Context(), caller), provider, account, model); err != nil {
		desktopJSONError(w, 400, err.Error())
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, base+"/v1/"+path, bytes.NewReader(body))
	if err != nil {
		desktopJSONError(w, 500, "Cannot reach the model proxy.")
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", r.Header.Get("Accept"))
	started := time.Now()
	resp, err := companyProxyClient.Do(req)
	if err != nil {
		log.Printf("[MODEL_PROXY] %s %s %s: proxy unreachable: %v", caller, provider, path, err)
		desktopJSONError(w, 502, "The server's model proxy is not answering.")
		return
	}
	defer resp.Body.Close()
	usage := &proxyUsage{stream: strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream")}
	copyProxyResponse(w, resp, usage)
	if model == "" {
		return
	}
	input, output, reasoning, cached, counted := usage.tokens()
	log.Printf("[MODEL_PROXY] %s used the server's %s account from the Mac: %s %s -> %d (%s) tokens in=%d out=%d cached=%d counted=%v", caller, provider, model, path, resp.StatusCode, time.Since(started).Round(time.Millisecond), input, output, cached, counted)
	if !counted || api.tokenLimitLedger() == nil {
		return
	}
	entry := costledger.Entry{
		Attribution: costledger.Attribution{AttributionVersion: costledger.AttributionVersion, ActorID: caller, Activity: "mac_app"},
		EventID:     uuid.NewString(), Timestamp: time.Now().UTC(), UserID: caller, Scope: "chat", Component: "mac_company_account",
		Provider: provider, ModelID: model, AccountID: account, LLMCallCount: 1, BillingBasis: "unpriced",
		PromptTokens: input, CompletionTokens: output, ReasoningTokens: reasoning, CacheReadTokens: cached,
		LLMGenerationDurationMS: time.Since(started).Milliseconds(),
	}
	if err := api.tokenLimitLedger().Append(entry); err != nil {
		log.Printf("[COST_LEDGER] Mac use of the %s account by %s not recorded: %v", provider, caller, err)
	}
}

// companyAccountOnMac finds the Mac's account for a server's company account, by its local id.
func companyAccountOnMac(records []storedProviderConnection, id string) (storedProviderConnection, bool) {
	for _, record := range records {
		if record.ID == id && record.ViaConnection != "" {
			return record, true
		}
	}
	return storedProviderConnection{}, false
}

// companyAccountBaseURL is where the program on this Mac sends its model requests: this Mac's own backend.
func companyAccountBaseURL(record storedProviderConnection) string {
	return "http://" + desktopBootstrapHost + "/api/desktop-model-proxy/" + record.ID + "/v1"
}

// ANY /api/desktop-model-proxy/{account}/v1/{path} — on the Mac: the program's model request, forwarded to the server
// over the saved connection. The program proves itself with the account's own key, which only this Mac knows; it
// never sees the connection's sign-in.
func (api *StreamingAPI) handleDesktopModelProxy(w http.ResponseWriter, r *http.Request) {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err != nil || !net.ParseIP(host).IsLoopback() {
		desktopJSONError(w, 403, "Only programs on this Mac can use this.")
		return
	}
	records, err := loadProviderConnections(r.Context())
	if err != nil {
		desktopJSONError(w, 500, "Cannot read your accounts.")
		return
	}
	record, ok := companyAccountOnMac(records, mux.Vars(r)["account"])
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(record.Credential)) != 1 {
		desktopJSONError(w, 401, "Unknown account.")
		return
	}
	c, err := desktopConnectionFor(record.OwnerUserID, record.ViaConnection)
	if err != nil {
		desktopJSONError(w, 502, "The server connection is gone. Connect to the server again in the Mac app.")
		return
	}
	path := mux.Vars(r)["path"]
	if !companyProxyPaths[path] {
		desktopJSONError(w, 404, "Not found.")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, c.Server+"/api/model-proxy/"+record.Provider+"/v1/"+path, io.LimitReader(r.Body, 32<<20))
	if err != nil {
		desktopJSONError(w, 500, "Cannot reach the server.")
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", r.Header.Get("Accept"))
	resp, err := companyProxyClient.Do(req)
	if err != nil {
		desktopJSONError(w, 502, "Cannot reach "+c.Name+".")
		return
	}
	defer resp.Body.Close()
	copyProxyResponse(w, resp, nil)
}

// GET  /api/desktop-connections/{connection}/company-accounts?provider=<program>
//
//	-> {"state": "added" | "available" | "unavailable" | "not_offered", "account_id", "reason"}
//
// POST /api/desktop-connections/{connection}/company-accounts {"provider"} -> adds "<server> · <program> (company)"
// to this Mac's accounts: the program runs here on the server's login.
func (api *StreamingAPI) handleDesktopCompanyAccount(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	owner := GetUserFromContext(r.Context()).UserID
	c, err := desktopConnectionFor(owner, mux.Vars(r)["connection"])
	if err != nil {
		desktopJSONError(w, 404, "Connection is unavailable. Connect to the server again.")
		return
	}
	provider := r.URL.Query().Get("provider")
	if r.Method == http.MethodPost {
		var input struct {
			Provider string `json:"provider"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&input)
		provider = input.Provider
	}
	if _, offered := companyProxyModels[provider]; !offered {
		desktopJSON(w, map[string]string{"state": "not_offered"})
		return
	}
	providerConnectionsMu.Lock()
	defer providerConnectionsMu.Unlock()
	records, err := loadProviderConnections(r.Context())
	if err != nil {
		desktopJSONError(w, 500, "Cannot read your accounts.")
		return
	}
	for _, record := range records {
		if record.OwnerUserID == owner && record.Provider == provider && record.ViaConnection == c.ID {
			desktopJSON(w, map[string]string{"state": "added", "account_id": record.ID})
			return
		}
	}
	// The server decides: it answers only when it lends this program's account and this person may use it.
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, c.Server+"/api/model-proxy/"+provider+"/v1/models", nil)
	if err != nil {
		desktopJSONError(w, 500, "Cannot reach the server.")
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := desktopHTTPClient().Do(req)
	if err != nil {
		desktopJSON(w, map[string]string{"state": "unavailable", "reason": "Cannot reach " + c.Name + "."})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var refusal struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&refusal)
		if refusal.Error == "" {
			refusal.Error = c.Name + " does not offer this yet."
		}
		desktopJSON(w, map[string]string{"state": "unavailable", "reason": refusal.Error})
		return
	}
	if r.Method != http.MethodPost {
		desktopJSON(w, map[string]string{"state": "available"})
		return
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		desktopJSONError(w, 500, "Cannot add the account.")
		return
	}
	record := storedProviderConnection{ProviderConnection: ProviderConnection{ID: uuid.NewString(), Provider: provider, DisplayName: c.Name + " · " + providerAccountProductName(provider) + " (company)",
		OwnerUserID: owner, Scope: "user", AuthMethod: "api_key", UpdatedAt: time.Now().UTC(), ViaConnection: c.ID}, Credential: hex.EncodeToString(secret)}
	if err := saveProviderConnections(r.Context(), append(records, record)); err != nil {
		desktopJSONError(w, 500, "Cannot save the account.")
		return
	}
	log.Printf("[DESKTOP_ACCOUNT] %s added the company %s account of connection %s as %s", owner, provider, c.ID, record.ID)
	desktopJSON(w, map[string]string{"state": "added", "account_id": record.ID})
}
