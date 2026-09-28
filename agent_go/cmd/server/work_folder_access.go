package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// Per-user external folder access for the Work product.
//
// Ordinary users may attach folders only inside their server-side allowed
// roots, which administrators manage. Administrators may attach anywhere.
// Grants reuse the shared folder-grant shape (alias, absolute path,
// access level); assignments and grants live in one workspace file:
//
//	{
//	  "assignments": { "manish": { "roots": ["/Users/manish/projects"] } },
//	  "grants": { "manish": [
//	    {"id": "...", "alias": "site", "path": "/Users/manish/projects/site", "access": "read_write"}
//	  ] }
//	}
//
// Keys are normalized (lowercase, trimmed) and matched against user ID,
// username, or email, same as user-product-access.json. config/ is outside
// every folder guard, so a session can never read or rewrite its own
// grants file. Every read-modify-write runs inside workFolderStore.mu so
// concurrent tabs or users cannot silently lose assignments.

// workFolderAssignment is the administrator-managed attach scope for one user.
type workFolderAssignment struct {
	Roots []string `json:"roots"`
}

// workFolderAccessDoc is the full config/work-folder-access.json document.
type workFolderAccessDoc struct {
	Assignments map[string]*workFolderAssignment               `json:"assignments"`
	Grants      map[string][]workflowtypes.WorkflowFolderGrant `json:"grants"`
}

func workFolderAccessFilePath() string {
	return "config/work-folder-access.json"
}

// workFolderStore serializes read-modify-write transactions over the
// grants document. The zero value is unusable; use workFolderAccessStore.
type workFolderStore struct {
	mu   sync.Mutex
	load func(ctx context.Context) (workFolderAccessDoc, error)
	save func(ctx context.Context, doc workFolderAccessDoc) error
}

func (s *workFolderStore) read(ctx context.Context) (workFolderAccessDoc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(ctx)
}

func (s *workFolderStore) update(ctx context.Context, fn func(doc *workFolderAccessDoc) error) error {
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

func loadWorkFolderAccessDoc(ctx context.Context) (workFolderAccessDoc, error) {
	doc := workFolderAccessDoc{Assignments: map[string]*workFolderAssignment{}, Grants: map[string][]workflowtypes.WorkflowFolderGrant{}}
	data, exists, err := readFileFromWorkspace(ctx, workFolderAccessFilePath())
	if err != nil {
		return doc, err
	}
	if !exists {
		return doc, nil
	}
	var raw workFolderAccessDoc
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return doc, err
	}
	for k, v := range raw.Assignments {
		if key := normalizeWorkflowPermissionKey(k); key != "" && v != nil {
			doc.Assignments[key] = v
		}
	}
	for k, v := range raw.Grants {
		if key := normalizeWorkflowPermissionKey(k); key != "" {
			doc.Grants[key] = v
		}
	}
	return doc, nil
}

func saveWorkFolderAccessDoc(ctx context.Context, doc workFolderAccessDoc) error {
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, workFolderAccessFilePath(), string(encoded))
}

var workFolderAccessStore = &workFolderStore{load: loadWorkFolderAccessDoc, save: saveWorkFolderAccessDoc}

func workFolderAccessKeysForClaims(claims *UserClaims) []string {
	if claims == nil {
		return nil
	}
	keys := make([]string, 0, 3)
	seen := map[string]struct{}{}
	for _, key := range []string{claims.UserID, claims.Username, claims.Email} {
		normalized := normalizeWorkflowPermissionKey(key)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		keys = append(keys, normalized)
	}
	return keys
}

// workFolderAccessKeyForClaims mirrors the platform's existing identity
// lookup: prefer the first user-id/username/email key that already has Work
// state, then use the normalized user ID for a brand-new record.
func workFolderAccessKeyForClaims(doc workFolderAccessDoc, claims *UserClaims) string {
	keys := workFolderAccessKeysForClaims(claims)
	for _, key := range keys {
		if _, ok := doc.Assignments[key]; ok {
			return key
		}
		if _, ok := doc.Grants[key]; ok {
			return key
		}
	}
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}

