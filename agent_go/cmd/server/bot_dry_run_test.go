package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

// End-to-end bot tests (docs/design/bot_destination_scope.md): each feeds a
// synthetic Slack mention through the real inbound path — the app's route
// and mention message, the production-wired bot manager's routing and turn
// building, then the query boundary's revalidation and target access — and
// stops before the model. They start from what the UI saves, not from
// hand-built physical paths, which is how the crew bugs of 2026-09-25/26
// slipped through step-by-step tests.

const dryRunOwnerEmail = "owner@example.com"

type botDryRunWorld struct {
	*crewRunModeFixture
	slack *services.SlackService
}

func newBotDryRunWorld(t *testing.T) botDryRunWorld {
	t.Helper()
	fx := newCrewRunModeFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"aman","email":"`+dryRunOwnerEmail+`","can_create":true},{"id":"reader","username":"vaibhav","email":"reader@example.com","can_create":true},{"id":"stranger","username":"stranger","email":"stranger@example.com","can_create":true},{"id":"twin-a","username":"twina","email":"twin@example.com","can_create":true},{"id":"twin-b","username":"twinb","email":"twin@example.com","can_create":true},{"id":"gone","username":"gone","email":"gone@example.com","can_create":true,"disabled":true}]}`)
	resetSlackServiceForTest(t)
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fx.api.chatStore = store
	svc, err := ensureSlackService()
	if err != nil {
		t.Fatal(err)
	}
	manager := services.NewBotConversationManager(store, "", os.Getenv("WORKSPACE_API_URL"))
	fx.api.wireBotManager(manager)
	manager.RegisterConnector(svc)
	fx.api.botManager = manager
	fx.api.scheduler = NewSchedulerService(fx.api)
	// Match the production Work profile: crews carry their attached
	// workflows and crews into every turn.
	profile := fx.profile
	profile.Runtime.Capabilities.WorkflowReferences = agentprofiles.CapabilityOptional
	registry := agentprofiles.NewRegistry()
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	fx.profile, fx.api.agentProfiles = profile, registry
	t.Cleanup(func() { services.SetDedicatedSlackRouteFunc(nil); services.SetSlackRoutingHooks(nil) })
	// The fixture pins an LLM the test profile does not offer; a crew
	// without one uses the profile's default.
	fx.mock.mu.Lock()
	var runtime map[string]any
	if err := json.Unmarshal([]byte(fx.mock.files[crewRunModeOwnerRoot+"/workflow.json"]), &runtime); err != nil {
		t.Fatal(err)
	}
	if caps, ok := runtime["capabilities"].(map[string]any); ok {
		delete(caps, "llm_config")
	}
	raw, _ := json.Marshal(runtime)
	fx.mock.files[crewRunModeOwnerRoot+"/workflow.json"] = string(raw)
	fx.mock.mu.Unlock()
	return botDryRunWorld{crewRunModeFixture: fx, slack: svc}
}

func (w botDryRunWorld) createApp(t *testing.T, name, workspacePath, profileID string) services.SlackConnection {
	t.Helper()
	conn, err := w.slack.CreateSlackConnection(context.Background(), services.SlackConnectionInput{
		DisplayName: name, BotToken: "xoxb-dryrun-" + strings.ToLower(name), AppToken: "xapp-dryrun-" + strings.ToLower(name),
		Enabled: true, WorkspacePath: workspacePath, ProfileID: profileID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func (w botDryRunWorld) mention(t *testing.T, connectionID, channelID string) services.BotDryRunOutcome {
	t.Helper()
	outcome, err := w.api.dryRunSlackMention(context.Background(), connectionID, channelID, dryRunOwnerEmail, "check again")
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}

func requireAdmitted(t *testing.T, outcome services.BotDryRunOutcome, destination string) {
	t.Helper()
	if !outcome.Admitted {
		t.Fatalf("mention was refused: %q replies=%q", outcome.Reason, outcome.Replies)
	}
	if !strings.Contains(outcome.Destination, destination) {
		t.Fatalf("destination = %q, want it to name %q", outcome.Destination, destination)
	}
}

// A crew's own Slack app answers for the crew in any channel.
func TestBotDryRunCrewOwnApp(t *testing.T) {
	w := newBotDryRunWorld(t)
	app := w.createApp(t, "SDE", crewRunModeOwnerRoot, "work")
	requireAdmitted(t, w.mention(t, app.ID, "C0CREWCHAN1"), "crew-aaa")
}

// server A 2026-09-26: a crew app saved with the logical path answered "This
// Slack route is no longer configured". The startup migration repairs it.
func TestBotDryRunLegacyLogicalCrewAppAfterMigration(t *testing.T) {
	w := newBotDryRunWorld(t)
	app := w.createApp(t, "SDE", crewRunModeOwnerRoot, "work")
	w.mock.mu.Lock()
	w.mock.files["config/slack-config.json"] = strings.ReplaceAll(w.mock.files["config/slack-config.json"], crewRunModeOwnerRoot, "Chats/Work/projects/alpha")
	w.mock.mu.Unlock()
	if err := w.slack.ReloadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}

	w.api.migrateCrewBotScopes(context.Background(), w.slack)

	requireAdmitted(t, w.mention(t, app.ID, "C0CREWCHAN1"), "crew-aaa")
}

// The shared bot answers for a crew in a channel its owner routed from the
// crew's Slack tab (which sends the crew's logical path).
func TestBotDryRunSharedBotCrewChannel(t *testing.T) {
	w := newBotDryRunWorld(t)
	shared := w.createApp(t, "Shared", "", "")
	if err := w.slack.SetDefaultSlackConnection(context.Background(), shared.ID); err != nil {
		t.Fatal(err)
	}
	// Save the route the way the crew's Slack tab does: read the config,
	// add the channel with the crew's logical path, post it back.
	owner := &UserClaims{UserID: "owner", Username: "aman", Email: dryRunOwnerEmail}
	get := httptest.NewRequest(http.MethodGet, "/api/human-feedback/slack/config", nil)
	get = get.WithContext(context.WithValue(get.Context(), UserContextKey, owner))
	got := httptest.NewRecorder()
	getSlackConfigHandler(w.api)(got, get)
	var config map[string]any
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &config) != nil {
		t.Fatalf("reading the Slack config = %d %s", got.Code, got.Body.String())
	}
	config["channel_routing"] = map[string]any{"C0SHARED01": map[string]any{"profile_id": "work", "conversation_key": "crew-aaa", "workspace_path": "Chats/Work/projects/alpha", "bot_grant": "run"}}
	body, _ := json.Marshal(config)
	req := httptest.NewRequest(http.MethodPost, "/api/human-feedback/slack/config", strings.NewReader(string(body)))
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, owner))
	rec := httptest.NewRecorder()
	updateSlackConfigHandler(w.api)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("saving the crew channel route = %d %s", rec.Code, rec.Body.String())
	}
	requireAdmitted(t, w.mention(t, shared.ID, "C0SHARED01"), "crew-aaa")
}

// A workflow's own Slack app answers for its workflow.
func TestBotDryRunWorkflowOwnApp(t *testing.T) {
	w := newBotDryRunWorld(t)
	app := w.createApp(t, "Shared-WF", "Workflow/shared", "")
	requireAdmitted(t, w.mention(t, app.ID, "C0WORKFLOW1"), "workflow")
}

// The shared bot in a channel nobody routed does not start a turn.
func TestBotDryRunSharedBotUnroutedChannelIsRefused(t *testing.T) {
	w := newBotDryRunWorld(t)
	shared := w.createApp(t, "Shared", "", "")
	if err := w.slack.SetDefaultSlackConnection(context.Background(), shared.ID); err != nil {
		t.Fatal(err)
	}
	if outcome := w.mention(t, shared.ID, "C0NOROUTE01"); outcome.Admitted {
		t.Fatalf("unrouted channel started a turn: %+v", outcome)
	}
}

// server A 2026-09-26: gptlive1 attaches two other crews by their logical path and
// a workflow; its Slack bot was refused ("One or more attached workflows or
// Crew projects are unavailable"). Attachments are the owner's context.
func TestBotDryRunCrewWithAttachedCrewsAndWorkflow(t *testing.T) {
	w := newBotDryRunWorld(t)
	w.mock.mu.Lock()
	w.mock.files["_users/owner/Chats/Work/projects/gamma/product.json"] = `{"schema_version":1,"product":"work","id":"crew-ggg","title":"Gamma","created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`
	var runtime map[string]any
	if err := json.Unmarshal([]byte(w.mock.files[crewRunModeOwnerRoot+"/workflow.json"]), &runtime); err != nil {
		w.mock.mu.Unlock()
		t.Fatal(err)
	}
	runtime["workflow_context_paths"] = []string{"Chats/Work/projects/gamma", "Workflow/shared"}
	raw, _ := json.Marshal(runtime)
	w.mock.files[crewRunModeOwnerRoot+"/workflow.json"] = string(raw)
	w.mock.mu.Unlock()
	app := w.createApp(t, "SDE", crewRunModeOwnerRoot, "work")
	outcome := w.mention(t, app.ID, "C0CREWCHAN1")
	requireAdmitted(t, outcome, "crew-aaa")
	if paths, _ := json.Marshal(outcome.Request["workflow_context_paths"]); !strings.Contains(string(paths), "gamma") {
		t.Fatalf("the crew's attachments never reached the turn: %s", paths)
	}
}

