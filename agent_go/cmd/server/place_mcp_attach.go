package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	workshop "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// The server-owned attachment index links a user's private MCP to a project.
// Only that owner can see or run it, even when the project is shared. New
// connections reuse the user's private store across projects; legacy per-place
// stores remain sealed at their original paths. Shared connections live in Vault.

const placeMCPAttachmentsFile = "attachments.json"

type placeMCPAttachment struct {
	Owner      string `json:"owner"`
	Scope      string `json:"scope,omitempty"`
	Server     string `json:"server"`
	AttachedAt string `json:"attached_at,omitempty"`
}

// New connections reuse the owner's private store across projects. Existing
// place stores keep their paths (encrypted files use path-bound AAD).
func attachmentStore(a placeMCPAttachment, root string) string {
	if a.Scope == "user" {
		return a.Owner
	}
	return placeMCPStoreID(a.Owner, root)
}
func privateStoreForAttachment(owner, server, root string) string {
	items, _ := placeMCPAttachmentsFor(root)
	for _, a := range items {
		if a.Owner == owner && a.Server == server {
			return attachmentStore(a, root)
		}
	}
	return owner
}
func recordPrivateMCP(owner, server, root string) error {
	placeMCPMu.Lock()
	defer placeMCPMu.Unlock()
	all, err := readPlaceMCPAttachmentsLocked()
	if err != nil {
		return err
	}
	for _, a := range all[root] {
		if a.Owner == owner && a.Server == server {
			return nil
		}
	}
	all[root] = append(all[root], placeMCPAttachment{Owner: owner, Server: server, Scope: "user", AttachedAt: time.Now().UTC().Format(time.RFC3339)})
	return writePlaceMCPAttachmentsLocked(all)
}

// placeMCPStoreID is the personal-store id of owner's connections in root.
func placeMCPStoreID(owner, root string) string {
	return "place:" + owner + ":" + root
}

func placeMCPAttachmentsPath() (string, error) {
	root, err := mcpConnectionsRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, placeMCPAttachmentsFile), nil
}

// cleanAttachRoot normalizes a workflow, Crew or Code root. It returns "" for
// any path that is not one.
func cleanAttachRoot(raw string) string {
	root := strings.Trim(strings.TrimSpace(raw), "/")
	if root == "" || strings.ContainsAny(root, "\\\x00") || path.Clean(root) != root {
		return ""
	}
	segments := strings.Split(root, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return ""
		}
	}
	if ref := workspaceref.MustParse(root); ref.HasOwner() {
		// Only a project root itself: _users/<owner>/Chats/(Work|Code)/projects/<id>.
		projectRoot, project, ok := ref.Project()
		if ok && ref.Logical() == projectRoot+"/"+project && ref.PhysicalKeepOwner("") == root {
			// A Crew that has moved is its Crew/<folder> root (PLAT-442 step 4): connections attached under the old
			// root are the same Crew's.
			if projectRoot == workspaceref.CrewProjectsRoot {
				if moved := crewPathAliases.lookup(context.Background(), root); moved != "" {
					return moved
				}
			}
			return root
		}
		return ""
	}
	switch {
	case len(segments) == 2 && segments[0] == "Workflow" && !strings.HasPrefix(segments[1], "."):
		return root
	case len(segments) == 2 && segments[0] == crewSharedRootName:
		return root
	}
	return ""
}

// isCodePlaceRoot reports whether a cleaned place root is a Code.
func isCodePlaceRoot(root string) bool {
	projectRoot, _, ok := workspaceref.MustParse(root).Project()
	return ok && projectRoot == workspaceref.CodeProjectsRoot && workspaceref.MustParse(root).HasOwner()
}

// placeRootOf returns the workflow, Crew or Code root a path lies in (a run
// folder or file inside it included), or "".
func placeRootOf(raw string) string {
	segments := strings.Split(strings.Trim(strings.TrimSpace(raw), "/"), "/")
	switch {
	case len(segments) >= 2 && (segments[0] == "Workflow" || segments[0] == crewSharedRootName):
		return cleanAttachRoot(strings.Join(segments[:2], "/"))
	}
	if ref := workspaceref.MustParse(raw); ref.HasOwner() {
		if projectRoot, project, ok := ref.Project(); ok {
			return cleanAttachRoot(ref.WithLogical(projectRoot + "/" + project).PhysicalKeepOwner(""))
		}
	}
	return ""
}

