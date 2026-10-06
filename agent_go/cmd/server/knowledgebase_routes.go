package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

var knowledgebaseInstance struct {
	sync.Mutex
	service *knowledgebase.Service
	config  knowledgebase.Config
}

func knowledgebaseConfig() (knowledgebase.Config, error) {
	org := strings.TrimSpace(os.Getenv("AGENTWORKS_KNOWLEDGEBASE_ORG"))
	if org == "" {
		org = "installation"
	}
	root := strings.TrimSpace(os.Getenv("AGENTWORKS_KNOWLEDGEBASE_ROOT"))
	explicitRoot := root != ""
	if root == "" {
		state, err := workflowCLIStateRoot()
		if err != nil {
			return knowledgebase.Config{}, err
		}
		sum := sha256.Sum256([]byte(org))
		root = filepath.Join(state, "knowledgebase", hex.EncodeToString(sum[:16]))
	}
	if !filepath.IsAbs(root) {
		return knowledgebase.Config{}, fmt.Errorf("AGENTWORKS_KNOWLEDGEBASE_ROOT must be absolute")
	}
	root = filepath.Clean(root)
	// Resolve existing ancestor symlinks even before this data directory exists.
	canonical := func(p string) string {
		p, _ = filepath.Abs(p)
		var tail []string
		for {
			if resolved, err := filepath.EvalSymlinks(p); err == nil {
				for i := len(tail) - 1; i >= 0; i-- {
					resolved = filepath.Join(resolved, tail[i])
				}
				return resolved
			}
			parent := filepath.Dir(p)
			if parent == p {
				return p
			}
			tail = append(tail, filepath.Base(p))
			p = parent
		}
	}
	inside := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	root = canonical(root)
	// Brain's private data (access database, requests, journal) stays outside the workspace tool roots.
	for _, docs := range []string{fsutil.WorkspaceDocsRoot(), fsutil.WorkspaceShellRoot()} {
		docs = canonical(docs)
		if inside(docs, root) || inside(root, docs) {
			return knowledgebase.Config{}, fmt.Errorf("Brain data must be outside workspace tool roots")
		}
	}
	// Its notes are plain files in the documents tree, Brain/, a normal Git folder like every product's (PLAT-633).
	// An explicitly placed Brain (AGENTWORKS_KNOWLEDGEBASE_ROOT, as tests and custom installs set) keeps its notes
	// inside that root unless AGENTWORKS_KNOWLEDGEBASE_LIVE_ROOT names another folder.
	live := strings.TrimSpace(os.Getenv("AGENTWORKS_KNOWLEDGEBASE_LIVE_ROOT"))
	if live == "" && explicitRoot {
		live = filepath.Join(root, "live")
	}
	if live == "" {
		live = filepath.Join(fsutil.WorkspaceDocsRoot(), brainFolderName)
	}
	if !filepath.IsAbs(live) {
		return knowledgebase.Config{}, fmt.Errorf("Brain's folder must be absolute")
	}
	live = canonical(live)
	branch := strings.TrimSpace(os.Getenv("AGENTWORKS_KNOWLEDGEBASE_BACKUP_BRANCH"))
	if branch == "" {
		branch = "main"
	}
	return knowledgebase.Config{Root: root, LiveRoot: live, OrganizationID: org, BackupRemote: strings.TrimSpace(os.Getenv("AGENTWORKS_KNOWLEDGEBASE_BACKUP_REMOTE")), BackupBranch: branch, BackupEncryptionKey: string(deriveSecretsKey()), AllowPrivateBackup: os.Getenv("AGENTWORKS_KNOWLEDGEBASE_BACKUP_ALLOW_PRIVATE") == "true", SecretResolver: knowledgebaseBackupSecret}, nil
}

