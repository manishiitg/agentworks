package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

func (api *StreamingAPI) crewPanelSources(r *http.Request, project, workspace, chat string, binding crewProjectBinding) []crewPanelSource {
	claims := GetUserFromContext(r.Context())
	owner := binding.OwnedByCaller
	_, connectionErr := personalConnectionItemScope(r, workspace, true, "Vault", "Vault")
	connections := connectionErr == nil
	q := url.Values{"workspace_path": {workspace}, "profile_id": {"work"}}
	vars := map[string]string{"project": project, "project_id": project}
	read := func(ctx context.Context, handler http.HandlerFunc, route string, query url.Values, out any) error {
		return crewPanelRead(r.WithContext(ctx), handler, http.MethodGet, route, vars, query, nil, out)
	}
	sources := []crewPanelSource{
		{id: "sharing", title: "Sharing", allowed: dashboardScopeAllowed(claims, "share_status"), read: func(ctx context.Context) (crewPanelSection, error) {
			var data struct {
				Owner    bool
				CanShare bool `json:"can_share"`
				Mode     string
				People   []struct{ ID string }
			}
			err := crewPanelRead(r.WithContext(ctx), api.handleDashboards, http.MethodPost, "/api/dashboards", nil, nil, map[string]string{"workspace": workspace, "action": "share_status"}, &data)
			detail, shared := "Only you", "Not shared"
			if !data.Owner && !data.CanShare {
				detail, shared = "Shared with you", "Shared with you"
			} else if data.Mode == "everyone" {
				detail, shared = "Everyone", "Everyone"
			} else if data.Mode == "people" && len(data.People) > 0 {
				people := map[string]bool{}
				for _, person := range data.People {
					people[person.ID] = true
				}
				detail = fmt.Sprintf("Shared with %d", len(people))
				shared = fmt.Sprintf("%d %s", len(people), plural(len(people), "person", "people"))
			}
			return crewPanelSection{Detail: detail, shared: shared}, err
		}},

		// Git status over a Crew's repos takes longer than the panel's other reads (5 Nov: past 2 s on every refresh).
		{id: "changes", title: "Changes", allowed: workspaceReadAllowed(r.Context(), claims, workspace, logicalPathIsCaller, true), timeout: 6 * time.Second, read: func(ctx context.Context) (crewPanelSection, error) {
			var data struct {
				Repos       []workspaceGitRepo `json:"repos"`
				Unavailable bool               `json:"git_unavailable"`
			}
			err := read(ctx, requireWorkflowReadAccessCallerRelative(api.handleWorkspaceGit), "/api/workspace-git", url.Values{"workspace_path": {workspace}, "session_id": {chat}, "op": {"status"}}, &data)
			section := crewPanelSection{}
			count := 0
			truncated := false
			for _, repo := range data.Repos {
				count += len(repo.Files)
				truncated = truncated || repo.Truncated
				detail := "Clean"
				if len(repo.Files) > 0 {
					detail = fmt.Sprintf("%d changed", len(repo.Files))
					if repo.Truncated {
						detail = fmt.Sprintf("%d+ changed", len(repo.Files))
					}
				}
				branch := repo.Branch
				if branch == "" {
					branch = "No commits yet"
				}
				section.Rows = append(section.Rows, crewPanelRow{ID: repo.Root, Label: crewPanelName(repo.Root, workspace), Detail: branch + " · " + detail, Destination: "files", Path: repo.Root, UsedInChat: repo.UsedInChat})
			}
			section.Detail = crewPanelCount(count, "file")
			if truncated {
				section.Detail = fmt.Sprintf("%d+ files", count)
			}
			if data.Unavailable {
				section.Detail = "Git unavailable"
			}
			return section, err
		}},
		{id: "dashboards", title: "Dashboards", allowed: dashboardScopeAllowed(claims, "list"), read: func(ctx context.Context) (crewPanelSection, error) {
			section := crewPanelSection{}
			// The native catalog already includes both authored HTML and managed dashboards.
			for offset := 0; ; {
				var data struct {
					Dashboards []dashboards.Dashboard `json:"dashboards"`
					HasMore    bool                   `json:"has_more"`
				}
				err := crewPanelRead(r.WithContext(ctx), api.handleDashboards, http.MethodPost, "/api/dashboards", nil, nil, map[string]any{"workspace": workspace, "action": "list", "published_only": true, "limit": 200, "offset": offset}, &data)
				if err != nil {
					return section, err
				}
				for _, d := range data.Dashboards {
					section.Rows = append(section.Rows, crewPanelRow{ID: d.ID, Label: d.Title, Path: d.DocumentPath, Destination: "dashboard", Motion: d.Motion, Artifact: d.Artifact})
				}
				if !data.HasMore || len(data.Dashboards) == 0 {
					break
				}
				offset += len(data.Dashboards)
			}
			section.Detail = crewPanelCount(len(section.Rows), "dashboard")
			return section, nil
		}},
		{id: "connections", title: "MCPs", allowed: connections, noCache: true, read: func(ctx context.Context) (crewPanelSection, error) {
			data, err := api.projectVaultInventory(ctx, claims.UserID, workspace)
			section := crewPanelSection{}
			connected := 0
			mcps := 0
			for _, group := range data.Vaults {
				for _, item := range group.Items {
					if !item.Selected {
						continue
					}
					destination := ""
					switch item.Kind {
					case "mcp":
						destination = "mcp:connected"
						mcps++
					case "google":
						destination = "mcp:gmail"
					case "slack":
						destination = "mcp:slack-account"
					default:
						continue
					}
					status := "Saved"
					switch item.Status {
					case "connected", "online":
						status = "Connected"
					case "needs_attention":
						status = "Needs attention"
					case "offline":
						status = "Offline"
					}
					on := item.Status == "connected" || item.Status == "online"
					if on && item.Kind == "mcp" {
						connected++
					}
					detail := status
					if item.AccountEmail != "" {
						detail = item.AccountEmail + " · " + status
					}
					section.Rows = append(section.Rows, crewPanelRow{ID: item.Kind + ":" + item.ID, Label: item.Name, Detail: detail, Destination: destination, Connected: on, Attention: item.Status == "needs_attention"})
				}
			}
			section.Detail = "None selected"
			if mcps > 0 {
				section.Detail = fmt.Sprintf("%d connected", connected)
				if mcps != connected {
					section.Detail = fmt.Sprintf("%d of %d connected", connected, mcps)
				}
			}
			// Inventory notices mean a partial lookup, not a confirmed empty selection.
			if err == nil && len(data.Notices) > 0 {
				section.Error = "Some connections could not be checked."
			}
			return section, err
		}},
		{id: "browser", title: "Browser", allowed: connections, read: func(ctx context.Context) (crewPanelSection, error) {
			var data struct {
				Selected, Connected bool
				AccountConnected    bool   `json:"account_connected"`
				BrowserID           string `json:"browser_id"`
				Browsers            []struct {
					ID, Name  string
					Connected bool
				}
			}
			section := crewPanelSection{}
			if err := read(ctx, api.handleBrowserExtension, "/api/browser/extension", q, &data); err != nil {
				return section, err
			}
			name := "Browser"
			label := "No browser open"
			connected := data.Connected
			if data.Selected {
				label = "Browser offline"
				for _, b := range data.Browsers {
					if b.ID == data.BrowserID {
						name = b.Name
						if b.Connected {
							label = "Connecting…"
						}
					}
				}
				if connected {
					label = "Connected"
				}
			} else {
				var live struct{ Sessions []struct{ State string } }
				if err := read(ctx, api.handleLiveBrowserSessions, "/api/browser/live/sessions", q, &live); err != nil {
					return section, err
				}
				if data.AccountConnected {
					label = "Choose browser"
				}
				for _, s := range live.Sessions {
					if s.State != "completed" {
						connected = true
						label = "Connected"
						break
					}
				}
			}
			section.Detail = label
			section.Rows = []crewPanelRow{{ID: "browser", Label: name, Detail: label, Destination: "browser", Connected: connected}}
			return section, nil
		}},
		{id: "github", title: "GitHub", allowed: connections, read: func(ctx context.Context) (crewPanelSection, error) {
			var data struct {
				Connected bool
				Login     string
			}
			err := read(ctx, api.handleGitHubVault, "/api/vault/github", url.Values{"view": {"summary"}}, &data)
			label := "Not connected"
			if data.Connected {
				label = "Connected"
				if data.Login != "" {
					label = data.Login + " · " + label
				}
			}
			return crewPanelSection{Rows: []crewPanelRow{{ID: "github", Label: "GitHub", Detail: label, Destination: "mcp:github", Connected: data.Connected}}}, err
		}},
		{id: "slack_bot", title: "Slack bot", allowed: connections, noCache: true, read: func(ctx context.Context) (crewPanelSection, error) {
			var data struct {
				Selected string `json:"selected_id"`
				Allow    *bool  `json:"allow_bots"`
				Bots     []struct {
					ID, Name            string
					Configured, Enabled bool
					Identity            struct {
						Name     string
						Verified bool
					}
				}
			}
			err := read(ctx, api.handleSlackVaultBots, "/api/integrations/slack/bots", q, &data)
			section := crewPanelSection{}
			if data.Allow != nil && !*data.Allow {
				return section, err
			}
			for _, b := range data.Bots {
				if b.ID != data.Selected {
					continue
				}
				name := b.Identity.Name
				if name == "" {
					name = b.Name
				}
				if name == "Default" {
					name = "Saved Slack bot"
				}
				label := "Saved"
				if !b.Configured {
					label = "Needs setup"
				} else if !b.Enabled {
					label = "Paused"
				} else if b.Identity.Verified {
					label = "Identity verified"
				}
				section.Rows = append(section.Rows, crewPanelRow{ID: "bot:" + b.ID, Label: name, Detail: name + " · " + label, Destination: "mcp:slack-bot", Connected: b.Configured && b.Enabled && b.Identity.Verified, Attention: !b.Configured})
			}
			return section, err
		}},
		{id: "computer_use", title: "Computer use", allowed: connections && IsLocalMode(), read: func(ctx context.Context) (crewPanelSection, error) {
			var data struct {
				Enabled, Active, Installed bool
				Platform                   string
				Permissions                struct {
					Data struct{ Accessibility struct{ State string } }
				}
			}
			err := crewPanelRead(r.WithContext(ctx), api.handleCrewComputerUse, http.MethodGet, "/api/agent-profiles/work/projects/"+url.PathEscape(project)+"/computer-use", map[string]string{"id": "work", "project_id": project}, nil, nil, &data)
			section := crewPanelSection{}
			if data.Enabled {
				label := "Needs setup"
				if data.Active {
					label = "Working"
				} else if data.Installed {
					label = "On"
				}
				section.Rows = []crewPanelRow{{ID: "computer-use", Label: "Computer use", Detail: label, Destination: "mcp:computer-use", Connected: data.Installed && (data.Platform != "darwin" || data.Permissions.Data.Accessibility.State == "granted")}}
			}
			return section, err
		}},
		{id: "whatsapp", title: "WhatsApp", allowed: connections && api.whatsappManager != nil, read: func(ctx context.Context) (crewPanelSection, error) {
			// Read the existing caller-scoped service state without the status
			// page's pairing-QR and route-provisioning side effects.
			svc, _, err := whatsappServiceForRequest(r.WithContext(ctx), api.whatsappManager)
			if err != nil {
				return crewPanelSection{}, err
			}
			data := struct{ Connected, Paired bool }{svc.IsConnected(), svc.IsPaired()}
			label := "Not set up"
			if data.Connected {
				label = "Connected"
			} else if data.Paired {
				label = "Not connected"
			}
			return crewPanelSection{Rows: []crewPanelRow{{ID: "whatsapp", Label: "WhatsApp", Detail: label, Destination: "mcp:whatsapp", Connected: data.Connected, Attention: data.Paired && !data.Connected}}}, err
		}},
		{id: "schedules", title: "Schedules", allowed: owner && api.scheduler != nil, read: func(ctx context.Context) (crewPanelSection, error) {
			var data struct{ Jobs []ScheduledJobResponse }
			err := read(ctx, listScheduledJobsHandler(api.scheduler), "/api/schedules", url.Values{"entity_type": {"product"}, "limit": {"100000"}, "workspace_path": {workspace}}, &data)
			// A person who may not see this Crew's schedules gets the plain empty list, like Watchers and Webhooks.
			if denied, ok := err.(crewPanelReadError); ok && (denied.status == http.StatusForbidden || denied.status == http.StatusNotFound) {
				err = nil
			}
			section := crewPanelSection{}
			for _, job := range data.Jobs {
				if strings.TrimRight(job.WorkspacePath, "/") != strings.TrimRight(workspace, "/") {
					continue
				}
				label := "On"
				if !job.Enabled {
					label = "Paused"
				} else if job.NextRunAt != nil {
					zone, zoneErr := time.LoadLocation(job.Timezone)
					if zoneErr != nil {
						zone = time.UTC
					}
					label = "Next · " + job.NextRunAt.In(zone).Format("2 Jan 15:04 MST")
				}
				if job.LastStatus == "running" {
					label = "Running now"
					section.Busy = true
				}
				row := crewPanelRow{ID: "schedule:" + job.ID, Label: job.Name, Detail: label, Destination: "schedules:schedules"}
				if job.Enabled && job.NextRunAt != nil {
					row.nextRun = *job.NextRunAt
				}
				section.Rows = append(section.Rows, row)
			}
			section.Rows = append(section.Rows, crewWorkflowTriggerRows(ctx, workspace, false)...)
			section.Detail = crewPanelCount(len(section.Rows), "schedule")
			return section, err
		}},
		{id: "watchers", title: "Watchers", allowed: owner && api.projectActions != nil, read: func(ctx context.Context) (crewPanelSection, error) {
			var data struct {
				Triggers []struct {
					ID, Name  string
					Enabled   bool
					Completed string `json:"completed_at"`
				}
			}
			err := read(ctx, api.projectActions.conditionHTTP, "/api/condition-triggers", url.Values{"profile_id": {"work"}, "project_id": {project}, "view": {"summary"}}, &data)
			section := crewPanelSection{}
			for _, item := range data.Triggers {
				label := "Paused"
				if item.Completed != "" {
					label = "Completed"
				} else if item.Enabled {
					label = "On"
				}
				section.Rows = append(section.Rows, crewPanelRow{ID: "watcher:" + item.ID, Label: item.Name, Detail: label, Destination: "schedules:conditions"})
			}
			return section, err
		}},
		{id: "webhooks", title: "Triggers", allowed: owner && api.projectActions != nil, read: func(ctx context.Context) (crewPanelSection, error) {
			var data struct {
				Triggers []struct {
					ID, Name string
					Enabled  bool
				}
			}
			err := read(ctx, api.projectActions.listProductWebhooks, "/api/product-webhooks", url.Values{"profile_id": {"work"}, "project_id": {project}}, &data)
			section := crewPanelSection{}
			for _, item := range data.Triggers {
				label := "Paused"
				if item.Enabled {
					label = "On"
				}
				section.Rows = append(section.Rows, crewPanelRow{ID: "trigger:" + item.ID, Label: item.Name, Detail: label, Destination: "schedules:triggers"})
			}
			section.Rows = append(section.Rows, crewWorkflowTriggerRows(ctx, workspace, true)...)
			return section, err
		}},
		// One row per Crew workflow, from the Crew's own records (any person with access to the Crew sees the summary).
		{id: "workflow", title: "Workflow", allowed: true, refreshFor: 5 * time.Second, read: func(ctx context.Context) (crewPanelSection, error) {
			return crewPanelWorkflows(ctx, workspace)
		}},
		// The Crew's incoming email: On or Off with the address, whenever this server can do inboxes at all.
		{id: "inbox", title: "Inbox", allowed: !IsLocalMode() && api.gmailInbound != nil, noCache: true, read: func(ctx context.Context) (crewPanelSection, error) {
			row, err := api.crewPanelInboxRow(ctx, workspace)
			if err != nil {
				return crewPanelSection{}, err
			}
			return crewPanelSection{Rows: []crewPanelRow{row}, Detail: row.Detail}, nil
		}},
		{id: "access", title: "Share", allowed: true, read: func(ctx context.Context) (crewPanelSection, error) {
			var data crewSyncReply
			err := read(ctx, api.handleCrewSync, "/api/crew-sync/"+url.PathEscape(project), nil, &data)
			access := &crewPanelAccess{Where: data.Where, Place: data.Place, Role: data.Role, View: data.AccessView, Summary: data.AccessSummary, Tree: data.AccessTree, Asking: []crewPanelAsk{}}
			section := crewPanelSection{Access: access, Busy: data.Place == "moving"}
			place := map[string]string{"mac": "On this Mac", "mac_off": "Mac off", "moving": "Publishing…", "online": "Online", "offline": "On this Mac · Offline"}[data.Place]
			section.Detail = place
			if data.Place != "offline" && len(data.AccessTree) == 0 && data.AccessSummary != "" {
				section.Detail = strings.Trim(place+" · "+data.AccessSummary, " ·")
			}
			for _, line := range data.AccessTree {
				if line.Progress != nil {
					section.Busy = true
				}
			}
			for _, ask := range data.Asking {
				action := "asked"
				ago := fmt.Sprintf("%d min ago", max(1, int(time.Since(ask.Started).Minutes()+0.5)))
				if ask.Active {
					action = "is asking"
					ago = "now"
					section.Busy = true
				}
				channel := ask.Channel
				switch channel {
				case "website":
					channel = "web"
				case "MCP":
					channel = "AI app"
				}
				if channel != "" {
					ago = channel + " · " + ago
				}
				access.Asking = append(access.Asking, crewPanelAsk{macAskActivity: ask, Label: ask.Sender + " " + action + " " + ask.Agent, Detail: ago})
			}
			return section, err
		}},
	}
	return sources
}

