package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
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