// attachRootForCaller maps the workspace path the UI sends to the stored
// root: a caller's own Crew or Code arrives in its logical form
// (Chats/Work/projects/<id>, Chats/Code/projects/<id>), which is their
// _users tree.
func attachRootForCaller(userID, raw string) string {
	raw = strings.Trim(strings.TrimSpace(raw), "/")
	if ref := workspaceref.MustParse(raw); !ref.HasOwner() && ref.IsProject() && ref.Logical() == raw {
		raw = ref.Physical(userID)
	}
	return cleanAttachRoot(raw)
}

func readPlaceMCPAttachmentsLocked() (map[string][]placeMCPAttachment, error) {
	file, err := placeMCPAttachmentsPath()
	if err != nil {
		return nil, err
	}
	all := map[string][]placeMCPAttachment{}
	if err := readPlaceMCPJSON(file, &all); err != nil {
		return nil, err
	}
	// Entries recorded under a migrated Crew's old root belong to its Crew/<folder> root (merged in memory; the
	// next write stores them under the new key).
	for key, list := range all {
		if folded := cleanAttachRoot(key); folded != "" && folded != key {
			all[folded] = append(all[folded], list...)
			delete(all, key)
		}
	}
	return all, nil
}

func writePlaceMCPAttachmentsLocked(all map[string][]placeMCPAttachment) error {
	file, err := placeMCPAttachmentsPath()
	if err != nil {
		return err
	}
	return writePlaceMCPJSON(file, all)
}

// userClaimsForDirectoryID builds the claims access checks need (access
// lists name usernames and emails) for a user who is not the caller. A
// disabled account has none.
func userClaimsForDirectoryID(userID string) *UserClaims {
	claims := &UserClaims{UserID: userID, Username: userID}
	if dir, err := loadUserDirectory(); err == nil && dir != nil {
		if rec := dir.byID(userID); rec != nil {
			if rec.Disabled {
				return nil
			}
			claims.Username, claims.Email = rec.Username, rec.Email
		}
	}
	return claims
}

// placeMCPCanAttach reports whether userID may add connections to root:
// they must be able to edit that workflow, or own that Crew or Code. It is
// checked on add and again on every use, so losing edit access stops their
// connection there at once.
func placeMCPCanAttach(ctx context.Context, userID, root string) bool {
	root = cleanAttachRoot(root)
	if root == "" || strings.TrimSpace(userID) == "" {
		return false
	}
	claims := userClaimsForDirectoryID(userID)
	if claims == nil {
		return false
	}
	if strings.HasPrefix(root, "Workflow/") {
		level, _ := workflowAccessForWorkspacePath(ctx, claims, root)
		return level == WorkflowAccessOwner || level == WorkflowAccessWrite
	}
	if isCodePlaceRoot(root) {
		// A Code lives in its owner's tree; only the owner connects there.
		return workspaceref.MustParse(root).OwnedBy(userID)
	}
	ref, ok := resolveCrewPath(ctx, userID, root)
	return ok && crewAccessFor(claims, ref) == crewAccessOwner
}

// recordPlaceMCP adds owner's server to root's index.
func recordPlaceMCP(owner, server, root string) error {
	placeMCPMu.Lock()
	defer placeMCPMu.Unlock()
	all, err := readPlaceMCPAttachmentsLocked()
	if err != nil {
		return err
	}
	for _, a := range all[root] {
		if a.Owner == owner && a.Server == server {
			return nil
		}
	}
	all[root] = append(all[root], placeMCPAttachment{Owner: owner, Server: server, AttachedAt: time.Now().UTC().Format(time.RFC3339)})
	return writePlaceMCPAttachmentsLocked(all)
}