// The Crew's workflows keep their own timers and web addresses in their manifest. They are listed beside the Crew-level ones:
// webhooks are the GitHub and bearer addresses; functions and the Crew's own calls are not triggers a person set up.
func crewWorkflowTriggerRows(ctx context.Context, workspace string, webhooks bool) []crewPanelRow {
	records, err := readCrewExecutables()
	if err != nil {
		return nil
	}
	var rows []crewPanelRow
	for _, binding := range records {
		if canonicalCrewWorkspaceRoot(binding.CrewRoot) != canonicalCrewWorkspaceRoot(workspace) {
			continue
		}
		manifest, found, manifestErr := ReadWorkflowManifest(ctx, binding.Root)
		if manifestErr != nil || !found {
			continue
		}
		for _, schedule := range manifest.Schedules {
			if schedule.Kind != "" || (schedule.ScheduleType == "webhook") != webhooks {
				continue
			}
			label := "Paused"
			if schedule.Enabled {
				label = "On"
			}
			prefix := "schedule:"
			if webhooks {
				prefix = "trigger:"
			}
			rows = append(rows, crewPanelRow{ID: prefix + schedule.ID, Label: schedule.Name, Detail: label + " · " + manifest.Label, Destination: "workflow"})
		}
	}
	return rows
}
