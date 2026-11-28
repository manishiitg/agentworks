package server

import (
	"context"
	"log"
	"strings"
	"time"
)

// desktopRelayBusy tells the server which of this Mac's Crew chats are working or waiting for their person.
// The runtime coordinator is the one live table of turn states; the relay learns which sessions changed from
// it and sends only those (#3187), instead of asking every chat for its status every few seconds. A minute
// heartbeat resends the busy chats, so the server keeps a long turn alive and notices a Mac that went away.
// Runs on the relay's own loop goroutine only.
type desktopRelayBusy struct {
	api  *StreamingAPI
	send func(crewRelayMessage) error
	// sessions maps a session to its Crew chat; read from the Crew registries on connect, when the offered
	// Crews change, when an unknown session changes and once a minute. checked holds unknown sessions
	// already looked up since the last read, so another Crew's or a workflow's chat triggers one read only.
	sessions map[string]desktopRelayChat
	checked  map[string]bool
	told     map[string]string // project/conversation -> running or waiting, as last told to the server
	crew     map[string]string // project -> working, waiting or idle, as last told (the phone's Crew list)
	count    int
}

type desktopRelayChat struct {
	project, local, pair, conversation string
	main                               bool
}

func newDesktopRelayBusy(api *StreamingAPI, send func(crewRelayMessage) error) *desktopRelayBusy {
	return &desktopRelayBusy{api: api, send: send, sessions: map[string]desktopRelayChat{}, checked: map[string]bool{}, told: map[string]string{}, crew: map[string]string{}}
}

// refresh rereads the session-to-chat map for the offered Crews (project -> pair).
func (b *desktopRelayBusy) refresh(ctx context.Context, pairs map[string]string, reason string) {
	sessions := map[string]desktopRelayChat{}
	crews := 0
	for project, pair := range pairs {
		local := localCrewForPeer(project, pair)
		if local == "" {
			continue
		}
		tabs, err := b.api.desktopCrewChatSessions(ctx, local, pair)
		if err != nil {
			continue
		}
		crews++
		for session, tab := range tabs {
			sessions[session] = desktopRelayChat{project: project, local: local, pair: pair, conversation: tab.Record.ConversationID, main: tab.IsDefault}
		}
	}
	b.sessions, b.checked = sessions, map[string]bool{}
	if reason != "heartbeat" || len(sessions) != b.count {
		log.Printf("[CREW_RELAY] busy chats read reason=%s crews=%d chats=%d", reason, crews, len(sessions))
	}
	b.count = len(sessions)
}

// changed handles sessions the coordinator reported; an unknown one rereads the map once.
func (b *desktopRelayBusy) changed(ctx context.Context, ids []string, pairs func() map[string]string) {
	for _, id := range ids {
		if _, known := b.sessions[id]; !known && !b.checked[id] {
			b.refresh(ctx, pairs(), "new_chat")
			break
		}
	}
	for _, id := range ids {
		if _, known := b.sessions[id]; !known {
			b.checked[id] = true
			continue
		}
		b.report(ctx, id, false)
	}
}

// state is a session's turn from the coordinator; a session with no live record has no turn.
func (b *desktopRelayBusy) state(id string) string {
	if b.api.runtimeCoordinator == nil {
		return runtimeTurnState(b.api.collectRuntimeSnapshot(id).Phase)
	}
	snapshot, ok := b.api.runtimeCoordinator.Snapshot(id)
	if !ok {
		return ""
	}
	return runtimeTurnState(snapshot.Phase)
}

