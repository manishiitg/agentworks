package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/mcpcache/openapi"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// The Mac's agents see both Vaults (Vault step 5A, PLAT-856). A Crew on the Mac linked to its company's server (a
// paired Crew, or a Team member's copy) also gets its owner's (the lender's) own Vault apps and secrets on that server.
// Apps are named srv_<first 8 of the saved connection id>_<server Vault id> so they never collide with this Mac's
// vault_* ones. The agent's MCP config points at a local route with a delegation this process signs; that route adds
// the saved server sign-in and forwards to the server's /api/vault/me routes, where the person's grants are checked on
// every call. The server token never reaches the agent, and nothing from the server is stored on disk.

const serverVaultPrefix = "srv_"
const serverVaultPurpose = "server-vault"

// serverVaultKey signs this process's own srv_* delegations; they end with the process.
var serverVaultKey = func() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("server Vault key: " + err.Error())
	}
	return hex.EncodeToString(b)
}()

func serverVaultConnShort(connID string) string {
	if len(connID) > 8 {
		return connID[:8]
	}
	return connID
}
func serverVaultName(connID, id string) string {
	return serverVaultPrefix + serverVaultConnShort(connID) + "_" + id
}
func parseServerVaultName(name string) (conn8, id string, ok bool) {
	rest, ok := strings.CutPrefix(name, serverVaultPrefix)
	if !ok || len(rest) < 10 || rest[8] != '_' {
		return "", "", false
	}
	return rest[:8], rest[9:], true
}

func logServerVault(user string, c desktopConnection, kind, name, result string, started time.Time) {
	log.Printf("[VAULT_REMOTE] user=%s conn=%s %s=%s result=%s ms=%d", user, serverVaultConnShort(c.ID), kind, name, result, time.Since(started).Milliseconds())
}

// serverVaultGet is one GET to the server's /api/vault/me routes with the saved sign-in (renewed when the server says
// so). result is ok, sign_in (the sign-in expired), unreachable or refused.
func serverVaultGet(ctx context.Context, owner string, c desktopConnection, route string) ([]byte, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Server, "/")+route, nil)
	if err != nil {
		return nil, "refused"
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := crewTeamHTTPClient(ctx).Do(req)
	if err != nil {
		return nil, "unreachable"
	}
	defer resp.Body.Close()
	if err := saveRenewedDesktopToken(owner, c, resp.Header.Get("X-Renewed-Token")); err != nil {
		log.Printf("[VAULT_REMOTE] user=%s conn=%s renewed sign-in not saved", owner, serverVaultConnShort(c.ID))
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, "sign_in"
	case resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout:
		return nil, "unreachable"
	case resp.StatusCode != http.StatusOK:
		return nil, "refused"
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, "unreachable"
	}
	return body, "ok"
}

type serverVaultInventoryKey struct{}
type serverVaultInventory struct {
	person, connID string
	servers        []vaultRuntimeServer
}

// fetchServerVaultTools lists the person's Vault apps on the server, named and labelled for this Mac.
func fetchServerVaultTools(ctx context.Context, owner string, c desktopConnection) ([]vaultRuntimeServer, error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	body, result := serverVaultGet(ctx, owner, c, "/api/vault/me/tools")
	var out struct {
		Servers []vaultRuntimeServer `json:"servers"`
	}
	if result == "ok" && json.Unmarshal(body, &out) != nil {
		result = "refused"
	}
	logServerVault(owner, c, "tool", "list", result, started)
	if result != "ok" {
		return nil, errors.New(result)
	}
	rows := make([]vaultRuntimeServer, 0, len(out.Servers))
	for _, row := range out.Servers {
		if !vaultMeIDPattern.MatchString(row.ID) {
			continue
		}
		row.Name = serverVaultName(c.ID, row.ID)
		row.Label = strings.TrimSpace(firstNonEmptyTrimmed(row.Label, row.Provider, row.ID)) + " (on the server)"
		rows = append(rows, row)
	}
	return rows, nil
}