// Each Slack thread is its own chat in the crew, never the crew's main
// conversation (server A 2026-09-26: every thread landed in the owner's web chat).
func TestBotDryRunCrewSlackThreadsGetTheirOwnChats(t *testing.T) {
	w := newBotDryRunWorld(t)
	app := w.createApp(t, "SDE", crewRunModeOwnerRoot, "work")
	first, second := w.mention(t, app.ID, "C0CREWCHAN1"), w.mention(t, app.ID, "C0CREWCHAN1")
	requireAdmitted(t, first, "crew-aaa")
	requireAdmitted(t, second, "crew-aaa")
	firstKey, _ := first.Request["agent_profile_conversation_key"].(string)
	secondKey, _ := second.Request["agent_profile_conversation_key"].(string)
	if !strings.HasPrefix(firstKey, "crew-aaa:slack-") || !strings.HasPrefix(secondKey, "crew-aaa:slack-") {
		t.Fatalf("Slack threads ran in the crew's main conversation: %q %q", firstKey, secondKey)
	}
	if firstKey == secondKey {
		t.Fatalf("two Slack threads share one chat: %q", firstKey)
	}
}

// Slack DMs (docs/design/bot_identity_model.md): a 1:1 DM with a workflow's
// or crew's own bot runs as the AgentWorks account the sender's Slack email
// maps to, in that account's own mode. Channels stay Run mode.

