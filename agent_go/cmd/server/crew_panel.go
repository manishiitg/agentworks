package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
)

// PLAT-856-542: one read for the Crew menu. Detail/edit APIs keep their own
// contracts. These readers call those same guarded handlers, never a new
// account/Team policy. Only display fields are retained in the response/cache.
type crewPanelRow struct {
	nextRun     time.Time // Server-only ordering for the automation summary.
	ID          string    `json:"id"`
	Label       string    `json:"label"`
	Detail      string    `json:"detail,omitempty"`
	Destination string    `json:"destination,omitempty"`
	Connected   bool      `json:"connected,omitempty"`
	Attention   bool      `json:"attention,omitempty"`
	Path        string    `json:"path,omitempty"`
	UsedInChat  bool      `json:"used_in_chat,omitempty"`
	Motion      bool      `json:"motion,omitempty"`
	Artifact    bool      `json:"artifact,omitempty"`
}
type crewPanelSection struct {
	ID     string           `json:"id"`
	Title  string           `json:"title"`
	Detail string           `json:"detail,omitempty"`
	Rows   []crewPanelRow   `json:"rows"`
	Busy   bool             `json:"busy,omitempty"`
	Stale  bool             `json:"stale,omitempty"`
	Error  string           `json:"error,omitempty"`
	Access *crewPanelAccess `json:"access,omitempty"`
	// shared is the dashboard's audience in the Share tree's words (the sharing section only).
	shared string
}
type crewPanelAccess struct {
	Where   string           `json:"where,omitempty"`
	Place   string           `json:"place,omitempty"`
	Role    string           `json:"role,omitempty"`
	View    string           `json:"view,omitempty"`
	Summary string           `json:"summary,omitempty"`
	Tree    []crewAccessLine `json:"tree"`
	Asking  []crewPanelAsk   `json:"asking"`
}
type crewPanelAsk struct {
	macAskActivity
	Label  string `json:"label"`
	Detail string `json:"detail"`
}
type crewPanelReply struct {
	Details map[string]string `json:"details"`

	Project      string             `json:"project"`
	Workspace    string             `json:"workspace"`
	Busy         bool               `json:"busy"`
	MacElsewhere bool               `json:"mac_elsewhere"`
	Sections     []crewPanelSection `json:"sections"`
}
type crewPanelSource struct {
	id, title string
	allowed   bool
	noCache   bool // Cross-service permissions cannot be rechecked from the Crew record alone.
	// timeout, when set, is this section's own time limit instead of the panel's (a Git status over several repos).
	timeout time.Duration
	read    func(context.Context) (crewPanelSection, error)
}
type crewPanelReadError struct{ status int }

func (e crewPanelReadError) Error() string { return "Panel section unavailable" }

const crewPanelTimeout = 2 * time.Second
const crewPanelCacheTTL = 30 * time.Second

type crewPanelCacheKey struct{ caller, project, section, chat, authority string }
type crewPanelCached struct {
	at      time.Time
	section crewPanelSection
}
type crewPanelCache struct {
	mu      sync.Mutex
	entries map[crewPanelCacheKey]crewPanelCached
}

