package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type mcpURLSignIn struct {
	ID, User, Connection, Target, ArgsHash, Account, OAuthState, AuthURL string
	Scope                                                                map[string]any
	Consumed, Opened, Complete                                           bool
}

func openMCPURLSignIns() (*mcpOAuthStore, error) {
	s, err := openMCPOAuthStore()
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS external_mcp_url_signins (
 id TEXT PRIMARY KEY,user_id TEXT NOT NULL,connection_id TEXT NOT NULL,target TEXT NOT NULL,args_hash TEXT NOT NULL,
 account_id TEXT NOT NULL,scope TEXT NOT NULL,auth_url TEXT NOT NULL,oauth_state TEXT NOT NULL,
 expires_at INTEGER NOT NULL,consumed INTEGER NOT NULL DEFAULT 0,opened INTEGER NOT NULL DEFAULT 0,complete INTEGER NOT NULL DEFAULT 0)`)
	if err == nil {
		_, err = s.db.Exec(`DELETE FROM external_mcp_url_signins WHERE expires_at < ?`, time.Now().Unix())
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func mcpURLHash(v []byte) string                { sum := sha256.Sum256(v); return hex.EncodeToString(sum[:]) }
func mcpURLArgsHash(args map[string]any) string { raw, _ := json.Marshal(args); return mcpURLHash(raw) }

func mcpURLScope(target string, args map[string]any) (map[string]any, bool) {
	out := map[string]any{}
	for _, key := range []string{"workflow_id", "crew_id"} {
		if v, ok := args[key].(string); ok {
			out[key] = v
		}
	}
	switch target {
	case "workflow_google_account", "workflow_slack_account", "manage_google_account", "manage_slack_account":
		out["action"] = "list_saved"
	case "crew_integrations":
		out["action"] = args["action"]
		out["arguments"] = map[string]any{"action": "list_saved"}
	case "manage_vault_access":
		out["operation"] = "connection_status"
	default:
		return nil, false
	}
	return out, true
}

func findMCPURLField(value any, key string) string {
	switch v := value.(type) {
	case map[string]any:
		if s, ok := v[key].(string); ok && s != "" {
			return s
		}
		for _, nested := range v {
			if s := findMCPURLField(nested, key); s != "" {
				return s
			}
		}
	case []any:
		for _, nested := range v {
			if s := findMCPURLField(nested, key); s != "" {
				return s
			}
		}
	case string:
		var decoded any
		if json.Unmarshal([]byte(v), &decoded) == nil {
			return findMCPURLField(decoded, key)
		}
	}
	return ""
}

func loadMCPURLSignIn(store *mcpOAuthStore, id string) (mcpURLSignIn, error) {
	var row mcpURLSignIn
	var scope string
	err := store.db.QueryRow(`SELECT id,user_id,connection_id,target,args_hash,account_id,scope,auth_url,oauth_state,consumed,opened,complete FROM external_mcp_url_signins WHERE id=? AND expires_at>=?`, id, time.Now().Unix()).Scan(&row.ID, &row.User, &row.Connection, &row.Target, &row.ArgsHash, &row.Account, &scope, &row.AuthURL, &row.OAuthState, &row.Consumed, &row.Opened, &row.Complete)
	if err == nil {
		err = json.Unmarshal([]byte(scope), &row.Scope)
	}
	return row, err
}

func (api *StreamingAPI) mcpURLAccess(r *http.Request, row mcpURLSignIn) bool {
	args := cloneTaskArgs(row.Scope)
	if row.Target == "manage_vault_access" {
		args["connection_id"] = strings.TrimPrefix(row.Account, "vault_")
	}
	rec, err := api.externalMCPInvoke(r.Context(), r, row.Target, args, true)
	return err == nil && rec.status >= 200 && rec.status < 300
}

func mcpURLForm(row mcpURLSignIn) *mcp.CallToolResult {
	origin, _, ok := mcpOAuthURLs()
	if !ok {
		return mcp.NewToolResultError("This server needs its public address configured before browser sign-in.")
	}
	return server.NewInputRequestBuilder("url:"+row.ID).Elicit(row.ID, mcp.ElicitationParams{
		Mode: mcp.ElicitationModeURL, Message: "Open AgentWorks to finish connecting your account. Sign in there with the same AgentWorks account you are using here.",
		URL: origin + "/mcp/elicitation/" + row.ID,
	}).ToolResult()
}

// URL state never carries credentials or grants authority. A signed-in browser
// consumes the one-time ID; the provider callback marks that exact flow done.
func (api *StreamingAPI) externalMCPURLCall(r *http.Request, target string, args map[string]any, request mcp.CallToolRequest) *mcp.CallToolResult {
	if !agentworksclient.MCPURLSignInCandidate(target, args) {
		return nil
	}
	capability := agentworksclient.ClientURLCapability(r.Context(), false)
	if !capability {
		return nil
	}
	store, err := openMCPURLSignIns()
	if err != nil {
		return mcp.NewToolResultError("Browser sign-in is temporarily unavailable.")
	}
	defer store.Close()
	user, connection := mcpTaskPrincipal(r)
	if strings.HasPrefix(request.Params.RequestState, "url:") {
		id := strings.TrimPrefix(request.Params.RequestState, "url:")
		row, err := loadMCPURLSignIn(store, id)
		if err != nil || row.User != user || row.Connection != connection || row.Target != target || row.ArgsHash != mcpURLArgsHash(args) || !api.mcpURLAccess(r, row) {
			return mcp.NewToolResultError("This sign-in request is no longer available. Start connecting again.")
		}
		response := server.ElicitationResponse(request.Params.InputResponses, id)
		if response == nil {
			return mcp.NewToolResultError("Answer the sign-in request that was shown.")
		}
		if response.Action != mcp.ElicitationResponseActionAccept {
			_, _ = store.db.Exec(`DELETE FROM external_mcp_url_signins WHERE id=?`, id)
			return mcp.NewToolResultText("Sign-in was not completed. You can ask to connect again later.")
		}
		status := "sign_in_pending"
		message := "Finish signing in in your browser, then check the saved account's connection status."
		if row.Opened && row.Complete {
			status, message = "connected", "Your account is connected."
			_, _ = store.db.Exec(`DELETE FROM external_mcp_url_signins WHERE id=?`, id)
		}
		result, _ := mcp.NewToolResultJSON(map[string]any{"status": status, "connection_id": row.Account, "message": message})
		return result
	}
	if request.Params.RequestState != "" {
		return mcp.NewToolResultError("This sign-in request does not match the current operation.")
	}
	rec, err := api.externalMCPInvoke(r.Context(), r, target, args, true)
	if err != nil {
		return mcp.NewToolResultError("The account could not be connected. Try again.")
	}
	if rec.status < 200 || rec.status >= 300 {
		return externalMCPDispatchResult(rec)
	}
	var native any
	_ = json.Unmarshal(rec.body.Bytes(), &native)
	authURL := findMCPURLField(native, "auth_url")
	account := findMCPURLField(native, "connection_id")
	if account == "" {
		account = findMCPURLField(native, "server_name")
	}
	parsed, err := url.Parse(authURL)
	if err != nil || parsed.Host == "" || parsed.Query().Get("state") == "" || account == "" {
		return externalMCPDispatchResult(rec)
	}
	scope, ok := mcpURLScope(target, args)
	if !ok {
		return externalMCPDispatchResult(rec)
	}
	id := uuid.NewString()
	scopeRaw, _ := json.Marshal(scope)
	_, err = store.db.Exec(`INSERT INTO external_mcp_url_signins(id,user_id,connection_id,target,args_hash,account_id,scope,auth_url,oauth_state,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, user, connection, target, mcpURLArgsHash(args), account, string(scopeRaw), authURL, mcpURLHash([]byte(parsed.Query().Get("state"))), time.Now().Add(10*time.Minute).Unix())
	if err != nil {
		return mcp.NewToolResultError("The sign-in request could not be saved. Ask to connect again.")
	}
	logfWithContext(api.httpRequestLogContext(r), "[MCP_ELICITATION] request=%s mode=url event=created", id)
	return mcpURLForm(mcpURLSignIn{ID: id})
}