// dmSenders fakes Slack's users.info + conversations.info per Slack user id.
func (w botDryRunWorld) dmSenders(t *testing.T, app services.SlackConnection, senders map[string]services.SlackDMSender) {
	t.Helper()
	svc, err := w.slack.ServiceForConnection(app.ID)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetDMSenderLookup(func(_ context.Context, userID, _ string) (services.SlackDMSender, error) {
		sender, ok := senders[userID]
		if !ok {
			return services.SlackDMSender{}, fmt.Errorf("users_not_found")
		}
		return sender, nil
	})
}

func (w botDryRunWorld) dm(t *testing.T, connectionID, senderSlackID string) services.BotDryRunOutcome {
	t.Helper()
	outcome, err := w.api.dryRunSlackDM(context.Background(), connectionID, senderSlackID, "D0DMCHAN01", "what changed today?")
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}

func dmSender(email string) services.SlackDMSender {
	return services.SlackDMSender{Name: strings.Split(email, "@")[0], Email: email, OneToOne: true}
}

func requireMode(t *testing.T, outcome services.BotDryRunOutcome, userID, mode string) {
	t.Helper()
	if !outcome.Admitted {
		t.Fatalf("DM was refused: %q replies=%q", outcome.Reason, outcome.Replies)
	}
	if outcome.UserID != userID || outcome.Mode != mode {
		t.Fatalf("DM runs as %q in %q mode, want %q in %q", outcome.UserID, outcome.Mode, userID, mode)
	}
}

