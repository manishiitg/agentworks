package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

// Reuse Files' Git handlers after the KB domain has authorized the caller and
// selected a private candidate repository. Client paths never select a Git root.
func knowledgebaseGitCall(ctx context.Context, s *knowledgebase.Service, p knowledgebase.Principal, args map[string]any, claims *UserClaims) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.RunGit(ctx, p, args, func(ctx context.Context, dir string) (any, error) {
		op, _ := args["op"].(string)
		if op == "status" || op == "pull" || op == "push" {
			repo, err := workspaceGitStatus(ctx, dir)
			if err != nil {
				return nil, &knowledgebase.Error{Code: "GIT_FAILED", Message: "Git status is unavailable."}
			}
			repo.Root = ""
			if op == "status" {
				return map[string]any{"repos": []workspaceGitRepo{repo}}, nil
			}
			return map[string]any{"ok": true, "repo": repo}, nil
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
		if knowledgebase.GitReadOperation(op) {
			query := url.Values{}
			for _, key := range []string{"op", "file", "commit"} {
				if v, ok := args[key].(string); ok {
					query.Set(key, v)
				}
			}
			req.URL.RawQuery = query.Encode()
			serveWorkspaceGitRead(rec, req, ctx, dir)
		} else {
			raw, _ := json.Marshal(args)
			var action workspaceGitActionRequest
			if err := json.Unmarshal(raw, &action); err != nil {
				return nil, err
			}
			action.Repo = ""
			serveWorkspaceGitAction(rec, req, dir, action, claims)
		}
		if rec.Code >= 400 {
			return nil, &knowledgebase.Error{Code: "GIT_FAILED", Message: "Git could not complete this action. Check the branch, staged changes, or repository state."}
		}
		var result any
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			return nil, err
		}
		return result, nil
	})
}
func (api *StreamingAPI) handleKnowledgebaseGit(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := GetUserFromContext(r.Context())
	if !knowledgebaseProductAllowed(claims) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	args := map[string]any{}
	if r.Method == http.MethodPost {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
		decoder.DisallowUnknownFields()
		// Validate the same MCP schema used by external clients.
		if decoder.Decode(&args) != nil {
			knowledgebaseHTTPError(w, &knowledgebase.Error{Code: "INVALID_ARGUMENT", Message: "Invalid Git request."})
			return
		}
		args["action"] = "git"
		if !knowledgebaseConnectionAllowsAction(claims, "backup_knowledgebase", args) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	} else {
		for _, key := range []string{"op", "file", "commit"} {
			if value := r.URL.Query().Get(key); value != "" {
				args[key] = value
			}
		}
		if args["op"] == nil {
			args["op"] = "status"
		}
		if !knowledgebase.GitReadOperation(args["op"].(string)) {
			http.Error(w, "Use POST for Git actions.", http.StatusMethodNotAllowed)
			return
		}
		args["action"] = "git"
	}
	if err := knowledgebase.ValidateToolArguments("backup_knowledgebase", args); err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	s, err := knowledgebaseService()
	if err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	if err = knowledgebaseSyncIdentities(r.Context(), s); err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	result, err := knowledgebaseGitCall(r.Context(), s, knowledgebasePrincipal(r, claims), args, claims)
	if err != nil {
		// A scoped reader still has the regular Files view; whole-repository history
		// is available only to root readers. Do not disclose private repo details.
		if r.Method == http.MethodGet && args["op"] == "status" {
			if domain, ok := err.(*knowledgebase.Error); ok && (domain.Code == "NOT_FOUND" || domain.Code == "FORBIDDEN" || domain.Code == "BACKUP_NOT_CONFIGURED") {
				knowledgebaseWriteJSON(w, map[string]any{"repos": []workspaceGitRepo{}, "writable": false})
				return
			}
		}
		knowledgebaseHTTPError(w, err)
		return
	}
	if r.Method == http.MethodGet && args["op"] == "status" {
		access, e := s.Call(r.Context(), knowledgebasePrincipal(r, claims), "get_knowledgebase_access", map[string]any{"folder_path": ""})
		raw, _ := json.Marshal(access)
		var role struct {
			Role string `json:"effective_role"`
		}
		json.Unmarshal(raw, &role)
		writable := e == nil && (strings.EqualFold(role.Role, "Owner") || strings.EqualFold(role.Role, "Editor")) && knowledgebasePrincipal(r, claims).Caps == nil
		if value, ok := result.(map[string]any); ok {
			value["writable"] = writable
		}
	}
	knowledgebaseWriteJSON(w, result)
}