func (api *StreamingAPI) handleMCPURLSignInOpen(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken != nil {
		externalError(w, 401, "sign_in_required", "Sign in to AgentWorks in this browser first.")
		return
	}
	store, err := openMCPURLSignIns()
	if err != nil {
		externalError(w, 503, "unavailable", "Sign-in is temporarily unavailable.")
		return
	}
	defer store.Close()
	row, err := loadMCPURLSignIn(store, mux.Vars(r)["request_id"])
	if err != nil {
		externalError(w, 404, "not_found", "This sign-in link has expired or was already used. Ask your AI to connect again.")
		return
	}
	// Consume even on an account mismatch: another person cannot later reuse it.
	result, err := store.db.Exec(`UPDATE external_mcp_url_signins SET consumed=1 WHERE id=? AND consumed=0`, row.ID)
	count := int64(0)
	if err == nil {
		count, _ = result.RowsAffected()
	}
	if err != nil || count != 1 || row.User != claims.UserID || !api.mcpURLAccess(r, row) {
		externalError(w, 404, "not_found", "Use the same AgentWorks account as your AI chat and ask it to connect again.")
		return
	}
	if _, err := store.db.Exec(`UPDATE external_mcp_url_signins SET opened=1 WHERE id=?`, row.ID); err != nil {
		externalError(w, 503, "unavailable", "Sign-in is temporarily unavailable. Ask your AI to connect again.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	logfWithContext(api.httpRequestLogContext(r), "[MCP_ELICITATION] request=%s mode=url event=browser_opened", row.ID)
	externalJSON(w, map[string]any{"url": row.AuthURL})
}

func completeMCPURLSignIn(state, account string) {
	store, err := openMCPURLSignIns()
	if err != nil {
		return
	}
	defer store.Close()
	_, _ = store.db.Exec(`UPDATE external_mcp_url_signins SET complete=1 WHERE oauth_state=? AND (account_id=? OR account_id=?) AND opened=1`, mcpURLHash([]byte(state)), account, "vault_"+account)
}

func (api *StreamingAPI) handleExternalMCPInput(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Target         string                       `json:"target"`
		Arguments      map[string]any               `json:"arguments"`
		RequestState   string                       `json:"requestState"`
		InputResponses map[string]mcp.InputResponse `json:"inputResponses"`
		Capabilities   *mcp.ClientCapabilities      `json:"capabilities"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, agentworksclient.MaxBodyBytes))
	decoder.UseNumber()
	if decoder.Decode(&input) != nil || !agentworksclient.MCPURLSignInCandidate(input.Target, input.Arguments) {
		externalError(w, 400, "invalid_arguments", "Choose a supported account sign-in operation.")
		return
	}
	catalog, err := externalTools()
	if err != nil {
		externalError(w, 503, "unavailable", "The tool catalog is unavailable.")
		return
	}
	for _, tool := range catalog {
		if tool.Name == input.Target && tool.actions != nil {
			member, rest, err := externalResolveMerged(tool, input.Arguments)
			if err != nil {
				externalError(w, 400, "invalid_arguments", "Choose an available account operation.")
				return
			}
			input.Target, input.Arguments = member.Name, rest
			member, input.Arguments = agentSourceCall(member, input.Arguments)
			input.Target = member.Name
			break
		}
	}
	r = r.WithContext(server.WithRequestProtocolInfo(r.Context(), &server.RequestProtocolInfo{Modern: true, ProtocolVersion: mcp.ProtocolVersion20260728, ClientCapabilities: input.Capabilities}))
	request := mcp.CallToolRequest{}
	request.Params.RequestState, request.Params.InputResponses = input.RequestState, input.InputResponses
	result := api.externalMCPURLCall(r, input.Target, input.Arguments, request)
	if result == nil {
		externalError(w, 400, "unsupported", "This client does not support URL sign-in requests.")
		return
	}
	result.ResultType = mcp.ResultTypeComplete
	if len(result.InputRequests) > 0 {
		result.ResultType = mcp.ResultTypeInputRequired
	}
	externalJSON(w, result)
}