// The crew's owner DMs its bot: their own full chat. A reader of the crew
// gets Run mode. A channel mention of the same bot is always Run mode.
func TestBotDryRunCrewDMRunsAsTheSender(t *testing.T) {
	w := newBotDryRunWorld(t)
	app := w.createApp(t, "SDE", crewRunModeOwnerRoot, "work")
	w.dmSenders(t, app, map[string]services.SlackDMSender{"U0OWNER": dmSender(dryRunOwnerEmail), "U0READER": dmSender("reader@example.com")})

	// One user, one chat: every DM continues the sender's own chat of the
	// crew, the one their web chat opens.
	owner := w.dm(t, app.ID, "U0OWNER")
	requireMode(t, owner, "owner", "full")
	if key, _ := owner.Request["agent_profile_conversation_key"].(string); key != "crew-aaa" {
		t.Fatalf("DM ran in %q, want the crew's own conversation", key)
	}
	if web := w.webChatSession(t, "owner"); owner.SessionID != web {
		t.Fatalf("owner's DM ran in session %q, want their web chat %q", owner.SessionID, web)
	}
	if title, _ := owner.Request["session_title"].(string); strings.Contains(title, "what changed today") {
		t.Fatalf("a DM renamed the user's own chat to %q", title)
	}
	if again := w.dm(t, app.ID, "U0OWNER"); again.SessionID != owner.SessionID {
		t.Fatalf("a second DM thread opened another chat: %q vs %q", again.SessionID, owner.SessionID)
	}
	// The DM is one chat keyed by its channel, so replies post directly.
	if thread, _ := owner.Request["bot_thread_ts"].(string); thread != "D0DMCHAN01" {
		t.Fatalf("DM thread = %q, want the DM channel (replies post directly)", thread)
	}
	// Attachments are checked as the sender, as in their web chat: the
	// crew's owner-only workflow keeps a reader out until it is removed.
	if outcome := w.dm(t, app.ID, "U0READER"); outcome.Admitted {
		t.Fatalf("a reader's DM got the owner's private attachment: %+v", outcome)
	}
	w.mock.mu.Lock()
	var runtime map[string]any
	if err := json.Unmarshal([]byte(w.mock.files[crewRunModeOwnerRoot+"/workflow.json"]), &runtime); err != nil {
		w.mock.mu.Unlock()
		t.Fatal(err)
	}
	runtime["workflow_context_paths"] = []string{"Workflow/shared"}
	raw, _ := json.Marshal(runtime)
	w.mock.files[crewRunModeOwnerRoot+"/workflow.json"] = string(raw)
	w.mock.mu.Unlock()
	reader := w.dm(t, app.ID, "U0READER")
	requireMode(t, reader, "reader", "run")
	if web := w.webChatSession(t, "reader"); reader.SessionID != web || reader.SessionID == owner.SessionID {
		t.Fatalf("reader's DM ran in session %q, want their own web chat %q (owner's is %q)", reader.SessionID, web, owner.SessionID)
	}

	channel := w.mention(t, app.ID, "C0CREWCHAN1")
	requireAdmitted(t, channel, "crew-aaa")
	if channel.Mode != "run" {
		t.Fatalf("channel mention runs in %q mode, want run", channel.Mode)
	}
}

