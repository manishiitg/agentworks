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
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	for _, profile := range workproduct.BuiltinAgentProfiles() {
		profile.Product = workproduct.ProfileID
		if err := env.svc.registry.RegisterProfile(profile); err != nil {
			t.Fatal(err)
		}
	}
	const root = "_users/owner/Chats/Code/projects/example"
	const key = "example:chat:abcdef12"
	const session = "product-11111111-2222-4333-8444-555555555555"
	record := ProductConversationRecord{ProfileID: workproduct.ProfileID, ConversationKey: key, SessionID: session, WorkspacePath: root, ResourceID: "example"}
	registry, _ := json.Marshal(productConversationRegistryDocument{Version: 1, Entries: map[string]ProductConversationRecord{"code/" + key: record}})
	initial, _ := json.Marshal(agentMessageStore{Agents: []namedMessageAgent{{ID: "agent-receiver", UserID: "owner", Name: "receiver", EverAttached: true, Attachment: &namedAgentAttachment{ID: "lease", DeliveryContract: 2}}}})
	env.mock.mu.Lock()
	env.mock.files[root+"/product.json"] = `{"schema_version":1,"product":"code","id":"example","owner_id":"owner","title":"Example","session_id":"project-main"}`
	env.mock.files[productConversationRegistryPath("owner")] = string(registry)
	env.mock.files[agentMessagesPath] = string(initial)
	env.mock.mu.Unlock()
	reg := &recordingRegistrar{}
	if err := env.api.registerAgentMessagingTools(reg, "owner", session, QueryRequest{}, crewTriggerLinkCaller(root), func(string) {}); err != nil {
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
	if err := env.api.externalAuthorizeMessagePeer(ctx, receiver, sender, false); err != nil {
		t.Fatalf("own hosted sender refused: %v", err)
	}
	sender.ChatKey = "session:" + session
	if err := env.api.externalAuthorizeMessagePeer(ctx, receiver, sender, false); err == nil {
		t.Fatal("a different chat key passed strict admission")
	}
}
