package server

import (
	"context"
	"net/http"
	"path"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Per-workflow READ authorization for the workflow data routes.
//
// These routes take a workspace_path (or a file_path) from the query and read
// run logs, costs, learnings, Pulse state and so on straight from the shared
// workspace. Until 2026-09-29 most of them checked nothing beyond a signed-in
// user, so any account on a multi-user server could read any workflow's runs,
// and logs/file could read any file under another user's _users/<id> tree.
//
// The rule, by the root a path names:
//
//	Workflow/<name>        the workflow's access record (owner, editor or
//	                       reader) plus the per-user workflow allow-list;
//	                       dot-named folders (Workflow/.relay_releases, ...)
//	                       hold no workflow and are admin-only
//	_users/<caller>/...    the caller's own tree
//	_users/<o>/Chats/Work/projects/<id>, Crew/<id>
//	                       the Crew's owner (Crew Run-mode readers use the
//	                       mediated shared-crew endpoints, not these)
//	_users/<o>/Chats/Code/projects/<id>
//	                       anyone the Code is shared with (codeRoleFor)
//	Chats/, Downloads/, chat_history/, memories/
//	                       per-user logical paths. These handlers read the
//	                       workspace without an X-User-ID, so the workspace
//	                       service resolves them under the server's default
//	                       user: they are that user's tree, not the caller's
//	                       (costs remaps them to the caller and says so)
//	anything else          admin-only
//
// Admins read everything; a bot-route token reads only its bound workflow;
// no claims at all is the single-user (auth disabled) mode.

// logicalPathOwner says whose tree a logical per-user path names for a route.
type logicalPathOwner int

const (
	logicalPathIsDefaultUser logicalPathOwner = iota
	logicalPathIsCaller
)

var workspaceLogicalPerUserRoots = map[string]bool{"Chats": true, "Downloads": true, "chat_history": true, "memories": true}

// cleanWorkspaceReadPath validates an untrusted workspace path: no "..",
// no empty or "." segments, no backslash or NUL. A leading slash is refused
// unless trimLeading (workspace_path params, which callers have always sent
// with or without one).
func cleanWorkspaceReadPath(raw string, trimLeading bool) (string, bool) {
	raw = strings.TrimSpace(raw)
	if trimLeading {
		raw = strings.TrimPrefix(raw, "/")
	}
	raw = strings.TrimSuffix(raw, "/")
	if raw == "" || strings.HasPrefix(raw, "/") || strings.ContainsAny(raw, "\\\x00") {
		return "", false
	}
	for _, segment := range strings.Split(raw, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
	}
	if path.Clean(raw) != raw {
		return "", false
	}
	return raw, true
}

// workspaceReadAllowed reports whether claims may read the workspace data
// at raw (a workflow root, or a file below one).
func workspaceReadAllowed(ctx context.Context, claims *UserClaims, raw string, logical logicalPathOwner, trimLeading bool) bool {
	clean, ok := cleanWorkspaceReadPath(raw, trimLeading)
	if !ok {
		return false
	}
	if claims == nil {
		return true
	}
	segments := strings.Split(clean, "/")
	botRoute := claims.Provider == "bot_route"
	admin := !botRoute && userAccessForClaims(claims).Admin
	if segments[0] == "Workflow" {
		if len(segments) < 2 {
			return admin
		}
		if segments[1] == relayReleasesFolder && len(segments) >= 4 {
			return relayReleaseReadAllowed(ctx, claims, clean)
		}
		if strings.HasPrefix(segments[1], ".") {
			return admin
		}
		level, manifest := workflowAccessForWorkspacePath(ctx, claims, "Workflow/"+segments[1])
		if level == WorkflowAccessNone {
			return false
		}
		return manifest == nil || userAllowedWorkflowID(claims, manifest.ID)
	}
	if botRoute {
		return false
	}
	if admin {
		return true
	}
	caller := sanitizeUserIDForPath(claims.UserID)
	if caller == "" {
		return false
	}
	if workspaceLogicalPerUserRoots[segments[0]] {
		owner := sanitizeUserIDForPath(GetDefaultUserID())
		if logical == logicalPathIsCaller {
			owner = caller
		}
		clean = workspaceref.PhysicalPath(owner, clean)
		segments = strings.Split(clean, "/")
	}
	// A migrated crew's old spelling is the shared Crew/<id> crew: its manifest owner decides, not the folder the
	// path names (PLAT-442 step 4).
	if crewRef, found := resolveCrewPath(ctx, claims.UserID, clean); found && crewRef.Shared {
		return crewAccessFor(claims, crewRef) == crewAccessOwner
	}
	if ref := workspaceref.MustParse(clean); ref.HasOwner() || ref.IsUsersRoot() {
		if !ref.HasOwner() {
			return false
		}
		if ref.Owner() == caller {
			return true
		}
		switch root, project, ok := ref.Project(); {
		case !ok:
		case root == workspaceref.CrewProjectsRoot:
			crewRef, found := resolveCrewPath(ctx, claims.UserID, clean)
			return found && crewAccessFor(claims, crewRef) == crewAccessOwner
		case root == workspaceref.CodeProjectsRoot:
			return codeRoleFor(ctx, claims.UserID, ref.Owner(), project) != codeRoleNone
		}
		return false
	}
	switch segments[0] {
	case crewSharedRootName:
		ref, ok := resolveCrewPath(ctx, claims.UserID, clean)
		return ok && crewAccessFor(claims, ref) == crewAccessOwner
	}
	return false
}

// requireWorkflowReadAccess guards a workflow data route by the
// workspace_path it names (query string or JSON body). A request that names
// none reaches the handler, which rejects it.
func requireWorkflowReadAccess(next http.HandlerFunc) http.HandlerFunc {
	return requireWorkspaceReadParam(logicalPathIsDefaultUser, next)
}

// requireWorkflowReadAccessCallerRelative is requireWorkflowReadAccess for a
// handler that maps a logical per-user path into the caller's own tree.
func requireWorkflowReadAccessCallerRelative(next http.HandlerFunc) http.HandlerFunc {
	return requireWorkspaceReadParam(logicalPathIsCaller, next)
}

func requireWorkspaceReadParam(logical logicalPathOwner, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next(w, r)
			return
		}
		if workspacePath := requestWorkflowWorkspacePath(r); workspacePath != "" &&
			!workspaceReadAllowed(r.Context(), GetUserFromContext(r.Context()), workspacePath, logical, true) {
			writeWorkflowPermissionDenied(w, "read")
			return
		}
		next(w, r)
	}
}

