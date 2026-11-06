package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A Mac Crew lives either on the Mac or online (owner, 3 Nov, the decisions log): where it lives is the binding's
// Editing side, and "Move online" is a one-way transfer. Nothing syncs in the
// background, so every pair is a relay pair.

// migrateCrewSyncToRelay turns each older synced pair (continuous, manual or off) into a relay pair once: it stops
// syncing, keeps the files where they are, and moves an unfinished background upload or download aside (its stage
// is kept as a .bak next to it). An unfinished move keeps its pending action so the owner can finish it. Each
// binding file is backed up before it changes. Runs on the Mac and on the server.
func migrateCrewSyncToRelay() {
	state, err := crewroot.StateRoot()
	if err != nil {
		return
	}
	dir := filepath.Join(state, "crew-sync")
	entries, _ := os.ReadDir(dir)
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if entry.IsDir() || !ok || strings.HasSuffix(id, "-files") {
			continue
		}
		b, e := crewroot.ReadSync(id)
		if e != nil || b == nil || (b.Mode == "relay" && (b.PendingAction == "" || b.PendingAction == "server")) {
			continue
		}
		raw, e := os.ReadFile(filepath.Join(dir, entry.Name()))
		if e != nil || os.WriteFile(filepath.Join(dir, entry.Name()+"."+stamp+".bak"), raw, 0600) != nil {
			log.Printf("[CREW_SYNC] project=%s migrate=skipped reason=%q", id, "binding backup failed")
			continue
		}
		old, dropped := b.Mode, ""
		if b.PendingAction != "" && b.PendingAction != "server" {
			dropped = b.PendingAction
			if p, e := crewSyncManifestPath(id); e == nil {
				if _, e = os.Stat(p + ".pending"); e == nil {
					_, _ = moveOldCrewTransfer(p+".pending", id)
				}
			}
			b.PendingAction = ""
		}
		b.Mode = "relay"
		if e = crewroot.ReplaceSync(id, *b); e != nil {
			log.Printf("[CREW_SYNC] project=%s migrate=failed reason=%q", id, e.Error())
			continue
		}
		log.Printf("[CREW_SYNC] project=%s side=%s editing=%s migrate=relay old_mode=%q dropped_pending=%q", id, b.Side, b.Editing, old, dropped)
	}
}

const crewOnMacRefusal = "This Crew lives on its owner's Mac, so it can't answer here."

// crewOnYourMacRefusal is what a member whose agents work on their Mac is told on the website.
const crewOnYourMacRefusal = "This Crew works on your Mac, so your agents here answer there. Open it in the AgentWorks app on your Mac, or choose Use it on the web instead."

// crewOnMacRefusalFor is the refusal the caller sees for a server turn of a Crew that lives on a Mac.
func crewOnMacRefusalFor(conversationKey, caller string) string {
	if crewTeamMemberOnMac(crewResourceProjectID(conversationKey), caller) {
		return crewOnYourMacRefusal
	}
	return crewOnMacRefusal
}

// crewLivesOnMac reports a server turn whose agent still runs on the Mac.
// Only a completed publication for this agent and the current connection makes
// its server runtime available; the other agents continue through the Mac relay.
func crewLivesOnMac(profileID, conversationKey, caller string) bool {
	project := crewResourceProjectID(conversationKey)
	if IsLocalMode() || profileID != crewProfileID {
		return false
	}
	if !crewRelayTwin(project) {
		// A Crew on the server: only a member who works on their Mac has their turns there (crew_team_where.go).
		return caller != "" && crewTeamMemberOnMac(project, caller)
	}
	// A Team member's own agents run on the server, as them (owner, 5 Nov); only the owner's agents live on the Mac.
	// After "Add to my Mac" a member's own agents work on their Mac instead (crew_team_where.go).
	if caller != "" && caller != ownerOfCrewProjectID(project) {
		// A thread on the member's own agent that the server answers for while their Mac is off runs here, as them.
		if _, served := crewStandbyServedTabOf(caller, conversationKey); served {
			return false
		}
		if t, err := readCrewTeam(project); err == nil && t.member(caller) {
			_, onMac := t.memberMac(caller)
			return onMac
		}
	}
	// A teammate's thread on an In team agent the server answers for from its standby copy (Always on) runs here.
	if crewStandbyServesKey(conversationKey) {
		return false
	}
	placement, err := crewPublishedAgent(project, crewAgentPublishTab(conversationKey))
	return err != nil || placement == nil || placement.Location != "server"
}

