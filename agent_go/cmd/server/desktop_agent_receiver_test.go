package server

import (
	"context"
	"encoding/json"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// PLAT-746-890: stored network mail must never recreate a deleted chat or
// follow the old name/key into a replacement conversation.
func TestDesktopAppRecipientRequiresCurrentOwnedConversation(t *testing.T) {
	env := newTriggerLinkEnv(t)
	_ = env
	ctx := t.Context()
	store := defaultProductConversationRegistryStore()
	path := productConversationRegistryPath("owner")
	record := ProductConversationRecord{ProfileID: crewProfileID, ResourceID: "alpha", ConversationKey: "alpha:chat:recipient", ConversationID: "conversation-before", SessionID: "session-before", WorkspacePath: linkAlphaPath, Description: "project description is not the agent purpose"}
	doc := productConversationRegistryDocument{Version: productConversationRegistryVersion, Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, record.ConversationKey): record}}
	doc.Agents = map[string]ProductAgentIdentity{productConversationRegistryEntryKey(crewProfileID, record.ConversationKey): {Name: "Named helper", Purpose: "Keeps notes"}}
	if err := store.writeDocument(ctx, path, doc); err != nil {
		t.Fatal(err)
	}
	if got, err := desktopAppAgentRecipient(ctx, "owner", record); err != nil || got.ConversationID != record.ConversationID || got.Title != "Named helper" || got.Description != "Keeps notes" {
		t.Fatalf("current owned recipient refused: %v", err)
	}
	replacement := record
	replacement.ConversationID = "conversation-after"
	replacement.SessionID = "session-after"
	doc.Entries[productConversationRegistryEntryKey(crewProfileID, record.ConversationKey)] = replacement
	if err := store.writeDocument(ctx, path, doc); err != nil {
		t.Fatal(err)
	}
	if _, err := desktopAppAgentRecipient(ctx, "owner", record); err == nil {
		t.Fatal("old inbox followed the replacement chat")
	}
	if got, err := desktopAppAgentRecipient(ctx, "owner", replacement); err != nil || got.SessionID != replacement.SessionID {
		t.Fatalf("current replacement refused: %v", err)
	}
	delete(doc.Entries, productConversationRegistryEntryKey(crewProfileID, record.ConversationKey))
	if err := store.writeDocument(ctx, path, doc); err != nil {
		t.Fatal(err)
	}
	if _, err := desktopAppAgentRecipient(ctx, "owner", replacement); err == nil {
		t.Fatal("deleted recipient admitted")
	}
	after, err := store.loadDocument(ctx, path)
	if err != nil || len(after.Entries) != 0 {
		t.Fatalf("recipient lookup recreated a chat: %v", err)
	}
	// PLAT-746-890 / optional Main: a project alias is not a physical recipient.
	alias := record
	alias.ConversationKey = alias.ResourceID
	doc.Entries[productConversationRegistryEntryKey(crewProfileID, alias.ConversationKey)] = alias
	if err := store.writeDocument(ctx, path, doc); err != nil {
		t.Fatal(err)
	}
	if _, err := desktopAppAgentRecipient(ctx, "owner", alias); err == nil {
		t.Fatal("project alias accepted instead of a selected physical chat")
	}
	// A registry reference to someone else's workspace does not advertise their
	// chat under this laptop login, even if its record sits in the local namespace.
	foreign := record
	foreign.ResourceID = "gamma"
	foreign.ConversationKey = "gamma:chat:foreign"
	foreign.WorkspacePath = linkGammaPath
	doc.Entries[productConversationRegistryEntryKey(crewProfileID, foreign.ConversationKey)] = foreign
	if err := store.writeDocument(ctx, path, doc); err != nil {
		t.Fatal(err)
	}
	if _, err := desktopAppAgentRecipient(ctx, "owner", foreign); err == nil {
		t.Fatal("another person's chat advertised as own")
	}
}