// requireLogFileReadAccess guards /workflow/logs/file by the file_path it
// reads, which must be a clean workspace-relative path.
func requireLogFileReadAccess(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next(w, r)
			return
		}
		filePath := r.URL.Query().Get("file_path")
		if filePath != "" && !workspaceReadAllowed(r.Context(), GetUserFromContext(r.Context()), filePath, logicalPathIsDefaultUser, false) {
			writeWorkflowPermissionDenied(w, "read")
			return
		}
		next(w, r)
	}
}

// workspacePathsReadableBy keeps the paths claims may read, in order.
func workspacePathsReadableBy(ctx context.Context, claims *UserClaims, paths []string) []string {
	kept := make([]string, 0, len(paths))
	for _, p := range paths {
		if workspaceReadAllowed(ctx, claims, p, logicalPathIsDefaultUser, true) {
			kept = append(kept, p)
		}
	}
	return kept
}

// registerWorkflowReadRoutes registers the workflow data read routes, each
// behind its read check. Kept in one place so the router test exercises the
// exact registrations the server serves.
func registerWorkflowReadRoutes(apiRouter *mux.Router, api *StreamingAPI) {
	read := func(route string, handler http.HandlerFunc) {
		apiRouter.HandleFunc(route, requireWorkflowReadAccess(handler)).Methods("GET", "OPTIONS")
	}
	read("/workflow/pulse-module-state", api.handleGetPulseModuleState)
	read("/workflow/pulse-findings", api.handleGetPulseFindings)
	read("/workflow/pulse-reviews", api.handleGetPulseReviews)
	read("/workflow/pulse-agent-metrics", api.handleGetPulseAgentMetrics)
	read("/workflow/pulse-impact", api.handleGetPulseImpact)
	read("/workflow/pulse-context", api.handleGetPulseContext)
	read("/workspace/state", api.handleLoadWorkspaceState)
	read("/workflow/run-folders", api.handleGetRunFolders)
	read("/workflow/learnings/all", api.handleGetAllStepLearnings)
	read("/workflow/variable-groups", api.handleGetVariableGroups)
	read("/workflow/logs", api.handleGetExecutionLogs)
	read("/workflow/review-data", api.handleGetWorkflowReviewData)
	read("/workflow/builder-doc", api.handleGetBuilderDoc)
	read("/workflow/framework-health", api.handleGetFrameworkHealth)
	read("/workflow/backup", api.handleGetWorkflowBackup)
	read("/workflow/publish", api.handleGetWorkflowPublish)
	read("/workflow/notifications", api.handleGetWorkflowNotifications)
	read("/workflow/active-executions", api.handleGetActiveExecutions)
	apiRouter.HandleFunc("/workflow/costs", requireWorkflowReadAccessCallerRelative(api.handleGetCosts)).Methods("GET", "OPTIONS")
	// Read-only git view of a workspace folder (Files pane).
	apiRouter.HandleFunc("/workspace-git", requireWorkflowReadAccessCallerRelative(api.handleWorkspaceGit)).Methods("GET", "OPTIONS")
	// Local git actions (stage, unstage, discard, commit); write access is checked in the handler.
	apiRouter.HandleFunc("/workspace-git", api.handleWorkspaceGitAction).Methods("POST")
	apiRouter.HandleFunc("/workflow/logs/file", requireLogFileReadAccess(api.handleGetLogFile)).Methods("GET", "OPTIONS")
	// These check inside the handler: they name a workflow by ID, by session,
	// by a list of paths, or through a report-preview token.
	apiRouter.HandleFunc("/workflow/status", api.handleGetWorkflowStatus).Methods("GET")
	apiRouter.HandleFunc("/workflow/running/{session_id}", api.handleGetRunningWorkflow).Methods("GET")
	apiRouter.HandleFunc("/workflows/summary", api.handleGetWorkflowsSummary).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflows/overview", api.handleGetWorkflowsOverview).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/report-preview/file", api.handleReportPreviewFile).Methods("GET")
	apiRouter.HandleFunc("/workflow/report-preview/costs", api.handleReportPreviewMetrics).Methods("GET")
}