func knowledgebaseService() (*knowledgebase.Service, error) {
	config, err := knowledgebaseConfig()
	if err != nil {
		return nil, err
	}
	knowledgebaseInstance.Lock()
	defer knowledgebaseInstance.Unlock()
	if knowledgebaseInstance.service == nil || !sameKnowledgebaseConfig(knowledgebaseInstance.config, config) {
		if knowledgebaseInstance.service != nil {
			_ = knowledgebaseInstance.service.Close()
		}
		if err := moveBrainNotesIntoDocuments(filepath.Join(config.Root, "live"), config.LiveRoot); err != nil {
			return nil, fmt.Errorf("move Brain's notes into %s: %w", brainFolderName, err)
		}
		service, err := knowledgebase.New(config)
		if err != nil {
			return nil, err
		}
		knowledgebaseInstance.service, knowledgebaseInstance.config = service, config
		// Brain's folder is a Git repository from the start, not only once a Brain chat opens it (PLAT-633).
		if folder, gitErr := service.EnsureGitRepository(context.Background()); gitErr != nil {
			log.Printf("[BRAIN] Git folder not ready: %v", gitErr)
		} else {
			log.Printf("[BRAIN] Git folder ready at %s (origin configured: %v)", folder.Path, folder.Remote != "")
		}
	}
	return knowledgebaseInstance.service, nil
}

func knowledgebaseSyncIdentities(ctx context.Context, service *knowledgebase.Service) error {
	directory, err := loadUserDirectory()
	if err != nil {
		return fmt.Errorf("platform identities unavailable")
	}
	if IsMultiUserMode() && (directory == nil || len(directory.Users) == 0) {
		return fmt.Errorf("platform identities unavailable: refusing an empty directory snapshot")
	}
	var identities []knowledgebase.Identity
	if !IsMultiUserMode() {
		id := GetDefaultUserID()
		identities = append(identities, knowledgebase.Identity{ID: id, Name: id, Type: "user"})
	}
	if directory != nil {
		for _, user := range directory.Users {
			identities = append(identities, knowledgebase.Identity{ID: user.ID, Name: firstNonEmptyTrimmed(user.Username, user.Email, user.ID), Type: "user", Disabled: user.Disabled})
		}
	}
	return service.SyncPlatformIdentities(ctx, identities)
}

// knowledgebaseMCPAllowed is who may connect to Brain through the MCP: every active account on a server that runs
// the product. What a connection can read or write is still decided by folder roles, so this grants no content.
func knowledgebaseMCPAllowed(claims *UserClaims) bool {
	return claims != nil && strings.TrimSpace(claims.UserID) != "" && productEnabled("knowledgebase") && !directoryUserIsDisabled(claims)
}

// knowledgebaseProductAllowed is the Brain product for the app and for agents running as the person: the
// `knowledgebase` product on the account (administrators have every product). A connection (MCP or local token) is
// bounded by its own scopes instead, so it needs only knowledgebaseMCPAllowed.
func knowledgebaseProductAllowed(claims *UserClaims) bool {
	return knowledgebaseMCPAllowed(claims) && (claims.AccessToken != nil || userAllowedProduct(claims, "knowledgebase"))
}

