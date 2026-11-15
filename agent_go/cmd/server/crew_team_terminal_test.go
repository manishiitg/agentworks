package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gorillamux "github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-746-1030: new joins are In team, without sharing the transcript or change permission.
func TestTerminalTeamJoinStartsInTeamWithPrivateChatAndKeepsNativeSessionIdentity(t *testing.T) {
	named := namedMessageAgent{ID: "named-agent", UserID: "owner", Name: "session-label", Attachment: &namedAgentAttachment{ID: "lease", SessionID: "exact-native-session", Provider: "claude"}}
	a := newCrewTeamTerminalAgent(crewTeamAgentIDPrefix+"11111111-1111-4111-8111-111111111111", "owner", named)
	if !a.InTeam || a.Rule.ChatShare != crewChatShareAgent || a.Rule.CanMakeChanges {
		t.Fatal("joining must put the agent In team without sharing its transcript or change permission")
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

	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			// Live may open only one exact regular native journal, never a symlink
			// into unrelated files or an ambiguous duplicate from another project.
			home := t.TempDir()
			t.Setenv("HOME", home)
			const nativeID = "11111111-2222-4333-8444-555555555555"
			project := filepath.Join(home, ".claude", "projects", "one")
			base := filepath.Join(home, ".claude", "projects")
			filename := nativeID + ".jsonl"
			if provider == "codex" {
				base = filepath.Join(home, ".codex", "sessions")
				project = filepath.Join(base, "2026", "11", "15")
				filename = "rollout-start-" + nativeID + ".jsonl"
			}
			t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
			if err := os.MkdirAll(project, 0700); err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(project, filename)
			if err := os.WriteFile(journal, []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			physical, err := filepath.EvalSymlinks(journal)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := crewTeamTerminalJournal(provider, nativeID); err != nil || got != physical {
				t.Fatalf("matching journal refused: %s %v", got, err)
			}
			second := filepath.Join(base, "two")
			if provider == "codex" {
				second = filepath.Join(base, "2026", "11", "16")
			}
			if err := os.MkdirAll(second, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(journal, filepath.Join(second, filename)); err != nil {
				t.Fatal(err)
			}
			if _, err := crewTeamTerminalJournal(provider, nativeID); err == nil {
				t.Fatal("ambiguous native session selected a private journal")
			}
			if err := os.Remove(journal); err != nil {
				t.Fatal(err)
			}
			if _, err := crewTeamTerminalJournal(provider, nativeID); err == nil {
				t.Fatal("symlink native journal was opened")
			}
			for _, invalid := range []string{"../../private", "urn:uuid:" + nativeID, "{" + nativeID + "}"} {
				if _, err := crewTeamTerminalJournal(provider, invalid); err == nil {
					t.Fatal("noncanonical native ID became a file selector")
				}
			}
		})
	}

}