// report sends one chat's change (or, on a heartbeat, its busy state again) and its Crew's list state.
func (b *desktopRelayBusy) report(ctx context.Context, id string, heartbeat bool) {
	chat := b.sessions[id]
	key := chat.project + "/" + chat.conversation
	state := b.state(id)
	switch {
	case state != "" && (b.told[key] != state || heartbeat):
		if b.told[key] != state {
			log.Printf("[CREW_RELAY] turn project=%s conversation=%s state=%s", chat.project, chat.conversation, state)
		}
		if b.send(crewRelayMessage{Type: "running", Project: chat.project, Conversation: chat.conversation, State: state}) == nil {
			b.told[key] = state
		}
	case state == "" && b.told[key] != "":
		log.Printf("[CREW_RELAY] turn project=%s conversation=%s state=idle", chat.project, chat.conversation)
		_ = b.send(crewRelayMessage{Type: "running", Project: chat.project, Conversation: chat.conversation, State: "idle"})
		delete(b.told, key)
	}
	if chat.main {
		b.reportCrew(ctx, chat, state)
	}
}

// reportCrew words the main chat's turn for the phone's Crew list, with its last line, when it changes.
func (b *desktopRelayBusy) reportCrew(ctx context.Context, chat desktopRelayChat, turn string) {
	state := map[string]string{"running": "working", "waiting": "waiting"}[turn]
	if state == "" {
		state = "idle"
	}
	if b.crew[chat.project] == state {
		return
	}
	b.crew[chat.project] = state
	_ = b.send(crewRelayMessage{Type: "state", Project: chat.project, State: state, LastLine: b.api.desktopCrewLastLine(ctx, chat.local, chat.pair), At: time.Now().UTC().Format(time.RFC3339)})
}

// reportAll sends every known chat's state: on connect, after the map changed and on the heartbeat. A chat
// told busy that is no longer offered is told idle.
func (b *desktopRelayBusy) reportAll(ctx context.Context, heartbeat bool) {
	known := map[string]bool{}
	mains := map[string]bool{}
	for id, chat := range b.sessions {
		known[chat.project+"/"+chat.conversation] = true
		if chat.main {
			mains[chat.project] = true
		}
		b.report(ctx, id, heartbeat)
	}
	for key := range b.told {
		if !known[key] {
			project, conversation, _ := strings.Cut(key, "/")
			log.Printf("[CREW_RELAY] turn project=%s conversation=%s state=idle", project, conversation)
			_ = b.send(crewRelayMessage{Type: "running", Project: project, Conversation: conversation, State: "idle"})
			delete(b.told, key)
		}
	}
	// A Crew whose Main chat has no session yet is idle on the phone's list.
	for _, chat := range b.sessions {
		if !mains[chat.project] {
			mains[chat.project] = true
			b.reportCrew(ctx, desktopRelayChat{project: chat.project, local: chat.local, pair: chat.pair}, "")
		}
	}
}

// sweep runs once a minute over this Mac's busy or still-"running" chats. A session marked running with no
// live work (its end was never reported) is completed and logged, so the missing end can be found and fixed;
// the status read used to do this on every poll. Live phases are read again, so a phase that ends by time
// alone (a start that never registered work, a background grace) is not left showing busy.
func (b *desktopRelayBusy) sweep() {
	for id := range b.sessions {
		active, ok := b.api.getActiveSession(id)
		if !ok || active == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(active.Status), "running") {
			background := b.api.bgAgentRegistry != nil && b.api.bgAgentRegistry.HasRunningAgents(id)
			if b.api.shouldCompleteIdleForegroundSession(id, active.Status, background) {
				log.Printf("[CREW_RELAY] stuck_turn session=%s reason=running_without_live_work action=completed", id)
				b.api.setSessionBusy(id, false)
				b.api.updateSessionStatus(id, "completed")
				continue
			}
		}
		if b.state(id) != "" {
			b.api.observeRuntimeSnapshot(id)
		}
	}
}

// prime reads the current turn of every known chat once, so a chat that has not changed since the server
// started still has a coordinator record. Only chats with a live session are read.
func (b *desktopRelayBusy) prime() {
	for id := range b.sessions {
		if active, ok := b.api.getActiveSession(id); ok && active != nil {
			b.api.observeRuntimeSnapshot(id)
		}
	}
}
