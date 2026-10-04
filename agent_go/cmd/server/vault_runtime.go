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
	"github.com/manishiitg/mcpagent/mcpcache/openapi"
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

// OpenAPI bridge URLs also normalize tool names. Recover only a unique name
// from this caller's live inventory; the gateway still authorizes the call.
func (api *StreamingAPI) vaultBridgeToolName(ctx context.Context, session, server, tool string) (context.Context, string, error) {
	vaultPath := strings.HasPrefix(server, "vault_")
	legacyReportPath := !vaultPath && strings.HasPrefix(session, "report-run-")
	if !vaultPath && !legacyReportPath {
		return ctx, tool, nil
	}
	person := api.mcpSessionPerson(session)
	if legacyReportPath {
		// Preserve old report URLs such as Linear/list_issues after migration,
		// but a viewer's private connection always retains its own tool names.
		resolved, err := api.resolveGovernedMCP(ctx, person, server)
		if err != nil {
			return ctx, "", err
		}
		if !strings.HasPrefix(resolved.Name, "vault_") {
			return ctx, tool, nil
		}
		server = resolved.Name
	}
	rows, err := vaultServersFor(ctx, person)
	if err != nil {
		return ctx, "", err
	}
	ctx = context.WithValue(ctx, vaultInventoryKey{}, vaultInventory{person: person, servers: rows})
	selection := vaultSelectionName(server)
	var matches []string
	for _, row := range rows {
		if selection != vaultServerName(row.ID) && selection != openapi.SanitizePathSegment(vaultServerName(row.ID)) {
			continue
		}
		for _, candidate := range row.Tools {
			_, upstream, hasPrefix := strings.Cut(candidate.Name, "__")
			if candidate.Name == tool || openapi.SanitizePathSegment(candidate.Name) == tool || legacyReportPath && hasPrefix && (upstream == tool || openapi.SanitizePathSegment(upstream) == tool) {
				matches = append(matches, candidate.Name)
			}
		}
	}
	if len(matches) > 1 {
		return ctx, "", errors.New("ambiguous Vault tool bridge path")
	}
	if len(matches) == 1 {
		return ctx, matches[0], nil
	}
	// Do not grant a tool absent from discovery. Its original name can still
	// reach the gateway's normal denial/audit handler.
	return ctx, tool, nil
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
	if strings.HasPrefix(session, "report-run-") {
		// Report sessions have a server-owned lifetime and viewer identity, not
		// an event-store chat owner. Never fall back after the script has ended.
		if cached, exists := api.reportRunSessions.Load(session); exists {
			if scope, ok := cached.(reportRunScope); ok {
				return scope.userID
			}
		}
		return ""
	}
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
	if path == "/api/admin/runtime/builder/servers" {
		req.Header.Set("X-Vault-Builder", "1")
	}
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
	// Only the host's directory populates this; never deserialize gateway data.
	Users []vaultDirectoryUser `json:"-"`
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
	path := "/api/admin/runtime/servers"
	if authority, builder := ctx.Value(vaultBuilderKey{}).(vaultBuilderAuthority); builder {
		if authority.Person != person || authority.Session == "" || authority.Session != executor.SessionIDFromContext(ctx) || !vaultBuilderAdministrator(person) {
			return out, errors.New("Vault administrator required")
		}
		path = "/api/admin/runtime/builder/servers"
	}
	if !vaultConfigured() {
		// No Vault: nothing is shared through it, and that is not an error.
		return out, nil
	}
	data, err := vaultRuntimeRequest(ctx, person, path)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	// Only the authorized Vault builder receives the platform directory.
	// Discard any upstream user list for ordinary product inventories.
	out.Users = nil
	if path == "/api/admin/runtime/builder/servers" {
		out.Users, err = vaultDirectoryUsers(&UserClaims{UserID: person})
		if err != nil {
			return out, errors.New("user directory unavailable")
		}
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
	Purpose   string `json:"purpose,omitempty"`
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
	runtimePath := "/api/admin/runtime/mcp"
	if in.Purpose != "" {
		if in.Purpose != "vault-builder" || in.Session == "" || !vaultBuilderAdministrator(in.Person) {
			http.Error(w, "Vault builder administrator required", 403)
			return
		}
		runtimePath = "/api/admin/runtime/builder/mcp"
	}
	proxy := &httputil.ReverseProxy{Transport: capLayerTransport, Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		pr.Out.URL.Path = strings.TrimRight(target.Path, "/") + runtimePath
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
		if in.Purpose == "vault-builder" {
			headers.Set("X-Vault-Builder", "1")
		}
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
	inPlace := placeScopedContext(ctx)
	if isPlaceMCPInternalName(server) {
		if inPlace {
			// A place's own connections resolve before this point; any other place's is not available here.
			return nil, errors.New("this connection is not attached to this workflow, Crew or Code")
		}
		plain := placeMCPPlainName(server)
		if placeMCPInternalName(person, plain) != server {
			return nil, errors.New("this connection does not belong to this user")
		}
		selector = plain
	}
	var p placeMCPServer
	var found bool
	if !inPlace {
		var lookupErr error
		p, found, lookupErr = lookupPersonalMCP(person, selector)
		if lookupErr != nil {
			return nil, lookupErr
		}
	}
	if found {
		dir, _ := placeMCPDir(person)
		if !placeMCPServerConnected(dir, person, p) {
			return nil, errors.New("this MCP connection needs sign-in; connect it in Integrations")
		}
		name, cfg, err := placeMCPServerConfig(person, p.Name)
		if err != nil {
			return nil, err
		}
		return &executor.ResolvedMCPServer{Name: name, Config: cfg, ConnectionSessionID: name}, nil
	}
	if isPlaceMCPInternalName(server) {
		return nil, errors.New("this connection does not belong to this user")
	}
	authority, builder, err := api.vaultBuilderAuthority(ctx, person)
	if err != nil {
		return nil, err
	}
	rows, err := vaultServersFor(ctx, person)
	if err != nil {
		return nil, err
	}
	matches := []vaultRuntimeServer{}
	for _, v := range rows {
		// Generated bridge paths normalize hyphens to underscores. Compare the
		// forward-normalized live ID, never guess an ID by replacing underscores.
		selection := vaultSelectionName(server)
		if selection == vaultServerName(v.ID) || selection == openapi.SanitizePathSegment(vaultServerName(v.ID)) || strings.EqualFold(server, v.Label) || strings.EqualFold(server, v.Provider) {
			matches = append(matches, v)
		}
	}
	if len(matches) != 1 {
		return nil, errors.New("MCP is not connected privately or permitted through Vault; use a specific connection ID from the live inventory")
	}
	v := matches[0]
	cfg, err := api.vaultServerConfig(person, v.ID)
	if err != nil {
		return nil, err
	}
	if builder {
		_, secret, err := capLayerServiceConfig()
		if err != nil {
			return nil, err
		}
		cfg.Headers["Authorization"] = "Bearer " + signVaultDelegation(secret, vaultDelegation{Person: person, Connector: v.ID, Session: authority.Session, Purpose: "vault-builder", Expires: time.Now().Truncate(time.Hour).Add(time.Hour).Unix()})
	}
	name := vaultRuntimeName(vaultServerName(v.ID), cfg)
	return &executor.ResolvedMCPServer{Name: name, Config: cfg, ConnectionSessionID: name}, nil
}

func (api *StreamingAPI) scopeAgentMCP(ctx context.Context, sessionID string, names []string, overrides mcpclient.RuntimeOverrides) ([]string, mcpclient.RuntimeOverrides, map[string]string, error) {
	person := mcpCaller(ctx)
	inPlace := api.placeRootForSession(sessionID) != ""
	if inPlace {
		ctx = context.WithValue(ctx, placeScopedKey{}, true)
	}
	if owner := api.mcpSessionPerson(sessionID); owner != "" {
		if person != "" && person != owner {
			return nil, nil, nil, errors.New("MCP user does not own this session")
		}
		person = owner
	}
	// Authorized Vault connections are defaults for every product, even when
	// the project selects no MCPs. Cache discovery once; live calls still
	// recheck the actor's current grants and argument rules at the gateway.
	rows, inventoryErr := vaultServersFor(ctx, person)
	ctx = context.WithValue(ctx, vaultInventoryKey{}, vaultInventory{person, rows, inventoryErr})
	names = append([]string(nil), names...)
	if inventoryErr == nil {
		for _, row := range rows {
			names = append(names, vaultServerName(row.ID))
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
			if _, private := personalMCPByCatalog(person, name); inPlace || !private {
				selected = append(selected, name)
				continue
			}
		}
		// A connection attached to this session's own workflow, Relay, Crew or Code is the place's: it is
		// available to every session of that place, including runs that have no person.
		if internal, cfg, found, denied, _ := api.placeAttachedMatch(ctx, sessionID, name); found {
			if !denied {
				if _, seen := scoped[internal]; !seen {
					selected = append(selected, internal)
				}
				config := cfg
				scoped[internal] = mcpclient.RuntimeConfigOverride{Server: &config}
				aliases[strings.ToLower(name)] = internal
				aliases[strings.ToLower(placeMCPPlainName(internal))] = internal
			}
			continue
		}
		// Legacy per-place credentials remain sealed at the original path, private
		// to their original owner. The caller's matching internal prefix is required.
		if !inPlace && isPlaceMCPInternalName(name) && api.ownsLegacyPlaceMCP(ctx, person, name) {
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
		_, builder := ctx.Value(vaultBuilderKey{}).(vaultBuilderAuthority)
		if strings.HasPrefix(resolved.Name, "vault_") && sessionID != "" && !builder {
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
			if own, found := personalMCPByCatalog(person, plain); found && own.Catalog != "" {
				if unambiguous, ok := personalMCPByCatalog(person, own.Catalog); !ok || unambiguous.Name != own.Name {
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

// Project selection limits place/private connections. Vault access is governed
// by current user/group grants, with tool and regex checks on each live call.
func (api *StreamingAPI) resolveScopedGovernedMCP(ctx context.Context, catalog *mcpclient.MCPConfig, selected, tools []string, person, server, tool string) (*executor.ResolvedMCPServer, error) {
	resolved, err := api.resolveGovernedMCP(ctx, person, server)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(resolved.Name, "vault_") {
		return resolved, nil
	}
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
		upstreamTool := tool
		if strings.HasPrefix(resolved.Name, "vault_") {
			if _, suffix, ok := strings.Cut(tool, "__"); ok {
				upstreamTool = suffix
			}
		}
		for _, entry := range tools {
			n, t, ok := strings.Cut(entry, ":")
			if ok && canonical(n) == canonical(server) && (t == "*" || t == tool || t == upstreamTool) {
				allowed = true
			}
		}
		if !allowed {
			return nil, errors.New("MCP tool is not selected for this project")
		}
	}
	return resolved, nil
}
func (api *StreamingAPI) discoverGovernedServerTools(ctx context.Context, person, server string) (*ToolStatus, error) {
	resolved, err := api.resolveGovernedMCP(ctx, person, server)
	if err != nil {
		return nil, err
	}
	return api.discoverResolvedServerTools(ctx, resolved)
}

// discoverResolvedServerTools reads the tools of one already-resolved server (a place's connection or a Vault one).
func (api *StreamingAPI) discoverResolvedServerTools(ctx context.Context, resolved *executor.ResolvedMCPServer) (*ToolStatus, error) {
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
	shown := placeMCPPlainName(resolved.Name)
	return &ToolStatus{Name: shown, Server: shown, Connection: connectionConnected, Status: "ok", Tools: details, FunctionNames: names, ToolsEnabled: len(names)}, nil
}
