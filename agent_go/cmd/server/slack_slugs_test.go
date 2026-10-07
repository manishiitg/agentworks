package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

// Slack slugs (docs/design/slack_slugs.md, PLAT-668), driven through the bot
// dry run: the real inbound path, the production-wired bot manager and the
// query boundary's revalidation, stopping before the model.

func (w botDryRunWorld) platformBot(t *testing.T) services.SlackConnection {
	t.Helper()
	shared := w.createApp(t, "Shared", "", "")
	if err := w.slack.SetDefaultSlackConnection(context.Background(), shared.ID); err != nil {
		t.Fatal(err)
	}
	return shared
}

func (w botDryRunWorld) adminRoutes(t *testing.T, routes map[string]services.ChannelRoute) {
	t.Helper()
	raw, _ := json.Marshal(routes)
	if _, err := w.api.chatStore.UpsertBotConnectorConfig(context.Background(), &chathistory.CreateBotConnectorConfigRequest{ID: "slack", AllowedChannels: string(raw)}); err != nil {
		t.Fatal(err)
	}
}

func (w botDryRunWorld) targetsRegistry(t *testing.T, registry services.SlackTargetsRegistry) {
	t.Helper()
	raw, _ := json.Marshal(registry)
	w.mock.mu.Lock()
	w.mock.files["config/slack-targets.json"] = string(raw)
	w.mock.mu.Unlock()
}

func (w botDryRunWorld) workflowID(t *testing.T) string {
	t.Helper()
	manifest, found, err := ReadWorkflowManifest(context.Background(), "Workflow/shared")
	if err != nil || !found {
		t.Fatalf("shared workflow manifest: found=%v err=%v", found, err)
	}
	return manifest.ID
}

var (
	slugWorkflow = services.SlackTargetRef{WorkspacePath: "Workflow/shared"}
	slugCrew     = services.SlackTargetRef{WorkspacePath: crewRunModeOwnerRoot, ProfileID: "work"}
)

// Migration: a route saved before slugs (an own bot's channel route, the
// admin's platform route) is a one-target list that is also the default, and
// it keeps answering exactly as before.
func TestSlackSlugsMigratedRouteIsOneTargetDefault(t *testing.T) {
	w := newBotDryRunWorld(t)
	ctx := context.Background()
	app := w.createApp(t, "SDE", crewRunModeOwnerRoot, "work")
	if _, err := w.slack.SetSlackConnectionChannelRoute(ctx, app.ID, "C0ROUTED01", &services.SlackConnectionRoute{WorkspacePath: "Workflow/shared"}); err != nil {
		t.Fatal(err)
	}
	routed := w.api.slackChannelTargets(ctx, app.ID, "C0ROUTED01")
	if len(routed.Targets) != 1 || routed.Default != 0 || !routed.Targets[0].Ref.Same(slugWorkflow) {
		t.Fatalf("own bot's channel route = %+v, want the workflow alone as default", routed)
	}
	requireAdmitted(t, w.mention(t, app.ID, "C0ROUTED01"), "workflow")
	if own := w.api.slackChannelTargets(ctx, app.ID, "C0ANYCHAN1"); len(own.Targets) != 1 || own.Default != 0 || !own.Targets[0].Ref.Same(slugCrew) {
		t.Fatalf("own bot in an unlisted channel = %+v, want its own crew as default", own)
	}

	shared := w.platformBot(t)
	w.adminRoutes(t, map[string]services.ChannelRoute{"C0ADMIN001": {WorkflowID: w.workflowID(t), WorkspacePath: "Workflow/shared", BotGrant: "run"}})
	platform := w.api.slackChannelTargets(ctx, shared.ID, "C0ADMIN001")
	if len(platform.Targets) != 1 || platform.Default != 0 || !platform.Targets[0].Legacy {
		t.Fatalf("admin route = %+v, want one legacy default target", platform)
	}
	requireAdmitted(t, w.mention(t, shared.ID, "C0ADMIN001"), "workflow")
}

