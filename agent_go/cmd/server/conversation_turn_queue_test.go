package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

func TestRetainedDeliveryPrecedesTurnQueueOnlyForHumanInput(t *testing.T) {
	if liveCodingAgentDeliveryTimeout <= 300*time.Second {
		t.Fatal("live delivery must outlast the maximum provider durable-ack budget")
	}
	tests := []struct {
		name string
		req  QueryRequest
		ctx  context.Context
		want bool
	}{
		{"ordinary chat", QueryRequest{AgentMode: "multi-agent"}, context.Background(), true},
		{"selected steer", QueryRequest{AgentMode: "multi-agent", DeliveryMode: "steer"}, context.Background(), true},
		{"selected queue", QueryRequest{AgentMode: "multi-agent", DeliveryMode: "queue"}, context.Background(), false},
		{"workflow chat", QueryRequest{AgentMode: "workflow_phase", TriggeredBy: "manual"}, context.Background(), true},
		{"synthetic notification", QueryRequest{AgentMode: "multi-agent", IsAutoNotification: true}, context.Background(), false},
		{"scheduled turn", QueryRequest{AgentMode: "workflow_phase", TriggeredBy: "cron"}, context.Background(), false},
		// A person's follow-up in a bot conversation steers the running CLI.
		{"bot turn", QueryRequest{AgentMode: "multi-agent", TriggeredBy: "bot:slack", BotPlatform: "slack"}, context.Background(), true},
		{"workflow bot turn", QueryRequest{AgentMode: "workflow_phase", TriggeredBy: "cron", BotPlatform: "slack"}, context.Background(), true},
		{"whatsapp turn", QueryRequest{AgentMode: "multi-agent", TriggeredBy: "bot:whatsapp", BotPlatform: "whatsapp"}, context.Background(), true},
		{"slack workflow trigger run", QueryRequest{AgentMode: "workflow_phase", TriggeredBy: "bot:slack", BotPlatform: "slack"}, context.WithValue(context.Background(), directWebhookExecutionKey{}, 1), false},
		{"bot trigger without platform", QueryRequest{AgentMode: "multi-agent", TriggeredBy: "bot:slack"}, context.Background(), false},
		{"token caller without trigger", QueryRequest{AgentMode: "multi-agent"}, context.WithValue(context.Background(), UserContextKey, &UserClaims{AccessToken: &accesstokens.Token{ID: "token-1"}}), false},
		{"explicit next turn", QueryRequest{AgentMode: "multi-agent", DisableLiveInputDelivery: true}, context.Background(), false},
		{"claimed queue worker", QueryRequest{AgentMode: "workflow_phase", TriggeredBy: "manual"}, withConversationTurnQueueExecution(context.Background()), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldTryRetainedDeliveryBeforeQueue(test.ctx, test.req, "session-1"); got != test.want {
				t.Fatalf("prefer retained delivery=%v, want %v", got, test.want)
			}
		})
	}
}

func newConversationTurnQueueTestAPI(files map[string]string) *StreamingAPI {
	return &StreamingAPI{
		sessionInputLanes:              map[string]*sessionInputLane{},
		conversationTurnQueueOwners:    map[string]map[string]bool{},
		conversationTurnQueueDraining:  map[string]bool{},
		conversationTurnQueueWaiters:   map[string]chan queuedConversationTurnResult{},
		conversationTurnQueueCallbacks: map[string]func(event *unifiedevents.AgentEvent){},
		internalTurnQueueRead: func(_ context.Context, path string) (string, bool, error) {
			value, ok := files[path]
			return value, ok, nil
		},
		internalTurnQueueWrite: func(_ context.Context, path, value string) error {
			files[path] = value
			return nil
		},
	}
}

