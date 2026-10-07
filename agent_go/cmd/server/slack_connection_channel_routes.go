package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// "One of my bots": the owner of a workflow's or crew's own Slack app can
// share it with other workflows and crews they can write, channel by
// channel, without a platform admin. The routes live on the connection
// (services.SlackConnection.ChannelRoutes); a channel routed there answers
// for the route's destination, every other channel for the app's own.
//
// Adding or removing a route needs both:
//   - manage access to the app (requireSlackConnectionAccess: owner of its
//     workflow/crew scope, or an admin), and
//   - write access to the destination (requireSlackRouteDestinationWriter).
//
// One channel maps to one destination per app.

// SlackConnectionChannelRouteResponse is one target allowed in a channel on
// a bot (a channel with several targets has one entry per target).
type SlackConnectionChannelRouteResponse struct {
	ChannelID     string `json:"channel_id"`
	WorkspacePath string `json:"workspace_path"`
	ProfileID     string `json:"profile_id,omitempty"`
	Label         string `json:"label,omitempty"`
	Slug          string `json:"slug,omitempty"`
	IsDefault     bool   `json:"is_default"`
}

// SlackBotTargetResponse is a target attached to a bot for DMs.
type SlackBotTargetResponse struct {
	WorkspacePath string `json:"workspace_path"`
	ProfileID     string `json:"profile_id,omitempty"`
	Label         string `json:"label,omitempty"`
	Slug          string `json:"slug,omitempty"`
}

// SlackUsableBotResponse is one bot the caller can share: a scoped
// connection they manage. Never carries tokens.
type SlackUsableBotResponse struct {
	ID            string                                `json:"id"`
	DisplayName   string                                `json:"display_name"`
	Enabled       bool                                  `json:"enabled"`
	Configured    bool                                  `json:"configured"`
	WorkspacePath string                                `json:"workspace_path"`
	ProfileID     string                                `json:"profile_id,omitempty"`
	OwnerLabel    string                                `json:"owner_label,omitempty"`
	OwnSlug       string                                `json:"own_slug,omitempty"`
	ChannelRoutes []SlackConnectionChannelRouteResponse `json:"channel_routes"`
	Targets       []SlackBotTargetResponse              `json:"targets"`
}

// SlackUsableBotsResponse is the "bots I can use" payload.
type SlackUsableBotsResponse struct {
	Bots []SlackUsableBotResponse `json:"bots"`
}

// SlackConnectionChannelRouteRequest names the destination for a channel.
type SlackConnectionChannelRouteRequest struct {
	WorkspacePath string `json:"workspace_path"`
	ProfileID     string `json:"profile_id,omitempty"`
	// Trigger sets automation on top-level messages for the channel's default
	// target (PLAT-668); ClearTrigger removes it. Saving a trigger for the
	// bot's own target in a channel creates that channel's route.
	Trigger      *services.SlackTrigger `json:"trigger,omitempty"`
	ClearTrigger bool                   `json:"clear_trigger,omitempty"`
}

func registerSlackConnectionChannelRoutes(r *mux.Router, api *StreamingAPI) {
	r.HandleFunc("/mine", listUsableSlackBotsHandler(api)).Methods("GET")
	r.HandleFunc("/{id}/channel-routes/{channel}", putSlackConnectionChannelRouteHandler(api)).Methods("PUT", "POST", "OPTIONS")
	r.HandleFunc("/{id}/channel-routes/{channel}", deleteSlackConnectionChannelRouteHandler(api)).Methods("DELETE")
	r.HandleFunc("/{id}/targets", putSlackConnectionTargetHandler(api)).Methods("PUT", "POST", "OPTIONS")
	r.HandleFunc("/{id}/targets", deleteSlackConnectionTargetHandler(api)).Methods("DELETE")
}

// cleanSlackDestinationPath canonicalizes a destination folder so the
// stored route and the permission check name the same folder.
func cleanSlackDestinationPath(workspacePath string) string {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return ""
	}
	return strings.Trim(filepath.ToSlash(filepath.Clean("/"+workspacePath)), "/")
}

