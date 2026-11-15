package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// PLAT-746-1030: joining is not consent to share the session or its transcript.
func TestTerminalTeamJoinStartsPrivateAndKeepsNativeSessionIdentity(t *testing.T) {
	named := namedMessageAgent{ID: "named-agent", UserID: "owner", Name: "session-label", Attachment: &namedAgentAttachment{ID: "lease", SessionID: "exact-native-session", Provider: "claude"}}
	a := newCrewTeamTerminalAgent(crewTeamAgentIDPrefix+"11111111-1111-4111-8111-111111111111", "owner", named)
	if a.InTeam || a.Rule.ChatShare != crewChatShareAgent || a.Rule.CanMakeChanges {
		t.Fatal("joining must not share a session, its transcript, or permission to change files for others")
	}
	if a.ChatKey != named.Attachment.SessionID || a.OwnerID != named.UserID || a.Kind != "terminal" {
		t.Fatal("terminal identity must come from the admitted native attachment")
	}
}

// PLAT-746-1030: real OAuth + HTTP + SSE mailbox, no provider or fake transport.
// A Team grant is only this inbox: never general access to a private Crew.
func TestTerminalTeamCrossPersonMailboxRechecksSharingAndLease(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AUTH_SECRET", "terminal-team-fixture")
	oauth, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer oauth.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/external/v1/call", env.api.handleExternalCall)
	mux.HandleFunc("/api/external/v1/agents/events", env.api.handleAgentEvents)
	mux.HandleFunc("/api/external/v1/agents/attachment", env.api.handleAgentAttachment)
	srv := httptest.NewServer(AuthMiddleware(mux))
	defer srv.Close()
	t.Setenv("PUBLIC_URL", srv.URL)
	client := func(user string) *agentworksclient.Client {
		t.Helper()
		device, verify, e := oauth.CreateCLIDevice(t.Context(), "crews:run", "workflows:read", "runs:execute")
		if e != nil {
			t.Fatal(e)
		}
		if e = oauth.DecideCLIDevice(t.Context(), verify, &UserClaims{UserID: user, Username: user}, true); e != nil {
			t.Fatal(e)
		}
		_, token, _, e := oauth.PollCLIDevice(t.Context(), device, srv.URL+"/api/external/v1")
		if e != nil {
			t.Fatal(e)
		}
		c, e := agentworksclient.New(srv.URL, token)
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	receiver, sender := client("owner"), client("other")
	call := func(c *agentworksclient.Client, args map[string]any) map[string]any {
		t.Helper()
		raw, e := c.Call(t.Context(), "messages", args)
		if e != nil {
			t.Fatal(e)
		}
		var out map[string]any
		if e = json.Unmarshal(raw, &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	call(receiver, map[string]any{"action": "register", "agent_name": "terminal"})
	call(sender, map[string]any{"action": "register", "agent_name": "asker"})
	attachment, err := receiver.AttachAgent(t.Context(), agentworksclient.AgentAttachmentOptions{AgentName: "terminal", Provider: "claude", SessionID: "native-exact"})
	if err != nil {
		t.Fatal(err)
	}
	if err = crewroot.WriteSync("alpha", crewroot.SyncBinding{Mode: "relay", Side: "server", Editing: "local", PairID: "owner-pair", PeerProjectID: "local-alpha"}); err != nil {
		t.Fatal(err)
	}
	team := crewTeam{ProjectID: "alpha", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}}
	named, err := namedAgentByID(t.Context(), "owner", attachment.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := upsertCrewTeamTerminal(&team, "owner", named, "owner-pair")
	if err != nil {
		t.Fatal(err)
	}
	terminal.InTeam = true
	terminal.Rule.WhenOthersAsk = "Summarize the test result; ask before editing."
	team.Agents[terminal.ID] = terminal
	if err = writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}

	// A Team grant must not widen explicit Crew or folder fences.
	narrowed := &UserClaims{UserID: "other", AccessToken: &accesstokens.Token{Scopes: []string{"messages:send", "messages:read"}, CrewIDs: []string{"alpha"}}}
	if !terminalTeamMessagingAllowed(narrowed, "alpha", true) {
		t.Fatal("allowed Team message scope refused")
	}
	if terminalTeamMessagingAllowed(narrowed, "gamma", true) {
		t.Fatal("Team message escaped the connection's Crew fence")
	}
	narrowed.AccessToken.FileGuard = &wf.FolderGuard{}
	if terminalTeamMessagingAllowed(narrowed, "alpha", true) {
		t.Fatal("Team message widened a folder-scoped connection")
	}
	streamCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	streamDone := make(chan error, 1)
	go func() {
		streamDone <- receiver.StreamAgentMessages(streamCtx, attachment, func(agentworksclient.AgentLiveMessage) error { return nil })
	}()
	defer func() {
		cancel()
		select {
		case <-streamDone:
		case <-time.After(3 * time.Second):
			t.Error("stream did not stop")
		}
	}()
	until := time.Now().Add(3 * time.Second)
	for !namedAgentOnline(named) && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	if !namedAgentOnline(named) {
		t.Fatal("attached terminal event stream did not connect")
	}
	sent := call(sender, map[string]any{"action": "send", "agent_name": "asker", "target": crewTeamAgentAddress("alpha", terminal.ID), "message": "Which tests passed?", "submission_id": "team-ask"})
	inbox, _ := sent["inbox_id"].(string)
	if inbox == "" {
		t.Fatal(sent)
	}
	incoming := call(receiver, map[string]any{"action": "read", "agent_name": "terminal", "inbox_id": inbox})
	raw, _ := json.Marshal(incoming)
	if !bytes.Contains(raw, []byte("Which tests passed?")) || !bytes.Contains(raw, []byte("team_instructions")) || !bytes.Contains(raw, []byte("Summarize the test result")) {
		t.Fatalf("missing admitted ask/instructions: %s", raw)
	}
	call(receiver, map[string]any{"action": "send", "agent_name": "terminal", "inbox_id": inbox, "message": "Retry test passed.", "submission_id": "team-answer"})
	answer := call(sender, map[string]any{"action": "read", "agent_name": "asker", "inbox_id": inbox})
	raw, _ = json.Marshal(answer)
	if !bytes.Contains(raw, []byte("Retry test passed.")) {
		t.Fatal("answer did not return in the same inbox")
	}

	// Even a producer carrying a native line cannot leak it into Team Live.
	live := crewTeamLiveFor(t.Context(), team, []crewTeamAgentsPerson{{UserID: "owner", Name: "Owner", Agents: []crewTeamAgent{{ID: terminal.ID, Kind: "terminal", ChatKey: terminal.ChatKey, InTeam: true, Place: "mac", lastMessage: crewTeamMessagePreview{LastMessage: "PRIVATE_TERMINAL_SENTINEL", LastMessageAt: time.Now().Format(time.RFC3339Nano)}}}}}, "other")
	projected, _ := json.Marshal(live)
	if bytes.Contains(projected, []byte("PRIVATE_TERMINAL_SENTINEL")) || !bytes.Contains(projected, []byte("Retry test passed.")) {
		t.Fatalf("terminal preview must contain only an admitted reply, never native text: %s", projected)
	}
	if out := terminalTeamPreview(t.Context(), team, terminal, "stranger"); out.LastMessage != "" {
		t.Fatal("outsider saw a terminal preview")
	}
	// Sharing is rechecked even for a known participant, not just first contact.
	terminal.InTeam = false
	team.Agents[terminal.ID] = terminal
	if err = writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	if _, e := receiver.Call(t.Context(), "messages", map[string]any{"action": "send", "agent_name": "terminal", "inbox_id": inbox, "message": "Must not arrive", "submission_id": "after-unshare"}); e == nil {
		t.Fatal("Only-me terminal replied through a stale inbox")
	}
	hidden := call(receiver, map[string]any{"action": "read", "agent_name": "terminal"})
	raw, _ = json.Marshal(hidden)
	if bytes.Contains(raw, []byte("Which tests passed?")) {
		t.Fatal("Only-me terminal read a revoked Team ask")
	}
	terminal.InTeam = true
	team.Agents[terminal.ID] = terminal
	if err = writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	terminal.Terminal.AttachmentID = "old-lease"
	team.Agents[terminal.ID] = terminal
	if err = writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	if _, e := receiver.Call(t.Context(), "messages", map[string]any{"action": "send", "agent_name": "terminal", "inbox_id": inbox, "message": "Must not arrive", "submission_id": "wrong-lease"}); e == nil {
		t.Fatal("a stale native lease replied")
	}
	terminal.Terminal.AttachmentID = attachment.ID
	team.Agents[terminal.ID] = terminal
	if err = writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	cancel()
	until = time.Now().Add(3 * time.Second)
	for namedAgentOnline(named) && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	if namedAgentOnline(named) {
		t.Fatal("stream still online after cancellation")
	}
	if _, e := sender.Call(t.Context(), "messages", map[string]any{"action": "send", "agent_name": "asker", "target": crewTeamAgentAddress("alpha", terminal.ID), "message": "Offline question", "submission_id": "no-home"}); e == nil {
		t.Fatal("offline terminal without a Team home falsely accepted a queued ask")
	}
}
