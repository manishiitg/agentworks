package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools/catalog"
)

// A real hosted send must use the registry's exact current chat identity, so
// receiving through a messaging-only connection passes the same strict check.
func TestHostedAgentMessageUsesCanonicalChatKey(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	f.api.projectActions = NewProjectActionDispatcher(f.api, f.api.agentProfiles)
	// PLAT-856-537: use the fixture's registered Crew and current registry namespace,
	// rather than the retired Code product that cannot resolve its registered tab.
	const root = externalCommandCrewRoot
	const key = "team:chat:abcdef12"
	const session = "product-11111111-2222-4333-8444-555555555555"
	record := ProductConversationRecord{ProfileID: workproduct.ProfileID, ConversationKey: key, SessionID: session, WorkspacePath: root, ResourceID: "team"}
	registry, _ := json.Marshal(productConversationRegistryDocument{Version: 1, Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(workproduct.ProfileID, key): record}})
	if err := saveAgentMessageStore(context.Background(), agentMessageStore{Agents: []namedMessageAgent{{ID: "agent-receiver", UserID: "owner", Name: "receiver", EverAttached: true, Attachment: &namedAgentAttachment{ID: "lease", DeliveryContract: 2}}}}); err != nil {
		t.Fatal(err)
	}
	f.write(t, productConversationRegistryPath("owner"), string(registry))
	reg := &recordingRegistrar{}
	if err := f.api.registerAgentMessagingTools(reg, "owner", session, QueryRequest{}, crewTriggerLinkCaller(root), func(string) {}); err != nil {
		t.Fatal(err)
	}
	ctx := internalBotRequestContext(context.Background(), "owner")
	if _, err := reg.tools["send_message"].exec(ctx, map[string]interface{}{"target": "agent:receiver", "message": "Review this", "submission_id": "hosted-canonical"}); err != nil {
		t.Fatal(err)
	}
	store, err := readAgentMessageStore(ctx)
	if err != nil || len(store.Conversations) != 1 {
		t.Fatalf("store: %+v %v", store, err)
	}
	sender := store.Conversations[0].Endpoints[0]
	if sender.ChatKey != key || sender.Session != session {
		t.Fatalf("wrong stored sender identity: %+v", sender)
	}
	receiver := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{ID: "connection", BoundAgentID: "agent-receiver", BoundAgentName: "receiver"}}
	if err := f.api.externalAuthorizeMessagePeer(ctx, receiver, sender, false); err != nil {
		t.Fatalf("own hosted sender refused: %v", err)
	}
	sender.ChatKey = "team:chat:missing1"
	if err := f.api.externalAuthorizeMessagePeer(ctx, receiver, sender, false); err == nil {
		t.Fatal("a different chat key passed strict admission")
	}
}

// PLAT-746: a real draft's review helper reached messages/send, but execution
// steps had no messaging pool. A scheduled run has no interactive parent chat.
func TestScheduledWorkflowReviewMessageRequiresExplicitStepTool(t *testing.T) {
	env := newTriggerLinkEnv(t)
	const session = "run-scheduled-review"
	executors := map[string]interface{}{}
	categories := map[string]string{}
	tools, err := env.api.appendWorkflowMessagingTools(nil, executors, categories, "owner", session, "Workflow/reports", QueryRequest{TriggeredBy: "scheduler"})
	if err != nil {
		t.Fatal(err)
	}
	_, unselected := orchestrator.FilterCustomToolsByCategory(tools, executors, []string{"workspace_advanced:*"})
	if _, ok := catalog.ScriptFamilies(scriptExecutorsForReviewTest(unselected))["messages"]; ok {
		t.Fatal("step without messaging selection can send")
	}
	_, selected := orchestrator.FilterCustomToolsByCategory(tools, executors, []string{"agent_messaging:send_message"})
	send := catalog.ScriptFamilies(scriptExecutorsForReviewTest(selected))["messages"]
	if send == nil {
		t.Fatal("selected step cannot call the current messages/send API")
	}
	ctx := context.Background()
	if err := saveAgentMessageStore(ctx, agentMessageStore{Agents: []namedMessageAgent{
		{ID: "agent-reviewer", UserID: "owner", Name: "reviewer", EverAttached: true, Attachment: &namedAgentAttachment{ID: "lease", DeliveryContract: 2}},
		{ID: "agent-private", UserID: "other", Name: "private", EverAttached: true, Attachment: &namedAgentAttachment{ID: "other-lease", DeliveryContract: 2}},
	}}); err != nil {
		t.Fatal(err)
	}
	raw, err := send(ctx, map[string]interface{}{"action": "send", "target": "agent:reviewer", "message": "Review the saved draft", "submission_id": "scheduled-review"})
	if err != nil {
		t.Fatal(err)
	}
	var receipt map[string]interface{}
	if json.Unmarshal([]byte(raw), &receipt) != nil || receipt["accepted"] != true {
		t.Fatalf("review handoff was not accepted: %s", raw)
	}
	store, err := readAgentMessageStore(ctx)
	if err != nil || len(store.Conversations) != 1 {
		t.Fatalf("saved handoff: %v, %v", store, err)
	}
	sender := store.Conversations[0].Endpoints[0]
	if sender.Kind != triggerCallerWorkflow || sender.ID == "" || sender.Session != session {
		t.Fatalf("scheduled workflow lost sender/reply identity: %+v", sender)
	}
	if _, err := send(ctx, map[string]interface{}{"action": "send", "target": "agent:private", "message": "Not shared", "submission_id": "denied-review"}); err == nil {
		t.Fatal("workflow reached another person's private agent")
	}
}

func scriptExecutorsForReviewTest(pool map[string]interface{}) map[string]catalog.ScriptExecutor {
	out := map[string]catalog.ScriptExecutor{}
	for name, value := range pool {
		if fn, ok := value.(func(context.Context, map[string]interface{}) (string, error)); ok {
			out[name] = fn
		}
	}
	return out
}