// Opt-in: nothing is reachable on the platform bot until its owner turns on
// "Use the AgentWorks bot", and turning it off cuts access at once, the
// admin's older route included.
func TestSlackSlugsPlatformBotNeedsOptIn(t *testing.T) {
	w := newBotDryRunWorld(t)
	shared := w.platformBot(t)
	channel := map[string]services.SlackPlatformChannel{"C0TEAM0001": {Targets: []services.SlackTargetRef{slugWorkflow, slugCrew}, Default: &slugWorkflow}}

	w.targetsRegistry(t, services.SlackTargetsRegistry{Channels: channel})
	if outcome := w.mention(t, shared.ID, "C0TEAM0001"); outcome.Admitted {
		t.Fatalf("a target nobody switched on answered: %+v", outcome)
	}

	w.targetsRegistry(t, services.SlackTargetsRegistry{Channels: channel, Targets: []services.SlackTargetSettings{
		{WorkspacePath: slugWorkflow.WorkspacePath, PlatformBot: true},
		{WorkspacePath: slugCrew.WorkspacePath, ProfileID: "work", PlatformBot: true},
	}})
	requireAdmitted(t, w.mention(t, shared.ID, "C0TEAM0001"), "workflow")

	w.targetsRegistry(t, services.SlackTargetsRegistry{Channels: channel, Targets: []services.SlackTargetSettings{
		{WorkspacePath: slugWorkflow.WorkspacePath, PlatformBot: false},
		{WorkspacePath: slugCrew.WorkspacePath, ProfileID: "work", PlatformBot: true},
	}})
	if outcome := w.mention(t, shared.ID, "C0TEAM0001"); outcome.Admitted && strings.Contains(outcome.Destination, "workflow") {
		t.Fatalf("a switched-off workflow still answered: %+v", outcome)
	}

	w.adminRoutes(t, map[string]services.ChannelRoute{"C0ADMIN001": {WorkflowID: w.workflowID(t), WorkspacePath: "Workflow/shared", BotGrant: "run"}})
	if outcome := w.mention(t, shared.ID, "C0ADMIN001"); outcome.Admitted {
		t.Fatalf("switching off did not cut the admin's older route: %+v", outcome)
	}
}

// teamRegistry: the workflow ("ops") and the crew ("alpha") switched on, both
// allowed in C0TEAM0001; def names the channel's default (nil for none).
func teamRegistry(def *services.SlackTargetRef) services.SlackTargetsRegistry {
	return services.SlackTargetsRegistry{
		Targets: []services.SlackTargetSettings{
			{WorkspacePath: slugWorkflow.WorkspacePath, Slug: "ops", Label: "Ops", PlatformBot: true},
			{WorkspacePath: slugCrew.WorkspacePath, ProfileID: "work", Slug: "alpha", Label: "Alpha", PlatformBot: true},
		},
		Channels: map[string]services.SlackPlatformChannel{"C0TEAM0001": {Targets: []services.SlackTargetRef{slugWorkflow, slugCrew}, Default: def}},
	}
}

func (w botDryRunWorld) say(t *testing.T, connectionID, channelID, text string) services.BotDryRunOutcome {
	t.Helper()
	outcome, err := w.api.dryRunSlackMention(context.Background(), connectionID, channelID, dryRunOwnerEmail, text)
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}