// workFolderRootsForKey returns the canonical allowed roots for a storage
// key. Unknown keys get none.
func workFolderRootsForKey(doc workFolderAccessDoc, key string) []string {
	if key == "" {
		return nil
	}
	assignment := doc.Assignments[key]
	if assignment == nil {
		return nil
	}
	return workproduct.NormalizeRoots(assignment.Roots)
}

// workFolderGrantsForClaims returns the stored external folder grants for
// the calling identity. An unknown identity gets none.
func workFolderAccessForClaims(ctx context.Context, claims *UserClaims) ([]workflowtypes.WorkflowFolderGrant, []string) {
	doc, err := workFolderAccessStore.read(ctx)
	if err != nil {
		return nil, nil
	}
	key := workFolderAccessKeyForClaims(doc, claims)
	if key == "" {
		return nil, nil
	}
	roots := workFolderRootsForKey(doc, key)
	grants := doc.Grants[key]
	// Assigned roots are authoritative even for an administrator using Work.
	// An administrator with no assignment may still use an explicitly stored
	// direct grant, preserving the administration escape hatch.
	if len(roots) > 0 || !workFolderClaimsAreAdmin(claims) {
		authorized := make([]workflowtypes.WorkflowFolderGrant, 0, len(grants))
		for _, grant := range grants {
			if workproduct.PathWithinRoots(grant.Path, roots) {
				authorized = append(authorized, grant)
			}
		}
		grants = authorized
	}
	return append([]workflowtypes.WorkflowFolderGrant(nil), grants...), roots
}

func workFolderClaimsAreAdmin(claims *UserClaims) bool {
	access := userAccessForClaims(claims)
	if access.Known {
		return access.Admin && !access.Disabled
	}
	return access.Admin || workflowAccessForClaims(claims) == WorkflowAccessOwner
}

func workFolderGrantsForClaims(ctx context.Context, claims *UserClaims) []workflowtypes.WorkflowFolderGrant {
	grants, _ := workFolderAccessForClaims(ctx, claims)
	return grants
}

// workFolderRootsForClaims returns the canonical allowed roots for the
// calling identity. An unknown identity gets none.
func workFolderRootsForClaims(ctx context.Context, claims *UserClaims) []string {
	_, roots := workFolderAccessForClaims(ctx, claims)
	return roots
}

// workFolderGuardInputs resolves the calling user's attached Work folders
// into folder-guard paths plus WORK_FOLDER_<ALIAS> session env. A folder that
// later becomes unavailable remains in the guard but is reported as unavailable
// by the folders API; current assigned roots are revalidated before this point.
func workFolderGuardInputs(ctx context.Context) (read, write, readOnly []string, env map[string]string) {
	return workproduct.ResolveGrants(workFolderGrantsForClaims(ctx, GetUserFromContext(ctx)))
}