// A workflow's own bot: the owner gets Builder, a reader Run mode, and
// someone without access to the workflow is refused.
func TestBotDryRunWorkflowDMRunsAsTheSender(t *testing.T) {
	w := newBotDryRunWorld(t)
	app := w.createApp(t, "Shared-WF", "Workflow/shared", "")
	w.dmSenders(t, app, map[string]services.SlackDMSender{"U0OWNER": dmSender(dryRunOwnerEmail), "U0READER": dmSender("reader@example.com"), "U0STRANGER": dmSender("stranger@example.com")})

	// One user, one chat: the owner's DM continues the Builder chat their web
	// UI has open; the reader's continues their latest saved Builder chat.
	w.api.activeSessionsMux.Lock()
	if w.api.activeSessions == nil {
		w.api.activeSessions = map[string]*ActiveSessionInfo{}
	}
	w.api.activeSessions["owner-web-builder"] = &ActiveSessionInfo{SessionID: "owner-web-builder", AgentMode: "workflow_phase", UserID: "owner", WorkspacePath: "Workflow/shared", LastActivity: time.Now()}
	w.api.activeSessionsMux.Unlock()
	w.mock.mu.Lock()
	w.mock.files["Workflow/shared/builder/conversation/users/reader/2026-09-26/session-reader-builder-conversation.json"] = `{"session_id":"reader-builder","user_id":"reader","phase_id":"workflow-builder","updated_at":"2026-09-26T08:00:00Z","conversation_history":[{"Role":"user","Parts":[{"Text":"earlier question"}]}]}`
	w.mock.mu.Unlock()

	owner := w.dm(t, app.ID, "U0OWNER")
	requireMode(t, owner, "owner", "full")
	if owner.SessionID != "owner-web-builder" {
		t.Fatalf("owner's DM ran in %q, want their open web Builder chat", owner.SessionID)
	}
	if again := w.dm(t, app.ID, "U0OWNER"); again.SessionID != owner.SessionID {
		t.Fatalf("a second DM thread opened another chat: %q", again.SessionID)
	}
	reader := w.dm(t, app.ID, "U0READER")
	requireMode(t, reader, "reader", "run")
	if reader.SessionID != "reader-builder" {
		t.Fatalf("reader's DM ran in %q, want their own saved Builder chat", reader.SessionID)
	}
	if outcome := w.dm(t, app.ID, "U0STRANGER"); outcome.Admitted {
		t.Fatalf("a user without workflow access got a turn: %+v", outcome)
	}
}

// Nobody is run as a user unless Slack proves a 1:1 DM with a full member
// whose email names exactly one enabled account; the shared bot takes no DMs.
func TestBotDryRunDMRefusals(t *testing.T) {
	w := newBotDryRunWorld(t)
	app := w.createApp(t, "SDE", crewRunModeOwnerRoot, "work")
	notOneToOne := dmSender(dryRunOwnerEmail)
	notOneToOne.OneToOne = false
	guest := dmSender(dryRunOwnerEmail)
	guest.Guest = true
	external := dmSender(dryRunOwnerEmail)
	external.External = true
	w.dmSenders(t, app, map[string]services.SlackDMSender{
		"U0GROUP": notOneToOne, "U0GUEST": guest, "U0EXTERNAL": external,
		"U0UNKNOWN": dmSender("nobody@example.com"), "U0TWIN": dmSender("twin@example.com"), "U0GONE": dmSender("gone@example.com"),
	})
	for _, sender := range []string{"U0GROUP", "U0GUEST", "U0EXTERNAL", "U0UNKNOWN", "U0TWIN", "U0GONE", "U0NOTINSLACK"} {
		if outcome := w.dm(t, app.ID, sender); outcome.Admitted || len(outcome.Replies) == 0 {
			t.Fatalf("%s: DM ran or got no explanation: %+v", sender, outcome)
		}
	}

	shared := w.createApp(t, "Shared", "", "")
	if err := w.slack.SetDefaultSlackConnection(context.Background(), shared.ID); err != nil {
		t.Fatal(err)
	}
	w.dmSenders(t, shared, map[string]services.SlackDMSender{"U0OWNER": dmSender(dryRunOwnerEmail)})
	if outcome := w.dm(t, shared.ID, "U0OWNER"); outcome.Admitted {
		t.Fatalf("the shared bot ran a DM: %+v", outcome)
	}
}

