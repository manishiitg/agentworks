package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// Every Crew, workflow and Relay opens on a dashboard (PLAT-750, share =
// dashboard). When its owner built none, the server writes a default one from
// what the product already declares: its purpose, its published functions as
// buttons and the Ask chat. Nothing is executed to build it. It is an ordinary
// managed dashboard the owner can edit; once edited (the marker below is gone)
// the server never overwrites it.

const defaultDashboardID = "overview"

// Two first opens at the same moment must not both create it: one lock per
// project root. Held only while the default is listed, written and published.
var defaultDashboardLocks sync.Map

func lockDefaultDashboard(root string) func() {
	value, _ := defaultDashboardLocks.LoadOrStore(root, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

var defaultDashboardMarker = regexp.MustCompile(`<!-- agentworks-default-dashboard:([0-9a-f]{16}) -->`)

// Keep an owner's edits even if their editor leaves the platform marker in
// place. Its fingerprint describes the exact generated body, not authorship.
func unmodifiedDefaultDashboard(page string) bool {
	marker := defaultDashboardMarker.FindStringSubmatchIndex(page)
	if marker == nil || page[marker[1]:] != "\n" {
		return false
	}
	sum := sha256.Sum256([]byte(page[:marker[0]]))
	return hex.EncodeToString(sum[:8]) == page[marker[2]:marker[3]]
}

type defaultDashboardFunction struct {
	Name        string
	Description string
}

type defaultDashboardInfo struct {
	Kind        string
	Title       string
	Description string
	Functions   []defaultDashboardFunction
}

func (api *StreamingAPI) registerDefaultDashboardRoutes(router *mux.Router) {
	router.HandleFunc("/dashboards/default", api.handleDefaultDashboard).Methods(http.MethodPost)
}

func defaultDashboardText(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > limit {
		s = string([]rune(s)[:limit-1]) + "…"
	}
	return s
}

// defaultDashboardHTML is the generated page: pure markup, no scripts that run
// anything. The chat panel and function buttons are the host's own widgets.
func defaultDashboardHTML(info defaultDashboardInfo) string {
	var buttons strings.Builder
	for _, fn := range info.Functions {
		name := html.EscapeString(fn.Name)
		fmt.Fprintf(&buttons, "      <button type=\"button\" data-report-action=\"%s\"><b>%s</b>", name, name)
		if fn.Description != "" {
			fmt.Fprintf(&buttons, "<small>%s</small>", html.EscapeString(defaultDashboardText(fn.Description, 140)))
		}
		buttons.WriteString("</button>\n")
	}
	functions := ""
	if buttons.Len() > 0 {
		functions = "    <h3>What it can do</h3>\n    <div class=\"fn\">\n" + buttons.String() + "    </div>\n"
	}
	description := ""
	if info.Description != "" {
		description = "    <p class=\"lead\">" + html.EscapeString(defaultDashboardText(info.Description, 600)) + "</p>\n"
	}
	body := `<!doctype html>
<html lang="en" data-mode="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + html.EscapeString(info.Title) + `</title>
<style>
  :root { color-scheme: dark; --report-mode: dark; --report-radius: 8px; --report-background:#0d1117; --report-foreground:#e6edf3; --report-card:#161b22; --report-card-foreground:#e6edf3; --report-muted:#161b22; --report-muted-foreground:#8b949e; --report-border:#30363d; --report-input:#30363d; --report-primary:#238636; --report-primary-foreground:#ffffff; --report-ring:#58a6ff;
    --bg:#0d1117; --card:#161b22; --fg:#e6edf3; --muted:#8b949e; --line:#30363d; --hover:#21262d; }
  * { box-sizing: border-box; }
  html, body { overflow-x: hidden; }
  body { margin:0; background:var(--bg); color:var(--fg); font:14px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif; }
  .layout { display:flex; min-height:600px; align-items:stretch; }
  .chat { flex:0 0 clamp(440px, 42vw, 680px); display:flex; flex-direction:column; min-height:600px; padding:16px; background:var(--card); border-right:1px solid var(--line); }
  .chat h2 { font-size:16px; font-weight:600; margin:0 0 2px; }
  .meta { color:var(--muted); font-size:12px; margin:0 0 10px; }
  #ask { flex:1 1 auto; min-height:500px; overflow:hidden; border:1px solid var(--line); border-radius:6px; }
  .main { flex:1 1 auto; min-width:0; padding:20px 24px; }
  h1 { font-size:22px; font-weight:600; margin:0 0 6px; }
  h3 { font-size:12px; font-weight:600; text-transform:uppercase; letter-spacing:.04em; color:var(--muted); margin:20px 0 8px; }
  .lead { margin:0 0 8px; color:var(--muted); max-width:70ch; }
  .fn { display:grid; grid-template-columns:repeat(auto-fit, minmax(220px, 1fr)); gap:10px; }
  button { font:inherit; text-align:left; padding:10px 12px; border:1px solid var(--line); border-radius:8px; background:var(--card); color:var(--fg); cursor:pointer; min-width:0; }
  button:hover { background:var(--hover); }
  button:disabled { opacity:.5; cursor:not-allowed; }
  button b { display:block; font-weight:600; overflow-wrap:anywhere; }
  button small { display:block; color:var(--muted); font-size:12px; margin-top:2px; }
  @media (max-width: 1000px) { .layout { flex-direction:column; min-height:0; } .chat { flex:none; height:560px; border-right:0; border-bottom:1px solid var(--line); } #ask { min-height:440px; } }
</style>
</head>
<body>
<div class="layout">
  <section class="chat">
    <h2>Ask</h2>
    <div class="meta">Ask a question in plain words.</div>
    <section id="ask" data-report-chat-panel data-report-chat-fit="viewport"></section>
  </section>
  <div class="main">
    <h1>` + html.EscapeString(info.Title) + `</h1>
` + description + functions + `  </div>
</div>
</body>
</html>
`
	sum := sha256.Sum256([]byte(body))
	return body + "<!-- agentworks-default-dashboard:" + hex.EncodeToString(sum[:8]) + " -->\n"
}

// defaultDashboardInfoFor reads only what the product declares.
func (api *StreamingAPI) defaultDashboardInfoFor(ctx context.Context, claims *UserClaims, target dashboardTarget) (defaultDashboardInfo, error) {
	info := defaultDashboardInfo{Kind: target.Kind, Title: target.Title}
	switch target.Kind {
	case "crew", "code":
		origin := crewProfileID
		if target.Kind == "code" {
			origin = "code"
		}
		crew, manifest, summary, ok := api.externalCrewResolve(ctx, claims, workspaceExternalID(origin, target.ID))
		if !ok {
			return info, dashboardFailure(404, "project unavailable")
		}
		info.Description = manifest.Description
		label := target.Title
		if name, _ := summary["name"].(string); name != "" {
			label = name
		}
		for _, fn := range externalCrewFunctionSummaries(ctx, crew, manifest, label) {
			name, _ := fn["name"].(string)
			description, _ := fn["description"].(string)
			if name != "" {
				info.Functions = append(info.Functions, defaultDashboardFunction{Name: name, Description: description})
			}
		}
	case "workflow", "relay":
		_, manifest := workflowAccessForWorkspacePath(ctx, claims, target.Root)
		if manifest == nil {
			return info, dashboardFailure(404, "project unavailable")
		}
		info.Description = manifest.Description
		if target.Kind == "workflow" {
			for _, fn := range workflowFunctions(manifest) {
				info.Functions = append(info.Functions, defaultDashboardFunction{Name: fn.Name, Description: fn.Description})
			}
		}
	default:
		return info, dashboardFailure(404, "no default dashboard for this kind of project")
	}
	return info, nil
}

// ensureDefaultDashboard returns the dashboard a person opens for this
// product: the owner's own published dashboard when there is one, otherwise the
// generated default, created or refreshed to match the product's declarations.
func (api *StreamingAPI) ensureDefaultDashboard(ctx context.Context, claims *UserClaims, target dashboardTarget) (string, bool, error) {
	defer lockDefaultDashboard(target.Root)()
	listed, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, Action: "list", IncludeDraft: true})
	if err != nil {
		return "", false, err
	}
	var existing *dashboards.Dashboard
	for i, d := range listed.Dashboards {
		if d.ID == defaultDashboardID {
			existing = &listed.Dashboards[i]
			continue
		}
		if d.Managed && d.Published != "" {
			return d.ID, false, nil
		}
	}
	info, err := api.defaultDashboardInfoFor(ctx, claims, target)
	if err != nil {
		return "", false, err
	}
	page := defaultDashboardHTML(info)
	files := map[string]string{"index.html": page}
	if existing != nil {
		current, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, Action: "get", ID: defaultDashboardID, IncludeDraft: true})
		if err != nil {
			return "", false, err
		}
		old := ""
		if current.Snapshot != nil {
			old = current.Snapshot.Files["index.html"]
		}
		if !unmodifiedDefaultDashboard(old) {
			// The owner edited it: it is theirs now.
			if existing.Published != "" {
				return defaultDashboardID, false, nil
			}
			return "", false, dashboardFailure(404, "dashboard is not published")
		}
		if old == page && existing.Published != "" {
			return defaultDashboardID, false, nil
		}
		updated, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, Action: "update", ID: defaultDashboardID, Title: target.Title, Files: files, ExpectedRevision: existing.Revision, IncludeDraft: true})
		if err != nil {
			return "", false, err
		}
		if updated.Dashboard == nil {
			return "", false, dashboardFailure(500, "dashboard update failed")
		}
		if _, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, Action: "publish", ID: defaultDashboardID, ExpectedRevision: updated.Dashboard.Revision, IncludeDraft: true}); err != nil {
			return "", false, err
		}
		log.Printf("[DASHBOARD_DEFAULT] default dashboard refreshed by the platform (not authored by the person who opened it): kind=%s root=%s", target.Kind, target.Root)
		return defaultDashboardID, true, nil
	}
	created, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, Action: "create", ID: defaultDashboardID, Title: target.Title, Files: files, IncludeDraft: true})
	if err != nil {
		return "", false, err
	}
	if created.Dashboard == nil {
		return "", false, dashboardFailure(500, "dashboard create failed")
	}
	if _, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, Action: "publish", ID: defaultDashboardID, ExpectedRevision: created.Dashboard.Revision, IncludeDraft: true}); err != nil {
		return "", false, err
	}
	log.Printf("[DASHBOARD_DEFAULT] default dashboard generated by the platform (not authored by the person who opened it): kind=%s root=%s", target.Kind, target.Root)
	return defaultDashboardID, true, nil
}

// Publication is a Builder action. Refresh only the platform's generated
// overview after its declared functions change; authored pages and unfinished
// drafts remain the person's, including dashboards in immutable source.
func (api *StreamingAPI) refreshPublicationDashboard(ctx context.Context, claims *UserClaims, target dashboardTarget) error {
	publication, err := readPublishedProject(ctx, target.Root)
	if err != nil {
		return err
	}
	if publishedDashboardSource(publication) {
		return nil
	}
	listed, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, Action: "list", IncludeDraft: true})
	if err != nil {
		return err
	}
	for _, dashboard := range listed.Dashboards {
		if !dashboard.Managed || dashboard.ID != defaultDashboardID {
			return nil
		}
		current, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, Action: "get", ID: defaultDashboardID, IncludeDraft: true})
		if err != nil {
			return err
		}
		if current.Snapshot == nil || !unmodifiedDefaultDashboard(current.Snapshot.Files["index.html"]) {
			return nil
		}
	}
	_, _, err = api.ensureDefaultDashboard(ctx, claims, target)
	return err
}

func (api *StreamingAPI) handleDefaultDashboard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Workspace string `json:"workspace"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || body.Workspace == "" {
		externalError(w, 400, "invalid_arguments", "workspace is required")
		return
	}
	claims := GetUserFromContext(r.Context())
	w.Header().Set("Cache-Control", "no-store")
	if claims == nil || claims.UserID == "" || claims.Scope != "" || claims.Provider == "bot_route" || !dashboardScopeAllowed(claims, "get") {
		externalError(w, 403, "insufficient_scope", "dashboard access required")
		return
	}
	target, err := api.dashboardTarget(r.Context(), claims, body.Workspace)
	if err != nil {
		dashboardError(w, err)
		return
	}
	id, generated, err := api.ensureDefaultDashboard(r.Context(), claims, target)
	if err != nil {
		dashboardError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"dashboard_id": id, "generated": generated})
}