func TestDurableConversationTurnQueueCoversEveryConversationProducer(t *testing.T) {
	tests := []struct {
		name string
		req  QueryRequest
		want bool
	}{
		{"ordinary chat", QueryRequest{AgentMode: "multi-agent"}, true},
		{"Crew chat", QueryRequest{AgentMode: "multi-agent", AgentProfileID: "work"}, true},
		// PLAT-746: triggers either run Python directly or ask an existing Crew agent.
		{"scheduled Crew agent", QueryRequest{AgentMode: "multi-agent", AgentProfileID: "work", TriggeredBy: "cron"}, true},
		{"webhook Crew agent", QueryRequest{AgentMode: "multi-agent", AgentProfileID: "work", TriggeredBy: "webhook"}, true},
		{"bot turn", QueryRequest{AgentMode: "multi-agent", TriggeredBy: "bot:slack", BotPlatform: "slack"}, true},
		{"synthetic notification", QueryRequest{AgentMode: "multi-agent", IsAutoNotification: true}, false},
		{"headless workflow execution", QueryRequest{AgentMode: "workflow"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldUseDurableConversationTurnQueue(test.req); got != test.want {
				t.Fatalf("queue coverage=%v, want %v", got, test.want)
			}
		})
	}
}

func TestDurableConversationTurnQueueNeverPersistsResolvedSecrets(t *testing.T) {
	files := map[string]string{}
	api := newConversationTurnQueueTestAPI(files)
	secret := "provider-secret"
	req := QueryRequest{Query: "hello", AgentMode: "multi-agent", LLMConfig: &orchestrator.LLMConfig{
		Primary: orchestrator.LLMModel{Provider: "openai", ModelID: "gpt", APIKey: &secret},
		APIKeys: &orchestrator.APIKeys{OpenAI: &secret},
	}}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "u1"})
	if _, _, err := api.enqueueConversationTurn(ctx, "u1", "session-1", req); err != nil {
		t.Fatal(err)
	}
	raw := files[conversationTurnQueuePath("u1")]
	if strings.Contains(raw, secret) {
		t.Fatalf("queue persisted resolved secret: %s", raw)
	}
}