// applyWorkFolderAdd is the attach decision: validate + authorize the
// grant, then append it to the caller's list. Pure over the document so
// authorization tests pin it directly.
func applyWorkFolderAdd(doc *workFolderAccessDoc, key string, input workFolderAddInput, isAdmin bool) (workflowtypes.WorkflowFolderGrant, error) {
	var zero workflowtypes.WorkflowFolderGrant
	canonical, err := workproduct.ValidateGrantForUser(input.Path, input.Alias, input.Access, workFolderRootsForKey(*doc, key), isAdmin)
	if err != nil {
		return zero, err
	}
	alias := strings.TrimSpace(input.Alias)
	for _, grant := range doc.Grants[key] {
		if strings.EqualFold(strings.TrimSpace(grant.Alias), alias) {
			return zero, errWorkFolderAliasConflict{alias: alias}
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	grant := workflowtypes.WorkflowFolderGrant{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		Alias:     alias,
		Path:      canonical,
		Access:    strings.TrimSpace(input.Access),
		Reason:    strings.TrimSpace(input.Reason),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if doc.Grants == nil {
		doc.Grants = map[string][]workflowtypes.WorkflowFolderGrant{}
	}
	doc.Grants[key] = append(doc.Grants[key], grant)
	return grant, nil
}

// applyWorkFolderDelete removes one grant by id from a user's list.
func applyWorkFolderDelete(doc *workFolderAccessDoc, key, id string) error {
	grants, ok := doc.Grants[key]
	if !ok {
		return errWorkFolderNotFound{}
	}
	kept := grants[:0]
	found := false
	for _, grant := range grants {
		if grant.ID == id {
			found = true
			continue
		}
		kept = append(kept, grant)
	}
	if !found {
		return errWorkFolderNotFound{}
	}
	doc.Grants[key] = kept
	return nil
}

// applyWorkFolderRoots replaces a user's allowed roots. Every root is
// strictly validated: administrator input that references a missing path
// is rejected, never stored.
func applyWorkFolderRoots(doc *workFolderAccessDoc, key string, roots []string) ([]string, error) {
	canonical := make([]string, 0, len(roots))
	for _, root := range roots {
		clean, err := workproduct.ValidateRoot(root)
		if err != nil {
			return nil, fmt.Errorf("root %q: %w", root, err)
		}
		canonical = append(canonical, clean)
	}
	if doc.Assignments == nil {
		doc.Assignments = map[string]*workFolderAssignment{}
	}
	doc.Assignments[key] = &workFolderAssignment{Roots: canonical}
	if doc.Grants != nil {
		kept := make([]workflowtypes.WorkflowFolderGrant, 0, len(doc.Grants[key]))
		for _, grant := range doc.Grants[key] {
			if workproduct.PathWithinRoots(grant.Path, canonical) {
				kept = append(kept, grant)
			}
		}
		doc.Grants[key] = kept
	}
	return append([]string(nil), canonical...), nil
}

// invalidateWorkFolderSessions closes native runtimes and replaces their
// cached guard with an explicit deny-all policy. The next authorized turn
// rebuilds the guard from current server-side grants.
func invalidateWorkFolderSessions(ctx context.Context, identityKey, reason string) {
	userID := normalizeWorkflowPermissionKey(identityKey)
	if record := directoryUserFor(identityKey, identityKey, identityKey); record != nil {
		userID = record.ID
	}
	if userID == "" {
		return
	}
	for _, product := range projectProducts {
		sessions, err := defaultProductConversationRegistryStore().liveSessionIDs(ctx, userID, product.ProfileID)
		if err != nil {
			continue
		}
		for sessionID := range sessions {
			common.SetSessionFolderGuard(sessionID, []string{}, []string{})
			closeAllCodingCLIInteractiveSessionsForOwner(sessionID, reason)
		}
	}
}

type workFolderAddInput struct {
	Path   string
	Alias  string
	Access string
	Reason string
}

type errWorkFolderAliasConflict struct{ alias string }

func (e errWorkFolderAliasConflict) Error() string {
	return fmt.Sprintf("folder alias %q is already attached", e.alias)
}

type errWorkFolderNotFound struct{}

func (errWorkFolderNotFound) Error() string { return "folder not found" }

func workFolderClaims(w http.ResponseWriter, r *http.Request) *UserClaims {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil
	}
	if !userAllowedProduct(claims, crewProfileID) && !userAllowedProduct(claims, codeproduct.ProfileID) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil
	}
	return claims
}

func (api *StreamingAPI) handleListWorkFolders(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := workFolderClaims(w, r)
	if claims == nil {
		return
	}
	grants := workFolderGrantsForClaims(r.Context(), claims)
	roots := workFolderRootsForClaims(r.Context(), claims)
	type grantView struct {
		workflowtypes.WorkflowFolderGrant
		Available bool `json:"available"`
	}
	views := make([]grantView, 0, len(grants))
	for _, grant := range grants {
		views = append(views, grantView{WorkflowFolderGrant: grant, Available: workproduct.FolderGrantAvailable(grant.Path)})
	}
	if roots == nil {
		roots = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"folders": views, "roots": roots})
}

func (api *StreamingAPI) handleAddWorkFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := workFolderClaims(w, r)
	if claims == nil {
		return
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	var input struct {
		Path   string `json:"path"`
		Alias  string `json:"alias"`
		Access string `json:"access"`
		Reason string `json:"reason"`
	}
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	isAdmin := currentUserIsAdmin(r)
	var added workflowtypes.WorkflowFolderGrant
	err := workFolderAccessStore.update(r.Context(), func(doc *workFolderAccessDoc) error {
		key := workFolderAccessKeyForClaims(*doc, claims)
		grant, err := applyWorkFolderAdd(doc, key, workFolderAddInput{
			Path:   input.Path,
			Alias:  input.Alias,
			Access: input.Access,
			Reason: input.Reason,
		}, isAdmin)
		if err != nil {
			return err
		}
		added = grant
		return nil
	})
	if err != nil {
		status := http.StatusBadRequest
		var aliasConflict errWorkFolderAliasConflict
		if errors.As(err, &aliasConflict) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{"folder": added})
}

