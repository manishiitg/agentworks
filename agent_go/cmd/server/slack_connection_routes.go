package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Per-workflow and per-project Slack apps: CRUD over Slack app identities,
// under the same /api/human-feedback/slack prefix the single-app endpoints
// already use.
//
// One connection = one Slack app (bot token + app token pair) with its own
// Socket Mode listener. Workflows select one via
// WorkflowCapabilities.SlackConnectionID and crew projects via
// capabilities.slack_connection_id in their runtime manifest; anything
// without a selection uses the default connection.
//
// Ownership: a connection scoped to a workflow or product project
// (workspace_path, plus profile_id for products) is managed by that
// workflow's owners or the product's owners; unscoped connections,
// including the default, are managed by platform admins. Tokens are
// encrypted at rest and masked in every response.

// SlackConnectionResponse is the wire shape of one connection.
//
// Built by explicit projection rather than marshaling
// services.SlackConnection, so a field added to the model later cannot leak
// through this API by default. Tokens are always masked here.
type SlackConnectionResponse struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	BotToken      string `json:"bot_token,omitempty"` // masked
	AppToken      string `json:"app_token,omitempty"` // masked
	Enabled       bool   `json:"enabled"`
	Configured    bool   `json:"configured"`
	IsDefault     bool   `json:"is_default"`
	WorkspacePath string `json:"workspace_path,omitempty"`
	ProfileID     string `json:"profile_id,omitempty"`
}

// SlackConnectionsResponse is the list payload.
type SlackConnectionsResponse struct {
	Connections         []SlackConnectionResponse `json:"connections"`
	DefaultConnectionID string                    `json:"default_connection_id,omitempty"`
}

// SlackConnectionRequest is the create/update body. On update, empty or
// masked tokens keep the stored value; Enabled, WorkspacePath, and
// ProfileID are pointers so "leave unchanged" stays distinct from an
// explicit value.
type SlackConnectionRequest struct {
	DisplayName   string  `json:"display_name,omitempty"`
	BotToken      string  `json:"bot_token,omitempty"`
	AppToken      string  `json:"app_token,omitempty"`
	Enabled       *bool   `json:"enabled,omitempty"`
	WorkspacePath *string `json:"workspace_path,omitempty"`
	ProfileID     *string `json:"profile_id,omitempty"`
}

// SlackConnectionRoutes wires the connection registry API, mirroring the
// Gmail connections routes.
func SlackConnectionRoutes(router *mux.Router, api *StreamingAPI) {
	r := router.PathPrefix("/api/human-feedback/slack/connections").Subrouter()
	r.HandleFunc("", listSlackConnectionsHandler(api)).Methods("GET")
	r.HandleFunc("", createSlackConnectionHandler(api)).Methods("POST", "OPTIONS")
	// Before "/{id}" so "/mine" is not read as a connection ID.
	registerSlackConnectionChannelRoutes(r, api)
	r.HandleFunc("/{id}", getSlackConnectionHandler(api)).Methods("GET")
	r.HandleFunc("/{id}", updateSlackConnectionHandler(api)).Methods("PATCH", "POST", "OPTIONS")
	r.HandleFunc("/{id}", deleteSlackConnectionHandler(api)).Methods("DELETE", "OPTIONS")
	r.HandleFunc("/{id}/default", setDefaultSlackConnectionHandler(api)).Methods("POST", "OPTIONS")
	r.HandleFunc("/{id}/test", testSlackConnectionEntryHandler(api)).Methods("POST", "OPTIONS")
	r.HandleFunc("/{id}/dry-run", slackConnectionDryRunHandler(api)).Methods("POST", "OPTIONS")
	r.HandleFunc("/project/selection", projectSlackConnectionHandler(api)).Methods("GET", "PUT", "POST", "OPTIONS")
	// Slack slugs (PLAT-668): /api/human-feedback/slack/targets/...
	registerSlackTargetRoutes(router, api)
}

