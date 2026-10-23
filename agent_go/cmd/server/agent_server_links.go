package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Server links are personal messaging grants, held in the private OAuth store.
// Neither the website nor an agent tool receives access or refresh credentials.
type agentServerLink struct {
	State       string    `json:"state"`
	Waiting     int       `json:"waiting"`
	ID          string    `json:"id"`
	UserID      string    `json:"-"`
	Alias       string    `json:"name"`
	Origin      string    `json:"server"`
	ClientID    string    `json:"-"`
	Credentials string    `json:"-"`
	CreatedAt   time.Time `json:"connected_at"`
}

type agentServerLinkToken struct {
	Access    string    `json:"access_token"`
	Refresh   string    `json:"refresh_token"`
	ExpiresIn int       `json:"expires_in"`
	ExpiresAt time.Time `json:"expires_at"`
}

var agentServerLinkTokenMu sync.Mutex

func openAgentServerLinkStore() (*mcpOAuthStore, error) {
	s, err := openMCPOAuthStore()
	if err != nil {
		return nil, err
	}
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS agent_server_links (id TEXT PRIMARY KEY,user_id TEXT NOT NULL,alias TEXT NOT NULL,origin TEXT NOT NULL,client_id TEXT NOT NULL,credentials TEXT NOT NULL,created_at INTEGER NOT NULL,UNIQUE(user_id,alias),UNIQUE(user_id,origin))`,
		`CREATE TABLE IF NOT EXISTS agent_server_link_requests (id TEXT PRIMARY KEY,user_id TEXT NOT NULL,alias TEXT NOT NULL,origin TEXT NOT NULL,client_id TEXT NOT NULL,verifier TEXT NOT NULL,expires_at INTEGER NOT NULL)`,
	} {
		if _, err = s.db.Exec(ddl); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

func agentServerLinkAAD(l agentServerLink) []byte {
	return []byte("agent-server-link:" + l.UserID + ":" + l.ID + ":" + l.Origin)
}

func readAgentServerLinks(ctx context.Context, user string) ([]agentServerLink, error) {
	s, err := openAgentServerLinkStore()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	query := `SELECT id,user_id,alias,origin,client_id,credentials,created_at FROM agent_server_links`
	args := []any{}
	if user != "" {
		query += ` WHERE user_id=?`
		args = append(args, user)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := []agentServerLink{}
	for rows.Next() {
		var l agentServerLink
		var created int64
		if err = rows.Scan(&l.ID, &l.UserID, &l.Alias, &l.Origin, &l.ClientID, &l.Credentials, &created); err != nil {
			return nil, err
		}
		l.CreatedAt = time.Unix(created, 0).UTC()
		links = append(links, l)
	}
	return links, rows.Err()
}

func ownAgentServerLink(ctx context.Context, user, id string) (agentServerLink, error) {
	access := userAccessForClaims(&UserClaims{UserID: user})
	if access.Disabled || IsMultiUserMode() && !access.Known {
		return agentServerLink{}, errors.New("The linked account is no longer available.")
	}
	links, err := readAgentServerLinks(ctx, user)
	if err != nil {
		return agentServerLink{}, err
	}
	for _, l := range links {
		if l.ID == id || l.Alias == strings.ToLower(id) {
			return l, nil
		}
	}
	return agentServerLink{}, errors.New("Linked server unavailable. Open AgentBridge to connect it.")
}

// Use only origin-relative fixed routes. Do not follow redirects with a grant.
func agentServerLinkLocalDevelopment() bool {
	origin, _, ok := mcpOAuthURLs()
	if !ok {
		return false
	}
	u, _ := url.Parse(origin)
	return u.Scheme == "http" && cliOAuthLoopback(u.Hostname())
}

func agentServerLinkAddressAllowed(addr netip.Addr, local bool) bool {
	addr = addr.Unmap()
	return local && addr.IsLoopback() && addr.Zone() == "" || knowledgebase.PublicNetworkAddress(addr) && addr.String() != "168.63.129.16"
}

func agentServerLinkDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("Use a public HTTPS server address.")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("The linked server's address could not be resolved.")
	}
	local := agentServerLinkLocalDevelopment()
	for _, addr := range addresses {
		if !agentServerLinkAddressAllowed(addr, local) {
			return nil, errors.New("Linked servers must use public HTTPS addresses.")
		}
	}
	var last error
	for _, addr := range addresses {
		dialer := net.Dialer{Timeout: 10 * time.Second}
		connection, e := dialer.DialContext(ctx, network, net.JoinHostPort(addr.Unmap().String(), port))
		if e == nil {
			return connection, nil
		}
		last = e
	}
	return nil, last
}

// Check the address at the actual dial and use that exact IP. No proxy,
// redirect or second DNS lookup can move a messaging grant to another host.
var agentServerLinkHTTP = &http.Client{
	Timeout:       20 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Transport:     &http.Transport{Proxy: nil, DialContext: agentServerLinkDial, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second, IdleConnTimeout: 30 * time.Second},
}

func agentServerLinkRequest(ctx context.Context, origin, path, token string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := agentServerLinkHTTP.Do(req)
	if err != nil {
		return errors.New("The linked server could not be reached.")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Linked server refused the request (HTTP %d). Check its connection in AgentBridge.", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 3<<20)).Decode(out) != nil {
		return errors.New("The linked server returned an invalid response.")
	}
	return nil
}

func exchangeAgentServerLink(ctx context.Context, origin string, values url.Values) (agentServerLinkToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+mcpOAuthTokenPath, strings.NewReader(values.Encode()))
	if err != nil {
		return agentServerLinkToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := agentServerLinkHTTP.Do(req)
	if err != nil {
		return agentServerLinkToken{}, errors.New("The linked server could not be reached.")
	}
	defer resp.Body.Close()
	var pair agentServerLinkToken
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 32768)).Decode(&pair) != nil || pair.Access == "" || pair.Refresh == "" || pair.ExpiresIn <= 0 {
		return pair, errors.New("The linked-server sign-in needs approval again. Open AgentBridge to reconnect it.")
	}
	pair.ExpiresAt = time.Now().Add(time.Duration(pair.ExpiresIn) * time.Second)
	return pair, nil
}

func sealAgentServerLink(l *agentServerLink, pair agentServerLinkToken) error {
	raw, err := json.Marshal(pair)
	if err != nil {
		return err
	}
	l.Credentials, err = encryptSecretValueWithAAD(string(raw), agentServerLinkAAD(*l))
	return err
}

func agentServerLinkAccess(ctx context.Context, user, id string) (string, error) {
	agentServerLinkTokenMu.Lock()
	defer agentServerLinkTokenMu.Unlock()
	l, err := ownAgentServerLink(ctx, user, id)
	if err != nil {
		return "", err
	}
	plain, err := decryptSecretValueWithAAD(l.Credentials, agentServerLinkAAD(l))
	if err != nil {
		return "", errors.New("Reconnect this server in AgentBridge.")
	}
	var pair agentServerLinkToken
	if json.Unmarshal([]byte(plain), &pair) != nil {
		return "", errors.New("Reconnect this server in AgentBridge.")
	}
	if pair.ExpiresAt.After(time.Now().Add(time.Minute)) {
		return pair.Access, nil
	}
	pair, err = exchangeAgentServerLink(ctx, l.Origin, url.Values{"grant_type": {"refresh_token"}, "client_id": {l.ClientID}, "resource": {l.Origin + externalMCPPath}, "refresh_token": {pair.Refresh}})
	if err != nil {
		return "", err
	}
	if err = sealAgentServerLink(&l, pair); err != nil {
		return "", err
	}
	s, err := openAgentServerLinkStore()
	if err != nil {
		return "", err
	}
	defer s.Close()
	result, err := s.db.ExecContext(ctx, `UPDATE agent_server_links SET credentials=? WHERE id=? AND user_id=? AND client_id=?`, l.Credentials, l.ID, l.UserID, l.ClientID)
	if err != nil {
		return "", err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return "", errors.New("This server was disconnected.")
	}
	return pair.Access, nil
}

// Only a person's normal sign-in can create/remove account links. Messaging
// children, scoped product callers and other linked servers cannot link accounts.
func personalAgentServerLinkClaims(c *UserClaims) bool {
	if c == nil || c.Scope != "" || c.ExecutionPrincipal != nil || c.BotRouteGrant != "" || c.ExternalBuilderOperationID != "" || userAccessForClaims(c).Disabled {
		return false
	}
	if c.AccessToken == nil {
		return true
	}
	t := c.AccessToken
	return t.BoundAgentID == "" && t.ProjectKind == "" && !isAgentServerLinkClient(t.OAuthClientID) && t.AllWorkflows && len(t.WorkflowIDs) == 0 && t.FileGuard == nil && personSignInScopes(t.Scopes)
}

func startAgentServerLink(ctx context.Context, c *UserClaims, args map[string]any) (map[string]any, error) {
	if !personalAgentServerLinkClaims(c) {
		return nil, errors.New("Use your normal AgentWorks sign-in to link another server.")
	}
	alias, err := normalizeMessageAgentName(externalArg(args, "name"))
	if err != nil || strings.ContainsAny(alias, "@.") {
		return nil, errors.New("Give this server a short name, such as team.")
	}
	origin := strings.TrimRight(strings.TrimSpace(externalArg(args, "server")), "/")
	if strings.HasPrefix(origin, "http:") && !agentServerLinkLocalDevelopment() {
		return nil, errors.New("Use the other server’s public HTTPS address.")
	}
	if _, err = agentServerNamespace(origin); err != nil {
		return nil, err
	}
	home, _, ok := mcpOAuthURLs()
	if !ok {
		return nil, errors.New("This server's public web address is not configured.")
	}
	if origin == home {
		return nil, errors.New("Your agents are already connected to this server.")
	}
	links, err := readAgentServerLinks(ctx, c.UserID)
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		if l.Alias == alias && l.Origin != origin {
			return nil, errors.New("That server name is already in use. Choose another name.")
		}
	}
	var client struct {
		ID string `json:"client_id"`
	}
	if err = agentServerLinkRequest(ctx, origin, mcpOAuthRegisterPath, "", map[string]any{"client_name": "AgentWorks at " + home, "redirect_uris": []string{home + agentServerLinkCallbackPath}, "agent_server_link": true}, &client); err != nil {
		return nil, err
	}
	if !isAgentServerLinkClient(client.ID) {
		return nil, errors.New("This server needs an AgentWorks update before it can be linked.")
	}
	state := uuid.NewString()
	verifier := strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")
	digest := sha256.Sum256([]byte(verifier))
	s, err := openAgentServerLinkStore()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	_, err = s.db.ExecContext(ctx, `DELETE FROM agent_server_link_requests WHERE expires_at<?`, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO agent_server_link_requests VALUES(?,?,?,?,?,?,?)`, state, c.UserID, alias, origin, client.ID, verifier, time.Now().Add(20*time.Minute).Unix())
	if err != nil {
		return nil, err
	}
	q := url.Values{"response_type": {"code"}, "client_id": {client.ID}, "redirect_uri": {home + agentServerLinkCallbackPath}, "resource": {origin + externalMCPPath}, "scope": {strings.Join(agentServerLinkScopes, " ")}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"}}
	return map[string]any{"approval_url": origin + mcpOAuthAuthorizePath + "?" + q.Encode(), "message": "Approve this messaging connection on the other server. Your agents keep their home connection."}, nil
}

