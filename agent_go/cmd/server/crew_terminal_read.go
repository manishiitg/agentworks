package server

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// The authenticated Mac connection supplies the server, never a browser path or
// session selector. A closed process may retain its journal, but a detached or
// rebound lease cannot select a replacement session's private file.
func (api *StreamingAPI) desktopNativeTerminalRead(ctx context.Context, connection desktopConnection, call crewChatCall) crewChatResult {
	var in struct{ Provider, Session, Attachment, Agent string }
	fail := func() crewChatResult {
		return crewChatResult{ID: call.ID, Status: 403, Body: json.RawMessage(`{"error":"This exact terminal session is unavailable on this Mac."}`)}
	}
	if !IsLocalMode() || connection.User.ID == "" || connection.Server == "" || json.Unmarshal(call.Args, &in) != nil || (in.Provider != "claude" && in.Provider != "codex") || in.Session == "" || in.Attachment == "" || in.Agent == "" {
		return fail()
	}
	proof := func() (agentworksclient.LocalAgentState, bool) {
		root, err := agentworksclient.DefaultLocalBridgeRoot()
		if err != nil {
			return agentworksclient.LocalAgentState{}, false
		}
		action := "attachments"
		if call.Operation == "native_terminal_status" {
			action = "list"
		}
		reply, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: action})
		if err != nil {
			return agentworksclient.LocalAgentState{}, false
		}
		for _, s := range reply.AgentStates {
			if !s.ServerClosed && strings.TrimRight(s.Server, "/") == strings.TrimRight(connection.Server, "/") && s.Provider == in.Provider && s.SessionID == in.Session && s.ID == in.Attachment && s.AgentID == in.Agent {
				return s, true
			}
		}
		return agentworksclient.LocalAgentState{}, false
	}
	state, ok := proof()
	if !ok {
		return fail()
	}
	if call.Operation == "native_terminal_status" {
		raw, _ := json.Marshal(map[string]any{"online": state.Online && state.HostAlive, "terminal_closed": !state.HostAlive})
		return crewChatResult{ID: call.ID, Status: 200, Body: raw}
	}
	if call.Operation != "native_terminal_read" {
		return fail()
	}
	result := readCrewTerminalJournal(in.Provider, in.Session)
	if _, ok = proof(); !ok {
		return fail()
	}
	result.ID = call.ID
	return result
}

// Existing owner-authenticated desktop sockets, not a new sync or mailbox.
// Only the Mac holding this exact helper lease can answer. The native tuple is
// server-derived and the row is re-admitted by the caller after the read.
func (api *StreamingAPI) readCrewTerminalMac(ctx context.Context, project string, a crewTeamRegisteredAgent, call crewChatCall) crewChatResult {
	if IsLocalMode() {
		return api.desktopNativeTerminalRead(ctx, desktopConnection{Server: localTerminalServer(ctx, a), User: UserInfo{ID: a.OwnerID}}, call)
	}
	crewMacSockets.Lock()
	sockets := []*crewMacSocket{}
	for _, s := range crewMacSockets.byDevice {
		if s.owner == a.OwnerID {
			sockets = append(sockets, s)
		}
	}
	crewMacSockets.Unlock()
	if len(sockets) == 0 {
		return crewChatResult{Status: 503, Body: json.RawMessage(`{"error":"Keep AgentWorks running on the terminal's Mac to read this conversation."}`)}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	answers := make(chan crewChatResult, len(sockets))
	for _, s := range sockets {
		go func(s *crewMacSocket) {
			result, _, err := s.command(ctx, project, call)
			if err != nil {
				result.Status = 503
			}
			answers <- result
		}(s)
	}
	var found crewChatResult
	matches := 0
	for range sockets {
		select {
		case result := <-answers:
			if result.Status == 200 {
				found = result
				matches++
			}
		case <-ctx.Done():
			return crewChatResult{Status: 503, Body: json.RawMessage(`{"error":"The terminal's Mac did not answer in time."}`)}
		}
	}
	if matches != 1 {
		return crewChatResult{Status: 409, Body: json.RawMessage(`{"error":"This exact terminal conversation is unavailable or connected on more than one Mac."}`)}
	}
	return found
}

func localTerminalServer(ctx context.Context, a crewTeamRegisteredAgent) string {
	root, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		return ""
	}
	reply, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "attachments"})
	if err != nil {
		return ""
	}
	for _, s := range reply.AgentStates {
		if a.Terminal != nil && s.AgentID == a.NamedAgentID && s.ID == a.Terminal.AttachmentID && s.SessionID == a.ChatKey && s.Provider == a.Terminal.Provider {
			return s.Server
		}
	}
	return ""
}