func (api *StreamingAPI) mustReadTurnQueueForTest(t *testing.T, userID string) []queuedConversationTurn {
	t.Helper()
	turns, err := api.readConversationTurnQueue(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return turns
}

// A started turn stays listed as started so its chat bubble does not vanish between the queue and the chat.
func TestPendingQueuedMessagesListKeyedTurnsOfTheSessionWithStartedOnesMarked(t *testing.T) {
	started := time.Now()
	document := conversationTurnQueueDocument{Version: conversationTurnQueueVersion, Turns: []queuedConversationTurn{
		{ID: "t1", UserID: "alice", SessionID: "chat", SubmissionID: "sub-running", Request: QueryRequest{Query: "running"}, StartedAt: &started},
		{ID: "t2", UserID: "alice", SessionID: "chat", SubmissionID: "sub-next", Request: QueryRequest{Query: "queued one"}, CreatedAt: started},
		{ID: "t3", UserID: "alice", SessionID: "chat", Request: QueryRequest{Query: "unkeyed"}},
		{ID: "t4", UserID: "alice", SessionID: "other", SubmissionID: "sub-other", Request: QueryRequest{Query: "elsewhere"}},
		{ID: "t5", UserID: "alice", SessionID: "chat", SubmissionID: "sub-later", Request: QueryRequest{Query: "queued two"}},
	}}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	api := newConversationTurnQueueTestAPI(map[string]string{conversationTurnQueuePath("alice"): string(raw)})
	pending := api.pendingQueuedMessages(context.Background(), "alice", "chat")
	if len(pending) != 3 || pending[0].ClientMessageID != "sub-running" || pending[1].ClientMessageID != "sub-next" || pending[2].ClientMessageID != "sub-later" {
		t.Fatalf("pending = %+v, want sub-running then sub-next then sub-later", pending)
	}
	if !pending[0].Started || pending[0].QueuePosition != 0 || pending[0].CanSteer {
		t.Fatalf("started turn = %+v, want started, no position, no steer", pending[0])
	}
	if pending[1].Started || pending[1].Content != "queued one" || pending[1].QueuePosition != 1 || pending[2].QueuePosition != 2 {
		t.Fatalf("pending details = %+v", pending)
	}
	if other := api.pendingQueuedMessages(context.Background(), "bob", "chat"); len(other) != 0 {
		t.Fatalf("another user's queue leaked: %+v", other)
	}
}

// The Stop button (handleStopSession) releases the stopped turn's markers too: after it, the next
// message is accepted instead of queueing behind a dead turn (server B, a Muse Code chat).
func TestStopButtonReleasesTheStoppedTurnsMarkers(t *testing.T) {
	files := map[string]string{}
	api := newConversationTurnQueueTestAPI(files)
	api.retainedMainTurns = map[string]time.Time{"session-1": time.Now()}
	api.activeSessions = map[string]*ActiveSessionInfo{"session-1": {SessionID: "session-1", UserID: "u1", Status: "running"}}
	api.stoppedSessions = map[string]bool{}
	api.sessionBusy = map[string]bool{"session-1": true}
	api.lastQueryRequests = map[string]QueryRequest{}
	api.sessionWorkspaceFolders = map[string]string{}
	api.sessionAgents = map[string]*agent.LLMAgentWrapper{}
	api.completionLoopStarted = map[string]bool{}
	api.bgAgentRegistry = NewBackgroundAgentRegistry()
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "u1", Username: "u1"})
	if _, _, err := api.enqueueConversationTurn(ctx, "u1", "session-1", QueryRequest{Query: "running", AgentMode: "multi-agent"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.claimNextConversationTurn(ctx, "u1", "session-1"); !ok {
		t.Fatal("could not claim the first turn")
	}
	if !api.conversationTurnOccupied("session-1") {
		t.Fatal("the session should be occupied before Stop")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/session/stop?cancelAgents=true&preserveConversation=true", nil).WithContext(ctx)
	req.Header.Set("X-Session-ID", "session-1")
	rec := httptest.NewRecorder()
	api.handleStopSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("stop status = %d body=%s", rec.Code, rec.Body.String())
	}
	if api.conversationTurnOccupied("session-1") {
		t.Fatal("the Stop button left the conversation marked busy")
	}
}

// Code chat sde-private, server A 2026-10-07: a deploy changed the Code definition
// while a tmux live-input turn held the input lane, and the message queued for
// the relaunch stayed "Queued" forever although the CLI sat idle at its prompt.
// An idle CLI's turn is ended after runtimeChangeIdleChecks ticks; a CLI that is
// mid-response keeps its turn (server B 2026-10-03).
func TestRuntimeChangeEndsOnlyAnIdleLiveTurn(t *testing.T) {
	original := retainedCLIAtPrompt
	t.Cleanup(func() { retainedCLIAtPrompt = original })
	for _, idle := range []bool{false, true} {
		name := map[bool]string{false: "mid-response waits", true: "idle at prompt runs"}[idle]
		t.Run(name, func(t *testing.T) {
			api := newConversationTurnQueueTestAPI(map[string]string{})
			release := api.lockSessionInputLane("session-1")
			defer release()
			canceled := false
			api.agentCancelFuncs = map[string]context.CancelFunc{"session-1": func() { canceled = true }}
			api.setConversationTurnRuntimeChange("session-1", true)
			retainedCLIAtPrompt = func(*StreamingAPI, string) bool { return idle }
			checks := 0
			for tick := 1; tick <= runtimeChangeIdleChecks; tick++ {
				ended := api.endIdleTurnForRuntimeChange("session-1", api.conversationTurnOccupiedBy("session-1"), &checks)
				if want := idle && tick == runtimeChangeIdleChecks; ended != want || canceled != want {
					t.Fatalf("tick %d: ended=%v canceled=%v, want %v", tick, ended, canceled, want)
				}
			}
		})
	}
}

func TestFreshIdleInputAppendsBehindAlreadyAdmittedTurns(t *testing.T) {
	api := newConversationTurnQueueTestAPI(map[string]string{})
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "u"})
	if _, _, err := api.enqueueConversationTurn(ctx, "u", "s", QueryRequest{AgentMode: "multi-agent", Query: "older"}); err != nil {
		t.Fatal(err)
	}
	// Observe admission without dispatching any paid agent work.
	ctx = context.WithValue(ctx, internalQueuedTurnAdmissionKey{}, func(string) { api.conversationTurnQueueDraining["s"] = true })
	r := httptest.NewRequest(http.MethodPost, "/api/query", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	if !api.queueOccupiedConversationTurn(w, r, "u", "s", QueryRequest{AgentMode: "multi-agent", Query: "newer"}) {
		t.Fatal("idle backlog was ignored")
	}
	turns := api.mustReadTurnQueueForTest(t, "u")
	if len(turns) != 2 || turns[0].Request.Query != "older" || turns[1].Request.Query != "newer" {
		t.Fatalf("queue reordered: %+v", turns)
	}
}