// forgetPlaceMCP drops owner's server from root's index.
func forgetPlaceMCP(owner, server, root string) error {
	placeMCPMu.Lock()
	defer placeMCPMu.Unlock()
	all, err := readPlaceMCPAttachmentsLocked()
	if err != nil {
		return err
	}
	kept := all[root][:0]
	for _, a := range all[root] {
		if a.Owner == owner && a.Server == server {
			continue
		}
		kept = append(kept, a)
	}
	if len(kept) == 0 {
		delete(all, root)
	} else {
		all[root] = kept
	}
	return writePlaceMCPAttachmentsLocked(all)
}

// placeMCPAttachmentsFor lists root's connections, sorted by name.
func placeMCPAttachmentsFor(root string) ([]placeMCPAttachment, error) {
	root = cleanAttachRoot(root)
	if root == "" {
		return nil, nil
	}
	placeMCPMu.Lock()
	all, err := readPlaceMCPAttachmentsLocked()
	placeMCPMu.Unlock()
	if err != nil {
		return nil, err
	}
	list := append([]placeMCPAttachment(nil), all[root]...)
	sort.Slice(list, func(i, j int) bool {
		if list[i].Server != list[j].Server {
			return list[i].Server < list[j].Server
		}
		return list[i].Owner < list[j].Owner
	})
	return list, nil
}

// attachedMCPServersForRoot is the runtime side: a workflow's, Crew's or Code's own
// connections as ordinary server names plus complete configs, for any chat or
// run there. A connection whose owner can no longer edit root is skipped.
func attachedMCPServersForRoot(ctx context.Context, root string) ([]string, mcpclient.RuntimeOverrides) {
	root = placeRootOf(root)
	attachments, err := placeMCPAttachmentsFor(root)
	if err != nil {
		log.Printf("[PLACE_MCP] connections of %s: %v", root, err)
		return nil, nil
	}
	if len(attachments) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(attachments))
	overrides := mcpclient.RuntimeOverrides{}
	for _, a := range attachments {
		if a.Owner != mcpCaller(ctx) {
			continue
		}
		if !placeMCPCanAttach(ctx, a.Owner, root) {
			log.Printf("[PLACE_MCP] skipping %s in %s: the person who added it can no longer edit it", a.Server, root)
			continue
		}
		internal, cfg, err := placeMCPServerConfig(attachmentStore(a, root), a.Server)
		if err != nil {
			log.Printf("[PLACE_MCP] skipping %s in %s: %v", a.Server, root, err)
			continue
		}
		config := cfg
		names = append(names, internal)
		overrides[internal] = mcpclient.RuntimeConfigOverride{Server: &config}
	}
	return names, overrides
}

// placeMCPSignedInInternalNames is the set of a place's attached connections (by internal
// name) whose sign-in is done, judged the way the connections list shows "connected".
func placeMCPSignedInInternalNames(ctx context.Context, root string) map[string]bool {
	out := map[string]bool{}
	root = placeRootOf(root)
	attachments, err := placeMCPAttachmentsFor(root)
	if err != nil {
		return out
	}
	for _, a := range attachments {
		if a.Owner != mcpCaller(ctx) {
			continue
		}
		if !placeMCPCanAttach(ctx, a.Owner, root) {
			continue
		}
		store := attachmentStore(a, root)
		internal, _, err := placeMCPServerConfig(store, a.Server)
		if err != nil {
			continue
		}
		servers, _ := listPlaceMCPServers(store)
		dir, _ := placeMCPDir(store)
		for _, server := range servers {
			if server.Name == a.Server {
				out[internal] = placeMCPServerConnected(dir, store, server)
			}
		}
	}
	return out
}

// placeMCPRoot reads and checks the workspace_path of a place route.
func placeMCPRoot(w http.ResponseWriter, userID, raw string) (string, bool) {
	root := attachRootForCaller(userID, raw)
	if root == "" {
		writeAgentProfileError(w, http.StatusBadRequest, "workspace_path must be a workflow, a Crew or a Code")
		return "", false
	}
	return root, true
}

