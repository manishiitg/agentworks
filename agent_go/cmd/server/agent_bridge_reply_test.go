package server

import (
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"testing"
	"time"

	"context"
)

// #2762: a recipient of the former sessions-message route could not reply in
// its original inbox although a fresh addressed send worked. Both tool calls
// must bind to the same registered identities, not invent a second thread.
func TestBridgeHostedRecipientRepliesInOriginalThread(t *testing.T) {
	env := newTriggerLinkEnv(t)
	crewOwnerTabsFixture(t, env)
	env.api.agentProfiles = env.svc.registry
	ctx := internalBotRequestContext(t.Context(), "owner")
	profile, err := env.svc.registry.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	records := []ProductConversationRecord{}
	root := ""
	for _, key := range []string{"beta:chat:sender", "beta:chat:recipient"} {
		binding, e := resolveProductConversationBinding(ctx, "owner", profile, key)
		if e != nil {
			t.Fatal(e)
		}
		r, e := defaultProductConversationRegistryStore().resolveOrCreate(ctx, "owner", profile, binding, "")
		if e != nil {
			t.Fatal(e)
		}
		records = append(records, r)
		root = binding.WorkspacePath
	}
	tools := func(record ProductConversationRecord) *recordingRegistrar {
		reg := &recordingRegistrar{}
		resolve := func(context.Context) (triggerLinkCaller, error) {
			return triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ProfileID: "work", ID: "beta"}, Path: root, Label: "Agent", Chat: &codeChat{OwnerID: "owner", Key: record.ConversationKey, ID: record.ConversationKey, SessionID: record.SessionID}}, nil
		}
		if e := env.api.registerAgentMessagingTools(reg, "owner", record.SessionID, QueryRequest{}, resolve, nil); e != nil {
			t.Fatal(e)
		}
		return reg
	}
	// The Team row is the anchor even when a hosted source registers before
	// its Mac attachment. Registration may bind once, never create a copy agent.
	people := []crewTeamAgentsPerson{{UserID: "owner", Agents: []crewTeamAgent{
		{Tab: "sender", ChatKey: records[0].ConversationKey, Place: "server"},
		{Tab: "recipient", ChatKey: records[1].ConversationKey, Place: "server"},
	}}}
	if err := assignCrewTeamAgentIDs(crewTeam{ProjectID: "beta", OwnerID: "owner", On: true}, people); err != nil {
		t.Fatal(err)
	}
	sender, recipient := tools(records[0]), tools(records[1])
	raw, err := sender.tools["send_message"].exec(ctx, map[string]interface{}{"target": bridgeChatAddress("owner", "work", "beta", records[1].ConversationKey), "message": "Please check this", "submission_id": "first-request"})
	if err != nil {
		t.Fatal(err)
	}
	var first map[string]interface{}
	if json.Unmarshal([]byte(raw), &first) != nil {
		t.Fatal("invalid send receipt")
	}
	inbox, _ := first["inbox_id"].(string)
	if inbox == "" {
		t.Fatal("missing original reply thread")
	}
	raw, err = recipient.tools["send_message"].exec(ctx, map[string]interface{}{"inbox_id": inbox, "message": "Checked", "submission_id": "first-reply"})
	if err != nil {
		t.Fatalf("recipient could not reply in original Bridge thread: %v", err)
	}
	var reply map[string]interface{}
	if json.Unmarshal([]byte(raw), &reply) != nil || reply["inbox_id"] != inbox {
		t.Fatal("reply created a different conversation", raw)
	}
	snapshot, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range snapshot.Conversations {
		if c.ID != inbox {
			continue
		}
		if c.Endpoints[0].NamedAgentID == "" || c.Endpoints[1].NamedAgentID == "" {
			t.Fatal("a peer endpoint bypassed named Bridge registration")
		}
		if len(c.Messages) != 2 || c.Messages[0].From == c.Messages[1].From {
			t.Fatal("original thread did not retain the recipient reply")
		}
		team, err := readCrewTeam("beta")
		if err != nil {
			t.Fatal(err)
		}
		row, ok := team.registeredAgent("owner", records[1].ConversationKey)
		if !ok || row.NamedAgentID != c.Endpoints[1].NamedAgentID {
			t.Fatal("Team row did not retain the recipient's named identity")
		}
		// Mac registration uses its routing labels, but reuses the existing
		// source ID. A device-derived name is not a second identity.
		claims := &UserClaims{UserID: "owner", RegisteredClaims: jwt.RegisteredClaims{ID: "app-login", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
		attached, err := registerAppMessageAgent(ctx, claims, map[string]any{
			"agent_name": desktopAppAgentName("device", "conversation"), "device_id": "device", "conversation_id": "conversation",
			"runtime_info": map[string]any{"team_project_id": "beta", "team_tab": "recipient"},
		})
		if err != nil || attached.ID != row.NamedAgentID {
			t.Fatalf("Mac minted or took a different source identity: %v", err)
		}
		saved, err := readAgentMessageSnapshot(ctx)
		if err != nil || len(saved.Agents) != 2 {
			t.Fatalf("registration created a duplicate: %v", err)
		}
		return
	}
	t.Fatal("original Bridge thread disappeared")
}
