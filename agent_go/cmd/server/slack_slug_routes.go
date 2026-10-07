package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// Slack slug settings API (docs/design/slack_slugs.md, PLAT-668), under
// /api/human-feedback/slack/targets:
//
//	GET/PUT  /settings?workspace_path=&profile_id=  a target's slug and its
//	         "Use the AgentWorks bot" switch (owner only to change)
//	POST     /channels/{channel}  add the target to a platform-bot channel
//	         (owner, and only a channel they are a member of in Slack)
//	DELETE   /channels/{channel}?workspace_path=&profile_id=
//	GET/PUT  /platform  products that may use the platform bot (admin)
//
// A slug is a name, never a grant: what it reaches is decided per channel
// list and per DM sender on every message (slack_slugs.go).

// SlackTargetChannelTarget is one target allowed in a channel.
type SlackTargetChannelTarget struct {
	Slug          string `json:"slug"`
	Label         string `json:"label"`
	WorkspacePath string `json:"workspace_path"`
	ProfileID     string `json:"profile_id,omitempty"`
	IsDefault     bool   `json:"is_default"`
	IsThis        bool   `json:"is_this"`
}

// SlackTargetChannel is a platform-bot channel this target is allowed in.
type SlackTargetChannel struct {
	ChannelID  string                     `json:"channel_id"`
	AdminRoute bool                       `json:"admin_route"`
	Targets    []SlackTargetChannelTarget `json:"targets"`
}

// SlackTargetSettingsResponse is one target's Slack settings.
type SlackTargetSettingsResponse struct {
	Slug              string               `json:"slug"`
	Label             string               `json:"label"`
	PlatformBot       bool                 `json:"platform_bot"`
	PlatformAvailable bool                 `json:"platform_available"`
	PlatformName      string               `json:"platform_name,omitempty"`
	ProductAllowed    bool                 `json:"product_allowed"`
	CanManage         bool                 `json:"can_manage"`
	DMOnly            bool                 `json:"dm_only"`
	Channels          []SlackTargetChannel `json:"channels"`
}

func registerSlackTargetRoutes(router *mux.Router, api *StreamingAPI) {
	r := router.PathPrefix("/api/human-feedback/slack/targets").Subrouter()
	r.HandleFunc("/settings", slackTargetSettingsHandler(api)).Methods("GET", "PUT", "POST", "OPTIONS")
	r.HandleFunc("/channels/{channel}", addSlackTargetChannelHandler(api)).Methods("POST", "PUT", "OPTIONS")
	r.HandleFunc("/channels/{channel}", removeSlackTargetChannelHandler(api)).Methods("DELETE")
	r.HandleFunc("/platform", slackPlatformBotSettingsHandler(api)).Methods("GET", "PUT", "POST", "OPTIONS")
	r.HandleFunc("/dry-run", slackTargetDryRunHandler(api)).Methods("POST", "OPTIONS")
}

// slackTargetDryRunHandler is "Test" on a channel card: a mention of the
// platform bot in that channel starting with this target's slug, through the
// real inbound path, stopping before the model. Nothing is posted.
func slackTargetDryRunHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		var body struct {
			WorkspacePath string `json:"workspace_path"`
			ProfileID     string `json:"profile_id"`
			ChannelID     string `json:"channel_id"`
			Text          string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
			return
		}
		ctx := r.Context()
		ref := slackTargetFromRequest(ctx, body.WorkspacePath, body.ProfileID)
		if err := requireSlackTargetOwner(ctx, api, ref); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		channel := services.NormalizeSlackChannelID(body.ChannelID)
		if !slackChannelIDPattern.MatchString(channel) || services.IsSlackDMChannel(channel) {
			http.Error(w, "an exact Slack channel ID is required (e.g. C1234567890)", http.StatusBadRequest)
			return
		}
		named := slackNamedTarget(ctx, slackLoadTargetsRegistry(ctx), ref)
		text := strings.TrimSpace(named.Slug + " " + firstNonBlank(strings.TrimSpace(body.Text), "dry run"))
		outcome, err := api.dryRunSlackMention(ctx, "", channel, GetUserFromContext(ctx).Email, text)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeSlackJSON(w, outcome)
	}
}

// slackTargetFromRequest canonicalizes the target a request names: a crew's
// logical path becomes the caller's physical project folder.
func slackTargetFromRequest(ctx context.Context, workspacePath, profileID string) services.SlackTargetRef {
	profileID = strings.TrimSpace(profileID)
	return services.SlackTargetRef{WorkspacePath: physicalProductSlackScope(ctx, profileID, cleanSlackDestinationPath(workspacePath)), ProfileID: profileID}
}