// "@bot <slug> ..." picks one of the channel's allowed targets; a first word
// that is not an allowed slug is message text and the default answers; with
// no default the bot asks with buttons. The run-time check refuses a turn
// whose target was removed from the channel.
func TestSlackSlugsChannelPickAndAllowedList(t *testing.T) {
	w := newBotDryRunWorld(t)
	shared := w.platformBot(t)
	w.targetsRegistry(t, teamRegistry(&slugWorkflow))

	crew := w.say(t, shared.ID, "C0TEAM0001", "alpha what changed?")
	requireAdmitted(t, crew, "crew-aaa")
	if query, _ := crew.Request["query"].(string); strings.Contains(query, "alpha what") {
		t.Fatalf("the slug stayed in the message: %q", query)
	}
	requireAdmitted(t, w.say(t, shared.ID, "C0TEAM0001", "ops what failed today?"), "workflow")
	text := w.say(t, shared.ID, "C0TEAM0001", "beta is not a slug here")
	requireAdmitted(t, text, "workflow")
	if query, _ := text.Request["query"].(string); !strings.Contains(query, "beta is not a slug here") {
		t.Fatalf("a word that is not an allowed slug was dropped: %q", query)
	}

	// The crew's slug is no grant where the crew is not allowed.
	w.adminRoutes(t, map[string]services.ChannelRoute{"C0OTHER001": {WorkflowID: w.workflowID(t), WorkspacePath: "Workflow/shared", BotGrant: "run"}})
	if outcome := w.say(t, shared.ID, "C0OTHER001", "alpha what changed?"); strings.Contains(outcome.Destination, "crew") {
		t.Fatalf("a slug reached a target this channel does not allow: %+v", outcome)
	}

	w.targetsRegistry(t, teamRegistry(nil))
	prompt := w.say(t, shared.ID, "C0TEAM0001", "hello")
	if prompt.Admitted || !strings.Contains(prompt.Reason, "ops") || !strings.Contains(prompt.Reason, "alpha") {
		t.Fatalf("no default should ask with one button per target: %+v", prompt)
	}

	// Removing the crew from the channel stops its turns at the query
	// boundary, every turn and tool call.
	registry := teamRegistry(&slugWorkflow)
	registry.Channels["C0TEAM0001"] = services.SlackPlatformChannel{Targets: []services.SlackTargetRef{slugWorkflow}, Default: &slugWorkflow}
	w.targetsRegistry(t, registry)
	if err := w.api.admitBotTurn(context.Background(), crew.Request, crew.SessionID, crew.UserID, nil); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("a turn for a target removed from the channel was admitted: %v", err)
	}
}

// DMs to the platform bot reach only what the sender can reach with their own
// access: the owner gets the full chat, a reader Run mode, anyone else
// nothing. A Code reaches its owner only.
func TestSlackSlugsDMAccessIsThePersons(t *testing.T) {
	w := newBotDryRunWorld(t)
	shared := w.platformBot(t)
	w.targetsRegistry(t, teamRegistry(&slugWorkflow))
	w.dmSenders(t, shared, map[string]services.SlackDMSender{"U0OWNER": dmSender(dryRunOwnerEmail), "U0READER": dmSender("reader@example.com"), "U0STRANGER": dmSender("stranger@example.com")})
	dm := func(sender, text string) services.BotDryRunOutcome {
		outcome, err := w.api.dryRunSlackDM(context.Background(), shared.ID, sender, "D0DMCHAN01", text)
		if err != nil {
			t.Fatal(err)
		}
		return outcome
	}

	requireMode(t, dm("U0OWNER", "ops what changed?"), "owner", "full")
	requireMode(t, dm("U0READER", "ops what changed?"), "reader", "run")
	if outcome := dm("U0STRANGER", "ops what changed?"); outcome.Admitted || len(outcome.Replies) == 0 {
		t.Fatalf("a sender without access reached the workflow: %+v", outcome)
	}
	if listed := dm("U0READER", "list"); !strings.Contains(listed.Reason, "ops") {
		t.Fatalf("list did not show what the reader can reach: %+v", listed)
	}

	code := ChannelRoute{ProfileID: "code", ConversationKey: "code-1", WorkspacePath: "_users/owner/Code/projects/one", WorkspaceUserID: "owner"}
	if !w.api.slackCanReach(context.Background(), "owner", code) || w.api.slackCanReach(context.Background(), "reader", code) {
		t.Fatal("a Code must reach its owner and nobody else")
	}
}