type crewMacCallerKey struct{}

// desktopCrewWorkspace runs the owner's own AI app call (ask, read_chat, reply_input) on this Mac, through the same
// conversation service a direct ask uses, in a tab named after the app. Only the server's checked owner call reaches
// here, over the owner's own connection; only the conversation arguments are taken from it.
func (api *StreamingAPI) desktopCrewWorkspace(ctx context.Context, claims *UserClaims, project string, call crewChatCall) crewChatResult {
	fail := func(code int, message string) crewChatResult {
		body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": "crew_on_mac", "message": message}})
		return crewChatResult{ID: call.ID, Status: code, Body: body}
	}
	if call.Action != "ask" && call.Action != "read_chat" && call.Action != "reply_input" {
		return fail(400, "Ask, read the chat or answer its question.")
	}
	var sent map[string]any
	if json.Unmarshal(call.Args, &sent) != nil {
		return fail(400, "The request could not be read.")
	}
	args := map[string]any{}
	for _, key := range []string{"message", "chat_id", "after", "before_seq", "wait_seconds", "submission_id", "query_id", "request_id", "answers"} {
		if value, ok := sent[key]; ok {
			args[key] = value
		}
	}
	binding, manifest, ok := api.resolveExternalWorkspace(ctx, claims, project)
	if !ok {
		return fail(404, "This Crew is unavailable on your Mac.")
	}
	callCtx := context.WithValue(ctx, crewMacCallerKey{}, call.Caller)
	r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(callCtx)
	recorder := httptest.NewRecorder()
	api.externalWorkspaceConversation(recorder, r, call.Action, args, binding, manifest)
	log.Printf("[CREW_CHAT_RELAY] local project=%s operation=workspace action=%s caller=%q status=%d", project, call.Action, call.Caller, recorder.Code)
	return crewChatResult{ID: call.ID, Status: recorder.Code, Body: recorder.Body.Bytes()}
}

// crewOnMacChannelReply is what a Slack or WhatsApp message to the online copy of a Crew on its owner's Mac gets
// (owner, 4 Nov: Slack and WhatsApp are the Crew's own integrations, not part of Access).
func crewOnMacChannelReply(platform string) string {
	name := "Slack"
	if platform == "whatsapp" {
		name = "WhatsApp"
	}
	return "This Crew runs on its owner's Mac. Connect " + name + " in the Crew's Integrations on that Mac."
}

// crewPlace is where a Crew lives, as its Access row shows it: on this Mac (or the Mac is off, seen from online),
// moving online, or online. The pair's editing side decides it; the Mac's live connection decides on or off.
func crewPlace(owner, project string, b *crewroot.SyncBinding) string {
	switch {
	case b == nil && IsLocalMode():
		return "offline" // on this Mac, Reach off: not reached from the phone or the web (owner, 3 Nov)
	case b == nil:
		return "online"
	case b.PendingAction == "server":
		return "moving"
	case b.Editing == "server":
		return "online"
	case IsLocalMode():
		return "mac"
	}
	if seen := crewMacLastSeen(owner, project, b.PairID); !seen.IsZero() && time.Since(seen) <= crewRelayDropAfter {
		return "mac"
	}
	return "mac_off"
}