// projectSlackConnection is the single place a connection becomes JSON.
func projectSlackConnection(conn services.SlackConnection, defaultID string) SlackConnectionResponse {
	masked := conn
	// The service already masks on read, but projection must not depend on
	// that: re-mask defensively so a future unmasked caller cannot leak.
	masked.BotToken = maskSlackTokenForAPI("xoxb-", conn.BotToken)
	masked.AppToken = maskSlackTokenForAPI("xapp-", conn.AppToken)
	return SlackConnectionResponse{
		ID:            conn.ID,
		DisplayName:   conn.DisplayName,
		BotToken:      masked.BotToken,
		AppToken:      masked.AppToken,
		Enabled:       conn.Enabled,
		Configured:    strings.TrimSpace(conn.BotToken) != "" && strings.TrimSpace(conn.AppToken) != "",
		IsDefault:     conn.ID != "" && conn.ID == defaultID,
		WorkspacePath: conn.WorkspacePath,
		ProfileID:     conn.ProfileID,
	}
}

// maskSlackTokenForAPI masks a token that may already be masked. Masked
// values pass through unchanged so presence checks keep working.
func maskSlackTokenForAPI(prefix string, token string) string {
	if strings.Contains(token, "...") || token == "" {
		return token
	}
	if len(token) <= 4 {
		return prefix + "..."
	}
	return prefix + "..." + token[len(token)-4:]
}

func writeSlackConnection(w http.ResponseWriter, svc *services.SlackService, conn services.SlackConnection) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(projectSlackConnection(conn, svc.DefaultConnectionID()))
}

// slackConnectionService resolves the service and the {id} path variable,
// writing the error response itself when either is unavailable.
func slackConnectionService(w http.ResponseWriter, r *http.Request) (*services.SlackService, string, bool) {
	svc, err := ensureSlackService()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to initialize Slack service: %v", err), http.StatusInternalServerError)
		return nil, "", false
	}
	return svc, strings.TrimSpace(mux.Vars(r)["id"]), true
}

// requireSlackConnectionCreateAccess enforces who may create a connection.
// Admins may create at any scope; anyone else must scope the connection to
// a workflow they own. Bot-route principals can never manage connections.
// physicalProductSlackScope turns a crew/product project path into the
// caller's physical project folder. The browser sends a crew's logical path
// ("Chats/Work/projects/<id>"); stored or read as-is it names no folder in the
// document root, so the project's manifest was "not found" when the new bot
// was selected, and the bot's Slack route had no owner (RTS 2026-09-25, #201
// sub-issue 5). A "_users/<owner>/..." path is already physical and is kept.
// Workflow scopes (no profile) are returned unchanged.
func physicalProductSlackScope(ctx context.Context, profileID, workspacePath string) string {
	workspacePath = strings.TrimSpace(workspacePath)
	if strings.TrimSpace(profileID) == "" || workspacePath == "" {
		return workspacePath
	}
	userID := productWorkspaceUserID(ctx)
	// A Crew that moved to the shared root is addressed there, whichever spelling the browser still sends.
	return followCrewAlias(userID, productConversationRuntimeWorkspace(userID, workspacePath))
}

func requireSlackConnectionCreateAccess(r *http.Request, api *StreamingAPI, workspacePath, profileID string) error {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.Provider == "bot_route" || claims.BotRouteGrant != "" {
		return fmt.Errorf("only an authenticated interactive user may manage Slack connections")
	}
	if currentUserIsAdmin(r) {
		return nil
	}
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return fmt.Errorf("only a platform admin may create a platform-managed Slack connection")
	}
	if strings.TrimSpace(profileID) != "" {
		return requireProductSlackScopeOwner(r.Context(), api, profileID, workspacePath)
	}
	return requireSlackConnectionWorkflowOwner(r.Context(), workspacePath)
}

