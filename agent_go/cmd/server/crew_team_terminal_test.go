package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
			logs := captureLogs(t)
			env := newTriggerLinkEnv(t)
			env.api.agentProfiles = env.svc.registry
			t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
			t.Setenv("AGENT_PRODUCTS", "")
			t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks,work,bullpen")
			t.Setenv("AUTH_SECRET", "terminal-join-fixture")
			oauth, err := openMCPOAuthStore()
			if err != nil {
				t.Fatal(err)
			}
			defer oauth.Close()
			mux := http.NewServeMux()
			mux.HandleFunc("/api/external/v1/call", env.api.handleExternalCall)
			mux.HandleFunc("/api/external/v1/agents/attachment", env.api.handleAgentAttachment)
			mux.HandleFunc("/api/external/v1/agents/events", env.api.handleAgentEvents)
			mux.HandleFunc("/api/external/v1/agents/sharing", env.api.handleAgentSharing)
			copyRouter := gorillamux.NewRouter()
			copyRouter.HandleFunc("/api/crew-teams/{project}/agents/{agent}/offline-copy/{op}", env.api.handleCrewOfflineCopy)
			mux.Handle("/api/crew-teams/", copyRouter)
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
			owner, token, attached := attach(parent, "terminal", "11111111-2222-4333-8444-555555555555")
			sender, memberToken, invited := attach(member, "member-terminal", "native-member")
			team := crewTeam{ProjectID: "alpha", OwnerID: "owner", Name: "Alpha", On: true, People: []crewTeamPerson{{UserID: "other", Invited: true}}}
			if err = writeCrewTeamAgentFixture(team); err != nil {
				t.Fatal(err)
			}
			if err = crewroot.WriteSync("alpha", crewroot.SyncBinding{Mode: "relay", Side: "server", Editing: "local", PairID: "owner-pair", PeerProjectID: "local-alpha", MacName: "Example laptop"}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			var metadataRefreshes atomic.Int64
			copyAvailable := true
			sock := &crewMacSocket{owner: "owner", device: "terminal-join-test", twins: map[string]string{"alpha": "owner-pair"}, lastSeen: time.Now(), pending: map[string]chan crewChatResult{}, dispatched: map[string]chan struct{}{}}
			sock.send = func(m crewRelayMessage) error {
				if m.Call == nil {
					return nil
				}
				if m.Call.Operation == "native_terminal_status" {
					sock.mu.Lock()
					sock.pending[m.Call.ID] <- crewChatResult{ID: m.Call.ID, Status: 200, Body: []byte(`{"online":false,"terminal_closed":true}`)}
					sock.mu.Unlock()
					return nil
				}
				if m.Call.Operation != "team_terminal_copy" && m.Call.Operation != "native_terminal_read" {
					metadataRefreshes.Add(1)
					// Central reach may refresh Mac tab metadata; that is not a native
					// journal read and must not affect the privacy-before-I/O counter.
					sock.mu.Lock()
					sock.pending[m.Call.ID] <- crewChatResult{ID: m.Call.ID, Status: 200, Body: []byte(`{"tabs":[]}`)}
					sock.mu.Unlock()
					return nil
				}
				calls++
				var identity map[string]string
				if (m.Call.Operation != "team_terminal_copy" && m.Call.Operation != "native_terminal_read") || json.Unmarshal(m.Call.Args, &identity) != nil || identity["session"] != attached.SessionID || identity["attachment"] != attached.ID || identity["agent"] != attached.AgentID || identity["provider"] != provider || identity["path"] != "" {
					return fmt.Errorf("caller changed the Mac's identity or path selector")
				}
				if m.Call.Operation == "team_terminal_copy" && !copyAvailable {
					sock.mu.Lock()
					sock.pending[m.Call.ID] <- crewChatResult{ID: m.Call.ID, Status: 403, Body: []byte(`{"error":"not here"}`)}
					sock.mu.Unlock()
					return nil
				}
				body := []byte(`{"path":"/accepted/shared-copy"}`)
				if m.Call.Operation == "native_terminal_read" {
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
				if action == "status" {
					operation = "terminal_status"
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
			if status, out := request(token, attached, "status"); status != 200 || out["attached"] != true || out["crew_name"] != nil || calls != 0 {
				t.Fatalf("connected-only status leaked a Crew or touched native data: %d %v", status, out)
			}
			if status, _ := request(wrongConnection, attached, "status"); status != 403 || calls != 0 {
				t.Fatal("another connection read attached-session status")
			}
			if status, _ := request(wrongConnection, attached); status != 403 || calls != 0 {
				t.Fatalf("another connection reached the Mac: %d calls=%d", status, calls)
			}
			if status, _ := request(memberToken, invited); status != 403 || calls != 0 {
				t.Fatalf("an invitation was treated as membership: %d calls=%d", status, calls)
			}
			copyAvailable = false
			if status, out := request(token, attached); status != 409 || !strings.Contains(fmt.Sprint(out), "Alpha lives on Example laptop") || !strings.Contains(fmt.Sprint(out), "join as a Team member") {
				t.Fatalf("wrong-Mac guidance: %d %v", status, out)
			}
			copyAvailable = true
			calls = 0
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

			waitNativeHome := func(project, source string) crewTeam {
				t.Helper()
				deadline := time.Now().Add(5 * time.Second)
				for {
					team, err := readCrewTeam(project)
					_, busy := crewOfflineNativePreparing.Load(project + "/" + source)
					if err == nil && team.OnlineHome != "" && !busy {
						return team
					}
					if time.Now().After(deadline) {
						t.Fatal("native join did not prepare its online home", err)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			saved = waitNativeHome("alpha", id)
			checkCrewOfflineCopyHTTP(t, srv.URL, token, wrongConnection, attached, saved, row)
			// Real child credential -> exact source admission -> plan/chunk/commit:
			// this is the same client invoked asynchronously by turn-end hooks.
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
			journalDir := filepath.Join(home, ".claude", "projects", "one")
			journalName := attached.SessionID + ".jsonl"
			if provider == "codex" {
				journalDir = filepath.Join(home, ".codex", "sessions", "2026", "11", "15")
				journalName = "rollout-now-" + attached.SessionID + ".jsonl"
			}
			if err = os.MkdirAll(journalDir, 0700); err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(journalDir, journalName)
			firstLine := "{\"type\":\"user\",\"text\":\"private source\"}\n"
			if err = os.WriteFile(journal, []byte(firstLine), 0600); err != nil {
				t.Fatal(err)
			}
			cursor, copyErr := owner.SyncNativeCopy(t.Context(), attached, agentworksclient.NativeCopyCursor{})
			if copyErr != nil || cursor.Offset != int64(len(firstLine)) {
				t.Fatalf("actual native ingest failed: %+v %v", cursor, copyErr)
			}
			again, copyErr := owner.SyncNativeCopy(t.Context(), attached, cursor)
			if copyErr != nil || again != cursor {
				t.Fatalf("unchanged native journal re-uploaded: %+v %v", again, copyErr)
			}
			secondLine := "{\"type\":\"assistant\"}\n"
			if err = os.WriteFile(journal, []byte(firstLine+secondLine), 0600); err != nil {
				t.Fatal(err)
			}
			again, copyErr = owner.SyncNativeCopy(t.Context(), attached, cursor)
			if copyErr != nil || again.Offset != int64(len(firstLine+secondLine)) || again.Revision != cursor.Revision+1 {
				t.Fatalf("appended native delta failed: %+v %v", again, copyErr)
			}
			if _, copyErr = sender.SyncNativeCopy(t.Context(), attached, cursor); copyErr == nil {
				t.Fatal("wrong attachment credential uploaded source history")
			}

			// PLAT-746-1030: real attachment label requests drive the shared
			// activity projection, never a private native journal or browser guess.
			streamCtx, stopStream := context.WithCancel(t.Context())
			connected, streamDone := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(streamDone)
				_ = owner.StreamAgentMessages(streamCtx, attached, func(agentworksclient.AgentLiveMessage) error { return nil }, func() { close(connected) })
			}()
			defer func() { stopStream(); <-streamDone }()
			select {
			case <-connected:
			case <-time.After(3 * time.Second):
				t.Fatal("exact native stream did not connect")
			}
			ownList := func(user string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, "/terminal-agents", nil)
				req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
				req = gorillamux.SetURLVars(req, map[string]string{"project": "alpha"})
				out := httptest.NewRecorder()
				env.api.handleCrewOwnTerminalAgents(out, req)
				return out
			}
			checkActivity := func(want string) {
				t.Helper()
				// PLAT-746-1049: the real own-menu endpoint uses the same live
				// attachment, including expired busy hooks and Stop ordering.
				res := ownList("owner")
				var menu struct {
					Agents []crewOwnTerminalAgent `json:"agents"`
				}
				if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &menu) != nil || len(menu.Agents) != 1 {
					t.Fatalf("own menu = %d %s", res.Code, res.Body.String())
				}
				label := "Idle"
				if want == "running" {
					label = "Working"
				}
				if menu.Agents[0].Activity != label {
					t.Fatalf("own menu activity = %q, want %q", menu.Agents[0].Activity, label)
				}

				for _, person := range teamAgentsByPerson(t.Context(), saved, "owner") {
					for _, agent := range person.Agents {
						if agent.ID == id {
							if agent.Activity != want {
								t.Fatalf("native turn activity = %q, want %q", agent.Activity, want)
							}
							return
						}
					}
				}
				t.Fatal("registered terminal missing from shared projection")
			}
			reported := attached
			reported.Activity, reported.ActivityAt = "busy", time.Now().UTC().Add(-11*time.Minute)
			if err = owner.UpdateAgentLabel(t.Context(), reported, "Terminal"); err != nil {
				t.Fatal(err)
			}
			checkActivity("") // Esc/interrupt may lose Stop while the stream stays up
			reported.ActivityAt = time.Now().UTC()
			if err = sender.UpdateAgentLabel(t.Context(), reported, "Terminal"); err == nil {
				t.Fatal("another connection changed native turn activity")
			}
			checkActivity("")
			if err = owner.UpdateAgentLabel(t.Context(), reported, "Terminal"); err != nil {
				t.Fatal(err)
			}
			checkActivity("running")
			live, liveErr := currentCrewTeamTerminal(t.Context(), saved, row)
			if liveErr != nil {
				t.Fatal(liveErr)
			}
			if label, _ := crewTeamTerminalState(live, nil, true, "Owner", true); label != "Working" {
				t.Fatal("an outstanding message hid live work")
			}
			stale := reported
			reported.Activity, reported.ActivityAt = "idle", reported.ActivityAt.Add(time.Millisecond)
			if err = owner.UpdateAgentLabel(t.Context(), reported, "Terminal"); err != nil {
				t.Fatal(err)
			}
			checkActivity("")
			if err = owner.UpdateAgentLabel(t.Context(), stale, "Terminal"); err != nil {
				t.Fatal(err)
			}
			checkActivity("") // delayed busy cannot overwrite the newer Stop
			live, liveErr = currentCrewTeamTerminal(t.Context(), saved, row)
			if liveErr != nil {
				t.Fatal(liveErr)
			}
			for _, viewer := range []struct {
				you   bool
				label string
			}{{true, "Waiting for you"}, {false, "Waiting for Colleague"}} {
				if label, state := crewTeamTerminalState(live, nil, true, "Colleague", viewer.you); label != viewer.label || state != "waiting" {
					t.Fatalf("idle live attachment wait = %q/%q, want %q", label, state, viewer.label)
				}
			}
			if label, state := crewTeamTerminalState(live, fmt.Errorf("binding changed"), true, "Owner", true); label != "Offline" || state != "" {
				t.Fatal("a wait hid a revoked attachment")
			}
			stopStream()
			<-streamDone

			// Explicit Only me remains available and survives rejoin.
			row.InTeam = false
			saved.Agents[id] = row
			if e = writeCrewTeamAgentFixture(saved); e != nil {
				t.Fatal(e)
			}
			if status, again := request(token, attached); status != 200 || again["in_team"] != false {
				t.Fatalf("rejoin changed Only me: %d %v", status, again)
			}
			if status, out := request(token, attached, "status"); status != 200 || out["crew_name"] != "Alpha" || out["in_team"] != false || out["online"] != nil || out["unread"] != nil {
				t.Fatalf("Not shared status widened visibility or ignored the current row: %d %v", status, out)
			}
			if status, out = request(token, attached, "work_brief"); status != 200 || out["hint"] == nil {
				t.Fatalf("Only-me agent lost its own task brief through a messaging child: %d %v", status, out)
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
			liveRead := func(users ...string) *httptest.ResponseRecorder {
				user := "other"
				if len(users) != 0 {
					user = users[0]
				}
				req := httptest.NewRequest(http.MethodGet, "/live", nil)
				req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
				req = gorillamux.SetURLVars(req, map[string]string{"project": "alpha", "agent": id})
				out := httptest.NewRecorder()
				env.api.handleCrewTeamTerminalLive(out, req)
				return out
			}

			before := calls
			if res := ownList("owner"); res.Code != 200 || !strings.Contains(res.Body.String(), `"activity":"Offline"`) || !strings.Contains(res.Body.String(), "Terminal closed") || strings.Contains(res.Body.String(), "Live-only fixture") || calls != before {
				t.Fatal("own native menu read a transcript instead of metadata", res.Code, res.Body.String())
			}
			privateCtx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Scope: "report-preview"})
			if _, err := crewTeamTerminalLiveRow(privateCtx, saved, "owner", id); err == nil {
				t.Fatal("a scoped caller borrowed the person's private transcript access")
			}
			if res := liveRead(); res.Code != 403 || calls != before {
				t.Fatal("Answers only reached the native journal", res.Code)
			}
			if res := liveRead("owner"); res.Code != 200 || !strings.Contains(res.Body.String(), "Live-only fixture") {
				t.Fatal("owner could not read their own Answers-only terminal", res.Code, res.Body.String())
			}
			if output := logs.String(); !strings.Contains(output, "[NATIVE_CONVERSATION] project=alpha agent="+id+" action=loaded reason=ok status=200 ms=") || strings.Contains(output, "Live-only fixture") {
				t.Fatal("native load lacks IDs/timing receipt or logs transcript text")
			}
			// Native conversations no longer have a direct composer: the person
			// replies in the terminal. The authenticated Bridge exchange below
			// covers current inbox delivery and the original-thread reply.
			before = calls
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
			if res := ownList("other"); res.Code != 200 || strings.Contains(res.Body.String(), id) {
				t.Fatal("teammate's native session leaked into the personal menu", res.Code, res.Body.String())
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
			receipt := mail(sender, map[string]any{"action": "send", "target": "agent:owner/terminal", "message": "Team fixture question", "submission_id": "team-question"})
			inbox, _ := receipt["inbox_id"].(string)
			if inbox == "" {
				t.Fatalf("normal bridge returned no inbox: %v", receipt)
			}
			// #2881: native mail admission must never discover unrelated app tabs
			// (those Mac RPCs can consume the helper's entire request deadline).
			crewMacTeamTabsCache.Delete("alpha\x00owner")
			if path, err := crewMacTabsPath("alpha"); err == nil {
				_ = os.Remove(path)
			}
			beforeRefreshes := metadataRefreshes.Load()
			if status, pending := request(token, attached, "inbox"); status != 200 || !strings.Contains(fmt.Sprint(pending["messages"]), "for that, say") {
				t.Fatalf("native hook inbox lost admitted Team mail/instructions: %d %v", status, pending)
			}
			if metadataRefreshes.Load() != beforeRefreshes {
				t.Fatal("native inbox fetched unrelated Mac metadata")
			}
			canceled, cancelInbox := context.WithCancel(t.Context())
			cancelInbox()
			snapshot, err := readAgentMessageSnapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, conversation := range snapshot.Conversations {
				if conversation.ID != inbox {
					continue
				}
				if err := snapshot.shareSenderAdmission(canceled, conversation, 0, "owner", time.Now()); !errors.Is(err, context.Canceled) {
					t.Fatal("canceled sender check became revocation", err)
				}
			}

			incoming := mail(owner, map[string]any{"action": "read", "inbox_id": inbox})
			if messages, _ := incoming["messages"].([]any); len(messages) != 1 {
				t.Fatalf("Team mail not readable by the exact attached receiver: %v", incoming)
			}
			if messages, _ := incoming["messages"].([]any); len(messages) == 1 {
				message, _ := messages[0].(map[string]any)
				instructions, _ := message["team_instructions"].(string)
				if !strings.Contains(instructions, "for that, say") || message["message"] != "Team fixture question" {
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
			// Agents are no longer put online by hand; the removed action is refused.
			if status, _ := request(token, attached, "put_online"); status != 400 {
				t.Fatal("removed put_online action was accepted", status)
			}
			// Seed the joined source's copy binding as native capture records it.
			copyDir, e := crewOfflineDirectory("alpha", id)
			if e != nil {
				t.Fatal(e)
			}
			seedRef, _ := json.Marshal(crewTeamOfflineCopyRef{Home: saved.OnlineHome, Binding: crewOfflineBinding{Provider: provider, Session: attached.SessionID, Attachment: attached.ID}})
			if e = os.WriteFile(filepath.Join(copyDir, "copy.json"), seedRef, 0600); e != nil {
				t.Fatal(e)
			}
			oldLease := attached
			privateBinding := crewOfflineBinding{Provider: provider, Session: attached.SessionID, Attachment: attached.ID}
			privateHistory := crewOfflineHistory{crewOfflineBinding: privateBinding, crewOfflineCursor: crewOfflineCursor{Revision: 7, Offset: 512, TailHash: crewOfflineEmptyHash()}, SourceSize: 1024}
			historyRaw, _ := json.Marshal(privateHistory)
			if e = os.WriteFile(filepath.Join(copyDir, "history.json"), historyRaw, 0600); e != nil {
				t.Fatal(e)
			}
			copyStatus := func(a agentworksclient.AgentAttachment) (int, map[string]any) {
				payload, _ := json.Marshal(map[string]any{"action": "offline_copy_status", "agent_name": a.AgentName, "attachment_id": a.ID, "provider": a.Provider, "session": a.SessionID})
				req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/external/v1/agents/attachment", bytes.NewReader(payload))
				req.Header.Set("Authorization", "Bearer "+token)
				response, e := http.DefaultClient.Do(req)
				if e != nil {
					t.Fatal(e)
				}
				defer response.Body.Close()
				var out map[string]any
				_ = json.NewDecoder(response.Body).Decode(&out)
				return response.StatusCode, out
			}
			// Live regression (owner, 16 Nov): a CLI update reattached the same native session with a new attachment
			// ID and every Team message was refused binding_changed. The row follows the same session automatically.
			reattached, e := owner.AttachAgent(t.Context(), agentworksclient.AgentAttachmentOptions{AgentName: "terminal", Provider: provider, SessionID: attached.SessionID})
			if e != nil {
				t.Fatal(e)
			}
			if reattached.AgentID != attached.AgentID || reattached.SessionID != attached.SessionID || reattached.ID == attached.ID {
				t.Fatal("fixture did not reattach the same named agent and session with a new attachment")
			}
			if _, e = currentCrewTeamTerminal(t.Context(), saved, row); e != nil {
				t.Fatal("same-session reattach cut Team reach", e)
			}
			for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
				if saved, e = readCrewTeam("alpha"); e == nil && saved.Agents[id].Terminal.AttachmentID == reattached.ID {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("Team row did not follow the reattachment")
				}
			}
			if code, out := copyStatus(reattached); code != 200 || out["agent"] != id || out["revision"] != float64(7) || out["offset"] != float64(512) {
				t.Fatalf("reattach lost current source cursor: %d %v", code, out)
			}
			if code, _ := copyStatus(oldLease); code != 403 {
				t.Fatal("obsolete lease retained copy access", code)
			}
			saved, _ = readCrewTeam("alpha")
			row = saved.Agents[id]
			attached = reattached
			replacement, e := owner.AttachAgent(t.Context(), agentworksclient.AgentAttachmentOptions{AgentName: "terminal", Provider: provider, SessionID: "22222222-3333-4444-8555-666666666666"})
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
			registry, _ := crewTeamDir("beta")
			info, err := os.Lstat(registry)
			if err != nil || agentworksclient.CheckPrivateAccess(registry, info) != nil {
				t.Fatal("new Off registry was not created private; later Team saves would refuse it", err)
			}
			betaID, _ := out["agent_id"].(string)
			beta = waitNativeHome("beta", betaID)
			privateRow := beta.Agents[betaID]
			privateRow.InTeam = false
			wanted, wantedErr := crewOfflineAppWanted(beta, privateRow)
			if wantedErr != nil || !wanted {
				t.Fatal("private joined terminal required Put online", wantedErr)
			}
			if _, bound := crewOfflineRowBinding(beta, privateRow); !bound {
				t.Fatal("private joined terminal lost its source binding")
			}
			if beta.On {
				t.Fatal("automatic native home turned Team on")
			}
			homeTeam, homeErr := readCrewTeam(beta.OnlineHome)
			if homeErr != nil || homeTeam.member("other") {
				t.Fatal("solo native copy granted a teammate home access", homeErr)
			}

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

// PLAT-746-1030: an owner may read a closed native process's journal, but
// neither a different server nor a rebound lease may select that private file.
func TestCrewTerminalReadBindsServerLeaseAndClosedSession(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "aw-native-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("HOME", root)
	const session = "11111111-2222-4333-8444-555555555555"
	bridge, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(bridge, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(bridge, "bridge.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(bridge, "bridge.sock"), 0600); err != nil {
		t.Fatal(err)
	}
	expected := agentworksclient.LocalAgentState{Server: "https://example.com", AgentAttachment: agentworksclient.AgentAttachment{ID: "lease", AgentID: "native-agent", Provider: "claude", SessionID: session}}
	var requests atomic.Int32
	var rebound atomic.Bool
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		state := expected
		if rebound.Load() && n%2 == 0 {
			state.ID = "replacement-lease"
		}
		_ = json.NewEncoder(w).Encode(agentworksclient.LocalBridgeReply{AgentStates: []agentworksclient.LocalAgentState{state}})
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	project := filepath.Join(root, ".claude", "projects", "fixture")
	if err = os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	journal := `{"type":"assistant","timestamp":"2026-11-15T00:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Private native sentinel"}]}}` + "\n"
	if err = os.WriteFile(filepath.Join(project, session+".jsonl"), []byte(journal), 0600); err != nil {
		t.Fatal(err)
	}
	connection := desktopConnection{Server: expected.Server, User: UserInfo{ID: "owner"}}
	payload := func(lease string) json.RawMessage {
		raw, _ := json.Marshal(map[string]string{"provider": "claude", "session": session, "attachment": lease, "agent": "native-agent"})
		return raw
	}
	api := &StreamingAPI{}
	call := crewChatCall{Operation: "native_terminal_read", Args: payload("lease")}
	if got := api.desktopNativeTerminalRead(t.Context(), connection, call); got.Status != 200 || !strings.Contains(string(got.Body), "Private native sentinel") {
		t.Fatalf("closed process journal unavailable: %d %s", got.Status, got.Body)
	}
	other := connection
	other.Server = "https://other.example.com"
	if got := api.desktopNativeTerminalRead(t.Context(), other, call); got.Status != 403 {
		t.Fatal("other server selected a private native journal")
	}
	call.Args = payload("replacement-lease")
	if got := api.desktopNativeTerminalRead(t.Context(), connection, call); got.Status != 403 {
		t.Fatal("wrong lease selected a private native journal")
	}
	call.Args = payload("lease")
	requests.Store(0)
	rebound.Store(true)
	if got := api.desktopNativeTerminalRead(t.Context(), connection, call); got.Status != 403 || strings.Contains(string(got.Body), "Private native sentinel") {
		t.Fatal("lease rebound during read released private native text")
	}
}

// Owner, 16 Nov: Answers only is one short line, said once per thread and again only when the setting or the owner's
// rule changes; Make changes keeps only the personal-accounts line.
func TestWhenOthersAskNoteIsOneLineSaidOncePerThread(t *testing.T) {
	answers := whenOthersAskNote("Asha", "Ravi", "", false)
	if strings.Count(answers, "\n") != 0 || !strings.Contains(answers, "Asha is asking, not Ravi.") || !strings.Contains(answers, "for that, say Ravi can allow it.") {
		t.Fatalf("answers-only note = %q", answers)
	}
	changes := whenOthersAskNote("Asha", "Ravi", "Never share the budget.", true)
	if strings.Contains(changes, "for that, say") || !strings.Contains(changes, "don't use Ravi's personal accounts") || !strings.HasSuffix(changes, "\nNever share the budget.") {
		t.Fatalf("make-changes note = %q", changes)
	}
	const thread = "session-1\x00agent-x\x00asha"
	delete(whenOthersAskSent.last, thread)
	for i, want := range []string{"first", "skipped", "changed", "skipped"} {
		note := answers
		if i >= 2 {
			note = changes
		}
		if got := whenOthersAskSentState(thread, note); got != want {
			t.Fatalf("turn %d sent=%s, want %s", i, got, want)
		}
	}
	// A new session of the same agent and asker (fresh chat, new thread) gets the line again.
	if got := whenOthersAskSentState("session-2\x00agent-x\x00asha", changes); got != "first" {
		t.Fatalf("new session sent=%s, want first", got)
	}
	forgetWhenOthersAskSent("session-1")
	if got := whenOthersAskSentState(thread, changes); got != "first" {
		t.Fatalf("after the CLI was closed sent=%s, want first", got)
	}
}

// Owner, 16 Nov: the agent's own chat is told, in the agent header's own summary words, when its Team settings
// change; the line is read once with its next turn.
func TestAgentTeamSettingsSentenceAndNextTurnNotice(t *testing.T) {
	team := func(rule crewTeamAgentRule, inTeam bool) crewTeam {
		return crewTeam{On: true, Agents: map[string]crewTeamRegisteredAgent{"agent-a": {ID: "agent-a", OwnerID: "owner", ChatKey: "chat-a", InTeam: inTeam, Rule: rule}}}
	}
	for _, tc := range []struct {
		name string
		t    crewTeam
		want []string
	}{
		{"whole team answers only live", team(crewTeamAgentRule{}, true), []string{"Your owner changed your Team settings", "Anyone on your team can ask Ada", "won't change anything", "watch your chat live"}},
		{"make changes hidden with note", team(crewTeamAgentRule{CanMakeChanges: true, ChatShare: crewChatShareAgent, WhenOthersAsk: "No budget talk."}, true), []string{"It can do the work for them", "can't see your chat", "No budget talk."}},
		{"only me", team(crewTeamAgentRule{}, false), []string{"Only you and your agents can reach Ada"}},
	} {
		sentence := crewTeamAgentSettingsSentence(tc.t, "owner", "agent-a", "Ada")
		for _, want := range tc.want {
			if !strings.Contains(sentence, want) {
				t.Fatalf("%s: %q lacks %q", tc.name, sentence, want)
			}
		}
	}
	crewTeamNotices.mu.Lock()
	crewTeamNotices.waiting["session-n"] = "line"
	crewTeamNotices.mu.Unlock()
	if takeCrewTeamAgentNotice("session-n") != "line" || takeCrewTeamAgentNotice("session-n") != "" {
		t.Fatal("the notice must be read exactly once")
	}
}
