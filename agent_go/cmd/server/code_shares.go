package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
)

// Sharing a Code workspace (docs/design/code_product.md, "Sharing").
//
// A Code stays in its owner's tree. Its share list is server-only state in
// config/code-shares.json, never in the project folder: an editor's agent can
// write the project, so a list kept there could be edited into a promotion.
//
// Roles, each including the one before:
//   - viewer: read files and chat history; cannot run the agent.
//   - editor: run the agent in their own chat of the Code, which may change
//     its files.
//   - co-owner: also manage sharing.

type codeRole string

const (
	codeRoleNone    codeRole = ""
	codeRoleViewer  codeRole = "viewer"
	codeRoleEditor  codeRole = "editor"
	codeRoleCoOwner codeRole = "co_owner"
	codeRoleOwner   codeRole = "owner"
)

func (r codeRole) rank() int {
	switch r {
	case codeRoleViewer:
		return 1
	case codeRoleEditor:
		return 2
	case codeRoleCoOwner:
		return 3
	case codeRoleOwner:
		return 4
	}
	return 0
}

func (r codeRole) atLeast(other codeRole) bool { return r.rank() >= other.rank() }

func parseCodeShareRole(raw string) (codeRole, bool) {
	switch codeRole(strings.TrimSpace(raw)) {
	case codeRoleViewer:
		return codeRoleViewer, true
	case codeRoleEditor:
		return codeRoleEditor, true
	case codeRoleCoOwner:
		return codeRoleCoOwner, true
	}
	return codeRoleNone, false
}

// codeWorkflowAccess maps a Code role onto the platform's access levels:
// read turns are read-only, and a viewer has no turn at all.
func (r codeRole) workflowAccess() WorkflowAccessLevel {
	switch r {
	case codeRoleOwner, codeRoleCoOwner:
		return WorkflowAccessOwner
	case codeRoleEditor:
		return WorkflowAccessWrite
	case codeRoleViewer:
		return WorkflowAccessRead
	}
	return WorkflowAccessNone
}

// codeShareEntry is one shared Code: its owner, project id and grants keyed
// by the grantee's user-directory id.
type codeShareEntry struct {
	OwnerID   string              `json:"owner_id"`
	ProjectID string              `json:"project_id"`
	Grants    map[string]codeRole `json:"grants"`
}

type codeSharesDoc struct {
	Projects map[string]*codeShareEntry `json:"projects"`
}

func codeSharesFilePath() string { return "config/code-shares.json" }

func codeShareKey(ownerID, projectID string) string {
	return sanitizeUserIDForPath(strings.TrimSpace(ownerID)) + "/" + strings.TrimSpace(projectID)
}

type codeShareStore struct {
	mu   sync.Mutex
	load func(ctx context.Context) (codeSharesDoc, error)
	save func(ctx context.Context, doc codeSharesDoc) error
}

func (s *codeShareStore) read(ctx context.Context) (codeSharesDoc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(ctx)
}

func (s *codeShareStore) update(ctx context.Context, fn func(doc *codeSharesDoc) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.load(ctx)
	if err != nil {
		return err
	}
	if err := fn(&doc); err != nil {
		return err
	}
	return s.save(ctx, doc)
}

func loadCodeSharesDoc(ctx context.Context) (codeSharesDoc, error) {
	doc := codeSharesDoc{Projects: map[string]*codeShareEntry{}}
	data, exists, err := readFileFromWorkspace(ctx, codeSharesFilePath())
	if err != nil || !exists || strings.TrimSpace(data) == "" {
		return doc, err
	}
	var raw codeSharesDoc
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return doc, fmt.Errorf("code-shares.json: %w", err)
	}
	for key, entry := range raw.Projects {
		if entry == nil || strings.TrimSpace(entry.OwnerID) == "" || strings.TrimSpace(entry.ProjectID) == "" {
			continue
		}
		clean := &codeShareEntry{OwnerID: sanitizeUserIDForPath(entry.OwnerID), ProjectID: strings.TrimSpace(entry.ProjectID), Grants: map[string]codeRole{}}
		for user, role := range entry.Grants {
			if parsed, ok := parseCodeShareRole(string(role)); ok && strings.TrimSpace(user) != "" {
				clean.Grants[strings.TrimSpace(user)] = parsed
			}
		}
		if key == codeShareKey(clean.OwnerID, clean.ProjectID) {
			doc.Projects[key] = clean
		}
	}
	return doc, nil
}

func saveCodeSharesDoc(ctx context.Context, doc codeSharesDoc) error {
	for key, entry := range doc.Projects {
		if entry == nil || len(entry.Grants) == 0 {
			delete(doc.Projects, key)
		}
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, codeSharesFilePath(), string(encoded))
}

var codeShares = &codeShareStore{load: loadCodeSharesDoc, save: saveCodeSharesDoc}