func (api *StreamingAPI) handleDeleteWorkFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := workFolderClaims(w, r)
	if claims == nil {
		return
	}
	id := strings.TrimSpace(mux.Vars(r)["id"])
	if id == "" {
		http.Error(w, "folder id is required", http.StatusBadRequest)
		return
	}
	// Administrators may revoke another user's grant via ?user=; everyone
	// else acts on their own list only.
	key := ""
	if target := normalizeWorkflowPermissionKey(r.URL.Query().Get("user")); target != "" {
		if !currentUserIsAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		key = target
	}
	err := workFolderAccessStore.update(r.Context(), func(doc *workFolderAccessDoc) error {
		if key == "" {
			key = workFolderAccessKeyForClaims(*doc, claims)
		}
		return applyWorkFolderDelete(doc, key, id)
	})
	if err != nil {
		var folderNotFound errWorkFolderNotFound
		if errors.As(err, &folderNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	invalidateWorkFolderSessions(r.Context(), key, "Work folder access was revoked")
}

// handleGetWorkRoots returns one user's allowed roots. Administrators
// only; ordinary users see their own roots inside GET /work/folders.
func (api *StreamingAPI) handleGetWorkRoots(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := workFolderClaims(w, r)
	if claims == nil {
		return
	}
	if !currentUserIsAdmin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	target := normalizeWorkflowPermissionKey(r.URL.Query().Get("user"))
	if target == "" {
		http.Error(w, "user is required", http.StatusBadRequest)
		return
	}
	doc, err := workFolderAccessStore.read(r.Context())
	if err != nil {
		http.Error(w, "failed to load assignments", http.StatusInternalServerError)
		return
	}
	roots := workFolderRootsForKey(doc, target)
	if roots == nil {
		roots = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"user": target, "roots": roots})
}

// handlePutWorkRoots replaces one user's allowed roots. Administrators
// only. Every root must be an existing absolute directory.
func (api *StreamingAPI) handlePutWorkRoots(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := workFolderClaims(w, r)
	if claims == nil {
		return
	}
	if !currentUserIsAdmin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	var input struct {
		User  string   `json:"user"`
		Roots []string `json:"roots"`
	}
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	target := normalizeWorkflowPermissionKey(input.User)
	if target == "" {
		http.Error(w, "user is required", http.StatusBadRequest)
		return
	}
	var saved []string
	err := workFolderAccessStore.update(r.Context(), func(doc *workFolderAccessDoc) error {
		roots, err := applyWorkFolderRoots(doc, target, input.Roots)
		if err != nil {
			return err
		}
		saved = roots
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"user": target, "roots": saved})
	invalidateWorkFolderSessions(r.Context(), target, "Work workspace roots changed")
}