// requireSlackConnectionAccess enforces who may read-for-write, update,
// test, or delete a connection. Admins manage everything; anyone else must
// own the workflow or product project the connection is scoped to.
// Unscoped connections are admin-only.
func requireSlackConnectionAccess(r *http.Request, api *StreamingAPI, conn services.SlackConnection) error {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.Provider == "bot_route" || claims.BotRouteGrant != "" {
		return fmt.Errorf("only an authenticated interactive user may manage Slack connections")
	}
	if currentUserIsAdmin(r) {
		return nil
	}
	if strings.TrimSpace(conn.WorkspacePath) == "" {
		return fmt.Errorf("only a platform admin may manage this Slack connection")
	}
	if strings.TrimSpace(conn.ProfileID) != "" {
		return requireProductSlackScopeOwner(r.Context(), api, conn.ProfileID, conn.WorkspacePath)
	}
	return requireSlackConnectionWorkflowOwner(r.Context(), conn.WorkspacePath)
}

// requireSlackConnectionWorkflowOwner requires Owner (not merely write)
// access on the workflow, matching the bar for managing its Slack routes.
func requireSlackConnectionWorkflowOwner(ctx context.Context, workspacePath string) error {
	manifest, exists, err := ReadWorkflowManifest(ctx, strings.TrimSpace(workspacePath))
	if err != nil {
		return err
	}
	if !exists || manifest == nil {
		return fmt.Errorf("no workflow owns this Slack connection scope")
	}
	if workflowAccessForManifest(GetUserFromContext(ctx), manifest) != WorkflowAccessOwner {
		return fmt.Errorf("only a workflow owner may manage this Slack connection")
	}
	return nil
}

// requireProductSlackScopeOwner is the product-project mirror of
// requireSlackConnectionWorkflowOwner: admins pass, anyone else must be
// allowed on the product and the scope must sit under their own product
// root. Product workspaces are per-user resources, so a path outside both
// roots names someone else's project. Manifest existence is deliberately
// not required: fixed-workspace products create their manifest on first
// use, and the selection write below does that.
func requireProductSlackScopeOwner(ctx context.Context, api *StreamingAPI, profileID, workspacePath string) error {
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.Provider == "bot_route" || claims.BotRouteGrant != "" {
		return fmt.Errorf("only an authenticated interactive user may manage Slack connections")
	}
	if userAccessForClaims(claims).Admin {
		return nil
	}
	profileID = strings.TrimSpace(profileID)
	workspacePath = strings.TrimSpace(workspacePath)
	if profileID == "" || workspacePath == "" {
		return fmt.Errorf("product Slack scope is incomplete")
	}
	if api == nil || api.agentProfiles == nil {
		return fmt.Errorf("agent profiles are unavailable; cannot verify product ownership")
	}
	userID := productWorkspaceUserID(ctx)
	// A connection saved before scopes were made physical still holds the
	// logical path, which names the caller's own crew; resolve it so its owner
	// is not locked out of the bot (and can re-save it to repair the scope).
	workspacePath = followCrewAlias(userID, productConversationRuntimeWorkspace(userID, workspacePath))
	profile, err := api.agentProfiles.Resolve(profileID, 0, userID)
	if err != nil {
		return fmt.Errorf("agent profile %q is unavailable: %w", profileID, err)
	}
	if !userAllowedProduct(claims, profile.Product) {
		return fmt.Errorf("only a product owner may manage this Slack connection")
	}
	if !productWorkspaceUnderCallerRoot(profile, userID, workspacePath) {
		return fmt.Errorf("Slack connection scope is outside your product workspace")
	}
	return nil
}

