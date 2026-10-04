package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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
	for _, docs := range []string{fsutil.WorkspaceDocsRoot(), fsutil.WorkspaceShellRoot()} {
		docs = canonical(docs)
		if inside(docs, root) || inside(root, docs) {
			return knowledgebase.Config{}, fmt.Errorf("Knowledge Base data must be outside workspace tool roots")
		}
	}
	branch := strings.TrimSpace(os.Getenv("AGENTWORKS_KNOWLEDGEBASE_BACKUP_BRANCH"))
	if branch == "" {
		branch = "main"
	}
	return knowledgebase.Config{Root: root, OrganizationID: org, BackupRemote: strings.TrimSpace(os.Getenv("AGENTWORKS_KNOWLEDGEBASE_BACKUP_REMOTE")), BackupBranch: branch}, nil
}

func knowledgebaseService() (*knowledgebase.Service, error) {
	config, err := knowledgebaseConfig()
	if err != nil {
		return nil, err
	}
	knowledgebaseInstance.Lock()
	defer knowledgebaseInstance.Unlock()
	if knowledgebaseInstance.service == nil || knowledgebaseInstance.config != config {
		if knowledgebaseInstance.service != nil {
			_ = knowledgebaseInstance.service.Close()
		}
		service, err := knowledgebase.New(config)
		if err != nil {
			return nil, err
		}
		knowledgebaseInstance.service, knowledgebaseInstance.config = service, config
	}
	return knowledgebaseInstance.service, nil
}

func knowledgebaseSyncIdentities(ctx context.Context, service *knowledgebase.Service) error {
	directory, err := loadUserDirectory()
	if err != nil {
		return fmt.Errorf("platform identities unavailable")
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

func knowledgebaseProductAllowed(claims *UserClaims) bool {
	return claims != nil && strings.TrimSpace(claims.UserID) != "" && productEnabled("knowledgebase") && !directoryUserIsDisabled(claims) && userAllowedProduct(claims, "knowledgebase")
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
		store, err := openAccessTokens()
		if err != nil {
			return &knowledgebase.Error{Code: "FORBIDDEN", Message: "Connection authority is unavailable."}
		}
		defer store.Close()
		if _, err = store.Active(ctx, claims.AccessToken.ID, time.Now()); err != nil {
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
		externalError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "Knowledge Base is temporarily unavailable.")
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
		externalError(w, http.StatusNotFound, "NOT_FOUND", "Knowledge Base not found.")
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
		knowledgebaseWriteJSON(w, map[string]any{"organization_id": cfg.OrganizationID, "profile_id": "knowledgebase", "chat_workspace": "Chats/Knowledgebase", "is_admin": currentUserIsAdmin(r), "identity_id": claims.UserID, "backup_configured": cfg.BackupRemote != ""})
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
	case "activity":
		tool = "get_knowledgebase_activity"
	case "backup":
		tool = "get_knowledgebase_backup_status"
	default:
		externalError(w, http.StatusNotFound, "NOT_FOUND", "Knowledge Base endpoint not found.")
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
	claims := GetUserFromContext(r.Context())
	if !knowledgebaseProductAllowed(claims) {
		externalError(w, 404, "NOT_FOUND", "Knowledge Base not found.")
		return
	}
	if !knowledgebaseConnectionAllowsAction(claims, tool, args) {
		externalError(w, http.StatusForbidden, "insufficient_scope", "This connection does not allow the requested Knowledge Base action.")
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
	result, err := service.CallTool(r.Context(), knowledgebasePrincipal(r, claims), tool, args)
	if err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	knowledgebaseWriteJSON(w, map[string]any{"result": result})
}
