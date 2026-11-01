package server

// Company AI accounts on the Mac (PLAT-856-441, part B): a person uses their server's AI login from the Mac app.
// The login never leaves the server. The server's model proxy holds it (model_proxy_sidecar.go); the server passes a
// person's requests to it when that person may use the server's account for the program. On the Mac, the program
// runs locally (its tools and files stay on the Mac) and sends its model requests to the Mac's own backend, which
// forwards them to the server over the saved connection. Muse takes the address with --base-url, Codex from
// openai_base_url in the account's own config.
import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// companyProxyModels is each program the server can lend through its model proxy, and the model names it may ask for.
var companyProxyModels = map[string]string{"muse-cli": "muse-", "codex-cli": "gpt-", "claude-code": "claude-", "cursor-cli": ""}

// companyProxyPaths are the only proxy routes a program needs.
var companyProxyPaths = map[string]bool{"responses": true, "models": true, "messages": true, "messages/count_tokens": true}

// companyProxyHeaders are the request headers a program's API needs passed through (Anthropic's version and betas).
var companyProxyHeaders = []string{"Accept", "Anthropic-Version", "Anthropic-Beta", "Openai-Beta"}

func copyProxyHeaders(to, from *http.Request) {
	for _, name := range companyProxyHeaders {
		if value := from.Header.Get(name); value != "" {
			to.Header.Set(name, value)
		}
	}
}

// proxyCallerKey is the key a program on the Mac presents: a Bearer token, or Anthropic's x-api-key.
func proxyCallerKey(r *http.Request) string {
	if key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); key != "" && key != r.Header.Get("Authorization") {
		return key
	}
	return r.Header.Get("X-Api-Key")
}

// companyProxyClient streams replies for as long as a turn runs.
var companyProxyClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

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
	stream       bool
	line         []byte
	final        []byte
	partial      bool
	anthropic    anthropicUsage
	sawAnthropic bool
}

// anthropicUsage is Anthropic's usage block; output_tokens in a stream is the running total, so the largest wins.
type anthropicUsage struct {
	Input       int `json:"input_tokens"`
	Output      int `json:"output_tokens"`
	CacheRead   int `json:"cache_read_input_tokens"`
	CacheCreate int `json:"cache_creation_input_tokens"`
}

func (a *anthropicUsage) add(next anthropicUsage) {
	a.Input, a.CacheRead, a.CacheCreate = max(a.Input, next.Input), max(a.CacheRead, next.CacheRead), max(a.CacheCreate, next.CacheCreate)
	a.Output = max(a.Output, next.Output)
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
		if data, ok := bytes.CutPrefix(u.line, []byte("data:")); ok {
			switch {
			case bytes.Contains(data, []byte(`"response.completed"`)):
				u.final = append([]byte(nil), bytes.TrimSpace(data)...)
			case bytes.Contains(data, []byte(`"message_start"`)), bytes.Contains(data, []byte(`"message_delta"`)):
				// Anthropic: input usage arrives with message_start, output usage with the last message_delta.
				var event struct {
					Message struct {
						Usage anthropicUsage `json:"usage"`
					} `json:"message"`
					Usage anthropicUsage `json:"usage"`
				}
				if json.Unmarshal(bytes.TrimSpace(data), &event) == nil {
					u.anthropic.add(event.Message.Usage)
					u.anthropic.add(event.Usage)
					u.sawAnthropic = true
				}
			}
		}
		u.line = u.line[:0]
	}
	return len(p), nil
}

