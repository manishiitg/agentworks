package server

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// Slack slugs (docs/design/slack_slugs.md, PLAT-668): what each Slack app
// reaches, resolved server-side on every message, turn and tool call.
//
//   - An own bot (a connection scoped to a workflow, Crew or Code) reaches
//     the targets its owner attached: its own destination, the targets on its
//     channel routes, and its DM-only targets (Codes included).
//   - The platform bot (an unscoped connection) reaches targets whose owners
//     turned on "Use the AgentWorks bot" (config/slack-targets.json), within
//     the admin's product limit. A target that already had the admin's
//     channel route counts as switched on until its owner decides.
//
// A channel reaches its allowed list (default first); a DM reaches the app's
// targets the matched person can reach with their own access. Codes never
// answer in channels.

// slackRoutingHooksFor wires services.SlackRoutingHooks to this server.
func (api *StreamingAPI) slackRoutingHooksFor() *services.SlackRoutingHooks {
	return &services.SlackRoutingHooks{
		Channel:  api.slackChannelTargets,
		DM:       api.slackDMTargets,
		Route:    api.slackTargetRoute,
		CanReach: api.slackCanReach,
		Trigger: func(ctx context.Context, connectionID, channelID string) *ChannelRoute {
			route, ok, _ := api.slackTriggerRoute(ctx, connectionID, channelID)
			if !ok {
				return nil
			}
			return &route
		},
	}
}

// slackTriggerRoute is the route whose saved trigger applies to top-level
// messages in a channel on an app: an own bot's channel route (its default
// target, with the route's trigger), else the shared bot's admin route while
// its target is still switched on. dedicated reports an own bot.
func (api *StreamingAPI) slackTriggerRoute(ctx context.Context, connectionID, channelID string) (ChannelRoute, bool, bool) {
	channelID = services.NormalizeSlackChannelID(channelID)
	conn, found := slackAppConnection(connectionID)
	if slackOwnBot(conn, found) {
		entry, ok := conn.ChannelRoutes[channelID]
		if !conn.Enabled || !ok || entry.Trigger == nil || entry.Default().Empty() || entry.Default().IsCode() {
			return ChannelRoute{}, false, true
		}
		route, err := api.slackDestinationRoute(ctx, entry.WorkspacePath, entry.ProfileID)
		if err != nil || route == nil {
			return ChannelRoute{}, false, true
		}
		route.Trigger = entry.Trigger
		return *route, true, true
	}
	_, routes, err := api.slackRoutes(ctx)
	if err != nil {
		return ChannelRoute{}, false, false
	}
	route, ok := routes[channelID]
	if !ok || (slackRouteHasDestination(route) && !slackLoadTargetsRegistry(ctx).PlatformOptIn(services.SlackTargetRefFromRoute(route), true)) {
		return ChannelRoute{}, false, false
	}
	return route, true, false
}

// slackAppConnection resolves the connection a listener serves. found=false
// means the platform bot without a registry entry (a legacy single-app
// config), which is the platform bot too.
func slackAppConnection(connectionID string) (services.SlackConnection, bool) {
	svc := services.GetSlackService()
	if svc == nil {
		return services.SlackConnection{}, false
	}
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		connectionID = svc.DefaultConnectionID()
	}
	if connectionID == "" {
		return services.SlackConnection{}, false
	}
	return svc.GetConnection(connectionID)
}

// slackOwnBot reports whether conn is a workflow's, Crew's or Code's own bot.
func slackOwnBot(conn services.SlackConnection, found bool) bool {
	return found && strings.TrimSpace(conn.WorkspacePath) != ""
}

// slackLoadTargetsRegistry reads the registry; a broken file fails closed for
// new targets while the admin's legacy routes keep working.
func slackLoadTargetsRegistry(ctx context.Context) *services.SlackTargetsRegistry {
	registry, err := services.LoadSlackTargetsRegistry(ctx)
	if err != nil || registry == nil {
		return &services.SlackTargetsRegistry{Products: []string{"__unreadable__"}}
	}
	return registry
}

// slackNamedTargets gives each ref its slug and label: the owner's slug when
// set, else the target's name.
func slackNamedTargets(ctx context.Context, registry *services.SlackTargetsRegistry, refs []services.SlackTargetRef) []services.SlackTarget {
	out := make([]services.SlackTarget, 0, len(refs))
	for _, ref := range refs {
		out = append(out, slackNamedTarget(ctx, registry, ref))
	}
	return out
}