// codeGranteeID is the user-directory id a grant is keyed by. Grants follow
// the account, not a username that can be renamed.
func codeGranteeID(callerID string) string {
	callerID = strings.TrimSpace(callerID)
	if record := directoryUserFor(callerID, callerID, callerID); record != nil {
		return record.ID
	}
	return callerID
}

// codeRoleFor is the caller's role on one owner's Code. A read failure of
// the share list fails closed for everyone but the owner.
func codeRoleFor(ctx context.Context, callerID, ownerID, projectID string) codeRole {
	if strings.TrimSpace(callerID) == "" || strings.TrimSpace(ownerID) == "" {
		return codeRoleNone
	}
	if sanitizeUserIDForPath(callerID) == sanitizeUserIDForPath(ownerID) {
		return codeRoleOwner
	}
	doc, err := codeShares.read(ctx)
	if err != nil {
		return codeRoleNone
	}
	entry := doc.Projects[codeShareKey(ownerID, projectID)]
	if entry == nil {
		return codeRoleNone
	}
	return entry.Grants[codeGranteeID(callerID)]
}

// codeShareOwnersFor lists the owners who shared the Code projectID with the
// caller. Resolution looks only here, never at every user's tree.
func codeShareOwnersFor(ctx context.Context, callerID, projectID string) []string {
	doc, err := codeShares.read(ctx)
	if err != nil {
		return nil
	}
	grantee := codeGranteeID(callerID)
	var owners []string
	for _, entry := range doc.Projects {
		if entry.ProjectID == strings.TrimSpace(projectID) && entry.Grants[grantee] != codeRoleNone {
			owners = append(owners, entry.OwnerID)
		}
	}
	sort.Strings(owners)
	return owners
}

// codeProjectsSharedWith lists every Code shared with the caller.
func codeProjectsSharedWith(ctx context.Context, callerID string) []codeShareEntry {
	doc, err := codeShares.read(ctx)
	if err != nil {
		return nil
	}
	grantee := codeGranteeID(callerID)
	var out []codeShareEntry
	for _, entry := range doc.Projects {
		if entry.Grants[grantee] != codeRoleNone && entry.OwnerID != sanitizeUserIDForPath(callerID) {
			out = append(out, *entry)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return codeShareKey(out[i].OwnerID, out[i].ProjectID) < codeShareKey(out[j].OwnerID, out[j].ProjectID)
	})
	return out
}

// ---- HTTP -----------------------------------------------------------------

type codeShareGrantView struct {
	UserID   string   `json:"user_id"`
	Username string   `json:"username,omitempty"`
	Role     codeRole `json:"role"`
}

type codeSharesResponse struct {
	OwnerID       string               `json:"owner_id"`
	OwnerUsername string               `json:"owner_username,omitempty"`
	Role          codeRole             `json:"role"`
	Grants        []codeShareGrantView `json:"grants"`
}

// resolveCodeProjectForShares finds the Code the caller addresses (their own,
// or one shared with them) and their role on it.
func (api *StreamingAPI) resolveCodeProjectForShares(r *http.Request) (ownerID, projectID string, role codeRole, ok bool) {
	claims := GetUserFromContext(r.Context())
	projectID = strings.TrimSpace(mux.Vars(r)["project_id"])
	if claims == nil || strings.TrimSpace(claims.UserID) == "" || projectID == "" || api == nil || api.agentProfiles == nil {
		return "", "", codeRoleNone, false
	}
	profile, err := api.agentProfiles.Resolve(codeproduct.ProfileID, 0, claims.UserID)
	if err != nil || !userAllowedProduct(claims, profile.Product) {
		return "", "", codeRoleNone, false
	}
	project, err := resolveCrewProjectBinding(r.Context(), claims.UserID, profile, projectID, "")
	if err != nil {
		return "", "", codeRoleNone, false
	}
	role = codeRoleFor(r.Context(), claims.UserID, project.OwnerID, projectID)
	return project.OwnerID, projectID, role, role != codeRoleNone
}

func codeSharesView(ctx context.Context, ownerID, projectID string, role codeRole) codeSharesResponse {
	response := codeSharesResponse{OwnerID: ownerID, OwnerUsername: crewOwnerDisplayName(ownerID), Role: role, Grants: []codeShareGrantView{}}
	doc, err := codeShares.read(ctx)
	if err != nil {
		return response
	}
	if entry := doc.Projects[codeShareKey(ownerID, projectID)]; entry != nil {
		for user, grant := range entry.Grants {
			view := codeShareGrantView{UserID: user, Role: grant}
			if record := directoryUserFor(user, user, user); record != nil {
				view.Username = record.Username
			}
			response.Grants = append(response.Grants, view)
		}
	}
	sort.Slice(response.Grants, func(i, j int) bool { return response.Grants[i].Username < response.Grants[j].Username })
	return response
}