// PLAT-746-890: a published visitor must not borrow the owner's private reach.
func TestDesktopAppVisitorCanOnlyReplyToAdmittedInbox(t *testing.T) {
	const reply = "network:login:inbox-admitted"
	visitor := QueryRequest{SharedAgentSender: "network:visitor", AgentMessageInbox: reply}
	if desktopAppTurnMayAddress(visitor, reply) {
		t.Fatal("serialized inbox field granted reply authority")
	}
	for _, trigger := range []QueryRequest{{AgentMessageFromOtherPerson: true}, {PinRunMode: true}, {CrewGuestCaller: "visitor"}, {BotPlatform: "webhook"}, {AskedBy: "visitor"}} {
		if !desktopAppTurnRestricted(trigger, "owner") {
			t.Fatal("non-owner recorded trigger acquired private reach")
		}
	}
	directory := map[string]interface{}{"agents": []map[string]interface{}{{"target": "agent:private", "yours": true}, {"target": "agent:published", "published": true}}}
	publicAgentDirectoryOnly(directory)
	rows := directory["agents"].([]map[string]interface{})
	if len(rows) != 1 || rows[0]["target"] != "agent:published" {
		t.Fatal("visitor listing exposed a private agent or hid a published agent")
	}
	visitor.appAgentReplyTo = reply
	if !desktopAppTurnMayAddress(visitor, reply) {
		t.Fatal("authenticated explicit reply refused")
	}
	for _, target := range []string{"network:login:agent:private", "network:login:inbox-other", "network:other:inbox-admitted"} {
		if desktopAppTurnMayAddress(visitor, target) {
			t.Fatal("visitor borrowed private reach")
		}
	}
	if !desktopAppTurnMayAddress(QueryRequest{}, "network:login:agent:private") {
		t.Fatal("owner's own turn refused")
	}
}