func slackNamedTarget(ctx context.Context, registry *services.SlackTargetsRegistry, ref services.SlackTargetRef) services.SlackTarget {
	target := services.SlackTarget{Ref: ref}
	if entry, ok := registry.Settings(ref); ok {
		target.Slug = strings.TrimSpace(entry.Slug)
		target.Label = strings.TrimSpace(entry.Label)
	}
	if target.Label == "" {
		target.Label = slackDestinationLabel(ctx, ref.WorkspacePath, ref.ProfileID)
	}
	if target.Slug == "" {
		target.Slug = services.SlackSlug(target.Label)
	}
	if target.Slug == "" {
		target.Slug = services.SlackSlug(path.Base(strings.TrimSpace(ref.WorkspacePath)))
	}
	return target
}

func appendSlackRef(refs []services.SlackTargetRef, ref services.SlackTargetRef) []services.SlackTargetRef {
	if ref.Empty() {
		return refs
	}
	for _, seen := range refs {
		if seen.Same(ref) {
			return refs
		}
	}
	return append(refs, ref)
}

// slackLegacyPlatformRoute is the admin's channel route for a channel on the
// platform bot (bot-connectors.json allowed_channels), when it names a valid
// destination.
func (api *StreamingAPI) slackLegacyPlatformRoute(ctx context.Context, channelID string) (*ChannelRoute, bool) {
	cfg, _, err := api.slackRoutes(ctx)
	if err != nil || cfg == nil || channelID == "" {
		return nil, false
	}
	route := services.ResolveChannelRoute(cfg.AllowedChannels, channelID)
	return route, route != nil
}

// slackLegacyPlatformTarget reports whether ref is the destination of any of
// the admin's channel routes: such a target counts as switched on until its
// owner decides (the migration of pre-slug setups).
func (api *StreamingAPI) slackLegacyPlatformTarget(ctx context.Context, ref services.SlackTargetRef) bool {
	_, routes, err := api.slackRoutes(ctx)
	if err != nil {
		return false
	}
	for _, route := range routes {
		if slackRouteHasDestination(route) && services.SlackTargetRefFromRoute(route).Same(ref) {
			return true
		}
	}
	return false
}

// slackChannelTargets is the channel's allowed list on an app.
func (api *StreamingAPI) slackChannelTargets(ctx context.Context, connectionID, channelID string) services.SlackTargetSet {
	channelID = services.NormalizeSlackChannelID(channelID)
	set := services.SlackTargetSet{Default: -1}
	conn, found := slackAppConnection(connectionID)
	registry := slackLoadTargetsRegistry(ctx)
	if slackOwnBot(conn, found) {
		set.Dedicated = true
		if !conn.Enabled {
			set.Disabled = true
			return set
		}
		// Slack decides the channels and slugs decide the target (owner,
		// 2026-10-07): an own bot answers in every channel it is a member of
		// (messages only arrive from those), for every target attached to it:
		// its own, the targets on its channel routes and its DM attachments.
		// Codes answer DMs only. A channel route's destination is kept as the
		// default automated messages (triggers) use; people pick by slug.
		refs := slackOwnBotTargets(conn)
		var kept []services.SlackTargetRef
		for _, ref := range refs {
			if !ref.IsCode() {
				kept = append(kept, ref)
			}
		}
		set.Targets = slackNamedTargets(ctx, registry, kept)
		if entry, ok := conn.ChannelRoutes[channelID]; ok && !entry.Default().Empty() {
			set.Default = set.Find(entry.Default())
		}
		return set
	}
	set.Platform = true
	if legacy, ok := api.slackLegacyPlatformRoute(ctx, channelID); ok {
		ref := services.SlackTargetRefFromRoute(*legacy)
		if !ref.IsCode() && registry.PlatformOptIn(ref, true) {
			target := slackNamedTarget(ctx, registry, ref)
			target.Legacy = true
			set.Targets = append(set.Targets, target)
			set.Default = 0
		}
	}
	entry := registry.Channels[channelID]
	for _, ref := range entry.Targets {
		if ref.Empty() || ref.IsCode() || set.Find(ref) >= 0 || !registry.PlatformOptIn(ref, api.slackLegacyPlatformTarget(ctx, ref)) {
			continue
		}
		set.Targets = append(set.Targets, slackNamedTarget(ctx, registry, ref))
	}
	if set.Default < 0 && entry.Default != nil {
		set.Default = set.Find(*entry.Default)
	}
	return set
}