// A target's owner adds a platform-bot channel only if Slack says they are a
// member of it; the first target added becomes the channel's default.
func TestSlackSlugsAddChannelNeedsMembership(t *testing.T) {
	w := newBotDryRunWorld(t)
	shared := w.platformBot(t)
	w.targetsRegistry(t, services.SlackTargetsRegistry{Targets: []services.SlackTargetSettings{{WorkspacePath: "Workflow/shared", PlatformBot: true}}})
	member := false
	previous := slackChannelMembership
	slackChannelMembership = func(_ context.Context, email, _ string) (bool, error) {
		return member && email == dryRunOwnerEmail, nil
	}
	t.Cleanup(func() { slackChannelMembership = previous })
	add := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/human-feedback/slack/targets/channels/C0TEAM0001", strings.NewReader(`{"workspace_path":"Workflow/shared"}`))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "aman", Email: dryRunOwnerEmail}))
		req = mux.SetURLVars(req, map[string]string{"channel": "C0TEAM0001"})
		rec := httptest.NewRecorder()
		addSlackTargetChannelHandler(w.api)(rec, req)
		return rec
	}

	if rec := add(); rec.Code != http.StatusForbidden {
		t.Fatalf("a non-member added the channel: %d %s", rec.Code, rec.Body.String())
	}
	if outcome := w.mention(t, shared.ID, "C0TEAM0001"); outcome.Admitted {
		t.Fatalf("a refused channel answers: %+v", outcome)
	}
	member = true
	if rec := add(); rec.Code != http.StatusOK {
		t.Fatalf("a member could not add the channel: %d %s", rec.Code, rec.Body.String())
	}
	requireAdmitted(t, w.mention(t, shared.ID, "C0TEAM0001"), "workflow")
}

// Slack triggers also run on an own bot's channel routes, not only the shared
// bot's (PLAT-668): the route resolves to the default target with the
// channel's trigger, for the app the event arrived on.
func TestSlackSlugsOwnBotChannelTrigger(t *testing.T) {
	w := newBotDryRunWorld(t)
	ctx := context.Background()
	app := w.createApp(t, "Shared-WF", "Workflow/shared", "")
	trigger := &services.SlackTrigger{Type: "human_message", Contains: "incident", GroupNames: []string{"default"}}
	if _, err := w.slack.ModifySlackConnection(ctx, app.ID, func(conn *services.SlackConnection) error {
		conn.ChannelRoutes["C0TRIGGER1"] = services.SlackConnectionRoute{WorkspacePath: "Workflow/shared", Trigger: trigger}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	route, ok, dedicated := w.api.slackTriggerRoute(ctx, app.ID, "C0TRIGGER1")
	if !ok || !dedicated || route.Trigger == nil || route.WorkflowID != w.workflowID(t) {
		t.Fatalf("own bot trigger route = %+v ok=%v dedicated=%v", route, ok, dedicated)
	}
	if _, ok, _ := w.api.slackTriggerRoute(ctx, app.ID, "C0NOTRIG01"); ok {
		t.Fatal("a channel without a trigger resolved one")
	}
}

// The slug settings API is reachable through the Slack routes the server
// registers (it once was never wired, so the Slack tab's calls 404ed).
func TestSlackSlugsSettingsRouteIsRegistered(t *testing.T) {
	w := newBotDryRunWorld(t)
	w.platformBot(t)
	router := mux.NewRouter()
	SlackConnectionRoutes(router, w.api)
	req := httptest.NewRequest(http.MethodGet, "/api/human-feedback/slack/targets/settings?workspace_path=Workflow/shared", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "aman", Email: dryRunOwnerEmail}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var settings SlackTargetSettingsResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &settings) != nil || settings.Slug == "" || !settings.CanManage {
		t.Fatalf("GET settings = %d %s", rec.Code, rec.Body.String())
	}
}