// requireSlackRouteDestinationWriter requires write access to the route's
// destination: Owner or Write on a workflow, product ownership of a crew
// project (crew projects are per-user, so ownership is the write bar).
func requireSlackRouteDestinationWriter(ctx context.Context, api *StreamingAPI, workspacePath, profileID string) error {
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.UserID == "" || claims.Provider == "bot_route" || claims.BotRouteGrant != "" {
		return fmt.Errorf("only an authenticated interactive user may route Slack channels")
	}
	if strings.TrimSpace(profileID) != "" {
		return requireProductSlackScopeOwner(ctx, api, profileID, workspacePath)
	}
	if !userAccessForClaims(claims).CanEdit {
		return fmt.Errorf("you need write access to route a Slack channel to %s", workspacePath)
	}
	manifest, exists, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil {
		return err
	}
	if !exists || manifest == nil || strings.TrimSpace(manifest.ID) == "" {
		return fmt.Errorf("no workflow at %s", workspacePath)
	}
	level := workflowAccessForManifest(claims, manifest)
	if !userAllowedWorkflowID(claims, manifest.ID) || (level != WorkflowAccessOwner && level != WorkflowAccessWrite) {
		return fmt.Errorf("you need write access to %s to route a Slack channel to it", firstNonBlank(strings.TrimSpace(manifest.Label), manifest.ID))
	}
	return nil
}

// slackDestinationExists reports whether a route's destination still has a
// manifest. A route to a deleted destination may be removed by whoever
// manages the bot.
func slackDestinationExists(ctx context.Context, workspacePath, profileID string) bool {
	if strings.TrimSpace(profileID) != "" {
		_, found, err := readProjectRuntimeManifest(ctx, profileID, workspacePath)
		return err != nil || found
	}
	_, found, err := ReadWorkflowManifest(ctx, workspacePath)
	return err != nil || found
}

// slackDestinationLabel names a destination for the UI.
func slackDestinationLabel(ctx context.Context, workspacePath, profileID string) string {
	if strings.TrimSpace(profileID) != "" {
		return firstNonBlank(productProjectLabel(ctx, profileID, workspacePath), workspacePath)
	}
	if manifest, found, err := ReadWorkflowManifest(ctx, workspacePath); err == nil && found && manifest != nil {
		return firstNonBlank(strings.TrimSpace(manifest.Label), strings.TrimSpace(manifest.ID), workspacePath)
	}
	return workspacePath
}

func projectUsableSlackBot(ctx context.Context, conn services.SlackConnection) SlackUsableBotResponse {
	registry := slackLoadTargetsRegistry(ctx)
	own := slackNamedTarget(ctx, registry, services.SlackTargetRef{WorkspacePath: conn.WorkspacePath, ProfileID: conn.ProfileID})
	out := SlackUsableBotResponse{
		ID:            conn.ID,
		DisplayName:   conn.DisplayName,
		Enabled:       conn.Enabled,
		Configured:    strings.TrimSpace(conn.BotToken) != "" && strings.TrimSpace(conn.AppToken) != "",
		WorkspacePath: conn.WorkspacePath,
		ProfileID:     conn.ProfileID,
		OwnerLabel:    own.Label,
		OwnSlug:       own.Slug,
		ChannelRoutes: []SlackConnectionChannelRouteResponse{},
		Targets:       []SlackBotTargetResponse{},
	}
	for channel, route := range conn.ChannelRoutes {
		def := route.Default()
		for _, ref := range route.Allowed() {
			target := slackNamedTarget(ctx, registry, ref)
			out.ChannelRoutes = append(out.ChannelRoutes, SlackConnectionChannelRouteResponse{
				ChannelID:     channel,
				WorkspacePath: ref.WorkspacePath,
				ProfileID:     ref.ProfileID,
				Label:         target.Label,
				Slug:          target.Slug,
				IsDefault:     !def.Empty() && def.Same(ref),
			})
		}
	}
	sort.SliceStable(out.ChannelRoutes, func(i, j int) bool { return out.ChannelRoutes[i].ChannelID < out.ChannelRoutes[j].ChannelID })
	for _, ref := range conn.Targets {
		target := slackNamedTarget(ctx, registry, ref)
		out.Targets = append(out.Targets, SlackBotTargetResponse{WorkspacePath: ref.WorkspacePath, ProfileID: ref.ProfileID, Label: target.Label, Slug: target.Slug})
	}
	return out
}

// usableSlackBots lists the scoped connections the caller manages.
func usableSlackBots(r *http.Request, api *StreamingAPI, svc *services.SlackService) []SlackUsableBotResponse {
	bots := []SlackUsableBotResponse{}
	for _, conn := range svc.ListConnections() {
		if strings.TrimSpace(conn.WorkspacePath) == "" {
			continue
		}
		if requireSlackConnectionAccess(r, api, conn) != nil {
			continue
		}
		bots = append(bots, projectUsableSlackBot(r.Context(), conn))
	}
	sort.Slice(bots, func(i, j int) bool {
		return strings.ToLower(bots[i].DisplayName) < strings.ToLower(bots[j].DisplayName)
	})
	return bots
}

func listUsableSlackBotsHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := GetUserFromContext(r.Context())
		if claims == nil || claims.Provider == "bot_route" || claims.BotRouteGrant != "" {
			http.Error(w, "only an authenticated interactive user may list Slack bots", http.StatusForbidden)
			return
		}
		svc, err := ensureSlackService()
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to initialize Slack service: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SlackUsableBotsResponse{Bots: usableSlackBots(r, api, svc)})
	}
}

// slackChannelRouteTarget resolves {id} and {channel} and checks the caller
// manages the connection.
func slackChannelRouteTarget(w http.ResponseWriter, r *http.Request, api *StreamingAPI) (*services.SlackService, services.SlackConnection, string, bool) {
	svc, id, ok := slackConnectionService(w, r)
	if !ok {
		return nil, services.SlackConnection{}, "", false
	}
	channel := services.NormalizeSlackChannelID(mux.Vars(r)["channel"])
	if !slackChannelIDPattern.MatchString(channel) {
		http.Error(w, "an exact Slack channel ID is required (e.g. C1234567890)", http.StatusBadRequest)
		return nil, services.SlackConnection{}, "", false
	}
	conn, found := svc.GetConnection(id)
	if !found {
		http.Error(w, fmt.Sprintf("slack connection %q not found", id), http.StatusNotFound)
		return nil, services.SlackConnection{}, "", false
	}
	if strings.TrimSpace(conn.WorkspacePath) == "" {
		http.Error(w, "the shared platform bot routes channels in Access > Slack", http.StatusBadRequest)
		return nil, services.SlackConnection{}, "", false
	}
	if err := requireSlackConnectionAccess(r, api, conn); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return nil, services.SlackConnection{}, "", false
	}
	return svc, conn, channel, true
}

func putSlackConnectionChannelRouteHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		svc, conn, channel, ok := slackChannelRouteTarget(w, r, api)
		if !ok {
			return
		}
		var req SlackConnectionChannelRouteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
			return
		}
		profileID := strings.TrimSpace(req.ProfileID)
		workspacePath := physicalProductSlackScope(r.Context(), profileID, cleanSlackDestinationPath(req.WorkspacePath))
		if workspacePath == "" {
			http.Error(w, "workspace_path is required", http.StatusBadRequest)
			return
		}
		// A Code talks 1:1 only (Slack DMs, WhatsApp); it never gets a channel.
		if strings.EqualFold(profileID, codeproduct.ProfileID) || isCodeProjectPath(workspacePath) {
			http.Error(w, "Code workspaces answer only 1:1 Slack DMs and WhatsApp, not Slack channels", http.StatusBadRequest)
			return
		}
		if err := requireSlackRouteDestinationWriter(r.Context(), api, workspacePath, profileID); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		// Fail at save time, not on the first mention, when the destination
		// cannot be answered for (e.g. a crew project without a conversation).
		destination, err := api.slackDestinationRoute(r.Context(), workspacePath, profileID)
		if err != nil {
			http.Error(w, fmt.Sprintf("cannot answer for %s in Slack: %v", workspacePath, err), http.StatusBadRequest)
			return
		}
		if req.Trigger != nil {
			withTrigger := *destination
			withTrigger.Trigger = req.Trigger
			if err := validateSlackTrigger(r.Context(), withTrigger); err != nil {
				http.Error(w, fmt.Sprintf("invalid trigger: %v", err), http.StatusBadRequest)
				return
			}
		}
		triggerChange := req.Trigger != nil || req.ClearTrigger
		addedBy := ""
		if claims := GetUserFromContext(r.Context()); claims != nil {
			addedBy = claims.UserID
		}
		next := services.SlackTargetRef{WorkspacePath: workspacePath, ProfileID: profileID, AddedBy: addedBy}
		// A channel that already answers for another target gets this one
		// added to its list (picked with "@bot <slug>", PLAT-668); a new
		// channel answers for this target by default, as before.
		updated, err := svc.ModifySlackConnection(r.Context(), conn.ID, func(c *services.SlackConnection) error {
			existing, found := c.ChannelRoutes[channel]
			if !found {
				if next.Same(services.SlackTargetRef{WorkspacePath: c.WorkspacePath, ProfileID: c.ProfileID}) && req.Trigger == nil {
					return fmt.Errorf("this bot already answers for its own %s in every channel", map[bool]string{true: "crew", false: "workflow"}[profileID != ""])
				}
				c.ChannelRoutes[channel] = services.SlackConnectionRoute{WorkspacePath: workspacePath, ProfileID: profileID, AddedBy: addedBy, Trigger: req.Trigger}
				return nil
			}
			for _, ref := range existing.Allowed() {
				if !ref.Same(next) {
					continue
				}
				if triggerChange && existing.Default().Same(next) {
					existing.Trigger = req.Trigger
					c.ChannelRoutes[channel] = existing
					return nil
				}
				if triggerChange {
					return fmt.Errorf("a trigger runs the channel's default target only")
				}
				return fmt.Errorf("channel %s already answers for this destination on this bot", channel)
			}
			if req.Trigger != nil {
				return fmt.Errorf("a trigger runs the channel's default target only")
			}
			existing.Targets = append(existing.Targets, next)
			c.ChannelRoutes[channel] = existing
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		registerSlackBotConnectorForOwnedConnections(api, svc)
		api.revokeSlackConnectionChannelSessions(r.Context(), conn.ID, channel)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(projectUsableSlackBot(r.Context(), updated))
	}
}

func deleteSlackConnectionChannelRouteHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, conn, channel, ok := slackChannelRouteTarget(w, r, api)
		if !ok {
			return
		}
		existing, found := conn.ChannelRoutes[channel]
		if !found {
			http.Error(w, fmt.Sprintf("channel %s has no route on this bot", channel), http.StatusNotFound)
			return
		}
		// With ?workspace_path= only that target leaves the channel's list;
		// without, the whole channel route goes.
		var only *services.SlackTargetRef
		if raw := strings.TrimSpace(r.URL.Query().Get("workspace_path")); raw != "" {
			profileID := strings.TrimSpace(r.URL.Query().Get("profile_id"))
			ref := services.SlackTargetRef{WorkspacePath: physicalProductSlackScope(r.Context(), profileID, cleanSlackDestinationPath(raw)), ProfileID: profileID}
			only = &ref
		}
		for _, ref := range existing.Allowed() {
			if only != nil && !ref.Same(*only) {
				continue
			}
			if err := requireSlackRouteDestinationWriter(r.Context(), api, ref.WorkspacePath, ref.ProfileID); err != nil && slackDestinationExists(r.Context(), ref.WorkspacePath, ref.ProfileID) {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
		}
		updated, err := svc.ModifySlackConnection(r.Context(), conn.ID, func(c *services.SlackConnection) error {
			if only == nil {
				delete(c.ChannelRoutes, channel)
				return nil
			}
			route := c.ChannelRoutes[channel]
			removed := false
			if def := route.Default(); !def.Empty() && def.Same(*only) {
				route.WorkspacePath, route.ProfileID, route.AddedBy = "", "", ""
				removed = true
			}
			kept := route.Targets[:0]
			for _, ref := range route.Targets {
				if ref.Same(*only) {
					removed = true
					continue
				}
				kept = append(kept, ref)
			}
			route.Targets = kept
			if !removed {
				return fmt.Errorf("channel %s does not answer for %s on this bot", channel, only.WorkspacePath)
			}
			c.ChannelRoutes[channel] = route
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		api.revokeSlackConnectionChannelSessions(r.Context(), conn.ID, channel)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(projectUsableSlackBot(r.Context(), updated))
	}
}

// revokeSlackConnectionChannelSessions cancels live bot turns on this app
// and channel whose destination no longer matches the channel's route.
// revalidateExecutionPrincipal refuses their next turn anyway; this stops
// the one in flight.
func (api *StreamingAPI) revokeSlackConnectionChannelSessions(ctx context.Context, connID, channel string) {
	if api == nil {
		return
	}
	api.botExecutionSessions.Range(func(key, value interface{}) bool {
		binding, ok := value.(botExecutionSession)
		if !ok || strings.TrimSpace(binding.Request.BotConnectionID) != connID || !strings.EqualFold(strings.TrimSpace(binding.Request.BotChannelID), channel) {
			return true
		}
		if binding.Claims == nil || binding.Claims.ExecutionPrincipal == nil {
			api.cancelSessionRuntimeWork(key.(string), "Slack channel route changed", runtimePhaseCanceled)
			return true
		}
		target := binding.Claims.ExecutionPrincipal.Target
		route, found, _ := api.slackRouteForTurn(ctx, connID, channel, nil, target, binding.Request)
		if !found || !sameSlackRouteDestination(route, target) {
			api.cancelSessionRuntimeWork(key.(string), "Slack channel route changed", runtimePhaseCanceled)
		}
		return true
	})
}

// putSlackConnectionTargetHandler attaches a target to a bot for DMs: its
// slug then reaches it in a 1:1 DM, for people who can reach it with their
// own access (a Code: its owner only). Needs manage access to the bot and
// write access to the target.
func putSlackConnectionTargetHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		svc, conn, ref, ok := slackConnectionTargetRequest(w, r, api, true)
		if !ok {
			return
		}
		if _, err := api.slackDestinationRoute(r.Context(), ref.WorkspacePath, ref.ProfileID); err != nil {
			http.Error(w, fmt.Sprintf("cannot answer for %s in Slack: %v", ref.WorkspacePath, err), http.StatusBadRequest)
			return
		}
		if claims := GetUserFromContext(r.Context()); claims != nil {
			ref.AddedBy = claims.UserID
		}
		updated, err := svc.ModifySlackConnection(r.Context(), conn.ID, func(c *services.SlackConnection) error {
			if ref.Same(services.SlackTargetRef{WorkspacePath: c.WorkspacePath, ProfileID: c.ProfileID}) {
				return fmt.Errorf("this is the bot's own target")
			}
			for _, existing := range c.Targets {
				if existing.Same(ref) {
					return nil
				}
			}
			c.Targets = append(c.Targets, ref)
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		registerSlackBotConnectorForOwnedConnections(api, svc)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(projectUsableSlackBot(r.Context(), updated))
	}
}

func deleteSlackConnectionTargetHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, conn, ref, ok := slackConnectionTargetRequest(w, r, api, false)
		if !ok {
			return
		}
		updated, err := svc.ModifySlackConnection(r.Context(), conn.ID, func(c *services.SlackConnection) error {
			kept := c.Targets[:0]
			for _, existing := range c.Targets {
				if !existing.Same(ref) {
					kept = append(kept, existing)
				}
			}
			c.Targets = kept
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(projectUsableSlackBot(r.Context(), updated))
	}
}

// slackConnectionTargetRequest resolves {id} and the target (body on add,
// query on remove) and checks the caller manages the bot and writes the
// target (a target that no longer exists may be removed by the bot's manager).
func slackConnectionTargetRequest(w http.ResponseWriter, r *http.Request, api *StreamingAPI, fromBody bool) (*services.SlackService, services.SlackConnection, services.SlackTargetRef, bool) {
	svc, id, ok := slackConnectionService(w, r)
	if !ok {
		return nil, services.SlackConnection{}, services.SlackTargetRef{}, false
	}
	conn, found := svc.GetConnection(id)
	if !found {
		http.Error(w, fmt.Sprintf("slack connection %q not found", id), http.StatusNotFound)
		return nil, services.SlackConnection{}, services.SlackTargetRef{}, false
	}
	if strings.TrimSpace(conn.WorkspacePath) == "" {
		http.Error(w, "the AgentWorks bot reaches targets whose owners turned it on in their Slack tab", http.StatusBadRequest)
		return nil, services.SlackConnection{}, services.SlackTargetRef{}, false
	}
	if err := requireSlackConnectionAccess(r, api, conn); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return nil, services.SlackConnection{}, services.SlackTargetRef{}, false
	}
	var body SlackConnectionChannelRouteRequest
	if fromBody {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
			return nil, services.SlackConnection{}, services.SlackTargetRef{}, false
		}
	} else {
		body.WorkspacePath, body.ProfileID = r.URL.Query().Get("workspace_path"), r.URL.Query().Get("profile_id")
	}
	ref := slackTargetFromRequest(r.Context(), body.WorkspacePath, body.ProfileID)
	if ref.Empty() {
		http.Error(w, "workspace_path is required", http.StatusBadRequest)
		return nil, services.SlackConnection{}, services.SlackTargetRef{}, false
	}
	if err := requireSlackRouteDestinationWriter(r.Context(), api, ref.WorkspacePath, ref.ProfileID); err != nil && (fromBody || slackDestinationExists(r.Context(), ref.WorkspacePath, ref.ProfileID)) {
		http.Error(w, err.Error(), http.StatusForbidden)
		return nil, services.SlackConnection{}, services.SlackTargetRef{}, false
	}
	return svc, conn, ref, true
}