// "Reach it from your phone and the web" (owner, 3 Nov: Off by default). A Mac Crew is reached from the phone and the
// web through its empty online copy; turning Reach off removes that copy. Only the Crew's owner, from the Mac that
// holds its pair, can remove it, and only while the Crew lives on the Mac: a copy that holds a moved Crew is never
// removed this way. The copy is backed up first.
func (api *StreamingAPI) unpairCrewRelayTwin(w http.ResponseWriter, r *http.Request, project, pair string) {
	claims := GetUserFromContext(r.Context())
	roots, err := api.crewSyncRoots(r.Context(), claims, project)
	if err != nil {
		externalError(w, 403, "sync_unavailable", err.Error())
		return
	}
	b, err := crewroot.ReadSync(roots.ID)
	if err != nil || b == nil || b.Mode != "relay" || b.Side != "server" || b.Editing != "local" || pair == "" || b.PairID != pair {
		externalError(w, 409, "wrong_pair", "Turn this off from the Mac that holds this Crew.")
		return
	}
	if crewTeamRecordOn(roots.ID) {
		log.Printf("[CREW_RELAY] project=%s reach_off refused: team_on", roots.ID)
		externalError(w, 409, "team_on", crewTeamOnOfflineRefused)
		return
	}
	if crewHasPublishedAgents(roots.ID) {
		externalError(w, 409, "published_agents", "This Crew has published agents on the server. Keep it connected to continue using them; you can close the Mac app and those agents will keep running.")
		return
	}
	backup, err := backupCrewRelayTwin(roots)
	if err != nil {
		log.Printf("[CREW_RELAY] project=%s reach_off refused: backup failed: %v", roots.ID, err)
		externalError(w, 409, "backup_failed", "It could not be backed up first, so nothing was removed. Try again.")
		return
	}
	status, body := scheduleHandler(r, api.handleDeleteAgentProfileProject, http.MethodDelete, "/api/agent-profiles/"+crewProfileID+"/projects/"+roots.ID,
		map[string]string{"id": crewProfileID, "project_id": roots.ID}, nil, nil)
	if status >= 300 {
		log.Printf("[CREW_RELAY] project=%s reach_off delete failed status=%d backup=%s: %s", roots.ID, status, backup, strings.TrimSpace(string(body)))
		externalError(w, 409, "remove_failed", "The online copy could not be removed. Try again.")
		return
	}
	removed := removeCrewSyncFiles(roots.ID)
	crewRelayStreams.forget(claims.UserID, roots.ID)
	log.Printf("[CREW_RELAY] project=%s reach_off owner=%s pair=%s mac_project=%s backup=%s sync_files_removed=%d", roots.ID, logUsernameForUserID(claims.UserID), pair, b.PeerProjectID, backup, removed)
	externalJSON(w, crewSyncReply{ProjectID: roots.ID})
}

// backupCrewRelayTwin keeps a copy of the online copy's folders and pair files under <state>/crew-backups.
func backupCrewRelayTwin(roots crewroot.Roots) (string, error) {
	state, err := crewroot.StateRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(state, "crew-backups")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	paths := []string{}
	for _, path := range []string{roots.Files, roots.Resources, roots.Private} {
		if _, e := os.Stat(path); path != "" && e == nil && !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(state, "crew-sync", roots.ID+"*"))
	paths = append(paths, matches...)
	target := filepath.Join(dir, roots.ID+"-"+time.Now().UTC().Format("20060102T150405Z")+".tar.gz")
	// Unreadable caches (a slot's own sandbox files) are skipped; the archive itself must be written.
	out, err := exec.Command("tar", append([]string{"czf", target, "--ignore-failed-read"}, paths...)...).CombinedOutput()
	if info, statErr := os.Stat(target); statErr != nil || info.Size() == 0 {
		return "", fmt.Errorf("tar: %v %s", err, strings.TrimSpace(string(out)))
	}
	return target, nil
}

// removeCrewSyncFiles removes a Crew's pair files: <id>.json, <id>-files.json and what sits next to them.
func removeCrewSyncFiles(id string) int {
	paths, err := crewSyncStatePaths(id)
	if err != nil {
		return 0
	}
	removed := 0
	for _, path := range paths {
		if os.RemoveAll(path) == nil {
			removed++
		}
	}
	return removed
}