func (api *StreamingAPI) handleAgentServerLinkCallback(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if r.Method != http.MethodPost || claims == nil || claims.AccessToken != nil || !personalAgentServerLinkClaims(claims) {
		externalError(w, 403, "access_denied", "Complete the connection from the home account that started it.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var input struct {
		State string `json:"state"`
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || input.State == "" {
		externalError(w, 400, "invalid_request", "Connection request unavailable. Start again from AgentBridge.")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	s, err := openAgentServerLinkStore()
	if err != nil {
		http.Error(w, "Connection could not be saved.", 503)
		return
	}
	defer s.Close()
	var owner string
	if s.db.QueryRowContext(r.Context(), `SELECT user_id FROM agent_server_link_requests WHERE id=?`, input.State).Scan(&owner) == nil && owner != claims.UserID {
		externalError(w, 403, "access_denied", "This connection was started by a different home account. Return to AgentBridge and start your own connection.")
		return
	}
	var l agentServerLink
	var verifier string
	var expires int64
	// Consume the random state exactly once before exchanging the bound code.
	err = s.db.QueryRowContext(r.Context(), `DELETE FROM agent_server_link_requests WHERE id=? AND user_id=? RETURNING user_id,alias,origin,client_id,verifier,expires_at`, input.State, claims.UserID).Scan(&l.UserID, &l.Alias, &l.Origin, &l.ClientID, &verifier, &expires)
	if err != nil || expires < time.Now().Unix() || userAccessForClaims(&UserClaims{UserID: l.UserID}).Disabled {
		http.Error(w, "Connection request expired. Start again from AgentBridge.", 400)
		return
	}
	if input.Error != "" || input.Code == "" {
		http.Error(w, "The connection was not approved. Return to AgentBridge to try again.", 400)
		return
	}
	home, _, ok := mcpOAuthURLs()
	if !ok {
		http.Error(w, "Server web address unavailable.", 503)
		return
	}
	pair, err := exchangeAgentServerLink(r.Context(), l.Origin, url.Values{"grant_type": {"authorization_code"}, "client_id": {l.ClientID}, "redirect_uri": {home + agentServerLinkCallbackPath}, "resource": {l.Origin + externalMCPPath}, "code_verifier": {verifier}, "code": {input.Code}})
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	l.ID = uuid.NewString()
	l.CreatedAt = time.Now().UTC()
	var oldID string
	if s.db.QueryRowContext(r.Context(), `SELECT id FROM agent_server_links WHERE user_id=? AND origin=?`, l.UserID, l.Origin).Scan(&oldID) == nil {
		l.ID = oldID
	}
	if err = sealAgentServerLink(&l, pair); err == nil {
		_, err = s.db.ExecContext(r.Context(), `INSERT INTO agent_server_links VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id,origin) DO UPDATE SET alias=excluded.alias,client_id=excluded.client_id,credentials=excluded.credentials,created_at=excluded.created_at`, l.ID, l.UserID, l.Alias, l.Origin, l.ClientID, l.Credentials, l.CreatedAt.Unix())
	}
	if err != nil {
		http.Error(w, "Connection could not be saved. Start again from AgentBridge.", 503)
		return
	}
	api.agentServerRelay().changed()
	externalJSON(w, map[string]any{"connected": true})
}

func (api *StreamingAPI) disconnectAgentServerLink(ctx context.Context, user, id string) error {
	l, err := ownAgentServerLink(ctx, user, id)
	if err != nil {
		return err
	}
	// Remove local access first, even if the remote server is unreachable.
	token, tokenErr := agentServerLinkAccess(ctx, user, l.ID)
	s, err := openAgentServerLinkStore()
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM agent_server_links WHERE id=? AND user_id=?`, l.ID, user)
	s.Close()
	if err != nil {
		return err
	}
	api.agentServerRelay().changed()
	if tokenErr == nil {
		_ = agentServerLinkRequest(ctx, l.Origin, "/api/external/v1/agents/link", token, map[string]string{"action": "revoke"}, nil)
	}
	return nil
}

func (api *StreamingAPI) handleAgentServerLinks(w http.ResponseWriter, r *http.Request) {
	c := GetUserFromContext(r.Context())
	if !personalAgentServerLinkClaims(c) {
		externalError(w, 403, "access_denied", "Use your normal sign-in to manage linked servers.")
		return
	}
	args := map[string]any{"action": "list"}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if json.NewDecoder(r.Body).Decode(&args) != nil {
			externalError(w, 400, "invalid_arguments", "Choose a server to link or disconnect.")
			return
		}
	}
	out, err := api.agentServerLinksCall(r.Context(), c, args)
	if err != nil {
		externalCommunicationError(w, 400, "connection_refused", err)
		return
	}
	externalJSON(w, out)
}

func (api *StreamingAPI) agentServerLinksCall(ctx context.Context, c *UserClaims, args map[string]any) (map[string]any, error) {
	if !personalAgentServerLinkClaims(c) {
		return nil, errors.New("Use your normal sign-in to manage linked servers.")
	}
	switch externalArg(args, "action") {
	case "list":
		links, err := readAgentServerLinks(ctx, c.UserID)
		if err == nil {
			api.agentServerRelay().linkStatus(ctx, links)
		}
		return map[string]any{"servers": links}, err
	case "connect":
		return startAgentServerLink(ctx, c, args)
	case "disconnect":
		err := api.disconnectAgentServerLink(ctx, c.UserID, externalArg(args, "name"))
		return map[string]any{"disconnected": err == nil}, err
	default:
		return nil, errors.New("Choose list, connect or disconnect.")
	}
}

func (api *StreamingAPI) handleAgentServerLinkGrant(w http.ResponseWriter, r *http.Request) {
	c := GetUserFromContext(r.Context())
	if c == nil || c.AccessToken == nil || !isAgentServerLinkClient(c.AccessToken.OAuthClientID) || c.AccessToken.BoundAgentID != "" {
		externalError(w, 403, "access_denied", "Only the linked server can disconnect this grant.")
		return
	}
	s, err := openMCPOAuthStore()
	if err == nil {
		err = s.RevokeFamily(r.Context(), strings.TrimPrefix(c.AccessToken.ID, "oauth-"), c.UserID)
		s.Close()
	}
	if err != nil {
		externalFailure(w, err)
		return
	}
	notifyAgentLiveChanged()
	externalJSON(w, map[string]any{"disconnected": true})
}