// tokens reads input, output, reasoning and cached-input tokens from the reply's usage.
func (u *proxyUsage) tokens() (input, output, reasoning, cached int, ok bool) {
	if u.sawAnthropic {
		a := u.anthropic
		return a.Input + a.CacheRead + a.CacheCreate, a.Output, 0, a.CacheRead, true
	}
	var plain struct {
		Type  string         `json:"type"`
		Usage anthropicUsage `json:"usage"`
	}
	if !u.stream && json.Unmarshal(u.final, &plain) == nil && plain.Type == "message" {
		a := plain.Usage
		return a.Input + a.CacheRead + a.CacheCreate, a.Output, 0, a.CacheRead, true
	}
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
	if _, offered := companyProxyModels[provider]; !offered || !providerEnabled(provider) || companyLendingLabel(r.Context(), provider) == "" {
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
	// A key or Cursor relay has no proxy model list: the Mac only asks whether the program is offered.
	if path == "models" && (companyLendsDirectly(r.Context(), provider) != "" || provider == "cursor-cli") {
		desktopJSON(w, map[string]any{"object": "list", "data": []map[string]string{{"id": provider, "object": "model"}}})
		return
	}
	if provider == "cursor-cli" {
		desktopJSONError(w, 404, "Not found.")
		return
	}
	base, sign, configured := companyUpstream(r.Context(), provider)
	if !configured {
		desktopJSONError(w, 404, "This server does not offer its "+providerAccountProductName(provider)+" account for use on your Mac.")
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
	req, err := http.NewRequestWithContext(r.Context(), r.Method, base+"/"+path, bytes.NewReader(body))
	if err != nil {
		desktopJSONError(w, 500, "Cannot reach the model proxy.")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	copyProxyHeaders(req, r)
	sign(req)
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

// companyProxyCatalogs is where each program asks for its model catalog, relative to the root of its address. Muse's
// full-screen program will not start without it (exec does not need it). The catalog carries each model's settings,
// so the server serves the provider's own catalog, which a root job on the server refreshes next to the proxy key.
var companyProxyCatalogs = map[string]string{"muse-cli": "/muse-code/models"}

// GET /api/model-proxy/{provider}/catalog — on the server: the program's model catalog, for people who may use the
// server's account.
func (api *StreamingAPI) handleCompanyModelCatalog(w http.ResponseWriter, r *http.Request) {
	caller, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	if u := GetUserFromContext(r.Context()); u == nil || u.AccessToken != nil {
		desktopJSONError(w, 403, "Use the Mac app's connection to this server.")
		return
	}
	provider := mux.Vars(r)["provider"]
	if companyProxyCatalogs[provider] == "" || companyLendingLabel(r.Context(), provider) == "" {
		desktopJSONError(w, 404, "Not found.")
		return
	}
	if _, err := api.admitProviderAccount(r.Context(), providerAccountScope{Principal: caller}, provider, "global:"+provider); err != nil {
		desktopJSONError(w, 403, err.Error())
		return
	}
	raw, err := modelProxyCatalog(r.Context(), provider)
	if err != nil && raw == nil {
		log.Printf("[MODEL_PROXY] %s catalog unavailable: %v", provider, err)
		desktopJSONError(w, 503, "The server has no model list for this program yet.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

// GET /muse-code/models (and other programs' catalog paths) — on the Mac: the program asks at the root of its
// address, so the account is found by the key it presents; then the server's catalog is fetched over the connection.
func (api *StreamingAPI) handleDesktopModelCatalog(provider string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err != nil || !net.ParseIP(host).IsLoopback() {
			desktopJSONError(w, 403, "Only programs on this Mac can use this.")
			return
		}
		records, err := loadProviderConnections(r.Context())
		if err != nil {
			desktopJSONError(w, 500, "Cannot read your accounts.")
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		var record *storedProviderConnection
		for i := range records {
			if records[i].ViaConnection != "" && records[i].Provider == provider && token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(records[i].Credential)) == 1 {
				record = &records[i]
			}
		}
		if record == nil {
			desktopJSONError(w, 401, "Unknown account.")
			return
		}
		c, err := desktopConnectionFor(record.OwnerUserID, record.ViaConnection)
		if err != nil {
			desktopJSONError(w, 502, "The server connection is gone. Connect to the server again in the Mac app.")
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, c.Server+"/api/model-proxy/"+provider+"/catalog", nil)
		if err != nil {
			desktopJSONError(w, 500, "Cannot reach the server.")
			return
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		resp, err := desktopHTTPClient().Do(req)
		if err != nil {
			desktopJSONError(w, 502, "Cannot reach "+c.Name+".")
			return
		}
		defer resp.Body.Close()
		copyProxyResponse(w, resp, nil)
	}
}

// desktopCompanyCaller checks a program on this Mac presenting a company account's own key, and returns the account
// and the server connection it runs on.
func desktopCompanyCaller(w http.ResponseWriter, r *http.Request) (storedProviderConnection, desktopConnection, bool) {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err != nil || !net.ParseIP(host).IsLoopback() {
		desktopJSONError(w, 403, "Only programs on this Mac can use this.")
		return storedProviderConnection{}, desktopConnection{}, false
	}
	records, err := loadProviderConnections(r.Context())
	if err != nil {
		desktopJSONError(w, 500, "Cannot read your accounts.")
		return storedProviderConnection{}, desktopConnection{}, false
	}
	record, ok := companyAccountOnMac(records, mux.Vars(r)["account"])
	token := proxyCallerKey(r)
	if !ok || token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(record.Credential)) != 1 {
		desktopJSONError(w, 401, "Unknown account.")
		return storedProviderConnection{}, desktopConnection{}, false
	}
	c, err := desktopConnectionFor(record.OwnerUserID, record.ViaConnection)
	if err != nil {
		desktopJSONError(w, 502, "The server connection is gone. Connect to the server again in the Mac app.")
		return storedProviderConnection{}, desktopConnection{}, false
	}
	return record, c, true
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
	record, c, ok := desktopCompanyCaller(w, r)
	if !ok {
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
	copyProxyHeaders(req, r)
	resp, err := companyProxyClient.Do(req)
	if err != nil {
		desktopJSONError(w, 502, "Cannot reach "+c.Name+".")
		return
	}
	defer resp.Body.Close()
	copyProxyResponse(w, resp, nil)
}

// companyAccountOffered asks a connected server whether it lends its account for the program to this person: it lists
// the program's models. known is false when the server could not be asked (offline): nothing changes then. An older
// server answers unknown addresses with its web page, which is "not offered".
func companyAccountOffered(ctx context.Context, c desktopConnection, provider string) (offered, known bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Server+"/api/model-proxy/"+provider+"/v1/models", nil)
	if err != nil {
		return false, false
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := desktopHTTPClient().Do(req)
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()
	var answer struct {
		Data []json.RawMessage `json:"data"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&answer)
	return resp.StatusCode == http.StatusOK && len(answer.Data) > 0, true
}

// companySync remembers when each person's company accounts were last matched with their servers.
var companySync = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

// forgetCompanySync makes the next account list ask the servers again (a server was connected or removed).
func forgetCompanySync(owner string) {
	companySync.Lock()
	delete(companySync.at, owner)
	companySync.Unlock()
}

// syncCompanyAccounts, on the Mac, keeps "<server> · <program> (company)" in the person's accounts for every connected
// server that lends that program's account to them, and removes it when the server stops or the connection is gone.
// Connecting to a server is all it takes: the account then shows wherever accounts are picked, Crews included.
func (api *StreamingAPI) syncCompanyAccounts(ctx context.Context, owner string) {
	if desktopBootstrapHost == "" {
		return
	}
	companySync.Lock()
	if time.Since(companySync.at[owner]) < 2*time.Minute {
		companySync.Unlock()
		return
	}
	companySync.at[owner] = time.Now()
	companySync.Unlock()
	desktopConnectionsMu.Lock()
	connections, err := readDesktopConnections(owner)
	desktopConnectionsMu.Unlock()
	if err != nil {
		return
	}
	// want[connection][program]: true offered, false not offered; absent when the server could not be asked.
	want := map[string]map[string]bool{}
	names := map[string]string{}
	for _, c := range connections {
		want[c.ID], names[c.ID] = map[string]bool{}, c.Name
		for provider := range companyProxyModels {
			if offered, known := companyAccountOffered(ctx, c, provider); known {
				want[c.ID][provider] = offered
			}
		}
	}
	providerConnectionsMu.Lock()
	defer providerConnectionsMu.Unlock()
	records, err := loadProviderConnections(ctx)
	if err != nil {
		return
	}
	next := make([]storedProviderConnection, 0, len(records))
	have := map[string]bool{}
	changed := false
	for _, record := range records {
		if record.ViaConnection != "" && record.OwnerUserID == owner {
			offered, known := want[record.ViaConnection][record.Provider]
			if _, connected := want[record.ViaConnection]; !connected || (known && !offered) {
				log.Printf("[DESKTOP_ACCOUNT] %s: company %s account removed (connection %s gone or no longer lends it)", owner, record.Provider, record.ViaConnection)
				changed = true
				continue
			}
			have[record.ViaConnection+"/"+record.Provider] = true
		}
		next = append(next, record)
	}
	for id, programs := range want {
		for provider, offered := range programs {
			if !offered || have[id+"/"+provider] {
				continue
			}
			secret := make([]byte, 32)
			if _, err := rand.Read(secret); err != nil {
				return
			}
			record := storedProviderConnection{ProviderConnection: ProviderConnection{ID: uuid.NewString(), Provider: provider, DisplayName: names[id] + " · " + providerAccountProductName(provider) + " (company)",
				OwnerUserID: owner, Scope: "user", AuthMethod: "api_key", UpdatedAt: time.Now().UTC(), ViaConnection: id}, Credential: hex.EncodeToString(secret)}
			switch provider {
			case "cursor-cli":
				// Cursor signs in from a login file in the account's home; its key mode needs a real key.
				record.AuthMethod, record.Credential = "cli_login", cursorPlaceholderLogin(record.Credential)
			case "claude-code":
				record.Credential = "sk-ant-oat01-agentworks-" + record.Credential
			case "codex-cli":
				record.Credential = "sk-agentworks-" + record.Credential
			}
			next = append(next, record)
			changed = true
			log.Printf("[DESKTOP_ACCOUNT] %s: company %s account of connection %s added", owner, provider, id)
		}
	}
	if changed {
		if err := saveProviderConnections(ctx, next); err != nil {
			log.Printf("[DESKTOP_ACCOUNT] %s: company accounts not saved: %v", owner, err)
		}
	}
}
