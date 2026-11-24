package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/mux"
)

// The chat backup is what a teammate sees in place of a Mac agent's chat while that Mac is offline (#3577): the
// copy its Mac synced to the server after each turn, shown with a line that says it is a backup and how old it is.
// It is only shown; nothing answers from it. The same rules as reading it from chat (crewTeamReadChat) apply: a Live
// chat only, and the agent's person is told on its next turn that a teammate opened it.

// crewTeamChatBackupLines and crewTeamChatBackupText bound one response: the newest lines, each clipped.
const (
	crewTeamChatBackupLines = 300
	crewTeamChatBackupText  = 4000
)

func (api *StreamingAPI) handleCrewTeamChatBackup(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, false)
	if !ok {
		return
	}
	viewer, id := claims.UserID, mux.Vars(r)["agent"]
	var row crewTeamReachableAgent
	found := false
	for _, candidate := range crewTeamReachableAgents(r.Context(), viewer) {
		if candidate.ID == id && candidate.Team == t.ProjectID {
			row, found = candidate, true
			break
		}
	}
	a, registered := t.Agents[id]
	if !found || !registered || row.Kind != "agent" {
		log.Printf("[TEAM_CHAT_READ] project=%s agent=%s caller=%s event=refused reason=not_reachable view=backup", t.ProjectID, id, viewer)
		externalError(w, http.StatusForbidden, "not_reachable", "This agent is not shared with you in this Team.")
		return
	}
	if a.OwnerID != viewer && a.Rule.ChatShare != crewChatShareLive {
		log.Printf("[TEAM_CHAT_READ] project=%s agent=%s caller=%s event=refused reason=chat_private view=backup", t.ProjectID, a.ID, viewer)
		externalError(w, http.StatusForbidden, "chat_private", crewChatPrivateLine(a.OwnerID))
		return
	}
	person := firstNonEmptyTrimmed(row.Person, "your teammate")
	where := person + "'s Mac"
	state := "online"
	if !row.Online {
		state = "offline for " + crewTeamOfflineFor(time.Since(row.OfflineSince))
	}
	out := map[string]any{"agent": row.Name, "person": row.Person, "online": row.Online, "lines": []chatHistoryLine{}}
	chat, err := crewTeamSyncedChatLines(t, a)
	if errors.Is(err, errCrewTeamNoSyncedChat) {
		out["banner"] = fmt.Sprintf("Nothing from this chat is on the server yet, so there is no backup to show. %s is %s; ask below and it answers when it is back.", where, state)
		log.Printf("[TEAM_CHAT_READ] project=%s agent=%s caller=%s event=backup_empty online=%t", t.ProjectID, a.ID, viewer, row.Online)
		writeAgentProfileJSON(w, http.StatusOK, out)
		return
	}
	if err != nil {
		log.Printf("[TEAM_CHAT_READ] project=%s agent=%s caller=%s event=refused reason=read_failed view=backup error=%q", t.ProjectID, a.ID, viewer, err.Error())
		externalError(w, http.StatusServiceUnavailable, "backup_unavailable", row.Name+"'s chat backup could not be read now. Try again in a minute.")
		return
	}
	synced := "recently"
	if dir, derr := crewOfflineDirectory(t.ProjectID, a.ID); derr == nil {
		if info, serr := os.Stat(filepath.Join(dir, "history.json")); serr == nil {
			synced = crewTeamAgo(time.Since(info.ModTime()))
			out["synced_at"] = info.ModTime().UTC()
		}
	}
	banner := fmt.Sprintf("Backup of this chat, last synced %s. %s is %s: you are reading a copy, not talking to the agent. Your ask below waits for the real agent.", synced, where, state)
	if chat.partial {
		banner += " The newest messages are still being copied."
	} else if chat.trimmed {
		banner += " Only the newest part of this long chat is shown."
	}
	out["banner"] = banner
	lines := chat.lines
	if len(lines) > crewTeamChatBackupLines {
		lines = lines[len(lines)-crewTeamChatBackupLines:]
		out["older"] = true
	}
	shown := make([]chatHistoryLine, len(lines))
	for i, line := range lines {
		if len(line.Text) > crewTeamChatBackupText {
			cut := crewTeamChatBackupText
			for cut > 0 && !utf8RuneStart(line.Text[cut]) {
				cut--
			}
			line.Text = line.Text[:cut] + "…"
		}
		shown[i] = line
	}
	out["lines"] = shown
	if a.OwnerID != viewer {
		seen := []string{}
		if len(shown) > 0 {
			seen = append(seen, fmt.Sprintf("%d-%d", shown[0].Line, shown[len(shown)-1].Line))
		}
		recordCrewTeamChatRead(t, a, chat.binding, viewer, "opened its chat backup", seen)
	}
	log.Printf("[TEAM_CHAT_READ] project=%s agent=%s caller=%s event=read view=backup online=%t lines_shown=%d revision=%d", t.ProjectID, a.ID, viewer, row.Online, len(shown), chat.revision)
	writeAgentProfileJSON(w, http.StatusOK, out)
}

func crewTeamAgo(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < 2*time.Minute {
		return "1 minute ago"
	}
	return crewTeamOfflineFor(d) + " ago"
}
