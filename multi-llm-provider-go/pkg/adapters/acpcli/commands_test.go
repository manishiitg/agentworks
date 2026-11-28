package acpcli

import (
	"encoding/json"
	"testing"
)

// The agent's slash commands are kept from available_commands_update for its current session only (agy sends them
// between turns too); before any arrive, whether it offers a command is unknown.
func TestAgentOffersCommandReadsTheAdvertisedCommands(t *testing.T) {
	owner := "chat-commands"
	s := &session{agent: Agent{ID: "agy"}, sessionID: "sess_1"}
	sessions.Lock()
	sessions.m[owner] = s
	sessions.Unlock()
	t.Cleanup(func() {
		sessions.Lock()
		delete(sessions.m, owner)
		sessions.Unlock()
	})
	if _, known := AgentOffersCommand(owner, "agy", "goal"); known {
		t.Fatal("commands known before the agent sent any")
	}
	update := func(session string, names ...string) {
		var commands []map[string]string
		for _, name := range names {
			commands = append(commands, map[string]string{"name": name, "description": "d"})
		}
		raw, _ := json.Marshal(map[string]any{"sessionId": session, "update": map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": commands}})
		s.noteCommands(raw)
	}
	update("other_session", "goal")
	if _, known := AgentOffersCommand(owner, "agy", "goal"); known {
		t.Fatal("another session's commands were kept")
	}
	update("sess_1", "compact", "plan")
	if offers, known := AgentOffersCommand(owner, "agy", "goal"); offers || !known {
		t.Fatalf("offers=%t known=%t, want known without goal", offers, known)
	}
	update("sess_1", "compact", "/goal")
	if offers, _ := AgentOffersCommand(owner, "agy", "goal"); !offers {
		t.Fatal("goal not offered after the agent advertised it")
	}
	if _, known := AgentOffersCommand(owner, "kiro", "goal"); known {
		t.Fatal("another agent's session answered")
	}
}