// GET /api/mcp/place?workspace_path=: only the caller's project connections,
// after project read authorization (never tokens or secrets).
func (api *StreamingAPI) handleListPlaceMCP(w http.ResponseWriter, r *http.Request) {
	userID, ok := placeMCPUser(w, r)
	if !ok {
		return
	}
	root, ok := placeMCPRoot(w, userID, r.URL.Query().Get("workspace_path"))
	if !ok {
		return
	}
	if !workspaceReadAllowed(r.Context(), GetUserFromContext(r.Context()), root, logicalPathIsCaller, true) {
		writeAgentProfileError(w, http.StatusForbidden, "no access to this workflow")
		return
	}
	attachments, err := placeMCPAttachmentsFor(root)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type row struct {
		Name      string `json:"name"`
		Label     string `json:"label,omitempty"`
		Catalog   string `json:"catalog,omitempty"`
		URL       string `json:"url"`
		Owner     string `json:"owner"`
		OwnerName string `json:"owner_name"`
		Mine      bool   `json:"mine"`
		Connected bool   `json:"connected"`
		SignIn    bool   `json:"sign_in"`
		// Active is false once the person who added it can no longer edit
		// this place: it is then skipped at runtime.
		Active  bool   `json:"active"`
		AddedAt string `json:"added_at,omitempty"`
	}
	dir, _ := loadUserDirectory()
	rows := make([]row, 0, len(attachments))
	for _, a := range attachments {
		if a.Owner != userID {
			continue
		}
		store := attachmentStore(a, root)
		servers, _ := listPlaceMCPServers(store)
		var server *placeMCPServer
		for i := range servers {
			if servers[i].Name == a.Server {
				server = &servers[i]
			}
		}
		if server == nil {
			continue
		}
		storeDir, _ := placeMCPDir(store)
		connected := placeMCPServerConnected(storeDir, store, *server)
		ownerName := a.Owner
		if dir != nil {
			if rec := dir.byID(a.Owner); rec != nil && rec.Username != "" {
				ownerName = rec.Username
			}
		}
		rows = append(rows, row{
			Name: a.Server, Label: server.Label, Catalog: server.Catalog, URL: redactedURL(server.URL),
			Owner: a.Owner, OwnerName: ownerName, Mine: a.Owner == userID, Connected: connected, SignIn: server.OAuth != nil,
			Active: placeMCPCanAttach(r.Context(), a.Owner, root), AddedAt: a.AttachedAt,
		})
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"servers": rows})
}

// POST /api/mcp/place {workspace_path, catalog | name+url+transport}: add a
// connection with the caller's own login to a workflow or Crew they can edit.
func (api *StreamingAPI) handleAddPlaceMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, ok := placeMCPUser(w, r)
	if !ok {
		return
	}
	var request struct {
		placeMCPServer
		Catalog       string `json:"catalog"`
		WorkspacePath string `json:"workspace_path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid request")
		return
	}
	root, ok := placeMCPRoot(w, userID, request.WorkspacePath)
	if !ok {
		return
	}
	if !placeMCPCanAttach(r.Context(), userID, root) {
		writeAgentProfileError(w, http.StatusForbidden, "you can add connections only where you can edit")
		return
	}
	// Credential headers are built from the adder's own personal secrets
	// (Setup > Secrets), resolved when the connection is made.
	store := userID
	if strings.TrimSpace(request.Label) == "" {
		store = privateStoreForAttachment(userID, request.Name, root)
	}
	saved, status, err := api.ensurePrivateMCP(r.Context(), store, request.placeMCPServer, request.Catalog)
	if err != nil {
		writeAgentProfileError(w, status, err.Error())
		return
	}
	if err := recordPrivateMCP(userID, saved.Name, root); err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("[PLACE_MCP] %s added %s to %s", userID, saved.Name, root)
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"name": saved.Name, "label": saved.Label, "oauth": saved.OAuth != nil})
}

// POST /api/mcp/place/{name}/connect?workspace_path= {client_id?}: sign in to
// the caller's own connection in this place.
func (api *StreamingAPI) handleConnectPlaceMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, ok := placeMCPUser(w, r)
	if !ok {
		return
	}
	root, ok := placeMCPRoot(w, userID, r.URL.Query().Get("workspace_path"))
	if !ok {
		return
	}
	if !placeMCPCanAttach(r.Context(), userID, root) {
		writeAgentProfileError(w, http.StatusForbidden, "you can connect only where you can edit")
		return
	}
	var body struct {
		SessionID    string `json:"session_id,omitempty"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body)
	sessionID, sessionErr := api.oauthNotificationSession(r, body.SessionID)
	if sessionErr != nil {
		writeAgentProfileError(w, http.StatusForbidden, "chat session not found or access denied")
		return
	}
	var entered *registeredClient
	if clientID := strings.TrimSpace(body.ClientID); clientID != "" {
		entered = &registeredClient{ClientID: clientID, ClientSecret: strings.TrimSpace(body.ClientSecret)}
	}
	authURL, discovery, status, err := api.startPlaceMCPSignIn(privateStoreForAttachment(userID, mux.Vars(r)["name"], root), mux.Vars(r)["name"], deriveOAuthRedirectURI(r), sessionID, entered)
	if err != nil {
		writeAgentProfileError(w, status, err.Error())
		return
	}
	if discovery != nil {
		writeAgentProfileJSON(w, http.StatusOK, discovery)
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"auth_url": authURL})
}