// PLAT-746: own AgentBridge mail was forced into queue even while a native
// turn could accept it. Exercise its profile request, live submission, and
// durable no-target fallback; visitors must never reach that live path.
func TestOwnAgentBridgeMailSteersOrQueuesWithoutReplayingUncertainSend(t *testing.T) {
	for _, tc := range []struct {
		name     string
		own      bool
		prior    QueryRequest
		result   string
		wantLive bool
	}{
		{"own confirmed", true, QueryRequest{}, "sent_to_cli", true},
		{"own pending refresh", true, QueryRequest{}, "sent_to_cli", true},
		{"own restart continuation", true, QueryRequest{IsAutoNotification: true, TriggeredBy: "restart_continue"}, "sent_to_cli", true},
		{"own background wake", true, QueryRequest{IsAutoNotification: true, TriggeredBy: "background"}, "sent_to_cli", true},
		{"own usage recovery", true, QueryRequest{IsAutoNotification: true, TriggeredBy: "usage_limit_continue"}, "sent_to_cli", true},
		{"restricted synthetic", true, QueryRequest{IsAutoNotification: true, PinRunMode: true}, "", false},
		{"own no target", true, QueryRequest{}, "no_target", true},
		{"own idle retained process", true, QueryRequest{}, "", false},
		{"own compaction", true, QueryRequest{}, "", false},
		{"own uncertain", true, QueryRequest{}, "uncertain", true},
		{"real access refusal", true, QueryRequest{}, "forbidden", true},
		{"visitor", false, QueryRequest{}, "", false},
		{"restricted current turn", true, QueryRequest{PinRunMode: true}, "", false},
		{"bot current turn", true, QueryRequest{BotPlatform: "slack"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := internalBotRequestContext(context.Background(), "owner")
			api := newConversationTurnQueueTestAPI(map[string]string{})
			api.internalChatSubmissionStore = newTestChatSubmissionStore()
			api.lastQueryRequests = map[string]QueryRequest{"recipient": tc.prior}
			api.activeSessions = map[string]*ActiveSessionInfo{"recipient": {SessionID: "recipient", IsSyntheticTurn: tc.prior.IsAutoNotification}}
			api.runningAgents = map[string]*mcpagent.Agent{"recipient": testCodingAgent(llm.ProviderCodexCLI, "codex-cli")}
			api.agentCancelFuncs = map[string]context.CancelFunc{"recipient": func() {}}
			api.internalSteerTransportReady = func(_, _ string) bool { return tc.name != "own idle retained process" }
			if tc.name == "own compaction" {
				run := newIdleCompactRun()
				if !idleCompacts.start("recipient", run, 100, time.Now()) {
					t.Fatal("compaction fixture not started")
				}
				t.Cleanup(func() { idleCompacts.end("recipient", run) })
			}
			record := ProductConversationRecord{SessionID: "recipient", ConversationKey: "crew:chat:recipient", WorkspacePath: "Chats"}
			input := api.desktopAppMessageInput("owner", record, desktopAppIncoming{Own: tc.own}, "admitted agent message")
			query, err := queryRequestForAgentProfileChat(routeTestProfile("example-product", true, ""), input, record)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "own pending refresh" {
				api.agentCancelFuncs = map[string]context.CancelFunc{record.SessionID: func() {}}
				api.setConversationTurnRuntimeChange(record.SessionID, true)
				query.RestartCodingCLI = true
				if !api.deferPendingSetupForLiveInput(ctx, query, record.SessionID, false) {
					t.Fatal("same-account agent input blocked by pending setup")
				}
				if api.deferPendingSetupForLiveInput(ctx, query, record.SessionID, true) {
					t.Fatal("cross-role input bypassed queue")
				}
			}
			prefer := shouldTryRetainedDeliveryBeforeQueue(ctx, query, record.SessionID)
			if prefer != tc.wantLive {
				t.Fatalf("live-first=%t want %t", prefer, tc.wantLive)
			}
			calls := 0
			api.internalLiveInputDeliver = func(w http.ResponseWriter, r *http.Request, _, _, _ string) bool {
				calls++
				if tc.result == "forbidden" {
					logQueryAdmissionRefusal(w, r, "provider_account")
					http.Error(w, "refused", http.StatusForbidden)
					return true
				}
				if tc.result == "no_target" {
					return false
				}
				if tc.result == "uncertain" {
					writeSubmissionUncertain(w, r.Header.Get("Idempotency-Key"))
					return true
				}
				_ = json.NewEncoder(w).Encode(QueryResponse{Status: queryStatusLiveInputDelivered, DeliveryStatus: "sent_to_cli"})
				return true
			}
			r := httptest.NewRequest(http.MethodPost, "/api/query", nil).WithContext(ctx)
			r.Header.Set("Idempotency-Key", "agentbridge:test:message")
			capture := httptest.NewRecorder()
			w, r, finish, ok := api.beginChatSubmission(capture, r, record.SessionID, "example-product", query.Query)
			if !ok {
				t.Fatal("submission refused")
			}
			handled := prefer && api.tryDeliverQueryAsLiveInput(w, r, record.SessionID, query.Query, "query-1", nil)
			if !handled {
				if _, _, err := api.enqueueConversationTurn(ctx, "owner", record.SessionID, query); err != nil {
					t.Fatal(err)
				}
			}
			finish()
			wantCalls := 0
			if tc.wantLive {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("native attempts=%d want %d", calls, wantCalls)
			}
			rows, err := api.readConversationTurnQueue(ctx, "owner")
			if err != nil {
				t.Fatal(err)
			}
			wantRows := 1
			if tc.result == "sent_to_cli" || tc.result == "uncertain" || tc.result == "forbidden" {
				wantRows = 0
			}
			if tc.result == "forbidden" && (capture.Code != 403 || capture.Header().Get("X-Query-Refusal-Reason") != "provider_account") {
				t.Fatal("access refusal lost its reason or fell through to queued execution")
			}
			if len(rows) != wantRows {
				t.Fatalf("durable queued rows=%d want %d", len(rows), wantRows)
			}
		})
	}
}