func knowledgebasePrincipal(r *http.Request, claims *UserClaims) knowledgebase.Principal {
	if claims == nil {
		return knowledgebase.Principal{}
	}
	p := knowledgebase.Principal{IdentityID: claims.UserID, IsAdmin: currentUserIsAdmin(r)}
	wasAdmin := p.IsAdmin
	p.Recheck = func(ctx context.Context) error {
		if !knowledgebaseProductAllowed(claims) || (wasAdmin && !currentUserIsAdmin(r)) {
			return &knowledgebase.Error{Code: "FORBIDDEN", Message: "This identity no longer has the required platform access."}
		}
		if claims.AccessToken == nil {
			return ctx.Err()
		}
		// Local tokens and OAuth MCP connections ("oauth-" IDs) are verified in their own stores.
		if live, err := activeExternalGrantClaims(ctx, claims.AccessToken.ID); err != nil || live == nil || live.UserID != claims.UserID {
			return &knowledgebase.Error{Code: "FORBIDDEN", Message: "This connection is expired or revoked."}
		}
		return nil
	}
	if token := claims.AccessToken; token != nil {
		if token.KnowledgebaseIdentityID != "" {
			p.IdentityID = token.KnowledgebaseIdentityID
			p.IsAdmin = false
			wasAdmin = false
		}
		if token.KnowledgebaseFolders != nil {
			caps := append([]knowledgebase.Cap{}, (*token.KnowledgebaseFolders)...)
			p.Caps = &caps
		}
		if !token.Allows("knowledgebase:write") {
			if p.Caps == nil {
				caps := []knowledgebase.Cap{{FolderPath: "", Role: "reader"}}
				p.Caps = &caps
			} else {
				for i := range *p.Caps {
					(*p.Caps)[i].Role = "reader"
				}
			}
		}
	}
	return p
}

func knowledgebaseHTTPError(w http.ResponseWriter, err error) {
	var domain *knowledgebase.Error
	if !errors.As(err, &domain) {
		externalError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "Brain is temporarily unavailable.")
		return
	}
	status := http.StatusBadRequest
	switch domain.Code {
	case "NOT_FOUND":
		status = http.StatusNotFound
	case "FORBIDDEN":
		status = http.StatusForbidden
	case "VERSION_CONFLICT", "ACL_VERSION_CONFLICT", "NAME_CONFLICT", "REQUEST_ID_REUSE", "BACKUP_VERSION_CONFLICT", "BACKUP_BRANCH_ADVANCED", "BACKUP_REMOTE_CHANGED", "PATH_PENDING_DELETION_BACKUP":
		status = http.StatusConflict
	case "BACKUP_BUSY", "REQUEST_IN_PROGRESS":
		status = http.StatusConflict
	case "BACKUP_UNAVAILABLE", "BACKUP_OUTCOME_UNKNOWN":
		status = http.StatusServiceUnavailable
	case "LIMIT_EXCEEDED":
		status = http.StatusRequestEntityTooLarge
	}
	if domain.RetryAfter != nil {
		w.Header().Set("Retry-After", strconv.Itoa(*domain.RetryAfter))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": domain})
}

func knowledgebaseWriteJSON(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}

// Explicit administrator maintenance only observes a backup branch. It never
// changes live content or publishes a commit, and is unavailable to MCP tokens.
func (api *StreamingAPI) handleKnowledgebaseReconcileBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if !knowledgebaseProductAllowed(claims) || claims.AccessToken != nil || !currentUserIsAdmin(r) {
		externalError(w, http.StatusForbidden, "FORBIDDEN", "Administrator access is required.")
		return
	}
	service, err := knowledgebaseService()
	if err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	if err = knowledgebaseSyncIdentities(r.Context(), service); err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	result, err := service.ReconcileBackup(r.Context(), knowledgebasePrincipal(r, claims))
	if err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	knowledgebaseWriteJSON(w, result)
}