// DELETE /api/mcp/place/{name}?workspace_path=&owner=: remove a connection
// from a workflow or Crew. Only its owner may detach it. New private logins
// are retained for that owner's other projects.
func (api *StreamingAPI) handleRemovePlaceMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, ok := placeMCPUser(w, r)
	if !ok {
		return
	}
	root, ok := placeMCPRoot(w, userID, r.URL.Query().Get("workspace_path"))
	if !ok {
		return
	}
	owner := strings.TrimSpace(r.URL.Query().Get("owner"))
	if owner == "" {
		owner = userID
	}
	if owner != userID {
		writeAgentProfileError(w, http.StatusForbidden, "only the owner can remove a private connection")
		return
	}
	name := mux.Vars(r)["name"]
	if err := removePlaceMCP(owner, name, root); err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("[PLACE_MCP] %s removed %s (added by %s) from %s", userID, name, owner, root)
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"removed": name})
}

// removePlaceMCP removes owner's connection from root with its login.
func removePlaceMCP(owner, name, root string) error {
	store := privateStoreForAttachment(owner, name, root)
	if err := forgetPlaceMCP(owner, name, root); err != nil {
		return err
	}
	if store == owner {
		return nil
	} // Detach here; keep the private account for other projects.
	if err := removePlaceMCPServer(store, name); err != nil {
		log.Printf("[PLACE_MCP] remove %s from %s: %v", name, root, err)
	}
	closePlaceMCPConnection(store, name)
	_ = forgetPlaceMCPLogin(store, name)
	return nil
}

// forgetPlaceConnections removes every connection of a deleted workflow, Crew
// or Code, with their logins.
func forgetPlaceConnections(root string) {
	root = cleanAttachRoot(root)
	if root == "" {
		return
	}
	attachments, err := placeMCPAttachmentsFor(root)
	if err != nil {
		log.Printf("[PLACE_MCP] connections of deleted %s: %v", root, err)
		return
	}
	for _, a := range attachments {
		if err := removePlaceMCP(a.Owner, a.Server, root); err != nil {
			log.Printf("[PLACE_MCP] remove %s from deleted %s: %v", a.Server, root, err)
		}
	}
}

// errPlaceMCPUnavailable is returned by the bridge for a personal-shaped
// server name that is not one of this place's connections.
var errPlaceMCPUnavailable = fmt.Errorf("this MCP connection is not available here")

func init() {
	workshop.PlaceMCPServers = attachedMCPServersForRoot
}

// projectSecretValue reads one secret of a workflow, Crew or Code from the
// shared project secret store (the same store Setup > Secrets writes).
func (api *StreamingAPI) projectSecretValue(root, name string) (string, error) {
	if api.chatStore == nil {
		return "", fmt.Errorf("project secrets are unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	secrets, err := api.chatStore.ListWorkflowSecrets(ctx, chathistory.SharedWorkflowSecretsUserID, root)
	if err != nil {
		return "", err
	}
	for _, secret := range secrets {
		if secret.Name == name {
			return decryptSharedWorkflowSecret(root, secret)
		}
	}
	return "", fmt.Errorf("this connection needs the secret %q; add it under Setup > Secrets", name)
}