func TestSharedSessionDrainsEveryCallersQueueInAdmissionOrder(t *testing.T) {
	api := newConversationTurnQueueTestAPI(map[string]string{})
	for i, owner := range []string{"alice", "bob", "alice"} {
		ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: owner, Provider: owner + "-provider"})
		if _, _, err := api.enqueueConversationTurn(ctx, owner, "shared", QueryRequest{AgentMode: "multi-agent", Query: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := api.conversationQueueOwners("shared"); len(got) != 2 {
		t.Fatalf("one caller replaced another's queue index: %v", got)
	}
	// Even another authorized sender must queue behind this session's backlog.
	if !api.conversationTurnHasBacklog("third-caller", "shared") {
		t.Fatal("another caller could overtake admitted shared-session work")
	}
	for i, owner := range []string{"alice", "bob", "alice"} {
		turn, ok, err := api.claimOldestConversationTurn(context.Background(), "shared")
		if err != nil || !ok || turn.UserID != owner || turn.Principal.Provider != owner+"-provider" || turn.Request.Query != fmt.Sprint(i) {
			t.Fatalf("turn %d: %+v %v %v", i, turn, ok, err)
		}
		if err := api.removeConversationTurn(context.Background(), turn); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := api.claimOldestConversationTurn(context.Background(), "shared"); err != nil || ok {
		t.Fatalf("empty claim: %v %v", ok, err)
	}
	if len(api.conversationQueueOwners("shared")) != 0 {
		t.Fatal("empty owner memberships retained")
	}
}

func TestSharedSessionStopKeepsOtherCallersUnclaimedMessages(t *testing.T) {
	api := newConversationTurnQueueTestAPI(map[string]string{})
	for _, owner := range []string{"alice", "bob"} {
		if _, _, err := api.enqueueConversationTurn(context.Background(), owner, "shared", QueryRequest{AgentMode: "multi-agent", Query: owner}); err != nil {
			t.Fatal(err)
		}
	}
	claimed, ok, err := api.claimOldestConversationTurn(context.Background(), "shared")
	if err != nil || !ok || claimed.UserID != "alice" {
		t.Fatalf("claim: %+v %v %v", claimed, ok, err)
	}
	if got := api.dropStartedConversationTurns("shared"); got != 1 {
		t.Fatalf("dropped %d rows, want only the claimed row", got)
	}
	bob := api.mustReadTurnQueueForTest(t, "bob")
	if len(bob) != 1 || bob[0].StartedAt != nil || bob[0].Request.Query != "bob" {
		t.Fatalf("another caller's waiting message was deleted: %+v", bob)
	}
	if next, ok, err := api.claimOldestConversationTurn(context.Background(), "shared"); err != nil || !ok || next.UserID != "bob" {
		t.Fatalf("waiting caller no longer dispatches: %+v %v %v", next, ok, err)
	}
}

func TestUnreadableCallerQueueDoesNotBlockAnotherSharedSender(t *testing.T) {
	for _, broken := range []string{"{invalid", `{"version":999,"turns":[]}`} {
		files := map[string]string{}
		api := newConversationTurnQueueTestAPI(files)
		if _, _, err := api.enqueueConversationTurn(context.Background(), "alice", "shared", QueryRequest{AgentMode: "multi-agent", Query: "alice"}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := api.enqueueConversationTurn(context.Background(), "bob", "shared", QueryRequest{AgentMode: "multi-agent", Query: "bob"}); err != nil {
			t.Fatal(err)
		}
		files[conversationTurnQueuePath("alice")] = broken
		turn, ok, err := api.claimOldestConversationTurn(context.Background(), "shared")
		if err != nil || !ok || turn.UserID != "bob" {
			t.Fatalf("Alice's broken queue blocked Bob: %+v %v %v", turn, ok, err)
		}
		if err := api.removeConversationTurn(context.Background(), turn); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := api.claimOldestConversationTurn(context.Background(), "shared"); ok || err == nil {
			t.Fatalf("unreadable owner's retry signal lost: %v %v", ok, err)
		}
		if !api.conversationTurnHasBacklog("alice", "shared") || files[conversationTurnQueuePath("alice")] != broken {
			t.Fatal("unreadable queue was dropped or rewritten")
		}
	}
}

func TestRetainedSendRechecksIdleBacklogAtDeliveryBoundary(t *testing.T) {
	for _, liveRoute := range []bool{false, true} {
		t.Run(fmt.Sprint(liveRoute), func(t *testing.T) {
			api := newConversationTurnQueueTestAPI(map[string]string{})
			ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "u"})
			if _, _, err := api.enqueueConversationTurn(ctx, "u", "s", QueryRequest{AgentMode: "multi-agent", Query: "older"}); err != nil {
				t.Fatal(err)
			}
			// This is the request admitted before the asynchronous provider send.
			ctx = context.WithValue(ctx, retainedQueueInputKey{}, retainedQueueInput{Request: QueryRequest{AgentMode: "multi-agent", Query: "newer"}, LiveInput: liveRoute})
			ctx = context.WithValue(ctx, internalQueuedTurnAdmissionKey{}, func(string) { api.conversationTurnQueueDraining["s"] = true })
			r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
			if api.queueIdleRetainedInput(httptest.NewRecorder(), r, "s", true) {
				t.Fatal("compatible active steering was queued")
			}
			w := httptest.NewRecorder()
			if !api.queueIdleRetainedInput(w, r, "s", false) {
				t.Fatal("CLI became idle but newer input bypassed its backlog")
			}
			if api.sessionTurnInProgress("s") {
				t.Fatal("queue admission leaked the input lane")
			}
			turns := api.mustReadTurnQueueForTest(t, "u")
			if len(turns) != 2 || turns[0].Request.Query != "older" || turns[1].Request.Query != "newer" {
				t.Fatalf("late send reordered: %+v", turns)
			}
			if liveRoute {
				var reply LiveInputResponse
				if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil || !reply.Success || reply.DeliveryStatus != "queued_for_turn" {
					t.Fatalf("live-input queue reply: %+v %v", reply, err)
				}
			}
		})
	}
}

// PLAT-746: a queued network message must keep its server-stamped display
// attribution, without trusting envelope text or changing turn authority.
func TestQueuedAgentMessageKeepsServerDisplayAttribution(t *testing.T) {
	ctx := internalBotRequestContext(context.Background(), GetDefaultUserID(), nil)
	stamp := map[string]interface{}{"source": "agent_message", "sender_label": "Reviewer", "display_content": "Actual message"}
	ctx = context.WithValue(ctx, deliveredAgentMessageKey{}, stamp)
	principal := queuedPrincipalFromContext(ctx)
	principal.DeliveredMessageSession = "chat"
	stamp["display_content"] = "changed after capture"
	raw, err := json.Marshal(principal)
	if err != nil {
		t.Fatal(err)
	}
	var saved queuedConversationPrincipal
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	restored, err := (&StreamingAPI{}).queuedConversationTurnContext(queuedConversationTurn{UserID: GetDefaultUserID(), SessionID: "chat", Principal: saved}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := restored.Value(deliveredAgentMessageKey{}).(map[string]interface{})
	if got["display_content"] != "Actual message" || got["sender_label"] != "Reviewer" {
		t.Fatalf("lost admitted display attribution: %#v", got)
	}
	other, err := (&StreamingAPI{}).queuedConversationTurnContext(queuedConversationTurn{UserID: GetDefaultUserID(), SessionID: "different-chat", Principal: saved}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if other.Value(deliveredAgentMessageKey{}) != nil {
		t.Fatal("attribution crossed conversations")
	}
	plain := queuedPrincipalFromContext(internalBotRequestContext(context.Background(), GetDefaultUserID(), nil))
	if len(plain.DeliveredMessage) != 0 {
		t.Fatal("ordinary input acquired sender attribution")
	}
}
