package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	if !ok || a.Kind != "terminal" {
		return crewTeamRegisteredAgent{}, fmt.Errorf("terminal agent unavailable")
	}
	named, err := currentCrewTeamTerminal(ctx, t, a)
	if err != nil {
		return crewTeamRegisteredAgent{}, err
	}
	// The person always sees their own exact session, even with Team off or
	// Answers only. This grants nothing to teammates or scoped agent tokens.
	if viewer == a.OwnerID && websiteDeviceClaims(GetUserFromContext(ctx)) && t.member(viewer) && crewTeamAccountActive(viewer) && ownerOfCrewProjectID(t.ProjectID) == t.OwnerID {
		return a, nil
	}
	address := "agent:" + shareOwnerLabel(a.OwnerID) + "/" + named.Name
	// The same central address admission as bridge discovery, send and read.
	// It owns membership/In-team and the terminal branch owns exact transport.
	reachable, current, err := crewTeamAdmitAddress(ctx, viewer, address)
	if err != nil || reachable.ID != id || current.ProjectID != t.ProjectID {
		return crewTeamRegisteredAgent{}, fmt.Errorf("terminal agent unavailable")
	}
	a, ok = current.Agents[id]
	if !ok || a.Kind != "terminal" || a.Rule.ChatShare != crewChatShareLive {
		return crewTeamRegisteredAgent{}, fmt.Errorf("this agent's conversation is not shared Live")
	}
	return a, nil
}

func (api *StreamingAPI) handleCrewTeamTerminalLive(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, false)
	if !ok {
		return
	}
	started := time.Now()
	action, reason, status, admittedID := "refused", "not_admitted", http.StatusForbidden, "-"
	defer func() {
		log.Printf("[NATIVE_CONVERSATION] project=%s agent=%s action=%s reason=%s status=%d ms=%d", t.ProjectID, admittedID, action, reason, status, time.Since(started).Milliseconds())
	}()
	id := mux.Vars(r)["agent"]
	a, err := crewTeamTerminalLiveRow(r.Context(), t, claims.UserID, id)
	if err != nil {
		externalError(w, 403, "chat_private", "This conversation is private or its attached session changed.")
		return
	}
	admittedID = a.ID
	log.Printf("[NATIVE_CONVERSATION] project=%s agent=%s action=load_started", t.ProjectID, a.ID)
	payload, _ := json.Marshal(map[string]string{"provider": a.Terminal.Provider, "session": a.ChatKey, "attachment": a.Terminal.AttachmentID, "agent": a.Terminal.NamedAgentID, "folder": t.Folder.ID})
	result := api.readCrewTerminalMac(r.Context(), t.ProjectID, a, crewChatCall{Operation: "native_terminal_read", Args: payload})
	// A revocation during the Mac read must not release the response.
	current, readErr := readCrewTeam(t.ProjectID)
	admitted, checkErr := crewTeamTerminalLiveRow(r.Context(), current, claims.UserID, id)
	if readErr != nil || checkErr != nil || admitted.ChatKey != a.ChatKey || admitted.Terminal == nil || *admitted.Terminal != *a.Terminal {
		reason = "admission_changed"
		externalError(w, 403, "chat_private", "This agent's sharing or attached session changed.")
		return
	}
	status = result.Status
	action, reason = "failed", "source_unavailable"
	if status == http.StatusOK {
		action, reason = "loaded", "ok"
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
}

// Every caller has proved the exact attachment before this bounded native read.
func readCrewTerminalJournal(provider, session string) crewChatResult {
	path, err := crewTeamTerminalJournal(provider, session)
	if err != nil {
		return crewChatResult{Status: 409, Body: json.RawMessage(`{"error":"This session's local conversation is unavailable."}`)}
	}
	runtime := "claude-code"
	if provider == "codex" {
		runtime = "codex-cli"
	}
	messages, stamp, err := readNativeTranscriptWindow(path, runtime, time.Time{}, true)
	if err != nil {
		return crewChatResult{Status: 409, Body: json.RawMessage(`{"error":"The local conversation could not be read."}`)}
	}
	for i := range messages {
		messages[i].ToolName = redaction.Text(messages[i].ToolName)
		for j := range messages[i].Parts {
			messages[i].Parts[j].Text = redaction.Text(messages[i].Parts[j].Text)
		}
	}
	raw, _ := json.Marshal(map[string]any{"messages": messages, "updated_at": stamp})
	return crewChatResult{Status: 200, Body: raw}
}

// Exact native UUID only, with no caller path. Ambiguous or symlink-escaped
// journals are refused rather than selecting another session's private file.
func crewTeamTerminalJournal(provider, session string) (string, error) {
	if id, err := uuid.Parse(session); err != nil || id.String() != session {
		return "", fmt.Errorf("invalid native session UUID")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	base := filepath.Join(home, ".claude", "projects")
	pattern := filepath.Join(base, "*", session+".jsonl")
	if provider == "codex" {
		base, err = codexSessionsRoot()
		if err != nil {
			return "", err
		}
		pattern = filepath.Join(base, "*", "*", "*", "rollout-*-"+session+".jsonl")
	} else if provider != "claude" {
		return "", fmt.Errorf("unsupported terminal provider")
	}
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) != 1 {
		return "", fmt.Errorf("exact session journal unavailable")
	}
	// The existing realpath admission also refuses escaping symlinks. Its
	// directory check is used on the parent, then the file itself is checked.
	parent, err := crewTeamTerminalCopyPath(base, filepath.Dir(matches[0]))
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(filepath.Join(parent, filepath.Base(matches[0])))
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("unsafe session journal")
	}
	return filepath.Join(parent, filepath.Base(matches[0])), nil
}