// withServerVaultInventory adds, for a turn of a Crew on the Mac linked to its company's server, the lender's Vault
// apps there (cached on the context for the turn). Offline or signed out, they are left out.
func (api *StreamingAPI) withServerVaultInventory(ctx context.Context, lender, sessionID string) (context.Context, []vaultRuntimeServer) {
	if !IsLocalMode() || lender == "" || sessionID == "" {
		return ctx, nil
	}
	root := api.projectRootForSession(sessionID)
	if root == "" {
		return ctx, nil
	}
	p, paired, err := pairedServerCrewFor(lender, root)
	if err != nil {
		log.Printf("[VAULT_REMOTE] user=%s conn= tool=list result=sign_in ms=0", lender)
		return ctx, nil
	}
	if !paired {
		return ctx, nil
	}
	rows, err := fetchServerVaultTools(ctx, lender, p.conn)
	if err != nil {
		return ctx, nil
	}
	return context.WithValue(ctx, serverVaultInventoryKey{}, serverVaultInventory{person: lender, connID: p.conn.ID, servers: rows}), rows
}

// serverVaultConnection is the person's own saved server sign-in whose id starts with conn8.
func serverVaultConnection(person, conn8 string) (desktopConnection, error) {
	desktopConnectionsMu.Lock()
	records, err := readDesktopConnections(person)
	desktopConnectionsMu.Unlock()
	if err != nil {
		return desktopConnection{}, err
	}
	var match []desktopConnection
	for _, c := range records {
		if serverVaultConnShort(c.ID) == conn8 {
			match = append(match, c)
		}
	}
	if len(match) != 1 {
		return desktopConnection{}, errors.New("sign in to your company's server again from Settings")
	}
	return match[0], nil
}

// serverVaultRows are the person's server Vault apps for name's connection: the turn's cached list, else fetched now.
func serverVaultRows(ctx context.Context, person, name string) (desktopConnection, []vaultRuntimeServer, error) {
	if !IsLocalMode() {
		return desktopConnection{}, nil, errors.New("server Vault apps are used only from the Mac")
	}
	conn8, _, ok := parseServerVaultName(name)
	if !ok {
		return desktopConnection{}, nil, errors.New("not a server Vault app")
	}
	c, err := serverVaultConnection(person, conn8)
	if err != nil {
		return c, nil, err
	}
	if cached, ok := ctx.Value(serverVaultInventoryKey{}).(serverVaultInventory); ok && cached.person == person && cached.connID == c.ID {
		return c, cached.servers, nil
	}
	rows, err := fetchServerVaultTools(ctx, person, c)
	return c, rows, err
}

func serverVaultRowFor(rows []vaultRuntimeServer, name string) (vaultRuntimeServer, bool) {
	for _, row := range rows {
		if row.Name == name || openapi.SanitizePathSegment(row.Name) == name {
			return row, true
		}
	}
	return vaultRuntimeServer{}, false
}

// resolveServerVaultMCP maps a srv_* name to a config for the local forwarding route, bound to this person, app and
// (when given) chat session.
func (api *StreamingAPI) resolveServerVaultMCP(ctx context.Context, person, name, session string) (*executor.ResolvedMCPServer, error) {
	c, rows, err := serverVaultRows(ctx, person, name)
	if err != nil {
		return nil, err
	}
	row, ok := serverVaultRowFor(rows, name)
	if !ok {
		return nil, errors.New("MCP is not available through your server's Vault; use a connection from the live inventory")
	}
	token := signVaultDelegation(serverVaultKey, vaultDelegation{Purpose: serverVaultPurpose, Session: session, Person: person, Connector: c.ID + "/" + row.ID, Expires: time.Now().Truncate(time.Hour).Add(24 * time.Hour).Unix()})
	cfg := mcpclient.MCPServerConfig{URL: api.GetAPIURL() + "/internal/server-vault/mcp/" + url.PathEscape(c.ID) + "/" + url.PathEscape(row.ID), Protocol: mcpclient.ProtocolHTTP, Headers: map[string]string{"Authorization": "Bearer " + token}, ToolAlias: vaultToolAlias(rows, row)}
	runtimeName := vaultRuntimeName(row.Name, cfg)
	return &executor.ResolvedMCPServer{Name: runtimeName, Config: cfg, ConnectionSessionID: runtimeName}, nil
}