// webChatSession is the session a user's web chat with the crew opens
// (resolveAgentProfileConversation's lookup, without the HTTP request).
func (w botDryRunWorld) webChatSession(t *testing.T, userID string) string {
	t.Helper()
	ctx := context.Background()
	binding, _, err := resolveConversationBindingForUser(ctx, userID, w.profile, "crew-aaa")
	if err != nil {
		t.Fatal(err)
	}
	record, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, userID, w.profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	return record.SessionID
}

// WhatsApp reaches other owners' crews read-only (crews are read-only for
// everyone but their owner): they are listed, admitted, and a message runs
// in the paired user's own reader chat — the one their web chat opens.
func TestWhatsAppReachesOtherOwnersCrewsReadOnly(t *testing.T) {
	w := newBotDryRunWorld(t)
	ctx := context.Background()

	others := w.api.whatsappOtherCrews(ctx, "reader")
	found := false
	for _, crew := range others {
		if crew.ID == "crew-bbb" {
			t.Fatalf("the reader's own crew is listed as someone else's: %+v", others)
		}
		if crew.ID == "crew-aaa" && crew.WorkspacePath == crewRunModeOwnerRoot {
			found = true
		}
	}
	if !found {
		t.Fatalf("the owner's crew is not offered to the reader: %+v", others)
	}

	route := services.ChannelRoute{ProfileID: "work", ConversationKey: "crew-aaa", WorkspacePath: crewRunModeOwnerRoot, BotGrant: "run", WorkshopMode: "run"}
	if allowed, err := w.api.checkWhatsAppWorkflowAccess(ctx, "reader", route); err != nil || !allowed {
		t.Fatalf("the reader cannot reach the owner's crew on WhatsApp: allowed=%v err=%v", allowed, err)
	}
	moved := route
	moved.WorkspacePath = "_users/reader/Chats/Work/projects/alpha"
	if allowed, _ := w.api.checkWhatsAppWorkflowAccess(ctx, "reader", moved); allowed {
		t.Fatal("a slug whose folder no longer holds the crew still authorizes messages")
	}

	msg := services.BotIncomingMessage{Platform: "whatsapp", UserID: "phone", WorkspaceUserID: "reader", ChannelID: "dm", Text: "status?", IsMention: true,
		PresetProfile: &services.ProfileRoute{ProfileID: "work", ConversationKey: "crew-aaa", UploadFolder: crewRunModeOwnerRoot, WorkspaceUserID: "owner"}}
	req, sessionID, handled, err := w.api.botProfileTurn(ctx, "reader", msg, services.ThreadID{Platform: "whatsapp", ChannelID: "dm", ThreadTS: "dm"})
	if err != nil || !handled {
		t.Fatalf("crew turn failed: handled=%v err=%v", handled, err)
	}
	if web := w.webChatSession(t, "reader"); sessionID != web || sessionID == w.webChatSession(t, "owner") {
		t.Fatalf("WhatsApp crew turn ran in %q, want the reader's own web chat %q", sessionID, web)
	}
	if key, _ := req["agent_profile_conversation_key"].(string); key != "crew-aaa" {
		t.Fatalf("conversation key = %q, want the crew's", key)
	}
}
