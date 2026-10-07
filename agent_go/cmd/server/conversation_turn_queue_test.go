package server

import (
	"context"
	"encoding/json"
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
		{"workflow chat", QueryRequest{AgentMode: "workflow_phase", TriggeredBy: "manual"}, context.Background(), true},
		{"synthetic notification", QueryRequest{AgentMode: "multi-agent", IsAutoNotification: true}, context.Background(), false},
		{"scheduled turn", QueryRequest{AgentMode: "workflow_phase", TriggeredBy: "cron"}, context.Background(), false},
		{"pulse with manual trigger", QueryRequest{AgentMode: "workflow_phase", TriggeredBy: "manual", PulseLifecycleTurn: true}, context.Background(), false},
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
		conversationTurnQueueOwners:    map[string]string{},
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
		{"workflow builder chat", QueryRequest{AgentMode: "workflow_phase", TriggeredBy: "manual"}, true},
		{"workflow schedule", QueryRequest{AgentMode: "workflow_phase", TriggeredBy: "cron"}, true},
		{"webhook trigger", QueryRequest{AgentMode: "workflow_phase", TriggeredBy: "webhook"}, true},
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

func TestDurableConversationTurnQueueIsFIFOAndSurvivesAPIReplacement(t *testing.T) {
	files := map[string]string{}
	firstAPI := newConversationTurnQueueTestAPI(files)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "u1", Username: "u1"})
	for _, message := range []string{"first", "second"} {
		_, position, err := firstAPI.enqueueConversationTurn(ctx, "u1", "session-1", QueryRequest{Query: message, AgentMode: "multi-agent"})
		if err != nil {
			t.Fatal(err)
		}
		if position != len(firstAPI.mustReadTurnQueueForTest(t, "u1")) {
			t.Fatalf("position=%d for %q", position, message)
		}
	}

	// A new API instance has no in-memory queue state. The workspace document
	// remains the source of truth and preserves submission order.
	restarted := newConversationTurnQueueTestAPI(files)
	first, ok := restarted.claimNextConversationTurn(context.Background(), "u1", "session-1")
	if !ok || first.Request.Query != "first" || first.StartedAt == nil {
		t.Fatalf("first=%+v ok=%v", first, ok)
	}
	if err := restarted.removeConversationTurn(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second, ok := restarted.claimNextConversationTurn(context.Background(), "u1", "session-1")
	if !ok || second.Request.Query != "second" {
		t.Fatalf("second=%+v ok=%v", second, ok)
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
	req.DecryptedSecrets = append(req.DecryptedSecrets, struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}{Name: "TOKEN", Value: "workspace-secret"})
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "u1"})
	if _, _, err := api.enqueueConversationTurn(ctx, "u1", "session-1", req); err != nil {
		t.Fatal(err)
	}
	raw := files[conversationTurnQueuePath("u1")]
	if strings.Contains(raw, secret) || strings.Contains(raw, "workspace-secret") {
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

func TestPendingQueuedMessagesListOnlyUnstartedKeyedTurnsOfTheSession(t *testing.T) {
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
	if len(pending) != 2 || pending[0].ClientMessageID != "sub-next" || pending[1].ClientMessageID != "sub-later" {
		t.Fatalf("pending = %+v, want sub-next then sub-later", pending)
	}
	if pending[0].Content != "queued one" || pending[0].QueuePosition != 1 || pending[1].QueuePosition != 2 {
		t.Fatalf("pending details = %+v", pending)
	}
	if other := api.pendingQueuedMessages(context.Background(), "bob", "chat"); len(other) != 0 {
		t.Fatalf("another user's queue leaked: %+v", other)
	}
}

// Stop closes the coding CLI, so the turn it was running can never finish. Its claimed queue
// entry and retained-turn record must not keep the session "occupied": the next message used to
// queue behind that dead turn forever.
func TestStopReleasesTheStoppedTurnsMarkers(t *testing.T) {
	files := map[string]string{}
	api := newConversationTurnQueueTestAPI(files)
	api.retainedMainTurns = map[string]time.Time{"session-1": time.Now(), "session-2": time.Now()}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "u1", Username: "u1"})
	for _, message := range []string{"running", "waiting", "other session"} {
		session := "session-1"
		if message == "other session" {
			session = "session-2"
		}
		if _, _, err := api.enqueueConversationTurn(ctx, "u1", session, QueryRequest{Query: message, AgentMode: "multi-agent"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := api.claimNextConversationTurn(ctx, "u1", "session-1"); !ok { // "running" is claimed
		t.Fatal("could not claim the first turn")
	}
	if !api.conversationTurnOccupied("session-1") {
		t.Fatal("the session should be occupied while its turn is retained")
	}

	api.releaseStoppedSessionTurnMarkers("session-1")

	if api.conversationTurnOccupied("session-1") {
		t.Fatal("a stopped session is still occupied by its dead turn")
	}
	var left []string
	for _, turn := range api.mustReadTurnQueueForTest(t, "u1") {
		left = append(left, turn.SessionID+":"+turn.Request.Query)
	}
	if strings.Join(left, ",") != "session-1:waiting,session-2:other session" {
		t.Fatalf("queue after stop = %v (the claimed turn goes, waiting and other sessions stay)", left)
	}
	if _, kept := api.retainedMainTurns["session-2"]; !kept {
		t.Fatal("another session's retained turn was released")
	}
}

// After a restart, a message that waited over half an hour without starting is dropped instead of
// re-run; recent waiting messages and turns that had started are kept.
func TestRecoveryDropsStaleWaitingTurnsOnly(t *testing.T) {
	now := time.Now().UTC()
	started := now.Add(-2 * time.Hour)
	turns := []queuedConversationTurn{
		{ID: "stale-waiting", SessionID: "s", CreatedAt: now.Add(-45 * time.Minute)},
		{ID: "recent-waiting", SessionID: "s", CreatedAt: now.Add(-5 * time.Minute)},
		{ID: "was-running", SessionID: "s", CreatedAt: now.Add(-3 * time.Hour), StartedAt: &started},
	}
	var kept []string
	for _, turn := range dropStaleWaitingTurns(turns, now) {
		kept = append(kept, turn.ID)
	}
	if strings.Join(kept, ",") != "recent-waiting,was-running" {
		t.Fatalf("kept %v, want [recent-waiting was-running]", kept)
	}
}

// The Stop button (handleStopSession) releases the stopped turn's markers too: after it, the next
// message is accepted instead of queueing behind a dead turn (excellence, a Muse Code chat).
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

// Closing a chat's CLI on purpose (access or runtime configuration changed) releases its busy
// markers, so the next message is not queued behind the turn that CLI was running.
func TestClosingACLIOnPurposeReleasesTheTurnMarkers(t *testing.T) {
	api := newConversationTurnQueueTestAPI(map[string]string{})
	api.retainedMainTurns = map[string]time.Time{"session-1": time.Now(), "session-2": time.Now()}
	old := runningServerAPI
	runningServerAPI = api
	t.Cleanup(func() { runningServerAPI = old })
	if !api.conversationTurnOccupied("session-1") {
		t.Fatal("session-1 should be occupied before the close")
	}

	closeCodingCLIAndReleaseTurnMarkers("session-1", "code access changed")

	if api.conversationTurnOccupied("session-1") {
		t.Fatal("the chat is still marked busy after its CLI was closed")
	}
	if !api.conversationTurnOccupied("session-2") {
		t.Fatal("another chat's turn was released")
	}
	runningServerAPI = nil
	closeCodingCLIAndReleaseTurnMarkers("session-2", "no api") // must not panic without an API
}

// Code chat sde-private, RTS 2026-10-07: a deploy changed the Code definition
// while a tmux live-input turn held the input lane, and the message queued for
// the relaunch stayed "Queued" forever although the CLI sat idle at its prompt.
// An idle CLI's turn is ended after runtimeChangeIdleChecks ticks; a CLI that is
// mid-response keeps its turn (Excellence 2026-10-03).
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
