package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/manishiitg/mcpagent/mcpcache"
	"io"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/mcpclient"
)

type vaultRuntimeTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}
type vaultRuntimeServer struct {
	Name     string             `json:"name"`
	ID       string             `json:"id"`
	Label    string             `json:"label"`
	Provider string             `json:"provider"`
	Tools    []vaultRuntimeTool `json:"tools"`
}

func vaultServerName(id string) string { return "vault_" + id }

// The SDK pools live connections globally by server name. Binding the name to
// the delegation prevents reuse across users, sessions, or token lifetimes.
func vaultRuntimeName(public string, cfg mcpclient.MCPServerConfig) string {
	sum := sha256.Sum256([]byte(cfg.Headers["Authorization"]))
	return public + "__scope_" + hex.EncodeToString(sum[:16])
}
func vaultSelectionName(name string) string {
	public, suffix, ok := strings.Cut(name, "__scope_")
	if ok && strings.HasPrefix(public, "vault_") && len(suffix) == 32 {
		if _, err := hex.DecodeString(suffix); err == nil {
			return public
		}
	}
	return name
}
func mcpCaller(ctx context.Context) string {
	if user := GetUserFromContext(ctx); user != nil {
		return user.UserID
	}
	id, _ := ctx.Value(common.UserIDKey).(string)
	return strings.TrimSpace(id)
}

// Workflow children carry server-owned parent registrations rather than their
// own persisted browser session. Never infer an owner from a client-supplied ID.
func (api *StreamingAPI) mcpSessionPerson(session string) string {
	if pin, ok, err := codeSessionPinFor(session); err == nil && ok {
		return pin.Person
	}
	if api.eventStore == nil {
		return ""
	}
	if person := api.eventStore.GetSessionOwner(session); person != "" {
		return person
	}
	if parent := mcpclient.GetSessionRegistry().HTTPSessionForMCPSession(session); parent != "" {
		return api.eventStore.GetSessionOwner(parent)
	}
	return ""
}
func activeMCPPerson(person string) bool {
	if person == "" {
		return false
	}
	access := userAccessForClaims(&UserClaims{UserID: person})
	return !access.Disabled && (!IsMultiUserMode() || access.Known)
}

