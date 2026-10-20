package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/mux"
)

// Crew review over the external API and MCP (docs/design/code_product.md,
// "Crew reviewers"). The tools are the Crew inspection endpoints
// (code_admin.go) under another name: same gate (currentUserCanReviewCode,
// re-checked on every call, so removing the reviewer flag ends a token's
// access at once), same read-only handlers, same audit log. They need the
// crews:review scope on a token; a token without an admin or reviewer account
// behind it gets 403 whatever its scopes.

var externalCodeReviewTools = map[string]bool{
	"list_review_workspaces": true,
	"get_review_costs":       true,
	"list_review_files":      true,
	"read_review_file":       true,
	"list_review_chats":      true,
	"read_review_chat":       true,
	"get_review_audit":       true,
}

func isExternalCodeReviewTool(name string) bool { return externalCodeReviewTools[name] }

// claimsCanReviewCode is currentUserCanReviewCode for a catalog listing,
// which has claims but no request: it hides the tools from everyone else.
func claimsCanReviewCode(c *UserClaims) bool {
	acc := userAccessForClaims(c)
	if acc.Known {
		return (acc.Admin || acc.CrewReviewer) && !acc.Disabled
	}
	return acc.Admin
}

func externalCodeReviewDefinitions(add func(name, description string, write, scoped bool, props map[string]any, required ...string)) {
	project := func(props map[string]any) map[string]any {
		if props == nil {
			props = map[string]any{}
		}
		props["owner_id"] = externalString("Owner ID from workspace_review action=workspaces.")
		props["project_id"] = externalString("Crew project ID from workspace_review action=workspaces.")
		return props
	}
	const suffix = " Read-only; every call is recorded in the Crew review audit log. Requires crews:review and an admin or Crew reviewer account."
	add("list_review_workspaces", "List every user's Crew projects: owner, ID, title, last update, and who each is shared with."+suffix, false, false, nil)
	add("get_review_costs", "Cost and token usage of every Crew project, split by person and by model, for a date range (YYYY-MM-DD, UTC; defaults to all recorded usage)."+suffix, false, false, map[string]any{
		"from": map[string]any{"type": "string", "pattern": `^\d{4}-\d{2}-\d{2}$`},
		"to":   map[string]any{"type": "string", "pattern": `^\d{4}-\d{2}-\d{2}$`},
	})
	add("list_review_files", "List a Crew project's files. Its HOME (git and CLI logins) and dependency folders are never shown."+suffix, false, false, project(nil), "owner_id", "project_id")
	add("read_review_file", "Read one file of a Crew project (up to 512 KiB)."+suffix, false, false, project(map[string]any{"path": externalString("Workspace-relative path from list_review_files.")}), "owner_id", "project_id", "path")
	add("list_review_chats", "List a Crew project's chats: the owner's and every sharer's, newest first."+suffix, false, false, project(nil), "owner_id", "project_id")
	add("read_review_chat", "Read one chat of a Crew project: messages, tool calls and their output."+suffix, false, false, project(map[string]any{
		"session_id": externalString("Chat session ID from list_review_chats."),
		"user_id":    externalString("The chat's user_id from list_review_chats."),
	}), "owner_id", "project_id", "session_id", "user_id")
	add("get_review_audit", "Read a month of the Crew review audit log: who viewed which Crew, what, and when (admins and reviewers)."+suffix, false, false, map[string]any{
		"month": map[string]any{"type": "string", "pattern": `^\d{4}-\d{2}$`, "description": "YYYY-MM; defaults to the current month."},
	})
}

// externalCodeReviewCall runs one tool through its inspection handler.
func (api *StreamingAPI) externalCodeReviewCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	if !currentUserCanReviewCode(r) {
		externalError(w, http.StatusForbidden, "forbidden", "Crew review needs an admin or Crew reviewer account.")
		return
	}
	vars := map[string]string{"owner": externalArg(args, "owner_id"), "project_id": externalArg(args, "project_id")}
	query := url.Values{}
	var handler func(http.ResponseWriter, *http.Request)
	switch name {
	case "list_review_workspaces":
		handler = api.handleAdminListCodeWorkspaces
	case "get_review_costs":
		api.externalCodeCosts(w, r, externalArg(args, "from"), externalArg(args, "to"))
		return
	case "list_review_files":
		handler = api.handleAdminCodeFiles
	case "read_review_file":
		query.Set("path", externalArg(args, "path"))
		handler = api.handleAdminCodeFile
	case "list_review_chats":
		handler = api.handleAdminCodeChats
	case "read_review_chat":
		vars["session_id"] = externalArg(args, "session_id")
		query.Set("user", externalArg(args, "user_id"))
		handler = api.handleAdminCodeChat
	case "get_review_audit":
		if month := externalArg(args, "month"); month != "" {
			query.Set("month", month)
		}
		handler = api.handleAdminCodeAudit
	default:
		externalError(w, http.StatusNotFound, "unknown_tool", "Tool is not exposed by this API.")
		return
	}
	sub := r.Clone(r.Context())
	sub.Method = http.MethodGet
	sub.Body = http.NoBody
	sub.URL = &url.URL{Path: r.URL.Path, RawQuery: query.Encode()}
	sub = mux.SetURLVars(sub, vars)
	rec := &externalMCPRecorder{header: http.Header{}}
	handler(rec, sub)
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	if status != http.StatusOK {
		// The inspection handlers answer {"error": "..."}; give it the
		// external API's {"error": {"code", "message"}} shape.
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.body.Bytes(), &failure)
		message := strings.TrimSpace(failure.Error)
		if message == "" {
			message = http.StatusText(status)
		}
		code := map[int]string{http.StatusNotFound: "not_found", http.StatusForbidden: "forbidden", http.StatusBadRequest: "invalid_arguments", http.StatusServiceUnavailable: "audit_unavailable"}[status]
		if code == "" {
			code = "upstream_error"
		}
		externalError(w, status, code, message)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(rec.body.Bytes())
}

// externalCodeCosts is the cost overview narrowed to Crew projects.
// Reading it is audited like every other review call.
func (api *StreamingAPI) externalCodeCosts(w http.ResponseWriter, r *http.Request, from, to string) {
	if api.costLedger == nil {
		externalError(w, http.StatusServiceUnavailable, "costs_unavailable", "The cost ledger is not initialized.")
		return
	}
	if err := recordCodeAdminView(r.Context(), GetUserFromContext(r.Context()), "read_costs", "", "", strings.TrimSpace(from+".."+to)); err != nil {
		externalError(w, http.StatusServiceUnavailable, "audit_unavailable", "The Crew review audit log is unavailable.")
		return
	}
	summary, err := api.costLedger.Summarize(from, to)
	if err != nil {
		externalError(w, http.StatusBadRequest, "invalid_arguments", err.Error())
		return
	}
	overview := buildCostOverview(summary, func(id, kind string) bool {
		return kind == costOverviewKindCrew && isCrewProjectPath(id) && crewAdminInspectionAllowed(id)
	}, false)
	externalJSON(w, map[string]any{
		"from": overview.From, "to": overview.To, "total": overview.Total,
		"by_provider": overview.ByProvider, "by_model": overview.ByModel,
		"workspaces": overview.Items, "by_user": overview.ByUser, "by_bot": overview.ByBot,
	})
}
