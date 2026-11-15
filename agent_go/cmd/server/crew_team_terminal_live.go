package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
)

// Live is opt-in on the existing agent rule. No native I/O occurs before both
// current audience and exact transport admission; a failed binding is not offline.
func crewTeamTerminalLiveRow(ctx context.Context, t crewTeam, viewer, id string) (crewTeamRegisteredAgent, error) {
	a, ok := t.Agents[id]
	allowed := ok && crewTeamAccountActive(viewer) && crewTeamReachRow(t, viewer, a)
	if viewer == a.OwnerID {
		allowed = ok && crewTeamReachOpen(t, viewer) && a.InTeam && crewTeamAccountActive(viewer)
	}
	if !allowed || a.Kind != "terminal" || a.Rule.ChatShare != crewChatShareLive {
		return crewTeamRegisteredAgent{}, fmt.Errorf("this agent's conversation is not shared Live")
	}
	if _, err := currentCrewTeamTerminal(ctx, t, a); err != nil {
		return crewTeamRegisteredAgent{}, err
	}
	return a, nil
}

func (api *StreamingAPI) handleCrewTeamTerminalLive(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, false)
	if !ok {
		return
	}
	id := mux.Vars(r)["agent"]
	a, err := crewTeamTerminalLiveRow(r.Context(), t, claims.UserID, id)
	if err != nil {
		externalError(w, 403, "chat_private", "This conversation is private or its attached session changed.")
		return
	}
	payload, _ := json.Marshal(map[string]string{"provider": a.Terminal.Provider, "session": a.ChatKey, "attachment": a.Terminal.AttachmentID, "agent": a.Terminal.NamedAgentID, "folder": t.Folder.ID})
	result := callCrewMacPair(r.Context(), a.OwnerID, t.ProjectID, a.Terminal.MacPair, crewChatCall{Operation: "team_terminal_live", Args: payload}, 15*time.Second)
	// A revocation during the Mac read must not release the response.
	current, readErr := readCrewTeam(t.ProjectID)
	admitted, checkErr := crewTeamTerminalLiveRow(r.Context(), current, claims.UserID, id)
	if readErr != nil || checkErr != nil || admitted.ChatKey != a.ChatKey || admitted.Terminal == nil || *admitted.Terminal != *a.Terminal {
		externalError(w, 403, "chat_private", "This agent's sharing or attached session changed.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
}

// The relay has authenticated this Mac's exact pair. Reuse the join proof of
// native liveness and the accepted Team copy before opening the exact journal.
func (api *StreamingAPI) desktopTeamTerminalLive(ctx context.Context, project string, call crewChatCall) crewChatResult {
	proof := api.desktopTeamTerminalCopy(ctx, project, call)
	if proof.Status != 200 {
		return proof
	}
	var in struct {
		Session string `json:"session"`
	}
	if json.Unmarshal(call.Args, &in) != nil {
		return crewChatResult{ID: call.ID, Status: 403}
	}
	path, err := crewTeamTerminalJournal(in.Session)
	if err != nil {
		return crewChatResult{ID: call.ID, Status: 409, Body: json.RawMessage(`{"error":"This session's local conversation is unavailable."}`)}
	}
	messages, stamp, err := readNewClaudeTranscriptMessages(path, time.Time{})
	if err != nil {
		return crewChatResult{ID: call.ID, Status: 409, Body: json.RawMessage(`{"error":"The local conversation could not be read."}`)}
	}
	for i := range messages {
		for j := range messages[i].Parts {
			messages[i].Parts[j].Text = redaction.Text(messages[i].Parts[j].Text)
		}
	}
	raw, _ := json.Marshal(map[string]any{"messages": messages, "updated_at": stamp})
	return crewChatResult{ID: call.ID, Status: 200, Body: raw}
}

// Exact native UUID only, with no caller path. Ambiguous or symlink-escaped
// journals are refused rather than selecting another session's private file.
func crewTeamTerminalJournal(session string) (string, error) {
	if _, err := uuid.Parse(session); err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	base := filepath.Join(home, ".claude", "projects")
	matches, err := filepath.Glob(filepath.Join(base, "*", session+".jsonl"))
	if err != nil || len(matches) != 1 {
		return "", fmt.Errorf("exact session journal unavailable")
	}
	// The existing realpath admission also refuses escaping symlinks. Its
	// directory check is used on the parent, then the file itself is checked.
	parent, err := crewTeamTerminalCopyPath(base, filepath.Dir(matches[0]))
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(filepath.Join(parent, session+".jsonl"))
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("unsafe session journal")
	}
	return filepath.Join(parent, session+".jsonl"), nil
}
