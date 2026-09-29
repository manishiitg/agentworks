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
	workshop "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
)

// Place connections (docs/design/personal_mcp_attach.md): someone who can
// edit a workflow or Crew adds an MCP server there with their own login (their
// Gmail, Drive, GitHub, ...). It then works there like any other MCP server:
// every chat and run of that workflow or Crew uses it, including schedules,
// triggers, calls and Slack channels. The person accepts that when adding it.
// It belongs to that one place: it never shows in their Code or anywhere else.
//
// Storage reuses the personal store (catalog add, sign-in apps, sealed
// tokens) under a store id per (person, place), so a Crew's Gmail login is
// never the same as the person's Code Gmail. The server-owned index says what
// each place has; nothing in the editable workflow.json can add one:
//
//	<state root>/personal-mcp/attachments.json
//	  { "<workspace root>": [ { "owner": "<user id>", "server": "gmail", ... } ] }

const personalMCPAttachmentsFile = "attachments.json"

type personalMCPAttachment struct {
	Owner      string `json:"owner"`
	Server     string `json:"server"`
	AttachedAt string `json:"attached_at,omitempty"`
}

// placeMCPStoreID is the personal-store id of owner's connections in root.
func placeMCPStoreID(owner, root string) string {
	return "place:" + owner + ":" + root
}

func personalMCPAttachmentsPath() (string, error) {
	root, err := personalMCPRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, personalMCPAttachmentsFile), nil
}

// cleanAttachRoot normalizes a workflow or Crew root. It returns "" for any
// path that is not one: a Code keeps its per-person servers instead.
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
	switch {
	case len(segments) == 2 && segments[0] == "Workflow" && !strings.HasPrefix(segments[1], "."):
		return root
	case len(segments) == 2 && segments[0] == crewSharedRootName:
		return root
	case len(segments) == 6 && segments[0] == "_users" && segments[2] == "Chats" && segments[3] == "Work" && segments[4] == "projects":
		return root
	}
	return ""
}

// placeRootOf returns the workflow or Crew root a path lies in (a run
// folder or file inside it included), or "".
func placeRootOf(raw string) string {
	segments := strings.Split(strings.Trim(strings.TrimSpace(raw), "/"), "/")
	switch {
	case len(segments) >= 2 && (segments[0] == "Workflow" || segments[0] == crewSharedRootName):
		return cleanAttachRoot(strings.Join(segments[:2], "/"))
	case len(segments) >= 6 && segments[0] == "_users":
		return cleanAttachRoot(strings.Join(segments[:6], "/"))
	}
	return ""
}

// attachRootForCaller maps the workspace path the UI sends to the stored
// root: a caller's own Crew arrives in its logical form
// (Chats/Work/projects/<id>), which is their _users tree.
func attachRootForCaller(userID, raw string) string {
	raw = strings.Trim(strings.TrimSpace(raw), "/")
	if strings.HasPrefix(raw, "Chats/Work/projects/") {
		raw = "_users/" + sanitizeUserIDForPath(userID) + "/" + raw
	}
	return cleanAttachRoot(raw)
}

func readPersonalMCPAttachmentsLocked() (map[string][]personalMCPAttachment, error) {
	file, err := personalMCPAttachmentsPath()
	if err != nil {
		return nil, err
	}
	all := map[string][]personalMCPAttachment{}
	if err := readPersonalMCPJSON(file, &all); err != nil {
		return nil, err
	}
	return all, nil
}