// Only the host supplies the actor; never forward a browser JWT or identity header.
func vaultRuntimeRequest(ctx context.Context, person, path string) ([]byte, error) {
	if !activeMCPPerson(person) {
		return nil, errors.New("active MCP user required")
	}
	target, secret, err := capLayerServiceConfig()
	if err != nil {
		return nil, errors.New("Vault is not configured")
	}
	target.Path = strings.TrimRight(target.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("X-CapLayer-Actor", person)
	req.Header.Set("X-Vault-Platform-User", "1")
	client := &http.Client{Transport: capLayerTransport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("Vault unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("Vault access unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if len(data) > 4*1024*1024 {
		return nil, errors.New("Vault catalog exceeds limit")
	}
	return data, err
}

type vaultInventoryKey struct{}
type vaultInventory struct {
	person  string
	servers []vaultRuntimeServer
	err     error
}

// Metadata inventory is always scoped to the centrally verified caller. Never contains values.
type vaultAccessInventory struct {
	Servers []vaultRuntimeServer  `json:"servers"`
	Groups  []vaultAccessGroup    `json:"groups"`
	Secrets []vaultSecretMetadata `json:"secrets"`
}
type vaultSecretMetadata struct {
	Name string `json:"name"`
}
type vaultAccessGroup struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Servers     []vaultRuntimeServer  `json:"servers"`
	Secrets     []vaultSecretMetadata `json:"secrets"`
}

func vaultAccessFor(ctx context.Context, person string) (vaultAccessInventory, error) {
	out := vaultAccessInventory{Servers: []vaultRuntimeServer{}, Groups: []vaultAccessGroup{}, Secrets: []vaultSecretMetadata{}}
	data, err := vaultRuntimeRequest(ctx, person, "/api/admin/runtime/servers")
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	for i := range out.Servers {
		out.Servers[i].Name = vaultServerName(out.Servers[i].ID)
	}
	for i := range out.Groups {
		for j := range out.Groups[i].Servers {
			out.Groups[i].Servers[j].Name = vaultServerName(out.Groups[i].Servers[j].ID)
		}
	}
	return out, nil
}

func vaultServersFor(ctx context.Context, person string) ([]vaultRuntimeServer, error) {
	if cached, ok := ctx.Value(vaultInventoryKey{}).(vaultInventory); ok && cached.person == person {
		return cached.servers, cached.err
	}
	out, err := vaultAccessFor(ctx, person)
	return out.Servers, err
}
func (api *StreamingAPI) handleMyVaultServers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rows, err := vaultAccessFor(r.Context(), mcpCaller(r.Context()))
	if err != nil {
		writeUsersError(w, 503, err.Error())
		return
	}
	writeUsersJSON(w, 200, rows)
}

type vaultDelegation struct {
	Session   string `json:"session,omitempty"`
	Person    string `json:"person"`
	Connector string `json:"connector"`
	Expires   int64  `json:"expires"`
}

func signVaultDelegation(secret string, in vaultDelegation) string {
	data, _ := json.Marshal(in)
	body := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("vault-runtime-v1\x00" + body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func verifyVaultDelegation(secret, token string) (vaultDelegation, error) {
	var out vaultDelegation
	body, sig, ok := strings.Cut(token, ".")
	if !ok || len(token) > 4096 {
		return out, errors.New("invalid delegation")
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return out, err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("vault-runtime-v1\x00" + body))
	if !hmac.Equal(got, mac.Sum(nil)) {
		return out, errors.New("invalid delegation")
	}
	data, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || json.Unmarshal(data, &out) != nil || out.Person == "" || out.Connector == "" || out.Expires <= time.Now().Unix() {
		return out, errors.New("expired or invalid delegation")
	}
	return out, nil
}
func (api *StreamingAPI) vaultServerConfig(person, connector string) (mcpclient.MCPServerConfig, error) {
	_, secret, err := capLayerServiceConfig()
	if err != nil {
		return mcpclient.MCPServerConfig{}, err
	}
	// Credential permits only this user's existing permissions for this connector.
	// The gateway service secret and upstream token never reach the agent.
	token := signVaultDelegation(secret, vaultDelegation{Person: person, Connector: connector, Expires: time.Now().Truncate(time.Hour).Add(24 * time.Hour).Unix()})
	// The MCP SDK connects from this Go process, not the workspace container.
	// Shell-facing host.docker.internal URLs cannot be used for this connection.
	return mcpclient.MCPServerConfig{URL: api.GetAPIURL() + "/internal/vault/mcp", Protocol: mcpclient.ProtocolHTTP, Headers: map[string]string{"Authorization": "Bearer " + token}}, nil
}
func (api *StreamingAPI) handleVaultRuntimeMCP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	target, secret, err := capLayerServiceConfig()
	if err != nil {
		http.Error(w, "Vault unavailable", 503)
		return
	}
	in, err := verifyVaultDelegation(secret, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if err != nil || r.Header.Get("Origin") != "" || !activeMCPPerson(in.Person) {
		http.Error(w, "unauthorized", 401)
		return
	}
	if in.Session != "" {
		owner := api.mcpSessionPerson(in.Session)
		if owner != in.Person {
			http.Error(w, "session ownership changed", 403)
			return
		}
	}
	proxy := &httputil.ReverseProxy{Transport: capLayerTransport, Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		pr.Out.URL.Path = strings.TrimRight(target.Path, "/") + "/api/admin/runtime/mcp"
		pr.Out.URL.RawPath = ""
		pr.Out.URL.RawQuery = ""
		headers := make(http.Header)
		for _, name := range []string{"Content-Type", "Accept", "Mcp-Session-Id", "Mcp-Protocol-Version", "Last-Event-Id"} {
			if value := r.Header.Get(name); value != "" {
				headers.Set(name, value)
			}
		}
		headers.Set("Authorization", "Bearer "+secret)
		headers.Set("X-CapLayer-Actor", in.Person)
		headers.Set("X-Vault-Platform-User", "1")
		headers.Set("X-Vault-Connector", in.Connector)
		pr.Out.Header = headers
	}, ModifyResponse: func(resp *http.Response) error {
		resp.Header.Del("Set-Cookie")
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			return errors.New("unexpected redirect")
		}
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) { http.Error(w, "Vault unavailable", 502) }}
	proxy.ServeHTTP(w, r)
}

// resolveGovernedMCP maps a selected name to the user's private connection or
// an authorized Vault connector. The global catalog is discovery metadata only.
func (api *StreamingAPI) resolveGovernedMCP(ctx context.Context, person, server string) (*executor.ResolvedMCPServer, error) {
	if !activeMCPPerson(person) {
		return nil, errors.New("active MCP user required")
	}
	selector := server
	if isPlaceMCPInternalName(server) {
		plain := placeMCPPlainName(server)
		if placeMCPInternalName(person, plain) != server {
			return nil, errors.New("private MCP does not belong to this user")
		}
		selector = plain
	}
	p, found, lookupErr := lookupPrivateMCP(person, selector)
	if lookupErr != nil {
		return nil, lookupErr
	}
	if found {
		dir, _ := placeMCPDir(person)
		if !placeMCPServerConnected(dir, person, p) {
			return nil, errors.New("your private MCP needs sign-in; connect it in My MCPs")
		}
		name, cfg, err := placeMCPServerConfig(person, p.Name)
		if err != nil {
			return nil, err
		}
		return &executor.ResolvedMCPServer{Name: name, Config: cfg, ConnectionSessionID: name}, nil
	}
	if isPlaceMCPInternalName(server) {
		return nil, errors.New("private MCP does not belong to this user")
	}
	rows, err := vaultServersFor(ctx, person)
	if err != nil {
		return nil, err
	}
	matches := []vaultRuntimeServer{}
	for _, v := range rows {
		if vaultSelectionName(server) == vaultServerName(v.ID) || strings.EqualFold(server, v.Label) || strings.EqualFold(server, v.Provider) {
			matches = append(matches, v)
		}
	}
	if len(matches) != 1 {
		return nil, errors.New("MCP is not connected privately or permitted through Vault; select a specific Vault connection")
	}
	v := matches[0]
	cfg, err := api.vaultServerConfig(person, v.ID)
	if err != nil {
		return nil, err
	}
	name := vaultRuntimeName(vaultServerName(v.ID), cfg)
	return &executor.ResolvedMCPServer{Name: name, Config: cfg, ConnectionSessionID: name}, nil
}

func (api *StreamingAPI) scopeAgentMCP(ctx context.Context, sessionID string, names []string, overrides mcpclient.RuntimeOverrides) ([]string, mcpclient.RuntimeOverrides, map[string]string, error) {
	person := mcpCaller(ctx)
	if owner := api.mcpSessionPerson(sessionID); owner != "" {
		if person != "" && person != owner {
			return nil, nil, nil, errors.New("MCP user does not own this session")
		}
		person = owner
	}
	// Fetch the caller's Vault inventory at most once per agent construction.
	// This is only discovery: the gateway still authorizes every live call.
	for _, name := range names {
		if name != mcpclient.NoServers && !isPlaceMCPInternalName(name) {
			if _, found := privateMCPByCatalog(person, name); !found && !common.IsBuiltinToolCategory(name) {
				rows, err := vaultServersFor(ctx, person)
				ctx = context.WithValue(ctx, vaultInventoryKey{}, vaultInventory{person, rows, err})
				break
			}
		}
	}

	scoped := mcpclient.RuntimeOverrides{}
	aliases := map[string]string{}
	selected := []string{}
	for _, name := range names {
		if name == mcpclient.NoServers {
			continue
		}
		if common.IsBuiltinToolCategory(name) {
			if _, private := privateMCPByCatalog(person, name); !private {
				selected = append(selected, name)
				continue
			}
		}
		// Legacy per-place credentials remain sealed at the original path, private
		// to their original owner. The caller's matching internal prefix is required.
		if isPlaceMCPInternalName(name) && api.ownsLegacyPlaceMCP(ctx, person, name) {
			placeMCPMu.Lock()
			all, _ := readPlaceMCPAttachmentsLocked()
			placeMCPMu.Unlock()
			for root, items := range all {
				for _, a := range items {
					if a.Owner == person && placeMCPInternalName(attachmentStore(a, root), a.Server) == name {
						_, cfg, err := placeMCPServerConfig(attachmentStore(a, root), a.Server)
						if err == nil {
							if _, seen := scoped[name]; !seen {
								selected = append(selected, name)
							}
							scoped[name] = mcpclient.RuntimeConfigOverride{Server: &cfg}
							aliases[strings.ToLower(name)] = name
						}
					}
				}
			}
			continue
		}

		resolved, err := api.resolveGovernedMCP(ctx, person, name)
		if err != nil {
			continue
		}
		cfg := resolved.Config
		if strings.HasPrefix(resolved.Name, "vault_") && sessionID != "" {
			_, secret, secretErr := capLayerServiceConfig()
			if secretErr != nil {
				continue
			}
			cfg.Headers = map[string]string{"Authorization": "Bearer " + signVaultDelegation(secret, vaultDelegation{Person: person, Connector: strings.TrimPrefix(vaultSelectionName(resolved.Name), "vault_"), Session: sessionID, Expires: time.Now().Truncate(time.Hour).Add(24 * time.Hour).Unix()})}
		}
		runtimeName := resolved.Name
		if strings.HasPrefix(runtimeName, "vault_") {
			runtimeName = vaultRuntimeName(vaultSelectionName(runtimeName), cfg)
		}
		if _, seen := scoped[runtimeName]; !seen {
			selected = append(selected, runtimeName)
		}
		scoped[runtimeName] = mcpclient.RuntimeConfigOverride{Server: &cfg}
		aliases[strings.ToLower(name)] = runtimeName
		if strings.HasPrefix(runtimeName, "vault_") {
			aliases[strings.ToLower(vaultSelectionName(runtimeName))] = runtimeName
		}
		if isPlaceMCPInternalName(runtimeName) {
			plain := placeMCPPlainName(runtimeName)
			aliases[strings.ToLower(plain)] = runtimeName
			if own, found := privateMCPByCatalog(person, plain); found && own.Catalog != "" {
				if unambiguous, ok := privateMCPByCatalog(person, own.Catalog); !ok || unambiguous.Name != own.Name {
					continue
				}
				aliases[strings.ToLower(own.Catalog)] = runtimeName
			}
		}
	}
	if len(selected) == 0 {
		selected = []string{mcpclient.NoServers}
	}
	return selected, scoped, aliases, nil
}
func (api *StreamingAPI) ownsLegacyPlaceMCP(ctx context.Context, person, internal string) bool {
	placeMCPMu.Lock()
	all, err := readPlaceMCPAttachmentsLocked()
	placeMCPMu.Unlock()
	if err != nil || person == "" {
		return false
	}
	for root, items := range all {
		for _, a := range items {
			if a.Scope != "user" && a.Owner == person && placeMCPCanAttach(ctx, person, root) && placeMCPInternalName(attachmentStore(a, root), a.Server) == internal {
				return true
			}
		}
	}
	return false
}

// Kept separate from management authorization: ordinary product users consume
// Vault tools through group grants without needing the Vault admin product.

// Project selection is an additional limit, never an authorization grant.
func (api *StreamingAPI) resolveScopedGovernedMCP(ctx context.Context, catalog *mcpclient.MCPConfig, selected, tools []string, person, server, tool string) (*executor.ResolvedMCPServer, error) {
	allowed := false
	canonical := func(name string) string {
		name = vaultSelectionName(name)
		if isPlaceMCPInternalName(name) {
			name = placeMCPPlainName(name)
		}
		if catalog != nil {
			if n, _, err := catalog.ResolveServer(name); err == nil {
				return n
			}
		}
		return strings.ToLower(name)
	}
	for _, name := range selected {
		if name != mcpclient.NoServers && canonical(name) == canonical(server) {
			allowed = true
		}
	}
	if !allowed {
		return nil, errors.New("MCP is not selected for this project")
	}
	if len(tools) > 0 {
		allowed = false
		for _, entry := range tools {
			n, t, ok := strings.Cut(entry, ":")
			if ok && canonical(n) == canonical(server) && (t == "*" || t == tool) {
				allowed = true
			}
		}
		if !allowed {
			return nil, errors.New("MCP tool is not selected for this project")
		}
	}
	return api.resolveGovernedMCP(ctx, person, server)
}
func (api *StreamingAPI) discoverGovernedServerTools(ctx context.Context, person, server string) (*ToolStatus, error) {
	resolved, err := api.resolveGovernedMCP(ctx, person, server)
	if err != nil {
		return nil, err
	}
	// Only a single actor-scoped server enters this temporary metadata config.
	file, err := os.CreateTemp("", "private-mcp-discovery-*.json")
	if err != nil {
		return nil, err
	}
	name := file.Name()
	file.Close()
	defer os.Remove(name)
	cfg := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{resolved.Name: resolved.Config}}
	if err := mcpclient.SaveConfig(name, cfg); err != nil {
		return nil, err
	}
	result, err := mcpcache.GetCachedOrFreshConnection(ctx, nil, resolved.Name, name, nil, api.logger, true, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, client := range result.Clients {
			if client != nil {
				_ = client.Close()
			}
		}
	}()
	details := []mcpclient.ToolDetail{}
	names := []string{}
	for _, tool := range api.extractServerTools(result.Tools, result.ToolToServer, resolved.Name) {
		if tool.Function == nil {
			continue
		}
		names = append(names, tool.Function.Name)
		parameters := map[string]interface{}{}
		encoded, _ := json.Marshal(tool.Function.Parameters)
		_ = json.Unmarshal(encoded, &parameters)
		details = append(details, mcpclient.ToolDetail{Name: tool.Function.Name, Description: tool.Function.Description, Parameters: parameters})
	}
	return &ToolStatus{Name: server, Server: server, Connection: connectionConnected, Status: "ok", Tools: details, FunctionNames: names, ToolsEnabled: len(names)}, nil
}