// requireSlackTargetOwner: only a target's owner (or an admin) turns its
// Slack switch on or off, edits its slug, or adds it to a channel. A
// workflow needs Owner; a Crew or Code project is its owner's.
func requireSlackTargetOwner(ctx context.Context, api *StreamingAPI, ref services.SlackTargetRef) error {
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.UserID == "" || claims.Provider == "bot_route" || claims.BotRouteGrant != "" || claims.ExecutionPrincipal != nil {
		return fmt.Errorf("only an authenticated interactive user may manage Slack targets")
	}
	if ref.Empty() {
		return fmt.Errorf("workspace_path is required")
	}
	if strings.TrimSpace(ref.ProfileID) != "" {
		return requireProductSlackScopeOwner(ctx, api, ref.ProfileID, ref.WorkspacePath)
	}
	if userAccessForClaims(claims).Admin {
		return nil
	}
	return requireSlackConnectionWorkflowOwner(ctx, ref.WorkspacePath)
}

// slackPlatformBot is the platform bot's live runtime and connection, when
// one is set up.
func slackPlatformBot() (*services.SlackService, services.SlackConnection, bool) {
	svc := services.GetSlackService()
	if svc == nil {
		return nil, services.SlackConnection{}, false
	}
	conn, found := slackAppConnection("")
	if found && strings.TrimSpace(conn.WorkspacePath) != "" {
		return nil, services.SlackConnection{}, false
	}
	if found && !conn.Enabled {
		return svc, conn, false
	}
	return svc, conn, svc.IsEnabled()
}

// slackChannelMembership checks Slack membership; tests replace it.
var slackChannelMembership = func(ctx context.Context, email, channelID string) (bool, error) {
	svc, _, ok := slackPlatformBot()
	if !ok || svc == nil {
		return false, fmt.Errorf("the AgentWorks bot is not set up")
	}
	return svc.SlackUserInChannel(ctx, email, channelID)
}

func (api *StreamingAPI) slackTargetSettings(ctx context.Context, ref services.SlackTargetRef, canManage bool) SlackTargetSettingsResponse {
	registry := slackLoadTargetsRegistry(ctx)
	legacy := api.slackLegacyPlatformTarget(ctx, ref)
	named := slackNamedTarget(ctx, registry, ref)
	_, conn, available := slackPlatformBot()
	out := SlackTargetSettingsResponse{
		Slug:              named.Slug,
		Label:             named.Label,
		PlatformBot:       registry.PlatformOptIn(ref, legacy),
		PlatformAvailable: available,
		PlatformName:      conn.DisplayName,
		ProductAllowed:    registry.ProductAllowed(ref),
		CanManage:         canManage,
		DMOnly:            ref.IsCode(),
		Channels:          []SlackTargetChannel{},
	}
	if !canManage || ref.IsCode() {
		return out
	}
	channels := map[string]bool{}
	for channel, entry := range registry.Channels {
		for _, target := range entry.Targets {
			if target.Same(ref) {
				channels[channel] = true
			}
		}
	}
	if _, routes, err := api.slackRoutes(ctx); err == nil {
		for channel, route := range routes {
			if slackRouteHasDestination(route) && services.SlackTargetRefFromRoute(route).Same(ref) {
				channels[services.NormalizeSlackChannelID(channel)] = true
			}
		}
	}
	ids := make([]string, 0, len(channels))
	for channel := range channels {
		ids = append(ids, channel)
	}
	sort.Strings(ids)
	for _, channel := range ids {
		set := api.slackChannelTargets(ctx, "", channel)
		card := SlackTargetChannel{ChannelID: channel, Targets: []SlackTargetChannelTarget{}}
		for i, target := range set.Targets {
			if target.Legacy {
				card.AdminRoute = true
			}
			card.Targets = append(card.Targets, SlackTargetChannelTarget{
				Slug: target.Slug, Label: target.Label, WorkspacePath: target.Ref.WorkspacePath, ProfileID: target.Ref.ProfileID,
				IsDefault: i == set.Default, IsThis: target.Ref.Same(ref),
			})
		}
		out.Channels = append(out.Channels, card)
	}
	return out
}

func slackTargetSettingsHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		claims := GetUserFromContext(r.Context())
		if claims == nil || claims.Provider == "bot_route" || claims.BotRouteGrant != "" {
			http.Error(w, "only an authenticated interactive user may read Slack settings", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodGet {
			ref := slackTargetFromRequest(r.Context(), r.URL.Query().Get("workspace_path"), r.URL.Query().Get("profile_id"))
			if ref.Empty() {
				http.Error(w, "workspace_path is required", http.StatusBadRequest)
				return
			}
			canManage := requireSlackTargetOwner(r.Context(), api, ref) == nil
			writeSlackJSON(w, api.slackTargetSettings(r.Context(), ref, canManage))
			return
		}
		var body struct {
			WorkspacePath string  `json:"workspace_path"`
			ProfileID     string  `json:"profile_id"`
			PlatformBot   *bool   `json:"platform_bot"`
			Slug          *string `json:"slug"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
			return
		}
		ref := slackTargetFromRequest(r.Context(), body.WorkspacePath, body.ProfileID)
		if err := requireSlackTargetOwner(r.Context(), api, ref); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		if _, err := api.slackDestinationRoute(r.Context(), ref.WorkspacePath, ref.ProfileID); err != nil {
			http.Error(w, fmt.Sprintf("cannot answer for %s in Slack: %v", ref.WorkspacePath, err), http.StatusBadRequest)
			return
		}
		slug := ""
		if body.Slug != nil {
			slug = strings.ToLower(strings.TrimSpace(*body.Slug))
			if slug != "" {
				if err := services.ValidSlackSlug(slug); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
			}
		}
		legacy := api.slackLegacyPlatformTarget(r.Context(), ref)
		label := slackDestinationLabel(r.Context(), ref.WorkspacePath, ref.ProfileID)
		turnedOn := false
		err := services.ModifySlackTargetsRegistry(r.Context(), func(registry *services.SlackTargetsRegistry) error {
			entry, found := registry.Settings(ref)
			if !found {
				entry = services.SlackTargetSettings{WorkspacePath: ref.WorkspacePath, ProfileID: ref.ProfileID, PlatformBot: registry.PlatformOptIn(ref, legacy)}
			}
			if body.PlatformBot != nil {
				if *body.PlatformBot && !registry.ProductAllowed(ref) {
					return fmt.Errorf("an admin has not allowed %s to use the AgentWorks bot", services.SlackTargetProduct(ref))
				}
				turnedOn = *body.PlatformBot && !entry.PlatformBot
				entry.PlatformBot = *body.PlatformBot
			}
			if body.Slug != nil {
				entry.Slug = slug
			}
			entry.Label = label
			entry.OwnerID = firstNonBlank(entry.OwnerID, GetUserFromContext(r.Context()).UserID)
			entry.UpdatedBy = GetUserFromContext(r.Context()).UserID
			entry.UpdatedAt = time.Now().UTC()
			registry.PutSettings(entry)
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if turnedOn {
			// The platform bot needs the bot manager's handler for DMs and
			// owner-added channels even when no admin route exists.
			if svc := services.GetSlackService(); svc != nil {
				registerSlackBotConnector(api.botManager, svc)
			}
		}
		writeSlackJSON(w, api.slackTargetSettings(r.Context(), ref, true))
	}
}

func addSlackTargetChannelHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		channel := services.NormalizeSlackChannelID(mux.Vars(r)["channel"])
		if !slackChannelIDPattern.MatchString(channel) || services.IsSlackDMChannel(channel) {
			http.Error(w, "an exact Slack channel ID is required (e.g. C1234567890)", http.StatusBadRequest)
			return
		}
		var body struct {
			WorkspacePath string `json:"workspace_path"`
			ProfileID     string `json:"profile_id"`
			MakeDefault   bool   `json:"make_default"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
			return
		}
		ctx := r.Context()
		ref := slackTargetFromRequest(ctx, body.WorkspacePath, body.ProfileID)
		if err := requireSlackTargetOwner(ctx, api, ref); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		if ref.IsCode() {
			http.Error(w, "Code answers only 1:1 Slack DMs, not channels", http.StatusBadRequest)
			return
		}
		registry := slackLoadTargetsRegistry(ctx)
		if !registry.PlatformOptIn(ref, api.slackLegacyPlatformTarget(ctx, ref)) {
			http.Error(w, "turn on \"Use the AgentWorks bot\" first", http.StatusBadRequest)
			return
		}
		if _, err := api.slackDestinationRoute(ctx, ref.WorkspacePath, ref.ProfileID); err != nil {
			http.Error(w, fmt.Sprintf("cannot answer for %s in Slack: %v", ref.WorkspacePath, err), http.StatusBadRequest)
			return
		}
		admin := currentUserIsAdmin(r)
		if !admin {
			// A channel is added only by someone in it: the bot answers there
			// as the route, for everyone in the channel.
			member, err := slackChannelMembership(ctx, GetUserFromContext(ctx).Email, channel)
			if err != nil {
				http.Error(w, fmt.Sprintf("could not check your membership of %s: %v", channel, err), http.StatusBadRequest)
				return
			}
			if !member {
				http.Error(w, fmt.Sprintf("you are not a member of %s in Slack; join it first", channel), http.StatusForbidden)
				return
			}
		}
		_, hasAdminRoute := api.slackLegacyPlatformRoute(ctx, channel)
		userID := GetUserFromContext(ctx).UserID
		err := services.ModifySlackTargetsRegistry(ctx, func(registry *services.SlackTargetsRegistry) error {
			entry := registry.Channels[channel]
			present := false
			for _, target := range entry.Targets {
				if target.Same(ref) {
					present = true
				}
			}
			if !present {
				added := ref
				added.AddedBy = userID
				entry.Targets = append(entry.Targets, added)
			}
			switch {
			case hasAdminRoute:
				// The admin's route stays the channel's default.
			case entry.Default == nil && (body.MakeDefault || len(entry.Targets) == 1):
				def := ref
				entry.Default = &def
			case body.MakeDefault && entry.Default != nil && !entry.Default.Same(ref):
				if !admin && requireSlackTargetOwner(ctx, api, *entry.Default) != nil {
					return fmt.Errorf("the channel's default belongs to another owner; ask them or an admin to change it")
				}
				def := ref
				entry.Default = &def
			}
			registry.Channels[channel] = entry
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeSlackJSON(w, api.slackTargetSettings(ctx, ref, true))
	}
}

func removeSlackTargetChannelHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channel := services.NormalizeSlackChannelID(mux.Vars(r)["channel"])
		ctx := r.Context()
		ref := slackTargetFromRequest(ctx, r.URL.Query().Get("workspace_path"), r.URL.Query().Get("profile_id"))
		if err := requireSlackTargetOwner(ctx, api, ref); err != nil && !currentUserIsAdmin(r) {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		removed := false
		err := services.ModifySlackTargetsRegistry(ctx, func(registry *services.SlackTargetsRegistry) error {
			entry := registry.Channels[channel]
			kept := entry.Targets[:0]
			for _, target := range entry.Targets {
				if target.Same(ref) {
					removed = true
					continue
				}
				kept = append(kept, target)
			}
			entry.Targets = kept
			if entry.Default != nil && entry.Default.Same(ref) {
				entry.Default = nil
			}
			registry.Channels[channel] = entry
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !removed {
			if _, ok := api.slackLegacyPlatformRoute(ctx, channel); ok {
				http.Error(w, "this channel's route was set by an admin in Access > Slack; remove it there", http.StatusConflict)
				return
			}
			http.Error(w, fmt.Sprintf("%s does not answer in %s", ref.WorkspacePath, channel), http.StatusNotFound)
			return
		}
		// Live turns in the channel whose target is gone stop now; later
		// turns are refused at the query boundary anyway.
		api.revokeSlackConnectionChannelSessions(ctx, "", channel)
		writeSlackJSON(w, api.slackTargetSettings(ctx, ref, true))
	}
}

func slackPlatformBotSettingsHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		claims := GetUserFromContext(r.Context())
		if claims == nil || claims.Provider == "bot_route" || !currentUserIsAdmin(r) {
			http.Error(w, "only a platform admin may change the AgentWorks bot's settings", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			var body struct {
				Products []string `json:"products"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
				return
			}
			var products []string
			for _, product := range body.Products {
				product = strings.ToLower(strings.TrimSpace(product))
				switch product {
				case "workflows", "crew", "code":
					products = append(products, product)
				case "":
				default:
					http.Error(w, fmt.Sprintf("unknown product %q (workflows, crew, code)", product), http.StatusBadRequest)
					return
				}
			}
			if err := services.ModifySlackTargetsRegistry(r.Context(), func(registry *services.SlackTargetsRegistry) error {
				registry.Products = products
				return nil
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		registry := slackLoadTargetsRegistry(r.Context())
		on := 0
		for _, entry := range registry.Targets {
			if entry.PlatformBot {
				on++
			}
		}
		writeSlackJSON(w, map[string]interface{}{"products": append([]string{}, registry.Products...), "targets_on": on})
	}
}

func writeSlackJSON(w http.ResponseWriter, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