func writePersonalMCPAttachmentsLocked(all map[string][]personalMCPAttachment) error {
	file, err := personalMCPAttachmentsPath()
	if err != nil {
		return err
	}
	return writePersonalMCPJSON(file, all)
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

// personalMCPCanAttach reports whether userID may add connections to root:
// they must be able to edit that workflow or own that Crew. It is checked on
// add and again on every use, so losing edit access stops their connection
// there at once.
func personalMCPCanAttach(ctx context.Context, userID, root string) bool {
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
	ref, ok := resolveCrewPath(ctx, userID, root)
	return ok && crewAccessFor(claims, ref) == crewAccessOwner
}

// recordPlaceMCP adds owner's server to root's index.
func recordPlaceMCP(owner, server, root string) error {
	personalMCPMu.Lock()
	defer personalMCPMu.Unlock()
	all, err := readPersonalMCPAttachmentsLocked()
	if err != nil {
		return err
	}
	for _, a := range all[root] {
		if a.Owner == owner && a.Server == server {
			return nil
		}
	}
	all[root] = append(all[root], personalMCPAttachment{Owner: owner, Server: server, AttachedAt: time.Now().UTC().Format(time.RFC3339)})
	return writePersonalMCPAttachmentsLocked(all)
}

// forgetPlaceMCP drops owner's server from root's index.
func forgetPlaceMCP(owner, server, root string) error {
	personalMCPMu.Lock()
	defer personalMCPMu.Unlock()
	all, err := readPersonalMCPAttachmentsLocked()
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
	return writePersonalMCPAttachmentsLocked(all)
}

// personalMCPAttachmentsFor lists root's connections, sorted by name.
func personalMCPAttachmentsFor(root string) ([]personalMCPAttachment, error) {
	root = cleanAttachRoot(root)
	if root == "" {
		return nil, nil
	}
	personalMCPMu.Lock()
	all, err := readPersonalMCPAttachmentsLocked()
	personalMCPMu.Unlock()
	if err != nil {
		return nil, err
	}
	list := append([]personalMCPAttachment(nil), all[root]...)
	sort.Slice(list, func(i, j int) bool {
		if list[i].Server != list[j].Server {
			return list[i].Server < list[j].Server
		}
		return list[i].Owner < list[j].Owner
	})
	return list, nil
}

// attachedMCPServersForRoot is the runtime side: a workflow's or Crew's own
// connections as ordinary server names plus complete configs, for any chat or
// run there. A connection whose owner can no longer edit root is skipped.
func attachedMCPServersForRoot(ctx context.Context, root string) ([]string, mcpclient.RuntimeOverrides) {
	root = placeRootOf(root)
	attachments, err := personalMCPAttachmentsFor(root)
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
		if !personalMCPCanAttach(ctx, a.Owner, root) {
			log.Printf("[PLACE_MCP] skipping %s in %s: the person who added it can no longer edit it", a.Server, root)
			continue
		}
		internal, cfg, err := personalMCPServerConfig(placeMCPStoreID(a.Owner, root), a.Server)
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

// placeMCPRoot reads and checks the workspace_path of a place route.
func placeMCPRoot(w http.ResponseWriter, userID, raw string) (string, bool) {
	root := attachRootForCaller(userID, raw)
	if root == "" {
		writeAgentProfileError(w, http.StatusBadRequest, "workspace_path must be a workflow or a Crew")
		return "", false
	}
	return root, true
}

// GET /api/mcp/place?workspace_path=: a workflow's or Crew's own connections,
// for anyone who can read it (never tokens or secrets).
func (api *StreamingAPI) handleListPlaceMCP(w http.ResponseWriter, r *http.Request) {
	userID, ok := personalMCPUser(w, r)
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
	attachments, err := personalMCPAttachmentsFor(root)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type row struct {
		Name      string `json:"name"`
		Catalog   string `json:"catalog,omitempty"`
		URL       string `json:"url"`
		Owner     string `json:"owner"`
		OwnerName string `json:"owner_name"`
		Mine      bool   `json:"mine"`
		Connected bool   `json:"connected"`
		// Active is false once the person who added it can no longer edit
		// this place: it is then skipped at runtime.
		Active  bool   `json:"active"`
		AddedAt string `json:"added_at,omitempty"`
	}
	dir, _ := loadUserDirectory()
	rows := make([]row, 0, len(attachments))
	for _, a := range attachments {
		store := placeMCPStoreID(a.Owner, root)
		servers, _ := listPersonalMCPServers(store)
		var server *personalMCPServer
		for i := range servers {
			if servers[i].Name == a.Server {
				server = &servers[i]
			}
		}
		if server == nil {
			continue
		}
		connected := true
		if server.OAuth != nil {
			storeDir, _ := personalMCPDir(store)
			_, loadErr := oauth.NewTokenStore(personalMCPTokenFile(storeDir, store, server.Name)).Load()
			connected = loadErr == nil
		}
		ownerName := a.Owner
		if dir != nil {
			if rec := dir.byID(a.Owner); rec != nil && rec.Username != "" {
				ownerName = rec.Username
			}
		}
		rows = append(rows, row{
			Name: a.Server, Catalog: server.Catalog, URL: redactedURL(server.URL),
			Owner: a.Owner, OwnerName: ownerName, Mine: a.Owner == userID, Connected: connected,
			Active: personalMCPCanAttach(r.Context(), a.Owner, root), AddedAt: a.AttachedAt,
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
	userID, ok := personalMCPUser(w, r)
	if !ok {
		return
	}
	var request struct {
		personalMCPServer
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
	if !personalMCPCanAttach(r.Context(), userID, root) {
		writeAgentProfileError(w, http.StatusForbidden, "you can add connections only where you can edit")
		return
	}
	// Credential headers come from personal secrets, which a place has no
	// UI for yet: sign-in (OAuth) and open servers only.
	if len(request.Headers) > 0 {
		writeAgentProfileError(w, http.StatusBadRequest, "servers with API-key headers can't be added here yet; use one with sign-in")
		return
	}
	store := placeMCPStoreID(userID, root)
	saved, status, err := api.addPersonalMCP(r.Context(), store, request.personalMCPServer, request.Catalog)
	if err != nil {
		writeAgentProfileError(w, status, err.Error())
		return
	}
	if err := recordPlaceMCP(userID, saved.Name, root); err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("[PLACE_MCP] %s added %s to %s", userID, saved.Name, root)
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"name": saved.Name, "oauth": saved.OAuth != nil})
}

// POST /api/mcp/place/{name}/connect?workspace_path= {client_id?}: sign in to
// the caller's own connection in this place.
func (api *StreamingAPI) handleConnectPlaceMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, ok := personalMCPUser(w, r)
	if !ok {
		return
	}
	root, ok := placeMCPRoot(w, userID, r.URL.Query().Get("workspace_path"))
	if !ok {
		return
	}
	if !personalMCPCanAttach(r.Context(), userID, root) {
		writeAgentProfileError(w, http.StatusForbidden, "you can connect only where you can edit")
		return
	}
	var body struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body)
	var entered *registeredClient
	if clientID := strings.TrimSpace(body.ClientID); clientID != "" {
		entered = &registeredClient{ClientID: clientID, ClientSecret: strings.TrimSpace(body.ClientSecret)}
	}
	authURL, discovery, status, err := api.startPersonalMCPSignIn(placeMCPStoreID(userID, root), mux.Vars(r)["name"], deriveOAuthRedirectURI(r), entered)
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
// from a workflow or Crew, with its login. The person who added it may always
// remove it; anyone else who can edit the place may remove it too.
func (api *StreamingAPI) handleRemovePlaceMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, ok := personalMCPUser(w, r)
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
	if owner != userID && !personalMCPCanAttach(r.Context(), userID, root) {
		writeAgentProfileError(w, http.StatusForbidden, "only the person who added it, or someone who can edit here, can remove it")
		return
	}
	name := mux.Vars(r)["name"]
	store := placeMCPStoreID(owner, root)
	if err := forgetPlaceMCP(owner, name, root); err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := removePersonalMCPServer(store, name); err != nil {
		log.Printf("[PLACE_MCP] remove %s from %s: %v", name, root, err)
	}
	closePersonalMCPConnection(store, name)
	_ = forgetPersonalMCPLogin(store, name)
	log.Printf("[PLACE_MCP] %s removed %s (added by %s) from %s", userID, name, owner, root)
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"removed": name})
}

// errPlaceMCPUnavailable is returned by the bridge for a personal-shaped
// server name that is not one of this place's connections.
var errPlaceMCPUnavailable = fmt.Errorf("this MCP connection is not available here")

func init() {
	workshop.PlaceMCPServers = attachedMCPServersForRoot
}