// Real OAuth and attachment HTTP, with only the remote Mac boundary replaced.
// The Mac receives server-stamped native identity, not the model's extra fields.
func TestTerminalTeamJoinUsesAttachedIdentityAndPairedCopy(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
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
			mux.HandleFunc("/api/external/v1/agents/sharing", env.api.handleAgentSharing)
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
			parent, _ := client("owner")
			_, wrongConnection := client("owner")
			member, _ := client("other")
			attach := func(c *agentworksclient.Client, name, session string) (*agentworksclient.Client, string, agentworksclient.AgentAttachment) {
				t.Helper()
				if _, e := c.Call(t.Context(), "messages", map[string]any{"action": "register", "agent_name": name}); e != nil {
					t.Fatal(e)
				}
				cfg, e := c.CreateAgentConnection(t.Context(), name)
				if e != nil {
					t.Fatal(e)
				}
				child, e := agentworksclient.New(srv.URL, cfg.Token)
				if e != nil {
					t.Fatal(e)
				}
				a, e := child.AttachAgent(t.Context(), agentworksclient.AgentAttachmentOptions{AgentName: name, Provider: provider, SessionID: session})
				if e != nil {
					t.Fatal(e)
				}
				return child, cfg.Token, a
			}
			owner, token, attached := attach(parent, "terminal", "native-owner")
			sender, memberToken, invited := attach(member, "member-terminal", "native-member")
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
				if m.Call.Operation != "team_terminal_copy" && m.Call.Operation != "team_terminal_live" {
					// Central reach may refresh Mac tab metadata; that is not a native
					// journal read and must not affect the privacy-before-I/O counter.
					sock.mu.Lock()
					sock.pending[m.Call.ID] <- crewChatResult{ID: m.Call.ID, Status: 200, Body: []byte(`{"tabs":[]}`)}
					sock.mu.Unlock()
					return nil
				}
				calls++
				var identity map[string]string
				if (m.Call.Operation != "team_terminal_copy" && m.Call.Operation != "team_terminal_live") || json.Unmarshal(m.Call.Args, &identity) != nil || identity["session"] != attached.SessionID || identity["attachment"] != attached.ID || identity["agent"] != attached.AgentID || identity["provider"] != provider || identity["path"] != "" {
					return fmt.Errorf("caller changed the Mac's identity or path selector")
				}
				body := []byte(`{"path":"/accepted/shared-copy"}`)
				if m.Call.Operation == "team_terminal_live" {
					body = []byte(`{"messages":[{"Role":"assistant","Parts":[{"Text":"Live-only fixture"}]}]}`)
				}
				sock.mu.Lock()
				sock.pending[m.Call.ID] <- crewChatResult{ID: m.Call.ID, Status: 200, Body: body}
				sock.mu.Unlock()
				return nil
			}
			crewMacSockets.Lock()
			crewMacSockets.byDevice[sock.device] = sock
			crewMacSockets.Unlock()
			t.Cleanup(func() { crewMacSockets.Lock(); delete(crewMacSockets.byDevice, sock.device); crewMacSockets.Unlock() })
			crewProject := "alpha"
			request := func(auth string, a agentworksclient.AgentAttachment, actions ...string) (int, map[string]any) {
				t.Helper()
				action := "join"
				if len(actions) != 0 {
					action = actions[0]
				}
				operation := "team_sync"
				if action == "join" || action == "leave" {
					operation = "crew_" + action
				}
				if action == "inbox" {
					operation = "inbox"
				}
				raw, _ := json.Marshal(map[string]any{"action": operation, "team_action": action, "line": "Fixture work", "why": "Fixture reason", "project": crewProject, "agent_name": a.AgentName, "attachment_id": a.ID, "session": "forged", "path": "/outside", "owner_id": "other"})
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
			if row.ID == "" || !row.InTeam || row.Rule.ChatShare != crewChatShareAgent || row.Terminal == nil || row.Terminal.NamedAgentID != attached.AgentID || row.ChatKey != attached.SessionID {
				t.Fatal("join did not preserve exact native identity and private defaults")
			}

			// Explicit Only me remains available and survives rejoin.
			row.InTeam = false
			saved.Agents[id] = row
			if e = writeCrewTeamAgentFixture(saved); e != nil {
				t.Fatal(e)
			}
			if status, again := request(token, attached); status != 200 || again["in_team"] != false {
				t.Fatalf("rejoin changed Only me: %d %v", status, again)
			}
			if status, out = request(token, attached, "doing"); status != 200 || out["private"] != true {
				t.Fatalf("Only-me activity through a messaging child must stay private: %d %v", status, out)
			}
			for _, action := range []string{"agents", "clashes", "resolve", "forks"} {
				if status, _ = request(token, attached, action); status != 400 {
					t.Fatalf("messaging child acquired a Crew operation %s: %d", action, status)
				}
			}

			// The actual Live handler must refuse before any Mac/native read unless
			// the person chose Live and both people remain in this Team.
			saved.People[0].Invited = false
			row.InTeam = true
			saved.Agents[id] = row
			if e = writeCrewTeamAgentFixture(saved); e != nil {
				t.Fatal(e)
			}
			liveRead := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, "/live", nil)
				req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "other", Username: "other"}))
				req = gorillamux.SetURLVars(req, map[string]string{"project": "alpha", "agent": id})
				out := httptest.NewRecorder()
				env.api.handleCrewTeamTerminalLive(out, req)
				return out
			}
			before := calls
			if res := liveRead(); res.Code != 403 || calls != before {
				t.Fatal("Answers only reached the native journal", res.Code)
			}
			row.Rule.ChatShare = crewChatShareLive
			saved.Agents[id] = row
			if e = writeCrewTeamAgentFixture(saved); e != nil {
				t.Fatal(e)
			}
			if res := liveRead(); res.Code != 200 || calls != before+1 || !strings.Contains(res.Body.String(), "Live-only fixture") {
				t.Fatal("admitted Live read failed", res.Code, res.Body.String())
			}
			saved.People[0].Invited = true
			if e = writeCrewTeamAgentFixture(saved); e != nil {
				t.Fatal(e)
			}
			if res := liveRead(); res.Code != 403 || calls != before+1 {
				t.Fatal("revoked membership reached the native journal", res.Code)
			}

			// Normal bridge mail, without a separate Team route or a public share.
			saved.People[0].Invited = false
			if e = writeCrewTeamAgentFixture(saved); e != nil {
				t.Fatal(e)
			}
			mail := func(client *agentworksclient.Client, args map[string]any) map[string]any {
				t.Helper()
				args["agent_name"] = "terminal"
				if client == sender {
					args["agent_name"] = "member-terminal"
				}
				raw, err := client.Call(t.Context(), "messages", args)
				if err != nil {
					t.Fatal(err)
				}
				var out map[string]any
				if err = json.Unmarshal(raw, &out); err != nil {
					t.Fatal(err)
				}
				return out
			}
			if _, err := sender.Call(t.Context(), "messages", map[string]any{"agent_name": "member-terminal", "action": "ask", "target": "agent:owner/terminal", "message": "Offline question", "submission_id": "offline-question"}); err == nil {
				t.Fatal("offline ASK without a Team home was not refused")
			}
			receipt := mail(sender, map[string]any{"action": "send", "target": "agent:owner/terminal", "message": "Team fixture question", "submission_id": "team-question"})
			inbox, _ := receipt["inbox_id"].(string)
			if inbox == "" {
				t.Fatalf("normal bridge returned no inbox: %v", receipt)
			}
			if status, pending := request(token, attached, "inbox"); status != 200 || !strings.Contains(fmt.Sprint(pending["messages"]), "answer questions only") {
				t.Fatalf("native hook inbox lost admitted Team mail/instructions: %d %v", status, pending)
			}
			incoming := mail(owner, map[string]any{"action": "read", "inbox_id": inbox})
			if messages, _ := incoming["messages"].([]any); len(messages) != 1 {
				t.Fatalf("Team mail not readable by the exact attached receiver: %v", incoming)
			}
			if messages, _ := incoming["messages"].([]any); len(messages) == 1 {
				message, _ := messages[0].(map[string]any)
				instructions, _ := message["team_instructions"].(string)
				if !strings.Contains(instructions, "answer questions only") || message["message"] != "Team fixture question" {
					t.Fatalf("Team instructions missing or mixed into sender content: %v", message)
				}
			}
			answer := mail(owner, map[string]any{"action": "send", "inbox_id": inbox, "message": "Team fixture answer", "submission_id": "team-answer"})
			if answer["inbox_id"] != inbox {
				t.Fatal("reply left the original inbox")
			}
			incoming = mail(sender, map[string]any{"action": "read", "inbox_id": inbox})
			if messages, _ := incoming["messages"].([]any); len(messages) != 1 {
				t.Fatalf("Team reply did not return to the original sender: %v", incoming)
			}
			saved.People[0].Invited = true
			if e = writeCrewTeamAgentFixture(saved); e != nil {
				t.Fatal(e)
			}
			if _, err := sender.Call(t.Context(), "messages", map[string]any{"agent_name": "member-terminal", "action": "send", "inbox_id": inbox, "message": "Refused after leaving", "submission_id": "team-revoked"}); err == nil {
				t.Fatal("removed Team member could reply in the old inbox")
			}

			// A disconnected stream is valid offline, but rebinding the same named
			// agent to a different native session must not inherit this Team route.
			current, e := currentCrewTeamTerminal(t.Context(), saved, row)
			if e != nil || current.ID != attached.AgentID || namedAgentOnline(current) {
				t.Fatalf("matching offline binding must remain valid: %v", e)
			}
			replacement, e := owner.AttachAgent(t.Context(), agentworksclient.AgentAttachmentOptions{AgentName: "terminal", Provider: provider, SessionID: "private-replacement-session"})
			if e != nil {
				t.Fatal(e)
			}
			if replacement.AgentID != attached.AgentID || replacement.ID == attached.ID {
				t.Fatal("fixture did not rotate the same named agent's native binding")
			}
			if _, e = currentCrewTeamTerminal(t.Context(), saved, row); e == nil {
				t.Fatal("a replacement private session inherited the old Team binding")
			}
			saved.People[0].Invited = false
			if e = writeCrewTeamAgentFixture(saved); e != nil {
				t.Fatal(e)
			}
			if _, err := sender.Call(t.Context(), "messages", map[string]any{"agent_name": "member-terminal", "action": "send", "target": "agent:owner/terminal", "message": "Must not reach replacement", "submission_id": "team-rebound"}); err == nil {
				t.Fatal("rebound terminal accepted Team mail before explicit rejoin")
			}
			attached = replacement // the Mac now proves the explicitly rejoined lease
			if status, out := request(token, attached); status != 200 || out["agent_id"] != id || out["in_team"] != true || out["chat_share"] != crewChatShareLive {
				t.Fatalf("explicit rejoin lost stable identity/choices: %d %v", status, out)
			}
			if status, out := request(token, attached, "leave"); status != 200 {
				t.Fatalf("leave failed: %d %v", status, out)
			}
			left, err := readCrewTeam("alpha")
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := left.Agents[id]; exists {
				t.Fatal("leave retained Team row")
			}
			if named, err := namedAgentByID(t.Context(), "owner", attached.AgentID); err != nil || named.Attachment == nil || named.Attachment.ID != attached.ID {
				t.Fatal("leave detached the native AgentBridge chat", err)
			}

			// PLAT-746-1030: owned Crews need no Team or local copy; Run access
			// and invitations never become membership. Turning Team on reuses rows.
			if _, err = parent.ShareAgent(t.Context(), "share", "terminal"); err != nil {
				t.Fatal("fixture could not save native Discovery publication", err)
			}
			publicReceipt := mail(sender, map[string]any{"action": "send", "target": "agent:owner/terminal", "message": "Saved Discovery fixture", "submission_id": "saved-discovery"})
			publicInbox, _ := publicReceipt["inbox_id"].(string)
			if publicInbox == "" {
				t.Fatal("published native agent was not reachable before Crew join")
			}
			crewProject = "beta"
			env.mock.mu.Lock()
			env.mock.files[linkBetaPath+"/PROJECT_INSTRUCTIONS.md"] = "Use the Crew fixture instructions."
			env.mock.mu.Unlock()
			if status, out := request(memberToken, invited); status != 403 {
				t.Fatalf("non-owner joined a Team-off Crew: %d %v", status, out)
			}
			status, out = request(token, attached)
			if status != 200 || out["copy"] != nil || out["team_on"] != false || out["in_team"] != false || !strings.Contains(fmt.Sprint(out["instructions"]), "Crew fixture instructions") {
				t.Fatalf("owned server Crew did not join without local files: %d %v", status, out)
			}
			if folders, ok := out["folders"].([]any); !ok || len(folders) != 0 {
				t.Fatalf("server-only join returned local folder selections: %v", out)
			}
			beta, err := readCrewTeam("beta")
			if err != nil || beta.On || beta.OwnerID != "owner" || len(beta.People) != 0 || len(beta.Agents) != 1 {
				t.Fatalf("join did not persist one minimal Off registry: %+v %v", beta, err)
			}
			if _, err := sender.Call(t.Context(), "messages", map[string]any{"agent_name": "member-terminal", "action": "send", "target": "agent:owner/terminal", "message": "Must not use saved Discovery", "submission_id": "joined-discovery"}); err == nil || !strings.Contains(err.Error(), "Crew") {
				t.Fatalf("joined native terminal fell through to Discovery: %v", err)
			}
			if _, err := sender.Call(t.Context(), "messages", map[string]any{"agent_name": "member-terminal", "action": "read", "inbox_id": publicInbox}); err == nil {
				t.Fatal("old Discovery inbox retained access while Crew joined")
			}
			betaID, _ := out["agent_id"].(string)
			if a := beta.Agents[betaID]; !a.InTeam || a.Rule.ChatShare != crewChatShareAgent || a.Rule.CanMakeChanges {
				t.Fatal("off-Team join lost the future sharing defaults")
			}
			crewProject = "alpha"
			if status, out := request(token, attached); status != 409 {
				t.Fatalf("one native agent joined a second Crew: %d %v", status, out)
			}
			beta.On = true
			beta.People = []crewTeamPerson{{UserID: "other"}}
			if e = writeCrewTeamAgentFixture(beta); e != nil {
				t.Fatal(e)
			}
			ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "other", Username: "other"})
			reachable := crewTeamReachableIn(ctx, beta, "other")
			found := false
			for _, a := range reachable {
				found = found || a.ID == betaID
			}
			if !found {
				t.Fatalf("turning Team on required a terminal rejoin: %+v", reachable)
			}
			crewProject = "beta"
			if status, out := request(token, attached, "leave"); status != 200 {
				t.Fatalf("leave server Crew failed: %d %v", status, out)
			}
			mail(sender, map[string]any{"action": "send", "target": "agent:owner/terminal", "message": "Discovery restored after leave", "submission_id": "left-discovery"})
			mail(sender, map[string]any{"action": "read", "inbox_id": publicInbox})

		})
	}

}