// productWorkspaceUnderCallerRoot reports whether workspacePath sits under
// the caller's own singleton root or keyed-projects runtime root for the
// profile. Paths are cleaned before the prefix comparison so ".." cannot
// escape the root.
func productWorkspaceUnderCallerRoot(profile agentprofiles.Profile, userID, workspacePath string) bool {
	clean := filepath.ToSlash(filepath.Clean("/" + strings.TrimSpace(workspacePath)))
	// A Crew at the shared root is the caller's own when the server's registry says so (PLAT-442 step 4).
	if project, shared := workspaceref.MustParse(clean).SharedProject(); shared &&
		strings.TrimSpace(profile.Runtime.Workspace.ProjectsRoot) == workspaceref.CrewProjectsRoot && sharedCrewOwner(project) == sanitizeUserIDForPath(userID) {
		return true
	}
	roots := []string{}
	if root := strings.TrimSpace(profile.Runtime.Workspace.Root); root != "" {
		if cleaned, err := cleanAgentProfileWorkspace(root, userID); err == nil {
			roots = append(roots, cleaned)
		}
	}
	if projectsRoot := strings.TrimSpace(profile.Runtime.Workspace.ProjectsRoot); projectsRoot != "" {
		if cleaned, err := cleanAgentProfileWorkspace(projectsRoot, userID); err == nil {
			roots = append(roots, agentProfileRuntimeWorkspace(userID, cleaned))
		}
	}
	for _, root := range roots {
		prefix := strings.TrimSuffix(filepath.ToSlash(filepath.Clean("/"+root)), "/") + "/"
		if clean == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(clean, prefix) {
			return true
		}
	}
	return false
}

// validateSlackConnectionScope checks an admin-assigned scope names a real
// workflow or product project. Workflow scopes need a workflow manifest;
// product scopes need the project's runtime manifest to exist already
// (creation flows use ensure-style writes instead, so fixed-workspace
// products without a manifest yet are unaffected).
func validateSlackConnectionScope(ctx context.Context, workspacePath, profileID string) error {
	workspacePath = strings.TrimSpace(workspacePath)
	profileID = strings.TrimSpace(profileID)
	if workspacePath == "" {
		return nil
	}
	if profileID == "" {
		if _, exists, err := ReadWorkflowManifest(ctx, workspacePath); err != nil || !exists {
			return fmt.Errorf("slack connection scope names an unknown workflow")
		}
		return nil
	}
	if _, found, err := readProjectRuntimeManifest(ctx, profileID, workspacePath); err != nil || !found {
		return fmt.Errorf("slack connection scope names an unknown product project")
	}
	return nil
}

func listSlackConnectionsHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, err := ensureSlackService()
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to initialize Slack service: %v", err), http.StatusInternalServerError)
			return
		}
		defaultID := svc.DefaultConnectionID()
		conns := svc.ListConnections()
		out := SlackConnectionsResponse{
			Connections:         make([]SlackConnectionResponse, 0, len(conns)),
			DefaultConnectionID: defaultID,
		}
		for _, c := range conns {
			out.Connections = append(out.Connections, projectSlackConnection(c, defaultID))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	}
}

func getSlackConnectionHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, id, ok := slackConnectionService(w, r)
		if !ok {
			return
		}
		conn, found := svc.GetConnection(id)
		if !found {
			http.Error(w, fmt.Sprintf("slack connection %q not found", id), http.StatusNotFound)
			return
		}
		writeSlackConnection(w, svc, conn)
	}
}

func createSlackConnectionHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		svc, err := ensureSlackService()
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to initialize Slack service: %v", err), http.StatusInternalServerError)
			return
		}
		var req SlackConnectionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
			return
		}
		workspacePath := ""
		if req.WorkspacePath != nil {
			workspacePath = strings.TrimSpace(*req.WorkspacePath)
		}
		profileID := ""
		if req.ProfileID != nil {
			profileID = strings.TrimSpace(*req.ProfileID)
		}
		workspacePath = physicalProductSlackScope(r.Context(), profileID, workspacePath)
		if err := requireSlackConnectionCreateAccess(r, api, workspacePath, profileID); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		conn, err := svc.CreateSlackConnection(r.Context(), services.SlackConnectionInput{
			DisplayName:   req.DisplayName,
			BotToken:      req.BotToken,
			AppToken:      req.AppToken,
			Enabled:       enabled,
			WorkspacePath: workspacePath,
			ProfileID:     profileID,
		})
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, services.ErrSlackTokenInUse) {
				status = http.StatusConflict
			}
			http.Error(w, err.Error(), status)
			return
		}
		registerSlackBotConnectorForOwnedConnections(api, svc)
		w.WriteHeader(http.StatusCreated)
		writeSlackConnection(w, svc, conn)
	}
}

func updateSlackConnectionHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		svc, id, ok := slackConnectionService(w, r)
		if !ok {
			return
		}
		current, found := svc.GetConnection(id)
		if !found {
			http.Error(w, fmt.Sprintf("slack connection %q not found", id), http.StatusNotFound)
			return
		}
		if err := requireSlackConnectionAccess(r, api, current); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		var req SlackConnectionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
			return
		}
		enabled := current.Enabled
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		workspacePath := current.WorkspacePath
		profileID := current.ProfileID
		if req.WorkspacePath == nil && req.ProfileID == nil {
			// A crew bot saved before paths were made physical keeps a logical
			// path. A logical path always names the caller's own crew (another
			// owner's crew is addressed by its "_users/<owner>/" path), so
			// re-saving it from the crew repairs the stored scope.
			workspacePath = physicalProductSlackScope(r.Context(), profileID, workspacePath)
		}
		if req.WorkspacePath != nil || req.ProfileID != nil {
			if !currentUserIsAdmin(r) {
				http.Error(w, "only a platform admin may change a Slack connection's scope", http.StatusForbidden)
				return
			}
			if req.WorkspacePath != nil {
				workspacePath = strings.TrimSpace(*req.WorkspacePath)
			}
			if req.ProfileID != nil {
				profileID = strings.TrimSpace(*req.ProfileID)
			}
			if workspacePath == "" {
				profileID = ""
			} else if err := validateSlackConnectionScope(r.Context(), workspacePath, profileID); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		conn, err := svc.UpdateSlackConnection(r.Context(), id, services.SlackConnectionInput{
			DisplayName:   req.DisplayName,
			BotToken:      req.BotToken,
			AppToken:      req.AppToken,
			Enabled:       enabled,
			WorkspacePath: workspacePath,
			ProfileID:     profileID,
		})
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, services.ErrSlackTokenInUse) {
				status = http.StatusConflict
			}
			http.Error(w, err.Error(), status)
			return
		}
		registerSlackBotConnectorForOwnedConnections(api, svc)
		writeSlackConnection(w, svc, conn)
	}
}

func deleteSlackConnectionHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		svc, id, ok := slackConnectionService(w, r)
		if !ok {
			return
		}
		current, found := svc.GetConnection(id)
		if !found {
			http.Error(w, fmt.Sprintf("slack connection %q not found", id), http.StatusNotFound)
			return
		}
		if err := requireSlackConnectionAccess(r, api, current); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		if ref := slackConnectionWorkflowReference(r.Context(), id); ref != "" {
			http.Error(w, fmt.Sprintf("This bot can't be removed yet: workflow %s still uses it. An owner of that workflow must choose another bot in its Integrations → Slack first.", ref), http.StatusConflict)
			return
		}
		if ref := slackConnectionProjectReference(r.Context(), current, id); ref != "" {
			http.Error(w, fmt.Sprintf("slack connection is still selected by product project %q; point the project elsewhere first", ref), http.StatusConflict)
			return
		}
		if err := svc.DeleteSlackConnection(r.Context(), id); err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			http.Error(w, err.Error(), status)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// validateSlackConnectionSelection fails a save that selects an unknown
// Slack connection. Empty (inherit the default) always passes, as does any
// selection when the registry cannot be read: an infra outage must not
// block unrelated edits, and sends fail loudly anyway.
func validateSlackConnectionSelection(connID string) error {
	connID = strings.TrimSpace(connID)
	if connID == "" {
		return nil
	}
	exists, err := services.SlackConnectionExists(connID)
	if err != nil {
		log.Printf("[SLACK] connection validation skipped: %v", err)
		return nil
	}
	if !exists {
		return fmt.Errorf("slack_connection_id %q names an unknown Slack connection", connID)
	}
	return nil
}

// validateWorkflowSlackConnectionID fails a manifest save that selects an
// unknown Slack connection.
func validateWorkflowSlackConnectionID(connID string) error {
	return validateSlackConnectionSelection(connID)
}