// slackDMTargets lists what an app offers in DMs, before the person's own
// access is checked. Default is an own bot's own target.
func (api *StreamingAPI) slackDMTargets(ctx context.Context, connectionID string) services.SlackTargetSet {
	set := services.SlackTargetSet{Default: -1}
	conn, found := slackAppConnection(connectionID)
	registry := slackLoadTargetsRegistry(ctx)
	if slackOwnBot(conn, found) {
		set.Dedicated = true
		if !conn.Enabled {
			set.Disabled = true
			return set
		}
		set.Targets = slackNamedTargets(ctx, registry, slackOwnBotTargets(conn))
		set.Default = 0
		return set
	}
	set.Platform = true
	var refs []services.SlackTargetRef
	for _, entry := range registry.Targets {
		if entry.PlatformBot && registry.ProductAllowed(entry.Ref()) {
			refs = appendSlackRef(refs, entry.Ref())
		}
	}
	if _, routes, err := api.slackRoutes(ctx); err == nil {
		channels := make([]string, 0, len(routes))
		for channel := range routes {
			channels = append(channels, channel)
		}
		sort.Strings(channels)
		for _, channel := range channels {
			route := routes[channel]
			if !slackRouteHasDestination(route) {
				continue
			}
			ref := services.SlackTargetRefFromRoute(route)
			if registry.PlatformOptIn(ref, true) {
				refs = appendSlackRef(refs, ref)
			}
		}
	}
	set.Targets = slackNamedTargets(ctx, registry, refs)
	return set
}

// slackTargetRoute resolves a target to its runnable Run-mode route, checking
// again that the app still reaches it: an own bot that is on, or a platform
// target still switched on and allowed. channelID is "" for a DM.
func (api *StreamingAPI) slackTargetRoute(ctx context.Context, connectionID, channelID string, target services.SlackTarget) (*ChannelRoute, error) {
	ref := target.Ref
	if ref.Empty() {
		return nil, fmt.Errorf("no target")
	}
	channelID = services.NormalizeSlackChannelID(channelID)
	if channelID != "" && ref.IsCode() {
		return nil, fmt.Errorf("Code answers only 1:1 DMs")
	}
	conn, found := slackAppConnection(connectionID)
	if slackOwnBot(conn, found) {
		if !conn.Enabled {
			return nil, fmt.Errorf("this bot is switched off")
		}
		route, err := api.slackDestinationRoute(ctx, ref.WorkspacePath, ref.ProfileID)
		if err != nil {
			return nil, err
		}
		if entry, ok := conn.ChannelRoutes[channelID]; ok && channelID != "" && entry.Trigger != nil && entry.Default().Same(ref) {
			route.Trigger = entry.Trigger
		}
		return route, nil
	}
	registry := slackLoadTargetsRegistry(ctx)
	var legacy *ChannelRoute
	if channelID != "" {
		if route, ok := api.slackLegacyPlatformRoute(ctx, channelID); ok {
			legacy = route
		}
	}
	if target.Legacy {
		if legacy == nil || !services.SlackTargetRefFromRoute(*legacy).Same(ref) || !registry.PlatformOptIn(ref, true) {
			return nil, fmt.Errorf("the channel route changed")
		}
		return legacy, nil
	}
	if !registry.PlatformOptIn(ref, api.slackLegacyPlatformTarget(ctx, ref)) {
		return nil, fmt.Errorf("%s is not switched on for the AgentWorks bot", firstNonBlank(target.Label, ref.WorkspacePath))
	}
	route, err := api.slackDestinationRoute(ctx, ref.WorkspacePath, ref.ProfileID)
	if err != nil {
		return nil, err
	}
	if legacy != nil && len(legacy.BlockedEmails) > 0 {
		// The admin's email exclusions are the channel's.
		route.BlockedEmails = append([]string(nil), legacy.BlockedEmails...)
	}
	return route, nil
}