// activeExecutionsReadableBy keeps the running executions on workflows
// claims may read.
func activeExecutionsReadableBy(ctx context.Context, claims *UserClaims, executions []ActiveWorkflowExecution) []ActiveWorkflowExecution {
	kept := make([]ActiveWorkflowExecution, 0, len(executions))
	for _, exec := range executions {
		if runningExecutionVisible(ctx, claims, exec) {
			kept = append(kept, exec)
		}
	}
	return kept
}

// runningExecutionVisible: the caller's own run, or a run on a workspace the
// caller may read. A run with neither owner nor workspace is listed to
// everyone, as the running list does.
func runningExecutionVisible(ctx context.Context, claims *UserClaims, exec ActiveWorkflowExecution) bool {
	if claims == nil || (exec.UserID != "" && exec.UserID == claims.UserID) {
		return true
	}
	if exec.WorkspacePath != "" {
		return workspaceReadAllowed(ctx, claims, exec.WorkspacePath, logicalPathIsDefaultUser, true)
	}
	return exec.UserID == ""
}

// workflowIDReadable reports whether claims may read the workflow whose
// manifest ID is workflowID. An ID that names no workflow protects nothing.
func workflowIDReadable(ctx context.Context, claims *UserClaims, workflowID string) bool {
	if claims == nil {
		return true
	}
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		return false
	}
	id := strings.TrimSpace(workflowID)
	for _, item := range discovered {
		if item.Manifest != nil && strings.TrimSpace(item.Manifest.ID) == id {
			return workspaceReadAllowed(ctx, claims, item.WorkspacePath, logicalPathIsDefaultUser, true)
		}
	}
	return true
}