// productSlackConnectionID reads capabilities.slack_connection_id from a
// product project's runtime manifest. A missing manifest or field means
// "inherit the platform default", never an error.
func productSlackConnectionID(ctx context.Context, profileID, workspacePath string) (string, error) {
	raw, found, err := readProjectRuntimeManifest(ctx, profileID, workspacePath)
	if err != nil {
		return "", err
	}
	if !found {
		return "", nil
	}
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return "", fmt.Errorf("decode product manifest: %w", err)
	}
	capabilities, _ := manifest["capabilities"].(map[string]interface{})
	selected, _ := capabilities["slack_connection_id"].(string)
	return strings.TrimSpace(selected), nil
}

// updateProductSlackConnectionID sets capabilities.slack_connection_id on a
// product project's runtime manifest, creating the manifest when the
// product creates its state on first use. Empty clears the selection.
func updateProductSlackConnectionID(ctx context.Context, profileID, workspacePath, connID string) error {
	connID = strings.TrimSpace(connID)
	if err := validateSlackConnectionSelection(connID); err != nil {
		return err
	}
	raw, manifestPath, err := ensureProjectRuntimeManifest(ctx, profileID, workspacePath)
	if err != nil {
		return err
	}
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return fmt.Errorf("decode product manifest: %w", err)
	}
	capabilities, _ := manifest["capabilities"].(map[string]interface{})
	if capabilities == nil {
		capabilities = map[string]interface{}{}
	}
	if connID == "" {
		delete(capabilities, "slack_connection_id")
	} else {
		capabilities["slack_connection_id"] = connID
	}
	manifest["capabilities"] = capabilities
	manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, manifestPath, string(encoded)+"\n")
}

// slackConnectionWorkflowReference names a workflow (label preferred) that
// still selects the connection, or "" when nothing references it. Discovery
// failures fail closed: deleting blind could strand a workflow's sends.
func slackConnectionWorkflowReference(ctx context.Context, connID string) string {
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		log.Printf("[SLACK] workflow discovery failed during connection delete: %v", err)
		return "unknown (workflow discovery failed)"
	}
	for _, entry := range discovered {
		manifest := entry.Manifest
		if manifest == nil {
			continue
		}
		if strings.TrimSpace(manifest.Capabilities.SlackConnectionID) != connID {
			continue
		}
		name := strings.TrimSpace(manifest.Label)
		if name == "" {
			name = manifest.ID
		}
		// Name who can change it: the person removing a bot is often not an owner of the workflow that still uses it
		// (Confida 2026-10-07).
		var owners []string
		for _, id := range manifest.Access.Owners {
			if rec := directoryUserFor(id, "", ""); rec != nil {
				owners = append(owners, firstNonEmptyTrimmed(rec.Username, rec.Email))
			}
		}
		if len(owners) > 0 {
			return fmt.Sprintf("%s (owners: %s)", name, strings.Join(owners, ", "))
		}
		return name
	}
	return ""
}

// slackConnectionProjectReference names the product project (label
// preferred) that still selects the connection, or "" when nothing
// references it. Only the connection's own project manifest is checked:
// product workspaces are per-user resources, so unlike workflows there is
// no global manifest listing to scan, and a cross-project selection strands
// sends loudly rather than silently. Lookup failures fail closed.
func slackConnectionProjectReference(ctx context.Context, conn services.SlackConnection, connID string) string {
	profileID := strings.TrimSpace(conn.ProfileID)
	workspacePath := strings.TrimSpace(conn.WorkspacePath)
	if profileID == "" || workspacePath == "" {
		return ""
	}
	raw, found, err := readProjectRuntimeManifest(ctx, profileID, workspacePath)
	if err != nil {
		log.Printf("[SLACK] project discovery failed during connection delete: %v", err)
		return "unknown (project manifest unreadable)"
	}
	if !found {
		return ""
	}
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		log.Printf("[SLACK] project manifest decode failed during connection delete: %v", err)
		return "unknown (project manifest unreadable)"
	}
	capabilities, _ := manifest["capabilities"].(map[string]interface{})
	selected, _ := capabilities["slack_connection_id"].(string)
	if strings.TrimSpace(selected) != connID {
		return ""
	}
	if label, _ := manifest["label"].(string); strings.TrimSpace(label) != "" {
		return strings.TrimSpace(label)
	}
	if id, _ := manifest["id"].(string); strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id)
	}
	return workspacePath
}