// registerWorkFolderTools exposes the same user-scoped grant store as the
// Work Setup panel. Authorization remains server-side; the model cannot grant
// a path outside the administrator-assigned roots.
func (api *StreamingAPI) registerWorkFolderTools(registrar definitionToolRegistrar, userID, sessionID string) error {
	register := func(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error)) error {
		return registrar.RegisterCustomTool(name, description, parameters, execute, "work_folder_tools")
	}
	claims := &UserClaims{UserID: userID}
	if record := directoryUserFor(userID, "", ""); record != nil {
		claims.Username = record.Username
		claims.Email = record.Email
	}
	if err := register("list_work_folders", "List the additional server folders attached to this Work account and the administrator-assigned roots within which folders may be attached.", map[string]interface{}{
		"type": "object", "properties": map[string]interface{}{},
	}, func(ctx context.Context, _ map[string]interface{}) (string, error) {
		grants, roots := workFolderAccessForClaims(ctx, claims)
		encoded, err := json.MarshalIndent(map[string]interface{}{"folders": grants, "allowed_roots": roots}, "", "  ")
		return string(encoded), err
	}); err != nil {
		return err
	}
	if err := register("attach_work_folder", "Attach an existing server folder to Work. The path must be inside an administrator-assigned root. Use only a path the user explicitly identified; choose read_only unless they explicitly need writes.", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path":   map[string]interface{}{"type": "string", "description": "Existing absolute server folder path."},
			"alias":  map[string]interface{}{"type": "string", "description": "Short unique alias used in WORK_FOLDER_<ALIAS>."},
			"access": map[string]interface{}{"type": "string", "enum": []string{"read_only", "read_write"}},
			"reason": map[string]interface{}{"type": "string"},
		},
		"required": []string{"path", "alias", "access"},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		path, _ := args["path"].(string)
		alias, _ := args["alias"].(string)
		access, _ := args["access"].(string)
		reason, _ := args["reason"].(string)
		var added workflowtypes.WorkflowFolderGrant
		var folderEnv map[string]string
		err := workFolderAccessStore.update(ctx, func(doc *workFolderAccessDoc) error {
			key := workFolderAccessKeyForClaims(*doc, claims)
			grant, addErr := applyWorkFolderAdd(doc, key, workFolderAddInput{Path: path, Alias: alias, Access: access, Reason: reason}, workFolderClaimsAreAdmin(claims))
			added = grant
			if addErr == nil {
				_, _, _, folderEnv = workproduct.ResolveGrants(doc.Grants[key])
			}
			return addErr
		})
		if err != nil {
			return "", err
		}
		if cfg := common.GetSessionShellConfig(sessionID); cfg != nil {
			reads := appendUniqueStrings(cfg.ReadPaths, added.Path)
			writes := cfg.WritePaths
			blockedWrites := cfg.BlockedWritePaths
			if added.Access == "read_write" {
				writes = appendUniqueStrings(writes, added.Path)
			} else {
				blockedWrites = appendUniqueStrings(blockedWrites, added.Path)
			}
			common.SetSessionFolderGuard(sessionID, reads, writes)
			common.SetSessionFolderGuardBlockedWritePaths(sessionID, blockedWrites)
		}
		common.ReplaceSessionShellEnvPrefix(sessionID, "WORK_FOLDER_", folderEnv)
		encoded, err := json.MarshalIndent(map[string]interface{}{"folder": added, "note": "The folder is attached and available now."}, "", "  ")
		return string(encoded), err
	}); err != nil {
		return err
	}
	return register("detach_work_folder", "Detach one additional server folder from Work. Call list_work_folders first and pass its exact folder id.", map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}},
		"required":   []string{"id"},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		id, _ := args["id"].(string)
		key := ""
		removedPath := ""
		var folderEnv map[string]string
		err := workFolderAccessStore.update(ctx, func(doc *workFolderAccessDoc) error {
			key = workFolderAccessKeyForClaims(*doc, claims)
			for _, grant := range doc.Grants[key] {
				if grant.ID == strings.TrimSpace(id) {
					removedPath = grant.Path
					break
				}
			}
			if deleteErr := applyWorkFolderDelete(doc, key, strings.TrimSpace(id)); deleteErr != nil {
				return deleteErr
			}
			_, _, _, folderEnv = workproduct.ResolveGrants(doc.Grants[key])
			return nil
		})
		if err != nil {
			return "", err
		}
		if cfg := common.GetSessionShellConfig(sessionID); cfg != nil {
			reads := make([]string, 0, len(cfg.ReadPaths))
			writes := make([]string, 0, len(cfg.WritePaths))
			blockedWrites := make([]string, 0, len(cfg.BlockedWritePaths))
			for _, path := range cfg.ReadPaths {
				if path != removedPath {
					reads = append(reads, path)
				}
			}
			for _, path := range cfg.WritePaths {
				if path != removedPath {
					writes = append(writes, path)
				}
			}
			for _, path := range cfg.BlockedWritePaths {
				if path != removedPath {
					blockedWrites = append(blockedWrites, path)
				}
			}
			common.SetSessionFolderGuard(sessionID, reads, writes)
			common.SetSessionFolderGuardBlockedWritePaths(sessionID, blockedWrites)
		}
		common.ReplaceSessionShellEnvPrefix(sessionID, "WORK_FOLDER_", folderEnv)
		return "Folder detached from Work.", nil
	})
}
