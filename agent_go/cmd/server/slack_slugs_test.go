package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
