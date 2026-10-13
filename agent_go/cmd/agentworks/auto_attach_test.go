package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// A new chat is reconnected on its own only when exactly one earlier agent of the
// folder exists; with several it only hints, and the folder switch and the kill
// switch stop everything. (The one-agent path attaches through the bridge and is
// covered by the live check.)
func TestAutoAttachNeverGuessesBetweenAgents(t *testing.T) {
	root := t.TempDir()
	folder := t.TempDir()
	req := agentworksclient.LocalBridgeRequest{Provider: "claude", SessionID: "s", HostKey: "h"}
	a := agentworksclient.AgentAttachment{AgentName: "one"}
	rememberAutoAttach(root, "claude", folder, a, "/login.json")
	rememberAutoAttach(root, "claude", folder, agentworksclient.AgentAttachment{AgentName: "two"}, "/login.json")
	if line := autoAttachOnStart(context.Background(), root, &options{}, req, folder); !strings.Contains(line, "/agentworks attach") || strings.Contains(line, "reconnected") {
		t.Fatalf("several earlier agents must only produce the attach hint: %q", line)
	}
	setAutoAttach(root, "claude", folder, false)
	if line := autoAttachOnStart(context.Background(), root, &options{}, req, folder); line != "" {
		t.Fatalf("auto off must be silent: %q", line)
	}
	setAutoAttach(root, "claude", folder, true)
	forgetAutoAttach(root, "claude", folder, "")
	if line := autoAttachOnStart(context.Background(), root, &options{}, req, folder); line != "" {
		t.Fatalf("a detached folder has nothing to reconnect: %q", line)
	}
	rememberAutoAttach(root, "claude", folder, a, "/login.json")
	t.Setenv("AGENTWORKS_AUTO_ATTACH", "0")
	if line := autoAttachOnStart(context.Background(), root, &options{}, req, folder); line != "" {
		t.Fatalf("the kill switch must disable it: %q", line)
	}
}

// The name of an earlier agent is taken only when nothing can still hold it: not an
// online chat, not an offline chat whose process runs, not another computer.
func TestAutoAttachDecisionNeverStealsALiveName(t *testing.T) {
	req := agentworksclient.LocalBridgeRequest{Provider: "claude", SessionID: "new"}
	state := func(online, hostAlive, closed bool) []agentworksclient.LocalAgentState {
		return []agentworksclient.LocalAgentState{{AgentAttachment: agentworksclient.AgentAttachment{AgentName: "one", Provider: "claude", SessionID: "old"}, Online: online, HostAlive: hostAlive, ServerClosed: closed}}
	}
	for name, c := range map[string]struct {
		states []agentworksclient.LocalAgentState
		server bool
		want   string
	}{
		"previous chat gone":               {state(false, false, false), false, "attach"},
		"previous chat online":             {state(true, true, false), false, "hint"},
		"offline but its process runs":     {state(false, true, false), false, "hint"},
		"held on another computer":         {state(false, false, false), true, "hint"},
		"closed by the server":             {state(false, false, true), false, "forget"},
		"nothing known locally, free name": {nil, false, "attach"},
	} {
		if got := autoAttachDecision(c.states, "one", req, c.server); got != c.want {
			t.Fatalf("%s: got %s, want %s", name, got, c.want)
		}
	}
}

// A bare attach typed by the person comes back as the folder's most recent earlier
// agent that nothing holds. A name a live chat holds, or the server shows online,
// is never taken, and a stale one is skipped.
func TestBareAttachTakesMostRecentFreeAgent(t *testing.T) {
	dir, err := os.MkdirTemp("", "aw")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ln, err := net.Listen("unix", filepath.Join(dir, "bridge.sock"))
	if err != nil {
		t.Skip("no unix sockets:", err)
	}
	if err = os.Chmod(filepath.Join(dir, "bridge.sock"), 0600); err != nil {
		t.Fatal(err)
	}
	serverOnline := map[string]bool{"c": true}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in agentworksclient.LocalBridgeRequest
		_ = json.NewDecoder(r.Body).Decode(&in)
		reply := agentworksclient.LocalBridgeReply{}
		switch in.Action {
		case "list":
			held := agentworksclient.LocalAgentState{AgentAttachment: agentworksclient.AgentAttachment{AgentName: "b", Provider: "claude", SessionID: "other"}, Online: true}
			reply.AgentStates = []agentworksclient.LocalAgentState{held}
		case "agent_online":
			reply.Result, _ = json.Marshal(map[string]bool{"online": serverOnline[in.AgentName]})
		}
		_ = json.NewEncoder(w).Encode(reply)
	})}
	go srv.Serve(ln)
	defer srv.Close()

	folder := t.TempDir()
	login := filepath.Join(dir, "login.json")
	if err = os.WriteFile(login, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} { // c is the most recent
		rememberAutoAttach(dir, "claude", folder, agentworksclient.AgentAttachment{AgentName: name}, login)
	}
	req := agentworksclient.LocalBridgeRequest{Provider: "claude", SessionID: "new", HostKey: "h"}
	free := freeAutoAttachRecords(context.Background(), dir, req, folder)
	if len(free) != 1 || free[0].Name != "a" {
		t.Fatalf("c is online on the server and b is held by a live chat; only a is free: %+v", free)
	}
	if line := autoAttachOnStart(context.Background(), dir, &options{}, req, folder); !strings.Contains(line, "agent:a (most recent)") {
		t.Fatalf("the hint must name the agent a bare attach would take: %q", line)
	}
	serverOnline["c"] = false
	if free = freeAutoAttachRecords(context.Background(), dir, req, folder); len(free) != 2 || free[0].Name != "c" {
		t.Fatalf("with c free again the most recent free agent comes first: %+v", free)
	}
}