func (api *StreamingAPI) handleKnowledgebaseViewer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := GetUserFromContext(r.Context())
	if !knowledgebaseProductAllowed(claims) {
		externalError(w, http.StatusNotFound, "NOT_FOUND", "Brain not found.")
		return
	}
	// Reader endpoints never admit a content mutation or an access mutation.
	tool := ""
	switch strings.TrimPrefix(r.URL.Path, "/api/knowledgebase/") {
	case "bootstrap":
		cfg, err := knowledgebaseConfig()
		if err != nil {
			knowledgebaseHTTPError(w, err)
			return
		}
		service, err := knowledgebaseService()
		if err != nil {
			knowledgebaseHTTPError(w, err)
			return
		}
		backupConfigured, err := service.BackupConfigured()
		if err != nil {
			knowledgebaseHTTPError(w, err)
			return
		}
		knowledgebaseWriteJSON(w, map[string]any{"organization_id": cfg.OrganizationID, "profile_id": "knowledgebase", "chat_workspace": "Chats/Knowledgebase", "is_admin": currentUserIsAdmin(r), "identity_id": claims.UserID, "backup_configured": backupConfigured})
		return
	case "folders":
		tool = "list_knowledgebase_folders"
	case "entries":
		tool = "list_knowledgebase"
	case "read":
		tool = "read_knowledgebase"
	case "search":
		tool = "search_knowledgebase"
	case "access":
		tool = "get_knowledgebase_access"
	case "backup":
		tool = "get_knowledgebase_backup_status"
	default:
		externalError(w, http.StatusNotFound, "NOT_FOUND", "Brain endpoint not found.")
		return
	}
	args := map[string]any{}
	for _, key := range []string{"folder_path", "entry_id", "path", "query", "type", "tag", "cursor", "glob"} {
		if r.URL.Query().Has(key) {
			args[key] = r.URL.Query().Get(key)
		}
	}
	for _, key := range []string{"limit", "depth", "start_line", "end_line"} {
		if raw := r.URL.Query().Get(key); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				externalError(w, 400, "INVALID_ARGUMENT", "Invalid integer parameter.")
				return
			}
			args[key] = value
		}
	}
	service, err := knowledgebaseService()
	if err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	if err = knowledgebaseSyncIdentities(r.Context(), service); err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	result, err := service.Call(r.Context(), knowledgebasePrincipal(r, claims), tool, args)
	if err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	knowledgebaseWriteJSON(w, result)
}

func isExternalKnowledgebaseTool(name string) bool {
	return knowledgebase.IsMCPTool(name)
}

func (api *StreamingAPI) externalKnowledgebaseCall(w http.ResponseWriter, r *http.Request, tool string, args map[string]any) {
	tool = knowledgebase.CanonicalToolName(tool)
	claims := GetUserFromContext(r.Context())
	if !knowledgebaseProductAllowed(claims) {
		externalError(w, 404, "NOT_FOUND", "Brain not found.")
		return
	}
	if !knowledgebaseConnectionAllowsAction(claims, tool, args) {
		externalError(w, http.StatusForbidden, "insufficient_scope", "This connection does not allow the requested Brain action.")
		return
	}
	service, err := knowledgebaseService()
	if err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	if err = knowledgebaseSyncIdentities(r.Context(), service); err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	principal := knowledgebasePrincipal(r, claims)
	// Only this authenticated external boundary enables direct access actions.
	// App chat continues to use proposals; managed sessions remain content-only.
	if action, _ := args["action"].(string); tool == knowledgebase.ToolAccess && action != "inspect" {
		principal.AccessOnly = true
	}
	result, err := knowledgebaseDispatch(context.WithValue(r.Context(), knowledgebaseMigrationAuthorityKey{}, true), service, principal, claims.UserID, tool, args)
	if err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	knowledgebaseWriteJSON(w, map[string]any{"result": result})
}

// knowledgebaseBackupSecret reads a platform (global) secret for a backup destination that names one. Only the
// administrator who sets backup up can name a secret, and only an unrestricted administrator can configure it.
func knowledgebaseBackupSecret(name string) (string, bool) {
	for _, secret := range getGlobalSecrets() {
		if secret.Name == name {
			return secret.Value, secret.Value != ""
		}
	}
	return "", false
}

// sameKnowledgebaseConfig compares everything except the secret resolver (a function, always the same one).
func sameKnowledgebaseConfig(a, b knowledgebase.Config) bool {
	return a.Root == b.Root && a.LiveRoot == b.LiveRoot && a.OrganizationID == b.OrganizationID && a.BackupRemote == b.BackupRemote &&
		a.BackupBranch == b.BackupBranch && a.AllowPrivateBackup == b.AllowPrivateBackup && a.BackupEncryptionKey == b.BackupEncryptionKey
}