// projectSlackConnectionHandler reads or writes a product project's Slack
// app selection: the crew mirror of the workflow manifest's
// slack_connection_id. Reads need product access; writes need product
// ownership (or admin).
func projectSlackConnectionHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		claims := GetUserFromContext(r.Context())
		if claims == nil || claims.Provider == "bot_route" || claims.BotRouteGrant != "" {
			http.Error(w, "only an authenticated interactive user may manage project Slack apps", http.StatusForbidden)
			return
		}
		profileID := strings.TrimSpace(r.URL.Query().Get("profile_id"))
		workspacePath := physicalProductSlackScope(r.Context(), profileID, r.URL.Query().Get("workspace_path"))
		if profileID == "" || workspacePath == "" {
			http.Error(w, "profile_id and workspace_path are required", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if api == nil || api.agentProfiles == nil {
				http.Error(w, "agent profiles are unavailable", http.StatusInternalServerError)
				return
			}
			profile, err := api.agentProfiles.Resolve(profileID, 0, productWorkspaceUserID(r.Context()))
			if err != nil {
				http.Error(w, fmt.Sprintf("agent profile %q is unavailable", profileID), http.StatusBadRequest)
				return
			}
			if !userAllowedProduct(claims, profile.Product) && !userAccessForClaims(claims).Admin {
				http.Error(w, "product access denied", http.StatusForbidden)
				return
			}
			selected, err := productSlackConnectionID(r.Context(), profileID, workspacePath)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to read project Slack selection: %v", err), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"slack_connection_id": selected})
		case http.MethodPut, http.MethodPost:
			var req struct {
				SlackConnectionID string `json:"slack_connection_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
				return
			}
			if err := requireProductSlackScopeOwner(r.Context(), api, profileID, workspacePath); err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			if err := updateProductSlackConnectionID(r.Context(), profileID, workspacePath, req.SlackConnectionID); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			registerSlackBotConnectorForOwnedConnections(api, services.GetSlackService())
			selected, err := productSlackConnectionID(r.Context(), profileID, workspacePath)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to read project Slack selection: %v", err), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"slack_connection_id": selected})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func setDefaultSlackConnectionHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		claims := GetUserFromContext(r.Context())
		if claims == nil || claims.Provider == "bot_route" || claims.BotRouteGrant != "" || !currentUserIsAdmin(r) {
			http.Error(w, "only a platform admin may change the default Slack connection", http.StatusForbidden)
			return
		}
		svc, id, ok := slackConnectionService(w, r)
		if !ok {
			return
		}
		if err := svc.SetDefaultSlackConnection(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		conn, found := svc.GetConnection(id)
		if !found {
			http.Error(w, fmt.Sprintf("slack connection %q not found", id), http.StatusNotFound)
			return
		}
		writeSlackConnection(w, svc, conn)
	}
}

func testSlackConnectionEntryHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		svc, id, ok := slackConnectionService(w, r)
		if !ok {
			return
		}
		current, found := svc.GetConnection(id)
		if !found {
			http.Error(w, fmt.Sprintf("slack connection %q not found", id), http.StatusNotFound)
			return
		}
		if err := requireSlackConnectionAccess(r, api, current); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		result := svc.DiagnoseConnectionFor(r.Context(), id)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

// slackConnectionIDForRoute resolves the connection a channel route sends
// through: the destination workflow's manifest selection, or "" (platform
// default) for profile routes and workflows without a selection.
func slackConnectionIDForRoute(ctx context.Context, route ChannelRoute) string {
	if strings.TrimSpace(route.WorkflowID) == "" {
		// Product route: the project's own selection, else the default.
		// Lookup failures resolve to the default rather than failing the
		// send: explicit selections are validated at save time, and the
		// default path fails loudly on its own when unusable.
		if strings.TrimSpace(route.ProfileID) == "" {
			return ""
		}
		selected, err := productSlackConnectionID(ctx, route.ProfileID, route.WorkspacePath)
		if err != nil {
			log.Printf("[SLACK] project selection lookup failed: %v", err)
			return ""
		}
		return selected
	}
	manifest, found, err := ReadWorkflowManifest(ctx, route.WorkspacePath)
	if err != nil || !found {
		return ""
	}
	return strings.TrimSpace(manifest.Capabilities.SlackConnectionID)
}

// slackToolConnectionID resolves the connection a bot-tool send uses. A
// live bot execution already carries its arrival connection, which wins so
// tool sends stay on the conversation's own Slack app; anything else falls
// back to the route workflow's manifest selection.
func slackToolConnectionID(ctx context.Context, api *StreamingAPI, session string, route ChannelRoute) string {
	if api != nil && session != "" {
		if execution, ok := api.botExecutionForSession(session); ok {
			if id := strings.TrimSpace(execution.Request.BotConnectionID); id != "" {
				return id
			}
		}
	}
	return slackConnectionIDForRoute(ctx, route)
}

// slackHasOwnedEnabledConnection reports whether any enabled Slack
// connection is owned by a workflow or crew project. Such an app has its
// own Socket Mode listener, so its @mentions need the bot manager's
// handler even when the shared bot switch is off and no channel route
// exists.
func slackHasOwnedEnabledConnection(svc *services.SlackService) bool {
	if svc == nil {
		return false
	}
	for _, conn := range svc.ListConnections() {
		if !conn.Enabled {
			continue
		}
		if strings.TrimSpace(conn.WorkspacePath) != "" || strings.TrimSpace(conn.ProfileID) != "" {
			return true
		}
	}
	return false
}

// registerSlackBotConnector registers Slack with the bot manager and starts
// listening, unless it is already registered. Registration only wires the
// message handler; per-message routing and ownership gates still decide
// what each message may do. Reports whether it registered now.
func registerSlackBotConnector(botManager *services.BotConversationManager, svc *services.SlackService) bool {
	if botManager == nil || svc == nil || botManager.GetConnector("slack") != nil {
		return false
	}
	botManager.RegisterConnector(svc)
	svc.StartListening(context.Background())
	return true
}

// registerSlackBotConnectorForOwnedConnections registers Slack when a
// workflow- or crew-owned connection is enabled. Called after connection
// saves; the shared-bot and route conditions are handled by the Slack
// config save and startup as before.
func registerSlackBotConnectorForOwnedConnections(api *StreamingAPI, svc *services.SlackService) {
	if api == nil || !slackHasOwnedEnabledConnection(svc) {
		return
	}
	if registerSlackBotConnector(api.botManager, svc) {
		log.Printf("[SLACK] Owned Slack connection enabled — registered with bot manager")
	} else if api.botManager != nil {
		// Already registered: the root re-propagated its handler to the
		// new or restarted child runtime on reload; resolve bot identities
		// so the child strips its own @mention.
		svc.StartListening(context.Background())
	}
}

// slackBotConnectorWantedAtStartup mirrors the startup registration rule:
// the platform switch, any owner-saved channel route, or any enabled
// workflow- or crew-owned connection.
func slackBotConnectorWantedAtStartup(botConfig *chathistory.BotConnectorConfig, svc *services.SlackService) bool {
	if botConfig != nil && (botConfig.BotMode || services.SlackBotConfigHasRoutes(botConfig)) {
		return true
	}
	return slackHasOwnedEnabledConnection(svc) || slackPlatformBotHasTargets(context.Background())
}

// slackPlatformBotHasTargets reports whether any target switched on "Use the
// AgentWorks bot": the platform bot then needs the bot manager's handler for
// DMs and owner-added channels (PLAT-668).
func slackPlatformBotHasTargets(ctx context.Context) bool {
	registry, err := services.LoadSlackTargetsRegistry(ctx)
	if err != nil || registry == nil {
		return false
	}
	for _, entry := range registry.Targets {
		if entry.PlatformBot {
			return true
		}
	}
	return false
}
