package server

import (
	"context"
	"encoding/json"
	"fmt"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	pkgevents "github.com/manishiitg/mcpagent/events"
	"path/filepath"
	"testing"
	"time"
)

// #2762: a recipient of the former sessions-message route could not reply in
// its original inbox although a fresh addressed send worked. Both tool calls
// must bind to the same registered identities, not invent a second thread.
func TestBridgeHostedRecipientRepliesInOriginalThread(t *testing.T) {
	for _, mode := range []string{"server", "local"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", mode)
			bridgeAppRecipientRepliesInOriginalThread(t, mode == "local")
		})
	}
}

func bridgeAppRecipientRepliesInOriginalThread(t *testing.T, localOnly bool) {
	env := newTriggerLinkEnv(t)
	crewOwnerTabsFixture(t, env)
	if localOnly {
		// #3157: local peer messaging must not depend on a Team, saved
		// server login or an inherited endpoint/token from the test runner.
		t.Setenv("MULTI_USER_MODE", "false")
		t.Setenv("AGENTWORKS_CONFIG", filepath.Join(t.TempDir(), "no-login.json"))
		t.Setenv("AGENTWORKS_SERVER", "")
		t.Setenv("AGENTWORKS_TOKEN", "")
		connections, err := readDesktopConnections("owner")
		if err != nil || len(connections) != 0 {
			t.Fatalf("local-only fixture has saved connections: %v", err)
		}
	} else {
		// #2983: Team-on used to skip own-tab discovery before lazy registration.
		joinCrewTeam(t, "owner-tabs")
		team, e := readCrewTeam("beta")
		if e != nil || !crewTeamAgentsVisible(team, "owner") {
			t.Fatalf("fixture must exercise the Team branch: %v", e)
		}
	}
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
		name := "Bridge recipient"
		if len(records) == 0 {
			name = "Bridge sender"
		}
		if _, e := defaultProductConversationRegistryStore().setAgentIdentity(ctx, "owner", "work", key, &name, nil, "owner"); e != nil {
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
	delivered := make(chan string, 2)
	oldTurn := agentMessageTurn
	agentMessageTurn = func(_ *StreamingAPI, _ context.Context, request map[string]interface{}, session, owner string) (internalSessionTurnResult, error) {
		delivered <- session + ":" + fmt.Sprint(request["agent_message_inbox"])
		return internalSessionTurnResult{}, nil
	}
	t.Cleanup(func() { agentMessageTurn = oldTurn })
	waitDelivery := func(session, inbox string) {
		t.Helper()
		select {
		case got := <-delivered:
			if got != session+":"+inbox {
				t.Fatalf("wrong receiving chat/thread: %s", got)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("named hosted delivery never reached the normal turn boundary")
		}
	}
	// #2852: local MCP must discover local app contacts, not just saved
	// remote networks. Send only to the exact address discovery returned.
	before, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := defaultProductConversationRegistryStore().setImportedAgentName(ctx, "owner", "work", records[0].ConversationKey, "Claude Code (example)"); err != nil {
		t.Fatal(err)
	}
	directory, err := listAgentDirectory(ctx, &UserClaims{UserID: "owner"}, env.api)
	if err != nil {
		t.Fatal(err)
	}
	after, err := readAgentMessageSnapshot(ctx)
	if err != nil || len(before.Agents) != len(after.Agents) {
		t.Fatal("#2980: listing created hosted registrations")
	}
	identity, err := defaultProductConversationRegistryStore().agentIdentity(ctx, "owner", "work", records[0].ConversationKey)
	if err != nil || !identity.GeneratedName {
		t.Fatal("import lost its generated-name marker")
	}
	for _, row := range directory["agents"].([]map[string]interface{}) {
		if row["target"] == bridgeChatAddress("owner", "work", "beta", records[0].ConversationKey) {
			t.Fatal("generated imported name listed as explicit")
		}
	}
	if err := setCodeTabName(context.WithValue(ctx, generatedCallerTabNameKey{}, true), "owner", records[0].ConversationKey, "Claude Code (example)", "work"); err != nil {
		t.Fatal(err)
	}
	identity, err = defaultProductConversationRegistryStore().agentIdentity(ctx, "owner", "work", records[0].ConversationKey)
	if err != nil || !identity.GeneratedName {
		t.Fatal("automatic caller label lacks provenance")
	}
	realName := "Claude Code (example)"
	identity, err = defaultProductConversationRegistryStore().setAgentIdentity(ctx, "owner", "work", records[0].ConversationKey, &realName, nil, "owner")
	if err != nil || identity.GeneratedName {
		t.Fatal("person's identical-text rename stayed generated")
	}
	address := ""
	for _, row := range directory["agents"].([]map[string]interface{}) {
		if row["target"] == bridgeChatAddress("owner", "work", "beta", records[1].ConversationKey) {
			address, _ = row["target"].(string)
		}
	}
	if address == "" {
		t.Fatal("owned local app missing from Bridge discovery")
	}
	sender, recipient := tools(records[0]), tools(records[1])
	raw, err := sender.tools["send_message"].exec(ctx, map[string]interface{}{"target": "bridge recipient", "ask": true, "message": "Please check this", "submission_id": "first-request"})
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
	waitDelivery(records[1].SessionID, inbox)
	retry, err := sender.tools["send_message"].exec(ctx, map[string]interface{}{"target": address, "ask": true, "message": "Please check this", "submission_id": "first-request"})
	if err != nil || decodeToolJSON(t, retry)["inbox_id"] != inbox {
		t.Fatalf("retry changed original thread: %v %s", err, retry)
	}

	// #2881: repeated completion checks forward once for this turn. A later
	// real turn may forward again; an update never closes the request.
	env.api.eventStore = openClientIdentityTestStore(t, "bridge-finals")
	// A real receiving turn remembers its owner; the model-boundary stub
	// above deliberately does not execute the query handler.
	env.api.lastQueryMu.Lock()
	if env.api.lastQueryRequests == nil {
		env.api.lastQueryRequests = map[string]QueryRequest{}
	}
	env.api.lastQueryRequests[records[1].SessionID] = QueryRequest{userID: "owner", AgentMessageInbox: inbox}
	env.api.lastQueryMu.Unlock()
	final := storeevents.Event{ID: "final-reply-turn", Type: "unified_completion", ReplyToAgentMessageID: first["message_id"].(string), Data: pkgevents.NewAgentEvent(storeevents.NewGenericEventData("unified_completion", map[string]interface{}{"status": "completed", "final_result": "Working on it"}))}
	// The delivery worker records the task session after returning from the
	// turn boundary above. Wait for its receipt, not a provider's timing.
	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, e := readAgentMessageSnapshot(ctx)
		ready := false
		if e != nil {
			t.Fatal(e)
		}
		for _, c := range snapshot.Conversations {
			if c.ID == inbox && c.Messages[0].TaskProgress.Session == records[1].SessionID {
				ready = true
			}
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task receipt never bound to delivered session")
		}
		time.Sleep(10 * time.Millisecond)
	}
	input := pkgevents.NewUserMessageEvent(0, "Please check this", "user")
	input.Metadata = map[string]interface{}{"source": "agent_message", "message_id": first["message_id"]}
	inputEvent := pkgevents.NewAgentEvent(input)
	inputEvent.TurnID = "reply-turn"
	final.Data.TurnID = inputEvent.TurnID
	env.api.eventStore.AddEvent(records[1].SessionID, storeevents.Event{ID: "delivered-request", Type: "user_message", Data: inputEvent})
	env.api.eventStore.AddEvent(records[1].SessionID, final)
	env.api.forwardUnsentAgentTaskFinal(records[1].SessionID)
	env.api.forwardUnsentAgentTaskFinal(records[1].SessionID)
	raw, err = recipient.tools["send_message"].exec(ctx, map[string]interface{}{"inbox_id": inbox, "message": "Checked", "submission_id": "first-reply"})
	if err != nil {
		t.Fatalf("recipient could not reply in original Bridge thread: %v", err)
	}
	var reply map[string]interface{}
	if json.Unmarshal([]byte(raw), &reply) != nil || reply["inbox_id"] != inbox {
		t.Fatal("reply created a different conversation", raw)
	}
	waitDelivery(records[0].SessionID, inbox)
	if localOnly {
		t.Logf("[AGENTBRIDGE] event=local_contract saved_networks=0 request=delivered reply=same_thread model_boundary=fixture")
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
		if len(c.Messages) != 3 || c.Messages[1].Kind != agentTaskUpdateKind || c.Messages[0].From == c.Messages[2].From {
			t.Fatalf("original thread lost its single update/reply: messages=%+v", c.Messages)
		}
		named, err := namedAgentByID(ctx, "owner", c.Endpoints[1].NamedAgentID)
		if err != nil || namedAgentOnline(named) || agentDeliveryMode(named) != "on_request" {
			t.Fatalf("#2980: hosted contact invented a live view: %v", err)
		}
		if state, _ := receiptState(agentMessage{Delivery: "pending"}, &named, true); state != "sent" {
			t.Fatal("callable idle hosted chat was reported offline")
		}
		closeView := observeHostedChatView("owner", *named.Hosted)
		if !namedAgentOnline(named) || agentDeliveryMode(named) != "live" {
			t.Fatal("owner view did not supply live presence")
		}
		closeView()
		if namedAgentOnline(named) {
			t.Fatal("closed owner view left invented presence")
		}
		// #2980: read-only projection must reuse a historical canonical alias,
		// not list both its synthesized hash and the existing registration.
		agentMessageMu.Lock()
		legacyStore, legacyErr := readAgentMessageStore(ctx)
		if legacyErr == nil {
			for i := range legacyStore.Agents {
				if legacyStore.Agents[i].ID == named.ID {
					legacyStore.Agents[i].Name = "prior-hosted-alias"
				}
			}
			legacyErr = saveAgentMessageStore(ctx, legacyStore)
		}
		agentMessageMu.Unlock()
		if legacyErr != nil {
			t.Fatal(legacyErr)
		}
		again, legacyErr := listAgentDirectory(ctx, &UserClaims{UserID: "owner"}, env.api)
		if legacyErr != nil {
			t.Fatal(legacyErr)
		}
		contacts := 0
		for _, row := range again["agents"].([]map[string]interface{}) {
			if row["agent_id"] == named.ID {
				contacts++
			}
		}
		if contacts != 1 {
			t.Fatalf("canonical hosted binding listed %d times", contacts)
		}
		// #2980 live regression: a Main's identity can still contain a login
		// or Crew name, although its displayed name is Main. Both the saved
		// registration and the read-only tab projection must hide it, while
		// its exact address and authenticated owner view keep working.
		env.mock.mu.Lock()
		var manifest map[string]any
		manifestErr := json.Unmarshal([]byte(env.mock.files[root+"/product.json"]), &manifest)
		if manifestErr == nil {
			manifest["main_chat_key"] = records[1].ConversationKey
			var raw []byte
			raw, manifestErr = json.Marshal(manifest)
			env.mock.files[root+"/product.json"] = string(raw)
		}
		env.mock.mu.Unlock()
		if manifestErr != nil {
			t.Fatal(manifestErr)
		}
		checkMain := func(want int) {
			t.Helper()
			out, err := listAgentDirectory(ctx, &UserClaims{UserID: "owner"}, env.api)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, row := range out["agents"].([]map[string]any) {
				if row["agent_id"] == named.ID {
					count++
				}
			}
			if count != want {
				t.Fatalf("Main registration listed %d times, want %d", count, want)
			}
		}
		for _, savedName := range []string{"owner-login", "Example Crew"} {
			if _, err := defaultProductConversationRegistryStore().setAgentIdentity(ctx, "owner", "work", records[1].ConversationKey, &savedName, nil, "owner"); err != nil {
				t.Fatal(err)
			}
			checkMain(0)
		}
		if _, err := env.api.resolveBridgeMessageAgent(ctx, &UserClaims{UserID: "owner"}, "agent:owner/prior-hosted-alias", ""); err != nil {
			t.Fatalf("Main lost exact-address admission: %v", err)
		}
		closeMain := observeHostedChatView("owner", *named.Hosted)
		checkMain(1)
		closeMain()
		checkMain(0)
		// JSON allocates distinct binding pointers after a restart; the
		// original source is still the same endpoint for retry dedupe.
		encoded, err := json.Marshal(c.Endpoints[0])
		if err != nil {
			t.Fatal(err)
		}
		var reopened agentMessageEndpoint
		if err := json.Unmarshal(encoded, &reopened); err != nil || !sameAgentMessageEndpoint(reopened, c.Endpoints[0]) {
			t.Fatalf("reopen changed the sender identity: %v", err)
		}
		other := internalBotRequestContext(t.Context(), "other")
		if _, err := env.api.resolveBridgeMessageAgent(other, &UserClaims{UserID: "other"}, bridgeChatAddress("owner", "work", "beta", records[1].ConversationKey), ""); err == nil {
			t.Fatal("outsider gained a private hosted contact")
		}
		return
	}
	t.Fatal("original Bridge thread disappeared")
}