func (c *crewPanelCache) put(key crewPanelCacheKey, section crewPanelSection) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[crewPanelCacheKey]crewPanelCached{}
	}
	now := time.Now()
	for k, v := range c.entries {
		if now.Sub(v.at) >= crewPanelCacheTTL {
			delete(c.entries, k)
		}
	}
	section.Rows = append([]crewPanelRow{}, section.Rows...)
	c.entries[key] = crewPanelCached{now, section}
}
func (c *crewPanelCache) previous(key crewPanelCacheKey) (crewPanelSection, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.entries[key]
	v.section.Rows = append([]crewPanelRow{}, v.section.Rows...)
	return v.section, ok && time.Since(v.at) < crewPanelCacheTTL
}
func (c *crewPanelCache) remove(key crewPanelCacheKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// Deadline bounds the whole fan-out, not one timeout multiplied by the number
// of sections. A timed-out reader cannot write back late or replace newer data.
func (c *crewPanelCache) collect(ctx context.Context, key crewPanelCacheKey, sources []crewPanelSource, timeout time.Duration) []crewPanelSection {
	// The panel waits for its slowest section; each section stops at its own limit.
	longest := timeout
	for _, source := range sources {
		if source.allowed && source.timeout > longest {
			longest = source.timeout
		}
	}
	ctx, cancel := context.WithTimeout(ctx, longest)
	defer cancel()
	type result struct {
		index   int
		section crewPanelSection
		err     error
	}
	results := make(chan result, len(sources))
	out := make([]crewPanelSection, len(sources))
	pending := map[int]bool{}
	for i, source := range sources {
		out[i] = crewPanelSection{ID: source.id, Title: source.title, Rows: []crewPanelRow{}}
		k := key
		k.section = source.id
		if !source.allowed {
			c.remove(k)
			continue
		}
		pending[i] = true
		go func(i int, source crewPanelSource) {
			limit := timeout
			if source.timeout > 0 {
				limit = source.timeout
			}
			sourceCtx, done := context.WithTimeout(ctx, limit)
			defer done()
			section, err := source.read(sourceCtx)
			if err == nil && sourceCtx.Err() != nil {
				err = sourceCtx.Err()
			}
			results <- result{i, section, err}
		}(i, source)
	}
	finish := func(i int, section crewPanelSection, err error) {
		k := key
		k.section = sources[i].id
		if err == nil {
			section.ID, section.Title = sources[i].id, sources[i].title
			if section.Rows == nil {
				section.Rows = []crewPanelRow{}
			}
			if section.Error == "" && !sources[i].noCache {
				c.put(k, section)
			}
			out[i] = section
		} else {
			denied, ok := err.(crewPanelReadError)
			// Which section shows "Try again", and why: the panel answer alone could not tell (5 Nov, a server Crew).
			log.Printf("[CREW_PANEL] project=%s section=%s result=failed status=%d reason=%q", key.project, sources[i].id, denied.status, err)
			if sources[i].noCache || (ok && denied.status >= 400 && denied.status < 500) {
				c.remove(k)
			} else if old, found := c.previous(k); found {
				// A slow refresh keeps the last good answer, marked stale, rather than asking to try again.
				out[i] = old
				out[i].Stale = true
				delete(pending, i)
				return
			}
			out[i].Error = "Could not refresh. Try again."
		}
		delete(pending, i)
	}
	for len(pending) > 0 {
		select {
		case result := <-results:
			finish(result.index, result.section, result.err)
		case <-ctx.Done():
			for i := range pending {
				finish(i, crewPanelSection{}, ctx.Err())
			}
		}
	}
	return out
}

// Authorization is read both before fan-out and before exposing any result,
// including a cached one. Changes to the caller's role or Crew selections get a
// different cache key. Scoped tool tokens use the ordinary detail APIs.
func (api *StreamingAPI) crewPanelScope(r *http.Request, project string) (crewProjectBinding, productProjectManifest, string, bool) {
	claims := GetUserFromContext(r.Context())
	if !websiteDeviceClaims(claims) || !externalWorkspaceAllowed(claims) {
		return crewProjectBinding{}, productProjectManifest{}, "", false
	}
	binding, doc, ok := api.resolveExternalWorkspace(r.Context(), claims, project)
	if !ok {
		return binding, doc, "", false
	}
	team, _ := readCrewTeam(project)
	authority, _ := json.Marshal([]any{binding.OwnerID, binding.OwnedByCaller, userAccessForClaims(claims), doc, team.On, team.People})
	return binding, doc, crewTransferDigest(string(authority)), true
}
func (api *StreamingAPI) handleCrewPanel(w http.ResponseWriter, r *http.Request) {
	project := mux.Vars(r)["project"]
	binding, _, authority, ok := api.crewPanelScope(r, project)
	if !ok {
		externalError(w, 403, "forbidden", "Open a Crew you can use.")
		return
	}
	workspace := binding.Binding.WorkspacePath
	chat := strings.TrimSpace(r.URL.Query().Get("session_id"))
	// Private chat navigation hints have their own permission, independent of files.
	if chat != "" {
		if api.eventStore == nil {
			chat = ""
		} else if status, _ := api.authorizeDurableChatRead(r, chat, workspace); status != 0 {
			chat = ""
		}
	}
	sources := api.crewPanelSources(r, project, workspace, chat, binding)
	key := crewPanelCacheKey{caller: GetUserIDFromContext(r.Context()), project: project, chat: chat, authority: authority}
	sections := api.crewPanels.collect(r.Context(), key, sources, crewPanelTimeout)
	_, _, current, allowed := api.crewPanelScope(r, project)
	if !allowed || current != authority {
		externalError(w, 409, "panel_changed", "Crew access changed. Refresh this Crew.")
		return
	}
	if chat != "" {
		if status, _ := api.authorizeDurableChatRead(r, chat, workspace); status != 0 {
			for i := range sections {
				for j := range sections[i].Rows {
					sections[i].Rows[j].UsedInChat = false
				}
			}
		}
	}
	out := crewPanelReply{Project: project, Workspace: workspace, Sections: sections, Details: map[string]string{}}
	api.activeSessionsMux.RLock()
	for _, session := range api.activeSessions {
		if session.WorkspacePath == workspace && (session.Status == "running" || session.Status == "pending") {
			out.Busy = true
			break
		}
	}
	api.activeSessionsMux.RUnlock()
	if crewTeamSyncActive(project) {
		out.Busy = true
	}
	for _, section := range sections {
		out.Busy = out.Busy || section.Busy
		if section.Access != nil {
			out.MacElsewhere = !IsLocalMode() && (section.Access.Place == "mac" || section.Access.Place == "mac_off")
		}
	}
	// Access wording includes sharing only when the tree has no richer line.
	sharing := ""
	for _, section := range sections {
		if section.ID == "sharing" && section.Error == "" {
			sharing = section.Detail
		}
	}
	for i := range out.Sections {
		section := &out.Sections[i]
		if section.Access != nil && section.Access.Place != "offline" && section.Access.Summary == "" && len(section.Access.Tree) == 0 && sharing != "" {
			section.Detail = strings.Trim(section.Detail+" · "+sharing, " ·")
		}
	}
	crewPanelDashboardLine(out.Sections)
	out.Details = crewPanelDetails(sections)
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, out)
}

