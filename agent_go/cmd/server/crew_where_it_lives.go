package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A Mac Crew lives either on the Mac or online (owner, 3 Nov, the decisions log): where it lives is the binding's
// Editing side, and "Move online" / "Move back to this Mac" are the only transfers. Nothing syncs in the
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
		if e != nil || b == nil || (b.Mode == "relay" && b.PendingAction != "update" && b.PendingAction != "download") {
			continue
		}
		raw, e := os.ReadFile(filepath.Join(dir, entry.Name()))
		if e != nil || os.WriteFile(filepath.Join(dir, entry.Name()+"."+stamp+".bak"), raw, 0600) != nil {
			log.Printf("[CREW_SYNC] project=%s migrate=skipped reason=%q", id, "binding backup failed")
			continue
		}
		old, dropped := b.Mode, ""
		if b.PendingAction == "update" || b.PendingAction == "download" {
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

// crewLivesOnMac reports a server turn aimed at the empty online copy of a Crew that lives on its owner's Mac.
// Such a turn would run with none of the Crew's files, so every server path refuses it; the owner's own chat reaches
// the Mac through the relay before this check.
func crewLivesOnMac(profileID, conversationKey string) bool {
	return !IsLocalMode() && profileID == crewProfileID && crewRelayTwin(crewResourceProjectID(conversationKey))
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
	r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(context.WithValue(ctx, crewMacCallerKey{}, call.Caller))
	recorder := httptest.NewRecorder()
	api.externalWorkspaceConversation(recorder, r, call.Action, args, binding, manifest)
	log.Printf("[CREW_CHAT_RELAY] local project=%s operation=workspace action=%s caller=%q status=%d", project, call.Action, call.Caller, recorder.Code)
	return crewChatResult{ID: call.ID, Status: recorder.Code, Body: recorder.Body.Bytes()}
}

const crewOnMacSlackRefusal = "This Crew is on its owner's Mac. Ask them to share it so it works here."

// slackCrewOnMac answers a Slack message for a Crew that lives on its owner's Mac: the owner's own proven direct
// message is asked on the Mac and its answer posted; a channel, a group message or anyone else gets a plain refusal.
func (api *StreamingAPI) slackCrewOnMac(ctx context.Context, sender, project string, msg services.BotIncomingMessage) error {
	if !msg.DirectMessage {
		return services.BotReplyText{Text: crewOnMacSlackRefusal}
	}
	binding, _, ok := api.resolveExternalWorkspace(ctx, &UserClaims{UserID: sender, Username: sender}, project)
	if !ok || binding.OwnerID != sender || binding.CoOwner {
		return services.BotReplyText{Text: crewOnMacSlackRefusal}
	}
	return services.BotReplyText{Text: api.askCrewMacAndWait(ctx, sender, project, "Slack", msg.Text)}
}

// askCrewMacAndWait asks the owner's Mac in the caller's named tab and waits for the answer, reading the same
// conversation service a direct ask does. It returns the answer, or a plain line saying why there is none.
func (api *StreamingAPI) askCrewMacAndWait(ctx context.Context, owner, project, caller, message string) string {
	call := func(action string, args map[string]any) crewChatResult {
		raw, _ := json.Marshal(args)
		return callCrewMacFor(ctx, owner, project, crewChatCall{Operation: "workspace", Action: action, Args: raw, Caller: caller}, 70*time.Second)
	}
	result := call("ask", map[string]any{"message": message, "wait_seconds": float64(externalCrewMaxWaitSeconds)})
	answer := ""
	deadline := time.Now().Add(15 * time.Minute)
	for {
		if result.Status >= 300 {
			return crewMacResultText(result)
		}
		var out struct {
			Status    string         `json:"status"`
			Working   bool           `json:"working"`
			QueryID   string         `json:"query_id"`
			ChatID    string         `json:"chat_id"`
			NextAfter int            `json:"next_after"`
			Messages  []codeTurnItem `json:"messages"`
		}
		if json.Unmarshal(result.Body, &out) != nil {
			return "Your Mac sent an answer that could not be read. Open the Crew to see it."
		}
		for _, item := range out.Messages {
			if item.Role == "assistant" && strings.TrimSpace(item.Text) != "" {
				answer = item.Text
			}
		}
		switch {
		case out.Status == "failed":
			return "It couldn't finish on your Mac. Try again."
		case out.Status == "waiting_for_input":
			return "It needs your answer: open the Crew on your Mac or the website to reply."
		case !out.Working && answer != "":
			return answer
		case !out.Working && out.QueryID == "":
			return "Done on your Mac."
		case time.Now().After(deadline) || ctx.Err() != nil:
			return "Still working on your Mac. Open the Crew to see the answer."
		}
		result = call("read_chat", map[string]any{"chat_id": out.ChatID, "query_id": out.QueryID, "after": float64(out.NextAfter), "wait_seconds": float64(externalCrewMaxWaitSeconds)})
	}
}

// crewMacResultText reads a plain reason from the Mac's or the relay's error body.
func crewMacResultText(result crewChatResult) string {
	var body struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(result.Body, &body) == nil && len(body.Error) > 0 {
		var text string
		if json.Unmarshal(body.Error, &text) == nil && text != "" {
			return text
		}
		var detail struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body.Error, &detail) == nil && detail.Message != "" {
			return detail.Message
		}
	}
	return http.StatusText(result.Status)
}
