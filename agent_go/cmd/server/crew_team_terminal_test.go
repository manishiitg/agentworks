package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// PLAT-746-1030: the returned working path stays within the admitted project,
// including after resolving symlinks. No caller-supplied path is authoritative.
func TestTerminalTeamCopyRefusesSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	inside := filepath.Join(base, "accepted-copy")
	if err := os.Mkdir(inside, 0700); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(inside)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := crewTeamTerminalCopyPath(base, inside); err != nil || got != want {
		t.Fatalf("admitted copy refused: %q %v", got, err)
	}
	outside := t.TempDir()
	link := filepath.Join(base, "escaped")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := crewTeamTerminalCopyPath(base, link); err == nil {
		t.Fatal("symlink expanded the accepted project")
	}
	if _, err := crewTeamTerminalCopyPath(base, outside); err == nil {
		t.Fatal("outside path expanded the accepted project")
	}
}

// Real OAuth and attachment HTTP, with only the remote Mac boundary replaced.
// The Mac receives server-stamped native identity, not the model's extra fields.
func TestTerminalTeamJoinUsesAttachedIdentityAndPairedCopy(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AUTH_SECRET", "terminal-join-fixture")
	oauth, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer oauth.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/external/v1/call", env.api.handleExternalCall)
	mux.HandleFunc("/api/external/v1/agents/attachment", env.api.handleAgentAttachment)
	srv := httptest.NewServer(AuthMiddleware(mux))
	defer srv.Close()
	t.Setenv("PUBLIC_URL", srv.URL)
	client := func(user string) (*agentworksclient.Client, string) {
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
		return c, token
	}
	owner, token := client("owner")
	_, wrongConnection := client("owner")
	member, memberToken := client("other")
	attach := func(c *agentworksclient.Client, name, session string) agentworksclient.AgentAttachment {
		t.Helper()
		if _, e := c.Call(t.Context(), "messages", map[string]any{"action": "register", "agent_name": name}); e != nil {
			t.Fatal(e)
		}
		a, e := c.AttachAgent(t.Context(), agentworksclient.AgentAttachmentOptions{AgentName: name, Provider: "claude", SessionID: session})
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	attached := attach(owner, "terminal", "native-owner")
	invited := attach(member, "member-terminal", "native-member")
	team := crewTeam{ProjectID: "alpha", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other", Invited: true}}}
	if err = writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	if err = crewroot.WriteSync("alpha", crewroot.SyncBinding{Mode: "relay", Side: "server", Editing: "local", PairID: "owner-pair", PeerProjectID: "local-alpha"}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	sock := &crewMacSocket{owner: "owner", device: "terminal-join-test", twins: map[string]string{"alpha": "owner-pair"}, lastSeen: time.Now(), pending: map[string]chan crewChatResult{}, dispatched: map[string]chan struct{}{}}
	sock.send = func(m crewRelayMessage) error {
		if m.Call == nil {
			return nil
		}
		calls++
		var identity map[string]string
		if m.Call.Operation != "team_terminal_copy" || json.Unmarshal(m.Call.Args, &identity) != nil || identity["session"] != attached.SessionID || identity["attachment"] != attached.ID || identity["agent"] != attached.AgentID || identity["provider"] != "claude" || identity["path"] != "" {
			return fmt.Errorf("caller changed the Mac's identity or path selector")
		}
		sock.mu.Lock()
		sock.pending[m.Call.ID] <- crewChatResult{ID: m.Call.ID, Status: 200, Body: []byte(`{"path":"/accepted/shared-copy"}`)}
		sock.mu.Unlock()
		return nil
	}
	crewMacSockets.Lock()
	crewMacSockets.byDevice[sock.device] = sock
	crewMacSockets.Unlock()
	t.Cleanup(func() { crewMacSockets.Lock(); delete(crewMacSockets.byDevice, sock.device); crewMacSockets.Unlock() })
	request := func(auth string, a agentworksclient.AgentAttachment) (int, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"action": "team_sync", "team_action": "join", "project": "alpha", "agent_name": a.AgentName, "attachment_id": a.ID, "session": "forged", "path": "/outside", "owner_id": "other"})
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/external/v1/agents/attachment", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+auth)
		req.Header.Set("Content-Type", "application/json")
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var out map[string]any
		if e = json.NewDecoder(res.Body).Decode(&out); e != nil {
			t.Fatal(e)
		}
		return res.StatusCode, out
	}
	if status, _ := request(wrongConnection, attached); status != 403 || calls != 0 {
		t.Fatalf("another connection reached the Mac: %d calls=%d", status, calls)
	}
	if status, _ := request(memberToken, invited); status != 403 || calls != 0 {
		t.Fatalf("an invitation was treated as membership: %d calls=%d", status, calls)
	}
	status, out := request(token, attached)
	if status != 200 || calls != 1 {
		t.Fatalf("admitted join failed: %d %v calls=%d", status, out, calls)
	}
	copy, _ := out["copy"].(map[string]any)
	if copy["path"] != "/accepted/shared-copy" {
		t.Fatal("join did not use the paired Mac's accepted copy")
	}
	saved, e := readCrewTeam("alpha")
	if e != nil {
		t.Fatal(e)
	}
	id, _ := out["agent_id"].(string)
	row := saved.Agents[id]
	if row.ID == "" || row.InTeam || row.Rule.ChatShare != crewChatShareAgent || row.Terminal == nil || row.Terminal.NamedAgentID != attached.AgentID || row.ChatKey != attached.SessionID {
		t.Fatal("join did not preserve exact native identity and private defaults")
	}
}