// slackCanReach is a DM sender's own access to a target: a workflow for
// anyone with access to it, a Crew for its owner and the people it is shared
// with, a Code for its owner only.
func (api *StreamingAPI) slackCanReach(ctx context.Context, accountID string, route ChannelRoute) bool {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || services.IsRevokedSlackRoute(route) {
		return false
	}
	if services.SlackTargetRefFromRoute(route).IsCode() {
		owner := services.RouteWorkspaceUserID(route, "")
		return owner != "" && owner == sanitizeUserIDForPath(accountID)
	}
	allowed, err := api.checkWhatsAppWorkflowAccess(ctx, accountID, route)
	return err == nil && allowed
}

// slackRequestMatchesRoute reports whether a turn's request runs on route.
func slackRequestMatchesRoute(route ChannelRoute, req QueryRequest) bool {
	return slackRouteFolderMatches(route, req.SelectedFolder) &&
		req.AgentProfileID == route.ProfileID &&
		req.PresetQueryID == route.WorkflowID &&
		sameProjectConversation(route.ConversationKey, req.AgentProfileConversationKey)
}

// slackRefMayMatch is a cheap prefilter before resolving a target.
func slackRefMayMatch(ref services.SlackTargetRef, profileID, workspacePath string) bool {
	if !strings.EqualFold(strings.TrimSpace(ref.ProfileID), strings.TrimSpace(profileID)) {
		return false
	}
	if strings.TrimSpace(profileID) != "" {
		// Crew paths have several spellings; the resolved route decides.
		return true
	}
	return services.SameSlackScopePath(ref.WorkspacePath, workspacePath)
}

// slackChannelRouteMatching finds the target allowed in a channel on an app
// whose route matches: the run-time check that a turn's target is still on
// the channel's list (and, on the platform bot, still switched on).
// dedicated reports an own bot.
func (api *StreamingAPI) slackChannelRouteMatching(ctx context.Context, connectionID, channelID, profileID, workspacePath string, match func(ChannelRoute) bool) (ChannelRoute, bool, bool) {
	set := api.slackChannelTargets(ctx, connectionID, channelID)
	if set.Disabled {
		return ChannelRoute{}, false, set.Dedicated
	}
	for _, target := range set.Targets {
		if !slackRefMayMatch(target.Ref, profileID, workspacePath) {
			continue
		}
		route, err := api.slackTargetRoute(ctx, connectionID, channelID, target)
		if err != nil || route == nil {
			continue
		}
		if match(*route) {
			return *route, true, set.Dedicated
		}
	}
	return ChannelRoute{}, false, set.Dedicated
}

// slackDMRouteMatching finds the DM target a turn runs on, if the app still
// offers it and the account can still reach it.
func (api *StreamingAPI) slackDMRouteMatching(ctx context.Context, connectionID, accountID string, req QueryRequest) (ChannelRoute, bool) {
	set := api.slackDMTargets(ctx, connectionID)
	if set.Disabled {
		return ChannelRoute{}, false
	}
	for _, target := range set.Targets {
		if !slackRefMayMatch(target.Ref, req.AgentProfileID, req.SelectedFolder) {
			continue
		}
		route, err := api.slackTargetRoute(ctx, connectionID, "", target)
		if err != nil || route == nil || !slackRequestMatchesRoute(*route, req) {
			continue
		}
		if api.slackCanReach(ctx, accountID, *route) {
			return *route, true
		}
	}
	return ChannelRoute{}, false
}

// slackOwnBotTargets lists every target attached to an own bot, its own
// first: attaching a target to a bot is the only grant (PLAT-668).
func slackOwnBotTargets(conn services.SlackConnection) []services.SlackTargetRef {
	refs := []services.SlackTargetRef{{WorkspacePath: conn.WorkspacePath, ProfileID: conn.ProfileID}}
	channels := make([]string, 0, len(conn.ChannelRoutes))
	for channel := range conn.ChannelRoutes {
		channels = append(channels, channel)
	}
	sort.Strings(channels)
	for _, channel := range channels {
		for _, ref := range conn.ChannelRoutes[channel].Allowed() {
			refs = appendSlackRef(refs, ref)
		}
	}
	for _, ref := range conn.Targets {
		refs = appendSlackRef(refs, ref)
	}
	return refs
}