// serverVaultBridgeToolName recovers a server Vault tool's exact name from a bridge path's normalized one.
func serverVaultBridgeToolName(ctx context.Context, person, server, tool string) (string, error) {
	name := vaultSelectionName(server)
	_, rows, err := serverVaultRows(ctx, person, name)
	if err != nil {
		return "", err
	}
	row, ok := serverVaultRowFor(rows, name)
	if !ok {
		return tool, nil
	}
	real, err := vaultRowToolName(rows, row, tool)
	if err != nil || real != "" {
		return real, err
	}
	return tool, nil
}

// serverVaultCallName is the tool a JSON-RPC body calls, for the log line (empty for other methods).
func serverVaultCallName(body []byte) string {
	var one struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if json.Unmarshal(body, &one) == nil && one.Method == "tools/call" {
		return one.Params.Name
	}
	return ""
}

// handleServerVaultMCP is the local route a srv_* app's MCP client calls. It checks the delegation, the chat's app
// selection and the lender like handleVaultRuntimeMCP, then forwards the request with the saved server sign-in.
func (api *StreamingAPI) handleServerVaultMCP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	connID, id := mux.Vars(r)["conn"], mux.Vars(r)["id"]
	in, err := verifyVaultDelegation(serverVaultKey, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if err != nil || !IsLocalMode() || in.Purpose != serverVaultPurpose || in.Connector != connID+"/"+id || r.Header.Get("Origin") != "" || !activeMCPPerson(in.Person) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name := serverVaultName(connID, id)
	if in.Session != "" {
		root := api.projectRootForSession(in.Session)
		if root != "" {
			selected, chosenOnly, err := projectAppSelection(r.Context(), root)
			if err != nil {
				http.Error(w, "project connection selection unavailable", http.StatusForbidden)
				return
			}
			if chosenOnly && !containsExactString(selected, name) {
				http.Error(w, "this server Vault connection is not selected for the project", http.StatusForbidden)
				return
			}
		}
		// The session's own person, or the owner of its Crew lending its Vault apps (scopeAgentMCP).
		owner := api.mcpSessionPerson(in.Session)
		if owner != in.Person {
			if owner == "" || root == "" || api.turnSecretsOwner(in.Session, owner, root) != in.Person {
				http.Error(w, "session ownership changed", http.StatusForbidden)
				return
			}
			_, rows, err := serverVaultRows(r.Context(), in.Person, name)
			if row, ok := serverVaultRowFor(rows, name); err == nil && ok && strings.EqualFold(row.Provider, "slack") {
				if refusal := lentSlackCallRefusal(r, in, owner, root); refusal != "" {
					http.Error(w, refusal, http.StatusForbidden)
					return
				}
			}
		}
	}
	c, err := desktopConnectionFor(in.Person, connID)
	if err != nil {
		logServerVault(in.Person, desktopConnection{ID: connID}, "tool", id, "sign_in", time.Now())
		http.Error(w, "Sign in to your company's server again from Settings.", http.StatusUnauthorized)
		return
	}
	target, err := url.Parse(strings.TrimRight(c.Server, "/"))
	if err != nil || target.Host == "" {
		http.Error(w, "Sign in to your company's server again from Settings.", http.StatusUnauthorized)
		return
	}
	called := id
	if r.Body != nil && r.Method == http.MethodPost {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		_ = r.Body.Close()
		if err != nil {
			http.Error(w, "request unreadable", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		if tool := serverVaultCallName(body); tool != "" {
			called = id + "/" + tool
		}
	}
	started := time.Now()
	proxy := &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = "/api/vault/me/mcp/" + id
			pr.Out.URL.RawPath = "/api/vault/me/mcp/" + url.PathEscape(id)
			pr.Out.URL.RawQuery = ""
			pr.Out.Host = target.Host
			headers := make(http.Header)
			for _, key := range []string{"Content-Type", "Accept", "Mcp-Session-Id", "Mcp-Protocol-Version", "Last-Event-Id"} {
				if value := r.Header.Get(key); value != "" {
					headers.Set(key, value)
				}
			}
			headers.Set("Authorization", "Bearer "+c.Token)
			pr.Out.Header = headers
		},
		ModifyResponse: func(resp *http.Response) error {
			if err := saveRenewedDesktopToken(in.Person, c, resp.Header.Get("X-Renewed-Token")); err != nil {
				log.Printf("[VAULT_REMOTE] user=%s conn=%s renewed sign-in not saved", in.Person, serverVaultConnShort(c.ID))
			}
			resp.Header.Del("X-Renewed-Token")
			resp.Header.Del("Set-Cookie")
			resp.Header.Del("X-AgentWorks-Login")
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				return fmt.Errorf("remote redirect refused")
			}
			result := "ok"
			switch {
			case resp.StatusCode == http.StatusUnauthorized:
				result = "sign_in"
			case resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout:
				result = "unreachable"
			case resp.StatusCode >= 400:
				result = "refused_" + strconv.Itoa(resp.StatusCode)
			}
			logServerVault(in.Person, c, "tool", called, result, started)
			if result == "sign_in" {
				msg := "Sign in to " + firstNonEmptyTrimmed(c.Name, "your company's server") + " again from Settings."
				_ = resp.Body.Close()
				resp.Body = io.NopCloser(strings.NewReader(msg))
				resp.ContentLength = int64(len(msg))
				resp.Header.Set("Content-Length", strconv.Itoa(len(msg)))
				resp.Header.Set("Content-Type", "text/plain; charset=utf-8")
			}
			return nil
		},
		ErrorHandler: func(out http.ResponseWriter, _ *http.Request, _ error) {
			logServerVault(in.Person, c, "tool", called, "unreachable", started)
			http.Error(out, "Your company's server can't be reached right now; try again when this Mac is online.", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// serverVaultSecretMissKey carries, for one secret read, why a server secret could not be fetched (name -> reason).
type serverVaultSecretMissKey struct{}
type serverVaultSecretMisses map[string]string

// addServerVaultSecrets adds, on the Mac, the selected secrets this Mac's Vault does not have from the lender's own
// Vault on the Crew's company server (10 seconds in all). The Mac's own secret wins on a name clash. Values are only
// held in memory; the log names the secret, never its value.
func (api *StreamingAPI) addServerVaultSecrets(ctx context.Context, userID, folder string, have []resolvedSecret, selection *[]string) []resolvedSecret {
	if !IsLocalMode() || selection == nil || len(*selection) == 0 || strings.TrimSpace(folder) == "" {
		return have
	}
	got := map[string]bool{}
	for _, s := range have {
		got[s.Name] = true
	}
	missing := []string{}
	for _, name := range *selection {
		name = strings.TrimSpace(name)
		if name != "" && !got[name] && !got[vaultSecretShortName(name)] && vaultMeIDPattern.MatchString(name) {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return have
	}
	owner := api.projectSecretsOwner(userID, folder)
	p, paired, err := pairedServerCrewFor(owner, folder)
	misses, _ := ctx.Value(serverVaultSecretMissKey{}).(serverVaultSecretMisses)
	if err != nil {
		for _, name := range missing {
			log.Printf("[VAULT_REMOTE] user=%s conn= secret=%s result=sign_in ms=0", owner, name)
			if misses != nil {
				misses[vaultSecretShortName(name)] = name + " may be in your Vault on your company's server. Sign in to it again from Settings, then try again."
			}
		}
		return have
	}
	if !paired {
		return have
	}
	round, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, name := range missing {
		started := time.Now()
		body, result := serverVaultGet(round, owner, p.conn, "/api/vault/me/secret/"+url.PathEscape(name))
		var out struct {
			Value string `json:"value"`
		}
		if result == "ok" && (json.Unmarshal(body, &out) != nil || out.Value == "") {
			result = "refused"
		}
		logServerVault(owner, p.conn, "secret", name, result, started)
		short := vaultSecretShortName(name)
		switch result {
		case "ok":
			if !got[short] {
				got[short] = true
				have = append(have, resolvedSecret{Name: short, Value: out.Value})
			}
		case "unreachable":
			if misses != nil {
				misses[short] = name + " comes from your company's server, which can't be reached right now. Try again when this Mac is online."
			}
		case "sign_in":
			if misses != nil {
				server := firstNonEmptyTrimmed(p.conn.Name, "your company's server")
				misses[short] = name + " may be in your Vault on " + server + ". Sign in to " + server + " again from Settings, then try again."
			}
		}
	}
	return have
}