// crewPanelDashboardLine adds the Crew's dashboard to the Share tree, from the panel's own dashboards and sharing
// reads (owner, 5 Nov: the side menu's separate Dashboards item moved here).
func crewPanelDashboardLine(sections []crewPanelSection) {
	var access *crewPanelAccess
	shared, listed, paths := "", false, []string{}
	for i := range sections {
		switch s := &sections[i]; s.ID {
		case "access":
			access = s.Access
		case "sharing":
			if s.Error == "" {
				shared = s.shared
			}
		case "dashboards":
			listed = s.Error == ""
			for _, row := range s.Rows {
				if row.Path != "" {
					paths = append(paths, row.Path)
				}
			}
		}
	}
	if access == nil || !listed {
		return
	}
	access.Tree = crewAccessWithDashboard(access.Tree, crewAccessDashboardLine(paths, shared))
}

func crewPanelRead(r *http.Request, handler http.HandlerFunc, method, route string, vars map[string]string, query url.Values, body, out any) error {
	status, raw := scheduleHandler(r, handler, method, route, vars, query, body)
	if status < 200 || status >= 300 {
		return crewPanelReadError{status}
	}
	if err := r.Context().Err(); err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
func crewPanelCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
func crewPanelName(root, workspace string) string {
	if root == "" {
		root = workspace
	}
	return path.Base(root)
}

// crewPanelAutomationLine is one kind's summary line in the menu: how many are on, how many paused, whether one is
// running now, and when the next scheduled run is ("2 on · 1 paused · next 9 Nov 09:00 IST").
func crewPanelAutomationLine(rows []crewPanelRow) string {
	on, paused, running := 0, 0, 0
	var next *crewPanelRow
	now := time.Now()
	for i := range rows {
		row := &rows[i]
		if row.Detail == "Paused" || row.Detail == "Completed" {
			paused++
		} else {
			on++
		}
		if row.Detail == "Running now" {
			running++
		}
		if row.nextRun.After(now) && (next == nil || row.nextRun.Before(next.nextRun)) {
			next = row
		}
	}
	parts := []string{}
	if on > 0 {
		parts = append(parts, fmt.Sprintf("%d on", on))
	}
	if paused > 0 {
		parts = append(parts, fmt.Sprintf("%d paused", paused))
	}
	if running > 0 {
		parts = append(parts, "running now")
	}
	if next != nil {
		parts = append(parts, "next "+strings.TrimPrefix(next.Detail, "Next · "))
	}
	return strings.Join(parts, " · ")
}

// crewPanelAgo says how long ago in plain words ("just now", "5 min ago", "2h ago", "3d ago").
func crewPanelAgo(since time.Duration) string {
	switch {
	case since < time.Minute:
		return "just now"
	case since < time.Hour:
		return fmt.Sprintf("%d min ago", int(since.Minutes()))
	case since < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(since.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(since.Hours()/24))
}

// crewPanelWorkflowLine is one workflow's summary from its newest run: "4 steps · last run finished 2h ago".
// folders arrive newest first.
func crewPanelWorkflowLine(folders []RunFolderInfo, now time.Time) string {
	if len(folders) == 0 || folders[0].Metadata == nil {
		return "Not run yet"
	}
	meta := folders[0].Metadata
	parts := []string{}
	if n := len(meta.PythonSteps); n > 0 {
		parts = append(parts, crewPanelCount(n, "step"))
	}
	at, _ := runFolderTime(folders[0])
	if meta.CompletedAt != nil {
		at = *meta.CompletedAt
	}
	when := ""
	if !at.IsZero() {
		when = " " + crewPanelAgo(now.Sub(at))
	}
	switch meta.Status {
	case "running":
		parts = append(parts, "running now")
	case "completed":
		parts = append(parts, "last run finished"+when)
	case "failed":
		parts = append(parts, "last run failed"+when)
	case "canceled":
		parts = append(parts, "last run canceled"+when)
	default:
		parts = append(parts, "not run yet")
	}
	return strings.Join(parts, " · ")
}

// crewPanelInboxRow is the Crew's incoming-email row: "On · N messages this week" or "Off".
// The access checks are agentEmailCrew's and agentEmailInboxResult's, not repeated here.
func (api *StreamingAPI) crewPanelInboxRow(ctx context.Context, workspace string) (crewPanelRow, error) {
	row := crewPanelRow{ID: "inbox", Label: "Inbox", Destination: "schedules:email", Detail: "Off"}
	_, enabled, err := api.agentEmailCrew(ctx, workspace)
	if err != nil {
		return row, err
	}
	if !enabled {
		return row, nil
	}
	row.Connected = true
	out, err := api.agentEmailInboxResult(ctx, map[string]interface{}{"workspace_path": workspace, "recent": true})
	if err != nil {
		// On, but the owner's Gmail is not ready: say so instead of failing the whole menu.
		row.Detail, row.Attention = "On · needs a Gmail account", true
		return row, nil
	}
	result, _ := out.(map[string]any)
	if inbox, _ := result["inbox"].(gmailinbound.Inbox); inbox.ID == "" {
		// The inbox could not be provisioned (no Gmail for the owner yet): the page still answers, so say so here too.
		row.Detail, row.Attention = "On · inbox setup failed", true
		if missing, _ := result["gmail_missing"].(bool); missing {
			row.Detail = "On · needs a Gmail account"
		}
		return row, nil
	}
	page, _ := result["mail"].(gmailinbound.Page)
	week := 0
	for _, item := range page.Messages {
		if item.ReceivedAt > time.Now().Add(-7*24*time.Hour).UnixMilli() {
			week++
		}
	}
	count := crewPanelCount(week, "message")
	if page.HasMore && week == len(page.Messages) {
		count = fmt.Sprintf("%d+ messages", week)
	}
	// The address is on the Inbox page; the menu line stays short (approved mock, 8 Nov).
	row.Detail = "On · " + count + " this week"
	return row, nil
}

func crewPanelDetails(sections []crewPanelSection) map[string]string {
	details := map[string]string{}
	groups := map[string][]crewPanelRow{}
	automations := []crewPanelRow{}
	workflows := []crewPanelRow{}
	for _, section := range sections {
		for _, row := range section.Rows {
			if row.Destination == "workflow" {
				workflows = append(workflows, row)
				continue
			}
			// Incoming email is listed under Automations always, but counts as one only while it is on.
			if row.Destination == "schedules:email" {
				details[row.Destination] = row.Detail
				if row.Connected {
					automations = append(automations, row)
				}
				continue
			}
			groups[row.Destination] = append(groups[row.Destination], row)
			if strings.HasPrefix(row.Destination, "schedules:") {
				automations = append(automations, row)
			}
		}
	}
	for destination, rows := range groups {
		// The left menu's Automations group lists one row per kind (Schedules, Watchers, Webhooks) with a plain summary.
		if strings.HasPrefix(destination, "schedules:") {
			details[destination] = crewPanelAutomationLine(rows)
			continue
		}
		if !strings.HasPrefix(destination, "mcp:") {
			continue
		}
		connected, attention := 0, 0
		for _, row := range rows {
			if row.Connected {
				connected++
			}
			if row.Attention {
				attention++
			}
		}
		label := "Not connected"
		if connected > 0 {
			label = "Connected"
		} else if attention > 0 {
			label = "Needs attention"
		}
		if len(rows) > 1 || destination == "mcp:connected" {
			label = fmt.Sprintf("%d connected", connected)
			if attention > 0 {
				label += fmt.Sprintf(" · %d need attention", attention)
			}
		}
		details[destination] = label
	}
	// A kind whose list loaded and is empty says so, instead of showing nothing.
	for _, kind := range []struct{ section, destination string }{{"schedules", "schedules:schedules"}, {"watchers", "schedules:conditions"}, {"webhooks", "schedules:triggers"}} {
		if _, has := details[kind.destination]; has {
			continue
		}
		for _, section := range sections {
			if section.ID == kind.section && section.Error == "" {
				details[kind.destination] = "None yet"
			}
		}
	}
	if _, ok := details["mcp:connected"]; !ok {
		for _, section := range sections {
			if section.ID == "connections" {
				if section.Error != "" {
					details["mcp:connected"] = "Unavailable"
				} else {
					details["mcp:connected"] = "0 connected"
				}
			}
		}
	}
	if len(workflows) == 1 {
		details["workflow"] = workflows[0].Detail
	} else if len(workflows) > 1 {
		details["workflow"] = fmt.Sprintf("%d workflows", len(workflows))
	}
	if len(automations) == 1 {
		details["automations"] = automations[0].Detail
	} else if len(automations) > 1 {
		details["automations"] = fmt.Sprint(len(automations))
		var next *crewPanelRow
		now := time.Now()
		for i := range automations {
			row := &automations[i]
			if row.nextRun.After(now) && (next == nil || row.nextRun.Before(next.nextRun)) {
				next = row
			}
		}
		if next != nil {
			details["automations"] += " · " + next.Detail
		}
	}
	return details
}
