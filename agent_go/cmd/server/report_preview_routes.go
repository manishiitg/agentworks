package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// Headless report preview (preview_report tool).
//
// The Go server serves a tiny page at /report-preview/ that renders a
// workflow's db/reports/index.html through the same host runtime the in-app
// Report tab uses (frontend/src/report-preview, built by `npm run
// build:report-preview` to the configured frontend static directory next to
// the rest of the deployed frontend -- read from disk on each request, the same way
// spaStaticFileHandler serves the app, NOT go:embed'd: this directory is
// deploy-populated and not committed, so embedding it would break `go
// build` on a fresh checkout). A headless browser loads the page with a
// short-lived, workspace-bound preview token; the page fetches its data
// from the two read-only endpoints below, which are the only API paths
// that token can reach (see scopeAllowsPath in auth_middleware.go).

const (
	reportPreviewScope     = "report-preview"
	reportPreviewTokenTTL  = 3 * time.Minute
	reportPreviewPagePath  = "/report-preview/"
	reportPreviewAPIPrefix = "/api/workflow/report-preview/"
)

const reportPreviewPageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Report preview</title>
</head>
<body>
<script src="/report-preview/report-preview.js"></script>
</body>
</html>
`

// reportPreviewRuntimePath deliberately shares the SPA's configured static
// root. Production releases put all browser assets in current/frontend while
// local development historically uses ./static; hard-coding the latter made a
// correctly built production bundle look missing at runtime.
func reportPreviewRuntimePath() string {
	return filepath.Join(staticFrontendDir(), "report-preview.js")
}

// mintReportPreviewToken issues a token that can only read one workflow's
// report data for a few minutes. It carries the current user's identity so
// workspace reads run as that user, but the scope claim keeps it away from
// every other endpoint.
func mintReportPreviewToken(claims *UserClaims, workspacePath string) (string, error) {
	if err := ValidateConfiguredAuthSecret(); err != nil {
		return "", err
	}
	userID, username, email := GetDefaultUserID(), "", ""
	if claims != nil {
		userID, username, email = claims.UserID, claims.Username, claims.Email
	}
	connectionID := ""
	if claims != nil && claims.AccessToken != nil {
		connectionID = claims.AccessToken.ID
	}
	now := time.Now()
	preview := &UserClaims{
		UserID:              userID,
		Username:            username,
		Email:               email,
		Scope:               reportPreviewScope,
		PreviewConnectionID: connectionID,
		ScopeWorkspace:      strings.Trim(strings.TrimSpace(workspacePath), "/"),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(reportPreviewTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "mcp-agent-builder",
			Subject:   userID,
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, preview).SignedString(GetAuthSecret())
}

// scopeAllowsPath: a scoped token reaches only the paths its scope names.
// Unscoped (normal session) tokens are unrestricted here.
func scopeAllowsPath(scope, requestPath string) bool {
	switch scope {
	case "":
		return true
	case reportPreviewScope:
		return strings.HasPrefix(requestPath, reportPreviewAPIPrefix)
	case reportMediaScope:
		return requestPath == reportMediaStreamPath
	}
	return false
}

// reportPreviewAllowedPath mirrors the frontend's allowedReportPath: the
// durable store and the other authored folders, never a run's scratch output,
// never a parent traversal.
func reportPreviewAllowedPath(relative string) (string, bool) {
	raw := strings.TrimSpace(relative)
	if path.Clean(raw) != raw || strings.Contains(raw, "\\") {
		return "", false
	}
	for _, part := range strings.Split(raw, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(strings.ToLower(part), ".sqlite") || strings.HasSuffix(strings.ToLower(part), ".sqlite3") || strings.HasSuffix(strings.ToLower(part), ".db") || strings.HasSuffix(part, "-wal") || strings.HasSuffix(part, "-shm") {
			return "", false
		}
	}
	normalized := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(strings.TrimSpace(relative), "\\", "/")), "/")
	if normalized == "" || normalized == "." || strings.HasPrefix(normalized, "../") || strings.Contains(normalized, "/../") {
		return "", false
	}
	for _, root := range []string{"db/", "knowledgebase/", "docs/", "planning/", "evaluation/", "costs/", "variables/"} {
		if strings.HasPrefix(normalized, root) {
			return normalized, true
		}
	}
	if normalized == "soul.md" || normalized == "workflow.json" {
		return normalized, true
	}
	return "", false
}

func reportPreviewWorkspace(r *http.Request, claims *UserClaims, requested string) (string, error) {
	workspacePath := strings.Trim(strings.TrimSpace(requested), "/")
	if workspacePath == "" {
		return "", fmt.Errorf("workspace is required")
	}
	if claims != nil && claims.Scope == reportPreviewScope && claims.ScopeWorkspace != "" && claims.ScopeWorkspace != workspacePath {
		return "", fmt.Errorf("this preview token is bound to another workflow")
	}
	if claims != nil {
		if crew, ok := resolveCrewPath(r.Context(), claims.UserID, workspacePath); ok && crew.Rest == "" {
			workspacePath = crew.Root
		}
	}
	return workspacePath, nil
}

// reportPreviewReadable authorizes a report-preview read of workspacePath.
// Report runs and media URLs check access themselves.
func reportPreviewReadable(r *http.Request, claims *UserClaims, workspacePath string) error {
	// A preview token is minted server-side for one workflow's session and
	// is bound to it by reportPreviewWorkspace; the binding is its authorization.
	if claims != nil && claims.Scope == reportPreviewScope && claims.ScopeWorkspace != "" {
		if claims.PreviewConnectionID == "" {
			return nil
		}
		copy := *claims
		copy.Scope = ""
		if copy.AccessToken == nil || !dashboardScopeAllowed(&copy, "preview") {
			return errWorkspaceReadDenied
		}
		if _, err := (&StreamingAPI{}).dashboardTarget(r.Context(), &copy, workspacePath); err != nil {
			return errWorkspaceReadDenied
		}
		return nil
	}
	// The preview reads with the caller's identity, so a logical per-user
	// path is the caller's own tree. A Crew's dashboard is readable in Crew
	// Run mode (crewAccessFor), the same rule report runs use.
	if claims != nil {
		if ref, ok := resolveCrewPath(r.Context(), claims.UserID, workspacePath); ok && ref.Rest == "" {
			if crewAccessFor(claims, ref) == crewAccessNone {
				return errWorkspaceReadDenied
			}
			return nil
		}
	}
	if !workspaceReadAllowed(r.Context(), claims, workspacePath, logicalPathIsCaller, false) {
		return errWorkspaceReadDenied
	}
	return nil
}

// errWorkspaceReadDenied is a named workspace the caller may not read.
var errWorkspaceReadDenied = errors.New("you do not have access to this workflow")

func reportPreviewWorkspaceErrorStatus(err error) int {
	if errors.Is(err, errWorkspaceReadDenied) {
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}

// Called only after reportPreviewReadable: legacy Crew databases still live in
// the owner's tree. Read them with that storage identity, while report scripts
// and their Vault/MCP calls retain the viewer's execution authority.
func (api *StreamingAPI) reportPreviewWorkspaceClient(ctx context.Context, claims *UserClaims, workspacePath string) *workspace.Client {
	userID := GetDefaultUserID()
	if claims != nil && claims.UserID != "" {
		userID = claims.UserID
	}
	if crew, ok := resolveCrewPath(ctx, userID, workspacePath); ok && crew.Rest == "" && !crew.Shared && crew.OwnerID != "" {
		userID = crew.OwnerID
	}
	return workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(userID))
}

// GET /api/workflow/report-preview/file?workspace=Workflow/x&path=db/reports/index.html
// Streams the file's bytes with a content type from its extension, so the
// preview page can fetch text and also point <img src> straight at it.
func (api *StreamingAPI) handleReportPreviewFile(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	workspacePath, err := reportPreviewWorkspace(r, claims, r.URL.Query().Get("workspace"))
	if err == nil {
		err = reportPreviewReadable(r, claims, workspacePath)
	}
	if err != nil {
		http.Error(w, err.Error(), reportPreviewWorkspaceErrorStatus(err))
		return
	}
	relative, ok := reportPreviewAllowedPath(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "path must be under db/, knowledgebase/, docs/, planning/, evaluation/, costs/ or variables/", http.StatusBadRequest)
		return
	}
	relative, err = api.resolveDashboardDocument(r, workspacePath, relative)
	if err != nil {
		dashboardError(w, err)
		return
	}
	if guard := dashboardFileGuard(claims); guard != nil && !guard.Allows(relative, false) {
		http.Error(w, "outside connection read grants", 403)
		return
	}
	full := filepath.ToSlash(filepath.Join(workspacePath, relative))
	data, err := api.reportPreviewWorkspaceClient(r.Context(), claims, workspacePath).DownloadFile(r.Context(), full)
	if err != nil {
		status := http.StatusNotFound
		if !strings.Contains(strings.ToLower(err.Error()), "not found") && !strings.Contains(err.Error(), "404") {
			status = http.StatusBadGateway
		}
		http.Error(w, fmt.Sprintf("read %s: %v", relative, err), status)
		return
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(relative)))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

// POST /api/workflow/report-preview/query {"workspace":"Workflow/x","sql":"SELECT ..."}
// Same read-only query the Report tab runs, shaped like the workspace
// service's /api/query envelope the report host expects.
func (api *StreamingAPI) handleReportPreviewQuery(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	var body struct {
		Workspace string        `json:"workspace"`
		SQL       string        `json:"sql"`
		Params    []interface{} `json:"params,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	workspacePath, err := reportPreviewWorkspace(r, claims, body.Workspace)
	if err == nil {
		err = reportPreviewReadable(r, claims, workspacePath)
	}
	if err != nil {
		http.Error(w, err.Error(), reportPreviewWorkspaceErrorStatus(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if strings.TrimSpace(body.SQL) == "" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "sql is required"})
		return
	}
	result, err := api.reportPreviewWorkspaceClient(r.Context(), claims, workspacePath).QueryAuthorizedWorkflowDB(r.Context(), workspace.QueryWorkflowDBParams{
		DBPath: filepath.ToSlash(filepath.Join(workspacePath, "db", "db.sqlite")),
		SQL:    body.SQL,
		Params: body.Params,
	})
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": result})
}

// The page and its script are served without auth (they carry nothing
// themselves; every request the page makes carries the token). Registered in
// server.go ahead of the SPA catch-all.
func (api *StreamingAPI) reportPreviewPageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := os.Stat(reportPreviewRuntimePath()); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`<!doctype html><html data-preview-state="failed"><head><title>Report preview unavailable</title></head><body>
<p>The report preview runtime is missing. Run npm run build:report-preview in frontend/ and retry.</p>
<script>window.__reportPreview={getState:function(){return {previewState:'failed',fetchErrors:['Report preview runtime is missing from the server static directory. Run npm run build:report-preview in frontend/ and retry.']}}};</script>
</body></html>`))
		return
	}
	_, _ = w.Write([]byte(reportPreviewPageHTML))
}

func (api *StreamingAPI) reportPreviewScriptHandler(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(reportPreviewRuntimePath())
	if err != nil {
		http.Error(w, "report preview runtime is not built into this server; run `npm run build:report-preview` in frontend/", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}
