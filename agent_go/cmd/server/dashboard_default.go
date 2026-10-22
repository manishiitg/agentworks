package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
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
}

func defaultDashboardText(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > limit {
		s = string([]rune(s)[:limit-1]) + "…"
	}
	return s
}

// defaultDashboardHTML is the generated page: pure markup, no scripts that run
// anything. The Ask button and function buttons are the host's own widgets.
func defaultDashboardHTML(info defaultDashboardInfo) (string, error) {
	elements := map[string]dashboards.JSONElement{
		"page":  {Type: "Stack", Props: map[string]any{"gap": "lg", "className": "dashboard-home"}, Children: []string{"title"}},
		"title": {Type: "Heading", Props: map[string]any{"text": info.Title, "level": "h1"}, Children: []string{}},
	}
	children := []string{"title"}
	if info.Description != "" {
		elements["purpose"] = dashboards.JSONElement{Type: "Text", Props: map[string]any{"text": defaultDashboardText(info.Description, 600)}, Children: []string{}}
		children = append(children, "purpose")
	}
	if len(info.Functions) > 0 {
		elements["actions-title"] = dashboards.JSONElement{Type: "Heading", Props: map[string]any{"text": "What it can do", "level": "h3"}, Children: []string{}}
		actions := []string{}
		for i, fn := range info.Functions {
			id := fmt.Sprintf("function-%d", i)
			actions = append(actions, id)
			elements[id] = dashboards.JSONElement{Type: "FunctionButton", Props: map[string]any{"name": fn.Name, "label": fn.Name, "className": "dashboard-button"}, Children: []string{}}
			if fn.Description != "" {
				description := id + "-description"
				card := id + "-card"
				elements[description] = dashboards.JSONElement{Type: "Text", Props: map[string]any{"text": defaultDashboardText(fn.Description, 140), "variant": "muted"}, Children: []string{}}
				elements[card] = dashboards.JSONElement{Type: "Stack", Props: map[string]any{"gap": "sm"}, Children: []string{id, description}}
				actions[len(actions)-1] = card
			}
		}
		elements["actions"] = dashboards.JSONElement{Type: "Grid", Props: map[string]any{"columns": 2, "gap": "sm"}, Children: actions}
		children = append(children, "actions-title", "actions")
	}
	elements["ask"] = dashboards.JSONElement{Type: "AskButton", Props: map[string]any{"label": "Ask about this dashboard"}, Children: []string{}}
	children = append(children, "ask")
	root := elements["page"]
	root.Children = children
	elements["page"] = root
	spec := dashboards.JSONSpec{Root: "page", Elements: elements, Styles: ".dashboard-home{max-width:1200px;margin:0 auto;padding:24px} @media(max-width:600px){.dashboard-home{padding:16px}}"}
	data, _ := json.Marshal(spec)
	body, err := dashboards.JSONHTML(info.Title, data)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(body))
	return body + "<!-- agentworks-default-dashboard:" + hex.EncodeToString(sum[:8]) + " -->\n", nil
}

func (api *StreamingAPI) defaultDashboardInfoFor(ctx context.Context, claims *UserClaims, target dashboardTarget) (defaultDashboardInfo, error) {
	info := defaultDashboardInfo{Kind: target.Kind, Title: target.Title}
	switch target.Kind {
	case "crew":
		origin := crewProfileID

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

// liveDefaultDashboard renders only the platform's untouched automatic page.
// Authored pages and explicitly selected snapshots remain exactly as saved.
func (api *StreamingAPI) liveDefaultDashboard(ctx context.Context, claims *UserClaims, root, document string, data []byte) ([]byte, bool, error) {
	if document != dashboards.Document(defaultDashboardID, "") || !unmodifiedDefaultDashboard(string(data)) || (claims != nil && claims.Scope == reportPreviewScope) {
		return data, false, nil
	}
	if guard := dashboardFileGuard(claims); guard != nil && !guard.Allows(document, false) {
		// A grant for one saved revision must not become a live-page grant.
		return data, false, nil
	}
	target, err := api.dashboardTarget(ctx, claims, root)
	if err != nil {
		return nil, false, err
	}
	publication, err := readPublishedProject(ctx, root)
	if err != nil {
		return storedDefaultDashboard(data, root, err)
	}
	if publishedDashboardSource(publication) {
		return data, false, nil
	}
	info, err := api.defaultDashboardInfoFor(ctx, claims, target)
	if err != nil {
		return storedDefaultDashboard(data, root, err)
	}
	log.Printf("[DASHBOARD_DEFAULT] live automatic page: kind=%s root=%s functions=%d", target.Kind, target.Root, len(info.Functions))
	page, err := defaultDashboardHTML(info)
	return []byte(page), true, err
}

// An unavailable declaration store does not take the saved Ask page down.
// Current access refusals still win over the previously published bytes.
func storedDefaultDashboard(data []byte, root string, err error) ([]byte, bool, error) {
	status := wf.StatusCode(err)
	if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound {
		return nil, false, err
	}
	log.Printf("[DASHBOARD_DEFAULT] live declarations unavailable; serving stored automatic page: root=%s status=%d error=%v", root, status, err)
	return data, false, nil
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
	page, err := defaultDashboardHTML(info)
	if err != nil {
		return "", false, err
	}
	spec, _, err := dashboards.JSONSpecFromHTML(page)
	if err != nil {
		return "", false, err
	}
	source, err := json.Marshal(spec)
	if err != nil {
		return "", false, err
	}
	files := map[string]string{"index.json": string(source), "index.html": page}
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
