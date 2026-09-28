package main

// Authorization for a workspace-api running in server mode (serving other
// people's laptops; see server_user_auth.go). The token only says WHO is
// calling; this decides WHAT they may touch:
//
//   - only an allowlist of routes is served; everything else is 403;
//   - every path must be inside Workflow/<name> the caller may access (per
//     that workflow's workflow.json access lists) or the caller's own
//     _users/<id>/ area; listings above that are filtered;
//   - owners and editors may write and execute, readers may only read;
//   - shell commands run under a Folder Guard the server computes (the
//     workflow folder), never the client's, and only with a working sandbox.
//
// Laptop mode (no WORKSPACE_SERVER_USER_TOKENS_FILE) is unaffected.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
)

type accessLevel int

const (
	accessNone accessLevel = iota
	accessRead
	accessWrite
)

type workflowAccessLists struct {
	Owners  []string `json:"owners"`
	Editors []string `json:"editors"`
	Readers []string `json:"readers"`
}

type serverAuthz struct {
	docsRoot string
}

// workflowAccess returns the caller's level on Workflow/<name>.
func (a *serverAuthz) workflowAccess(workflow, user string) accessLevel {
	raw, err := os.ReadFile(filepath.Join(a.docsRoot, filepath.FromSlash(workflow), "workflow.json")) // #nosec G304 -- Workflow/<name> validated by caller
	if err != nil {
		return accessNone
	}
	var manifest struct {
		CreatedBy string               `json:"created_by"`
		Access    *workflowAccessLists `json:"access"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return accessNone
	}
	has := func(list []string) bool {
		for _, u := range list {
			if strings.TrimSpace(u) == user {
				return true
			}
		}
		return false
	}
	if manifest.CreatedBy == user {
		return accessWrite
	}
	if manifest.Access == nil {
		return accessNone
	}
	switch {
	case has(manifest.Access.Owners), has(manifest.Access.Editors):
		return accessWrite
	case has(manifest.Access.Readers):
		return accessRead
	}
	return accessNone
}

// workflowOf returns "Workflow/<name>" for a path inside a workflow.
func workflowOf(rel string) string {
	parts := strings.SplitN(rel, "/", 3)
	if len(parts) >= 2 && parts[0] == "Workflow" && parts[1] != "" && !strings.HasPrefix(parts[1], ".") {
		return parts[0] + "/" + parts[1]
	}
	return ""
}

// relFromRequest normalizes a client path (relative or absolute under the
// server root) to a docs-relative path. ok=false when it escapes the root.
func (a *serverAuthz) relFromRequest(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" || p == "." {
		return "", true
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(a.docsRoot, filepath.Clean(p))
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			return "", false
		}
		p = rel
	}
	return cleanRelPath(p), true
}

// levelFor is the caller's access to one path.
func (a *serverAuthz) levelFor(rel, user string) accessLevel {
	if wf := workflowOf(rel); wf != "" {
		return a.workflowAccess(wf, user)
	}
	own := "_users/" + user
	if rel == own || strings.HasPrefix(rel, own+"/") {
		return accessWrite
	}
	return accessNone
}

// isListingAncestor reports folders above the caller's reachable areas, which
// may be listed but only with filtered results.
func isListingAncestor(rel, user string) bool {
	switch rel {
	case "", "Workflow", "_users", "_users/" + user:
		return true
	}
	return false
}

type authzTarget struct {
	path  string
	level accessLevel
}

func forbid(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"success": false, "error": msg})
}

func (a *serverAuthz) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if !strings.HasPrefix(p, "/api/") || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		user := strings.TrimSpace(c.GetHeader("X-User-ID")) // pinned from the token
		if !c.GetBool(serverUserAuthenticatedFlag) || user == "" {
			forbid(c, http.StatusUnauthorized, "workspace server authorization required")
			return
		}
		method := c.Request.Method

		// Routes answered without a path check.
		if p == "/api/remote/whoami" && method == http.MethodGet {
			c.Next()
			return
		}

		// Listings and globs at or above the caller's areas: filtered.
		if method == http.MethodGet && (p == "/api/documents" || p == "/api/glob") {
			rel, ok := a.relFromRequest(c.Query("folder"))
			if !ok {
				forbid(c, http.StatusForbidden, "path is outside the workspace")
				return
			}
			if isListingAncestor(rel, user) {
				a.filterListing(c, user)
				return
			}
		}

		targets, err := a.targets(c)
		if err != nil {
			forbid(c, http.StatusForbidden, err.Error())
			return
		}
		for _, t := range targets {
			rel, ok := a.relFromRequest(t.path)
			if !ok || rel == "" {
				forbid(c, http.StatusForbidden, "path is outside the caller's workflows")
				return
			}
			level := a.levelFor(rel, user)
			if level < t.level {
				if level == accessNone && t.level == accessRead {
					// Same answer whether or not it exists: no probing of
					// other users' workflow names, and "not there yet" reads
					// (a move's empty-folder check) behave normally.
					forbid(c, http.StatusNotFound, "not found: "+rel)
					return
				}
				forbid(c, http.StatusForbidden, fmt.Sprintf("no %s access to %s", map[accessLevel]string{accessRead: "read", accessWrite: "write"}[t.level], rel))
				return
			}
		}

		switch {
		case p == "/api/execute":
			if err := a.pinExecuteGuard(c, user); err != nil {
				forbid(c, http.StatusForbidden, err.Error())
				return
			}
		case p == "/api/remote/workflow/import":
			a.importWithOwnership(c, user)
			return
		}
		c.Next()
	}
}

// targets lists the paths a request touches and the access each needs.
// Routes not listed here are refused.
func (a *serverAuthz) targets(c *gin.Context) ([]authzTarget, error) {
	p, method := c.Request.URL.Path, c.Request.Method
	body := func() []byte { return peekBody(c) }
	field := func(name string) string {
		var m map[string]interface{}
		_ = json.Unmarshal(body(), &m)
		s, _ := m[name].(string)
		return s
	}
	read := func(path string) []authzTarget { return []authzTarget{{path, accessRead}} }
	write := func(path string) []authzTarget { return []authzTarget{{path, accessWrite}} }
	unescape := func(prefix string) string {
		rel, _ := url.PathUnescape(strings.TrimPrefix(p, prefix))
		return rel
	}

	switch {
	case strings.HasPrefix(p, "/api/documents/"):
		rel := unescape("/api/documents/")
		switch method {
		case http.MethodGet, http.MethodHead:
			return read(strings.TrimSuffix(rel, "/raw")), nil
		case http.MethodPost:
			if strings.HasSuffix(rel, "/move") {
				return []authzTarget{{strings.TrimSuffix(rel, "/move"), accessWrite}, {field("destination_path"), accessWrite}}, nil
			}
			return write(rel), nil
		case http.MethodPut, http.MethodPatch, http.MethodDelete:
			return write(strings.TrimSuffix(rel, "/diff")), nil
		}
	case p == "/api/documents" && method == http.MethodGet, p == "/api/glob" && method == http.MethodGet, p == "/api/search" && method == http.MethodGet:
		return read(c.Query("folder")), nil
	case p == "/api/documents" && method == http.MethodPost:
		return write(field("filepath")), nil
	case strings.HasPrefix(p, "/api/versions/") && method == http.MethodGet:
		return read(unescape("/api/versions/")), nil
	case strings.HasPrefix(p, "/api/restore/") && method == http.MethodPost:
		return write(unescape("/api/restore/")), nil
	case p == "/api/folders" && method == http.MethodPost:
		return write(field("folder_path")), nil
	case strings.HasPrefix(p, "/api/folders/") && method == http.MethodDelete:
		return write(unescape("/api/folders/")), nil
	case p == "/api/folders/copy" && method == http.MethodPost:
		return []authzTarget{{field("source_path"), accessRead}, {field("destination_path"), accessWrite}}, nil
	case p == "/api/upload" && method == http.MethodPost:
		return write(uploadFolderPath(c)), nil
	case p == "/api/query" && method == http.MethodPost:
		return read(field("db_path")), nil
	case p == "/api/db/tables" && method == http.MethodGet:
		return read(c.Query("db_path")), nil
	case (p == "/api/mutate" || p == "/api/db/initialize" || p == "/api/db/backup-snapshot") && method == http.MethodPost:
		return write(field("db_path")), nil
	case p == "/api/report-field" && method == http.MethodPost:
		path := field("db_path")
		if path == "" {
			path = field("workspace_path")
		}
		return write(path), nil
	case p == "/api/execute" && method == http.MethodPost:
		return write(field("working_directory")), nil
	case p == "/api/remote/workflow/export" && method == http.MethodPost:
		rel, err := validateWorkflowRel(field("workspace_path"))
		if err != nil {
			return nil, err
		}
		return read(rel), nil
	case p == "/api/remote/workflow/import" && method == http.MethodPost:
		rel, err := validateWorkflowRel(multipartField(c, "workspace_path"))
		if err != nil {
			return nil, err
		}
		if _, statErr := os.Stat(filepath.Join(a.docsRoot, filepath.FromSlash(rel), "workflow.json")); statErr == nil {
			return write(rel), nil // replacing an existing workflow needs write access
		}
		return nil, nil // a new workflow: the importer becomes an owner
	}
	return nil, fmt.Errorf("%s %s is not available on a workspace server", method, p)
}

// pinExecuteGuard replaces the client's Folder Guard with one scoped to the
// command's workflow and strips client env that must not be trusted.
func (a *serverAuthz) pinExecuteGuard(c *gin.Context, user string) error {
	if !security.CurrentSandboxCapability().Available {
		return fmt.Errorf("shell execution is disabled: this server has no working sandbox")
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(peekBody(c), &raw); err != nil {
		return fmt.Errorf("invalid request body")
	}
	wd, _ := raw["working_directory"].(string)
	rel, ok := a.relFromRequest(wd)
	wf := workflowOf(rel)
	if !ok || wf == "" {
		return fmt.Errorf("commands must run inside a workflow folder on a workspace server")
	}
	guard := map[string]interface{}{
		"enabled":          true,
		"read_paths":       []string{wf},
		"write_paths":      []string{wf},
		"enforcement_mode": "strict",
	}
	// Keep client restrictions that only narrow access inside the workflow.
	if client, ok := raw["folder_guard"].(map[string]interface{}); ok {
		for _, key := range []string{"blocked_paths", "blocked_write_paths"} {
			var kept []string
			if list, ok := client[key].([]interface{}); ok {
				for _, item := range list {
					if s, ok := item.(string); ok {
						if r, ok := a.relFromRequest(s); ok && (r == wf || strings.HasPrefix(r, wf+"/")) {
							kept = append(kept, r)
						}
					}
				}
			}
			if len(kept) > 0 {
				guard[key] = kept
			}
		}
		if deny, ok := client["deny_network"].(bool); ok && deny {
			guard["deny_network"] = true
		}
	}
	raw["folder_guard"] = guard
	if env, ok := raw["extra_env"].(map[string]interface{}); ok {
		for key := range env {
			if forwardedEnvDenied(key) {
				delete(env, key)
			}
		}
		env["RUNLOOP_SERVER_USER"] = user
	}
	out, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	setBody(c, out)
	return nil
}

// importWithOwnership runs the import, then makes sure the importing user is
// an owner of the imported workflow (a laptop's own user id rarely matches the
// server's).
func (a *serverAuthz) importWithOwnership(c *gin.Context, user string) {
	rel, _ := validateWorkflowRel(multipartField(c, "workspace_path"))
	c.Next()
	if c.Writer.Status() != http.StatusOK || rel == "" {
		return
	}
	manifestPath := filepath.Join(a.docsRoot, filepath.FromSlash(rel), "workflow.json")
	raw, err := os.ReadFile(manifestPath) // #nosec G304 -- validated Workflow/<name>
	if err != nil {
		return
	}
	var manifest map[string]interface{}
	if json.Unmarshal(raw, &manifest) != nil {
		return
	}
	access, _ := manifest["access"].(map[string]interface{})
	if access == nil {
		access = map[string]interface{}{"owners": []interface{}{}, "readers": []interface{}{}}
	}
	owners, _ := access["owners"].([]interface{})
	for _, o := range owners {
		if s, _ := o.(string); s == user {
			return
		}
	}
	access["owners"] = append(owners, user)
	manifest["access"] = access
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if enc.Encode(manifest) == nil {
		_ = os.WriteFile(manifestPath, buf.Bytes(), 0o644) // #nosec G306 -- workspace documents are 0644
	}
}

// filterListing runs a listing or glob and keeps only entries the caller may
// read, plus the folders above them.
func (a *serverAuthz) filterListing(c *gin.Context, user string) {
	original := c.Writer
	capture := &captureWriter{ResponseWriter: original}
	c.Writer = capture
	c.Next()
	c.Writer = original

	var resp map[string]interface{}
	if capture.Status() != http.StatusOK || json.Unmarshal(capture.buf.Bytes(), &resp) != nil {
		original.WriteHeader(capture.Status())
		_, _ = original.Write(capture.buf.Bytes())
		return
	}
	levels := map[string]accessLevel{}
	keep := func(p string) bool {
		if isListingAncestor(p, user) {
			return true
		}
		if wf := workflowOf(p); wf != "" {
			lvl, ok := levels[wf]
			if !ok {
				lvl = a.workflowAccess(wf, user)
				levels[wf] = lvl
			}
			return lvl >= accessRead
		}
		return a.levelFor(p, user) >= accessRead
	}
	resp["data"] = nodesToAny(filterNodes(toNodes(resp["data"]), keep))
	out, err := json.Marshal(resp)
	if err != nil {
		forbid(c, http.StatusInternalServerError, "could not filter listing")
		return
	}
	original.Header().Set("Content-Type", "application/json; charset=utf-8")
	original.WriteHeader(http.StatusOK)
	_, _ = io.Copy(original, bytes.NewReader(out))
}