// GET /api/agent-profiles/code/projects/{project_id}/shares — anyone with
// access sees who else has it; only the owner and co-owners may change it.
func (api *StreamingAPI) handleGetCodeShares(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ownerID, projectID, role, ok := api.resolveCodeProjectForShares(r)
	if !ok {
		writeAgentProfileError(w, http.StatusNotFound, "Code workspace not found")
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, codeSharesView(r.Context(), ownerID, projectID, role))
}

type codeSharesUpdate struct {
	Grants []struct {
		User string `json:"user"`
		Role string `json:"role"`
	} `json:"grants"`
}

// PUT /api/agent-profiles/code/projects/{project_id}/shares replaces the share
// list. Removing someone takes effect at once: their live chats of this Code
// are closed and every later turn, file read or link is refused.
func (api *StreamingAPI) handlePutCodeShares(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ownerID, projectID, role, ok := api.resolveCodeProjectForShares(r)
	if !ok {
		writeAgentProfileError(w, http.StatusNotFound, "Code workspace not found")
		return
	}
	if !role.atLeast(codeRoleCoOwner) {
		writeAgentProfileError(w, http.StatusForbidden, "Only the owner or a co-owner can change sharing")
		return
	}
	var body codeSharesUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid share list")
		return
	}
	grants := map[string]codeRole{}
	for _, grant := range body.Grants {
		parsed, valid := parseCodeShareRole(grant.Role)
		if !valid {
			writeAgentProfileError(w, http.StatusBadRequest, fmt.Sprintf("invalid role %q (want viewer, editor or co_owner)", grant.Role))
			return
		}
		record := directoryUserFor(grant.User, grant.User, grant.User)
		if record == nil || record.Disabled {
			writeAgentProfileError(w, http.StatusBadRequest, fmt.Sprintf("unknown user %q", grant.User))
			return
		}
		if sanitizeUserIDForPath(record.ID) == ownerID {
			continue // the owner is never a grantee
		}
		if !userAllowedProduct(&UserClaims{UserID: record.ID, Username: record.Username, Email: record.Email}, codeproduct.ProfileID) {
			writeAgentProfileError(w, http.StatusBadRequest, fmt.Sprintf("%s cannot use Code on this server", record.Username))
			return
		}
		grants[record.ID] = parsed
	}
	var removed []string
	err := codeShares.update(r.Context(), func(doc *codeSharesDoc) error {
		key := codeShareKey(ownerID, projectID)
		previous := doc.Projects[key]
		if previous != nil {
			for user, before := range previous.Grants {
				if after, kept := grants[user]; !kept || after.rank() < before.rank() {
					removed = append(removed, user)
				}
			}
		}
		doc.Projects[key] = &codeShareEntry{OwnerID: ownerID, ProjectID: projectID, Grants: grants}
		return nil
	})
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "could not save sharing")
		return
	}
	for _, user := range removed {
		closeCodeSessionsForGrantee(r.Context(), user, projectID)
	}
	// People removed entirely (not just demoted) drop their personal MCP
	// switches for this Code.
	if codeRoot := api.codeRootForOwner(r.Context(), ownerID, projectID); codeRoot != "" {
		for _, user := range removed {
			if _, kept := grants[user]; !kept {
				if err := forgetPersonalMCPCode(user, codeRoot); err != nil {
					log.Printf("[PERSONAL_MCP] forget unshared Code for %s: %v", user, err)
				}
			}
		}
	}
	writeAgentProfileJSON(w, http.StatusOK, codeSharesView(r.Context(), ownerID, projectID, role))
}

// closeCodeSessionsForGrantee ends a removed or demoted grantee's live chats
// of one Code, so a running CLI keeps no access its next turn would not get.
func closeCodeSessionsForGrantee(ctx context.Context, userID, projectID string) {
	sessions, err := defaultProductConversationRegistryStore().liveSessionIDsMatching(ctx, userID, codeproduct.ProfileID, func(record ProductConversationRecord) bool {
		return strings.TrimSpace(record.ResourceID) == projectID || strings.HasPrefix(strings.TrimSpace(record.ConversationKey), projectID)
	})
	if err != nil {
		return
	}
	for sessionID := range sessions {
		closeAllCodingCLIInteractiveSessionsForOwner(sessionID, "code access changed")
	}
}

// codeRootForOwner is the physical root of an owner's Code, or "".
func (api *StreamingAPI) codeRootForOwner(ctx context.Context, ownerID, projectID string) string {
	if api == nil || api.agentProfiles == nil {
		return ""
	}
	profile, err := api.agentProfiles.Resolve(codeproduct.ProfileID, 0, ownerID)
	if err != nil {
		return ""
	}
	binding, err := resolveProductProjectBindingWithStore(ctx, ownerID, profile, projectID, defaultProductProjectStore())
	if err != nil {
		return ""
	}
	return cleanCodeRoot(agentProfileRuntimeWorkspace(ownerID, binding.WorkspacePath))
}
