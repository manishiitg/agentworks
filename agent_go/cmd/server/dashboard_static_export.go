package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// A static host receives only an explicitly exported publication and this
// Builder's authorized data. It never receives a preview token or live API.
func (api *StreamingAPI) dashboardStaticExport(w http.ResponseWriter, r *http.Request, target dashboardTarget, options dashboardExportOptions) {
	claims := GetUserFromContext(r.Context())
	if !dashboardAuthoringAllowed(claims, &target) {
		externalError(w, 403, "forbidden", "Ask this project's Builder to export its dashboard.")
		return
	}
	options.SourceMode = "dashboard"
	options, _, err := normalizeDashboardExport(options, "")
	if err != nil {
		dashboardError(w, err)
		return
	}
	options, err = resolveDashboardExportSource(r.Context(), target, options)
	if err != nil {
		dashboardError(w, err)
		return
	}
	id := uuid.NewString()
	output := "publish/exports/" + id
	if guard := dashboardFileGuard(claims); guard != nil && (!guard.Allows(options.Document, false) || !guard.Allows(output, true)) {
		externalError(w, 403, "protected_path", "This connection needs read access to the published dashboard and write access to publish/exports.")
		return
	}
	files := map[string]string{}
	for _, name := range []string{"report-preview.js", "dashboard-react.js", "dashboard.css"} {
		data, readErr := os.ReadFile(filepath.Join(staticFrontendDir(), name))
		if readErr != nil {
			externalError(w, 503, "runtime_unavailable", "The dashboard export runtime is unavailable. Prepare the platform frontend assets and try again.")
			return
		}
		files[name] = string(data)
	}
	base := api.reportPreviewResultBaseURL()
	liveURL, err := parseDashboardURL(sharedAssetPublicURLForUser(base, "report", target.Root, ""), options.Document)
	if err != nil {
		dashboardError(w, err)
		return
	}
	token, err := mintReportPreviewToken(claims, target.Root, options.Document)
	if err != nil {
		dashboardError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	session := "dashboard-static-" + id
	common.SetSessionWorkingDir(session, target.Root)
	common.SetSessionFolderGuard(session, []string{target.Root + "/"}, []string{path.Join(target.Root, output) + "/"})
	if parent, _ := ctx.Value(common.ChatSessionIDKey).(string); parent != "" {
		if config := common.GetSessionShellConfig(parent); config != nil {
			common.SetSessionFolderGuardBlockedPaths(session, config.BlockedPaths)
			common.SetSessionFolderGuardBlockedWritePaths(session, config.BlockedWritePaths)
		}
	}
	common.BindSessionBrowserIsolationForSession(session)
	defer common.ClearSessionShellConfig(session)
	ctx = context.WithValue(ctx, common.UserIDKey, claims.UserID)
	ctx = context.WithValue(ctx, common.ChatSessionIDKey, session)
	ctx = context.WithValue(ctx, common.WorkflowSessionIDKey, session)
	ctx = context.WithValue(ctx, common.BrowserDownloadsPathKey, target.Root)
	executor := browser.NewExecutor(browser.NewClient(getWorkspaceAPIURL()), browser.WithBrowserRuntimeConfig(browser.NewBrowserRuntimeConfig("headless", nil)))
	run := func(command string, args ...string) (string, error) {
		return executor.HandleAgentBrowser(ctx, map[string]interface{}{"command": command, "args": args, "session": session})
	}
	defer func() { _, _ = run("close") }()
	previewURL := strings.TrimRight(api.GetCodeExecAPIURL(), "/") + reportPreviewPagePath + "?" + url.Values{
		"workspace": {target.Root}, "document": {options.Document}, "token": {token}, "theme": {options.Theme}, "width": {fmt.Sprint(options.Width)}, "export": {"1"}, "static": {"1"},
	}.Encode()
	if _, err = run("open", previewURL); err != nil {
		externalError(w, 502, "export_failed", "The dashboard export browser could not start.")
		return
	}
	state := "loading"
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline) && state == "loading"; {
		raw, evalErr := run("eval", "document.documentElement.dataset.previewState || 'loading'")
		if evalErr == nil {
			if parsed, parseErr := parseBrowserEvalOutput(raw); parseErr == nil {
				state = strings.Trim(parsed, `"`)
			}
		}
		if state != "loading" {
			break
		}
		select {
		case <-ctx.Done():
			dashboardError(w, ctx.Err())
			return
		case <-time.After(reportPreviewPollInterval):
		}
	}
	if state != "ready" {
		log.Printf("[DASHBOARD_STATIC_EXPORT] dashboard=%s caller=%s state=%s", options.Dashboard, claims.UserID, state)
		externalError(w, 422, "export_failed", "The dashboard did not load successfully. Check its dashboard preview before exporting.")
		return
	}
	raw, err := run("eval", fmt.Sprintf("window.__reportPreview.exportStatic(%q)", liveURL))
	if err == nil {
		files["index.html"], err = parseBrowserEvalOutput(raw)
	}
	if err != nil || !strings.HasPrefix(files["index.html"], "<!doctype html>") || strings.Contains(files["index.html"], token) {
		externalError(w, 422, "export_failed", "The dashboard data could not be frozen into a static snapshot. Check its preview and data sources.")
		return
	}
	client := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(claims.UserID))
	total := 0
	for name, content := range files {
		if guard := dashboardFileGuard(claims); guard != nil && !guard.Allows(path.Join(output, name), true) {
			externalError(w, 403, "protected_path", "This connection cannot save every static export file.")
			return
		}
		total += len(content)
		if total > 64<<20 {
			externalError(w, 422, "export_too_large", "The static dashboard exceeds the 64 MB export limit.")
			return
		}
		result, err := client.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: path.Join(target.Root, output, name), Content: content})
		if err != nil || !result.Success {
			externalError(w, 502, "export_failed", "Could not save the static dashboard. Check this connection's write access.")
			return
		}
	}
	log.Printf("[DASHBOARD_STATIC_EXPORT] dashboard=%s revision=%s caller=%s output=%s bytes=%d status=completed", options.Dashboard, options.Revision, claims.UserID, output, total)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "path": output, "entry": path.Join(output, "index.html"), "dashboard_id": options.Dashboard, "revision": options.Revision, "live_url": liveURL, "bytes": total, "files": []string{"index.html", "report-preview.js", "dashboard-react.js", "dashboard.css"}, "note": "Deploy this whole folder to the configured static host. It contains the publishing Builder's frozen data; republish to refresh. Live actions open the signed-in dashboard."})
}
