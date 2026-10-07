package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
)

// Chats of one Code ask each other (PLAT-648). A Code has a main chat and
// side chats (`<projectId>:chat:<id>`, PLAT-571), each a full Builder chat.
// One chat lists its sibling chats and asks one (ask_project_chat). The ask
// is a function call like any cross-product call (crew_functions.go): it gets
// a call_id and a saved record, runs as a turn in the target chat's own
// conversation (queued behind its current turn, visible in its tab), and the
// target answers with return_function_result; the answer comes back to the
// sender as the standard call [AUTO-NOTIFICATION]. Loops are stopped by the
// shared call-chain guards, where each chat is its own participant.
//
// The sender and the project come from the trusted turn (its session and
// verified workspace), never from tool arguments, and targets are read from
// the owner's own conversation registry: only chats of the same Code, of the
// same person (a Code is owner-only), are reachable.

const (
	codeChatMainName       = "main"
	codeChatSideMarker     = ":chat:"
	codeChatMessageMaxRune = 20000
	// codeChatAsksPerHour caps asks between one Code's chats (owner, 2026-10-07):
	// two chats can trade asks forever, because each answer starts a fresh call
	// chain that the shared loop guards do not count.
	codeChatAsksPerHour = 20
)

// codeChatAsks remembers each Code's recent asks between its chats, by call
// id, so a joined or resubmitted ask (the same call) counts once.
var codeChatAsks = struct {
	sync.Mutex
	byCode map[string][]codeChatAsk
}{byCode: map[string][]codeChatAsk{}}

type codeChatAsk struct {
	callID string
	at     time.Time
}

func codeChatAskKey(project codeChatProject) string { return project.OwnerID + "/" + project.ProjectID }

func recentCodeChatAsksLocked(key string, now time.Time) []codeChatAsk {
	kept := codeChatAsks.byCode[key][:0]
	for _, ask := range codeChatAsks.byCode[key] {
		if now.Sub(ask.at) < time.Hour {
			kept = append(kept, ask)
		}
	}
	codeChatAsks.byCode[key] = kept
	return kept
}

// admitCodeChatAsk refuses a new ask once this Code's chats made
// codeChatAsksPerHour asks in the last hour.
func admitCodeChatAsk(project codeChatProject, now time.Time) error {
	codeChatAsks.Lock()
	defer codeChatAsks.Unlock()
	if len(recentCodeChatAsksLocked(codeChatAskKey(project), now)) >= codeChatAsksPerHour {
		return fmt.Errorf("refused: this Code's chats already asked each other %d times in the last hour; stop and tell the person what the chats are doing instead of asking again", codeChatAsksPerHour)
	}
	return nil
}

// recordCodeChatAsk counts a call once, however often it is joined or resubmitted.
func recordCodeChatAsk(project codeChatProject, callID string, now time.Time) {
	codeChatAsks.Lock()
	defer codeChatAsks.Unlock()
	key := codeChatAskKey(project)
	for _, ask := range recentCodeChatAsksLocked(key, now) {
		if ask.callID == callID {
			return
		}
	}
	codeChatAsks.byCode[key] = append(codeChatAsks.byCode[key], codeChatAsk{callID: callID, at: now})
}

// codeChatTurn, when set (tests), replaces the turn in the target chat.
var codeChatTurn func(ctx context.Context, call *crewFunctionCall, text string) (internalSessionTurnResult, error)

type codeChat struct {
	Key       string // conversation key: <projectId> or <projectId>:chat:<id>
	ID        string // "main" or the side chat's id
	Name      string
	SessionID string
	UpdatedAt string
}

type codeChatProject struct {
	OwnerID   string
	ProjectID string
	Workspace string
	Chats     []codeChat
	Self      codeChat
}

// codeChatsFor resolves the calling chat's Code and its chats. Every input is
// trusted turn state: the turn's user, session and verified workspace.
func (api *StreamingAPI) codeChatsFor(ctx context.Context, userID, sessionID, workspacePath string) (codeChatProject, error) {
	denied := codeChatProject{}
	unavailable := fmt.Errorf("messaging other chats is available only in a Code chat")
	if api == nil || api.agentProfiles == nil || strings.TrimSpace(userID) == "" || strings.TrimSpace(sessionID) == "" {
		return denied, unavailable
	}
	raw, exists, err := readFileFromWorkspace(ctx, strings.TrimSuffix(strings.TrimSpace(workspacePath), "/")+"/product.json")
	var manifest productProjectManifest
	if err != nil || !exists || json.Unmarshal([]byte(raw), &manifest) != nil || manifest.Product != codeproduct.ProfileID || strings.TrimSpace(manifest.ID) == "" {
		return denied, unavailable
	}
	profile, err := api.agentProfiles.Resolve(codeproduct.ProfileID, 0, userID)
	if err != nil {
		return denied, unavailable
	}
	// A Code resolves only in its owner's own tree (resolveCrewProjectBinding).
	binding, owned, err := resolveConversationBindingForUser(ctx, userID, profile, manifest.ID)
	if err != nil || !owned || !workspacePathsMatchForUser(userID, binding.WorkspacePath, workspacePath) {
		return denied, unavailable
	}
	chats, err := defaultProductConversationRegistryStore().projectChats(ctx, userID, codeproduct.ProfileID, manifest.ID)
	if err != nil {
		return denied, err
	}
	project := codeChatProject{OwnerID: sanitizeUserIDForPath(userID), ProjectID: manifest.ID, Workspace: binding.WorkspacePath}
	titles := map[string]string{}
	if sessions, listErr := ListChatHistorySessions(userID, 200, 0, binding.WorkspacePath); listErr == nil {
		for _, session := range sessions {
			titles[session.SessionID] = strings.TrimSpace(session.Title)
		}
	}
	found := false
	for _, record := range chats {
		if !workspacePathsMatchForUser(userID, record.WorkspacePath, binding.WorkspacePath) {
			continue
		}
		chat := codeChat{Key: record.ConversationKey, SessionID: record.SessionID, UpdatedAt: record.UpdatedAt}
		if record.ConversationKey == manifest.ID {
			chat.ID, chat.Name = codeChatMainName, "Main chat"
		} else {
			chat.ID = strings.TrimPrefix(record.ConversationKey, manifest.ID+codeChatSideMarker)
			chat.Name = firstNonEmptyTrimmed(titles[record.SessionID], "Chat "+chat.ID)
		}
		if record.SessionID == sessionID {
			project.Self, found = chat, true
		}
		project.Chats = append(project.Chats, chat)
	}
	if !found {
		return denied, unavailable
	}
	sort.SliceStable(project.Chats, func(i, j int) bool {
		if (project.Chats[i].ID == codeChatMainName) != (project.Chats[j].ID == codeChatMainName) {
			return project.Chats[i].ID == codeChatMainName
		}
		return project.Chats[i].UpdatedAt > project.Chats[j].UpdatedAt
	})
	return project, nil
}

// find returns the chat named by id ("main", a side chat id) or exact name.
func (p codeChatProject) find(raw string) (codeChat, error) {
	want := strings.ToLower(strings.TrimSpace(raw))
	if want == "" {
		return codeChat{}, fmt.Errorf("chat is required: pass an id or name from list_project_chats")
	}
	if want == "primary" || want == "main chat" {
		want = codeChatMainName
	}
	var matches []codeChat
	for _, chat := range p.Chats {
		if strings.ToLower(chat.ID) == want || strings.ToLower(chat.Key) == want || strings.ToLower(chat.Name) == want {
			matches = append(matches, chat)
		}
	}
	switch len(matches) {
	case 0:
		return codeChat{}, fmt.Errorf("no chat %q in this Code; call list_project_chats", raw)
	case 1:
		return matches[0], nil
	default:
		return codeChat{}, fmt.Errorf("%q matches more than one chat; pass its id from list_project_chats", raw)
	}
}

// codeChatCaller is the calling chat as a call source: its Code, plus the
// chat itself, all from the trusted turn.
func codeChatCaller(project codeChatProject, base triggerLinkCaller) triggerLinkCaller {
	self := project.Self
	base.Label = self.Name
	base.Chat = &self
	return base
}

// codeChatTarget is a sibling chat as a call target; codePath is the Code's
// physical folder, the same one its calls are keyed by.
func codeChatTarget(project codeChatProject, chat codeChat, codePath string) triggerTarget {
	return triggerTarget{Kind: triggerCallerCrew, Path: codePath, Label: chat.Name,
		CrewID: project.ProjectID, CrewProfile: codeproduct.ProfileID, CrewOwner: project.OwnerID, Chat: &chat}
}

func codeChatCallTaskText(call *crewFunctionCall, message string, handoff bool) string {
	ending := fmt.Sprintf("Chat %q is waiting for this result; it does not see this conversation.", call.CallerLabel)
	if handoff {
		ending = fmt.Sprintf("Chat %q handed this over and is not waiting for an answer, but still finish the call so the hand-off is recorded.", call.CallerLabel)
	}
	return fmt.Sprintf(`[Function call %[1]s] Chat %[2]q, another chat of this Code (same folder, same person), asks:

%[3]s

%[4]s Report milestones with report_function_progress(call_id=%[1]q, message=...). When you are done, call return_function_result(call_id=%[1]q, result={"answer": "<your self-contained answer>"}); if you cannot do it, call return_function_result(call_id=%[1]q, error="<why>"). Do not ask that chat back with ask_project_chat; the result reaches it on its own.`,
		call.ID, call.CallerLabel, strings.TrimSpace(message), ending)
}

// runCodeChatCall runs a sibling chat call as a turn in the target chat's own
// conversation. The target settles it with return_function_result; a turn
// that ends without one settles it with its final reply as the answer. As
// with any ask, the timeout counts from the chat's last sign of life and only
// releases the caller; a late answer is still delivered.
func (api *StreamingAPI) runCodeChatCall(call *crewFunctionCall, target triggerTarget, args map[string]interface{}, timeout time.Duration) {
	hardCap := crewFunctionHardCap(timeout)
	ctx, cancel := context.WithTimeout(internalBotRequestContext(context.Background(), call.UserID), hardCap)
	defer cancel()
	ctx = virtualtools.WithFeedbackOperation(ctx, call.ID)
	message, _ := args["message"].(string)
	handoff := args["reply"] == false
	text := codeChatCallTaskText(call, message, handoff)
	sessionID := target.Chat.SessionID
	var reqMap map[string]interface{}
	if codeChatTurn == nil {
		var err error
		if reqMap, sessionID, err = api.codeChatTurnRequest(ctx, call.UserID, target, text, call.CallerLabel); err != nil {
			call.settle("failed", nil, err.Error())
			return
		}
	}
	call.mu.Lock()
	call.Status = "running"
	call.RunID, call.RunIDs = sessionID, []string{sessionID}
	call.mu.Unlock()
	call.persist()
	turnDone := make(chan struct{})
	go api.watchAskActivity(call, sessionID, fmt.Sprintf("chat %q", call.TargetLabel), timeout, turnDone)
	var result internalSessionTurnResult
	var err error
	if codeChatTurn != nil {
		result, err = codeChatTurn(ctx, call, text)
	} else {
		result, err = api.startSessionInternalWithResult(ctx, reqMap, sessionID, call.UserID, nil)
	}
	close(turnDone)
	if call.settled() {
		return
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			call.settle("failed", nil, fmt.Sprintf("chat %q was still not done after %s", call.TargetLabel, hardCap))
			return
		}
		call.settle("failed", nil, fmt.Sprintf("chat %q did not answer: %v", call.TargetLabel, err))
		return
	}
	answer := strings.TrimSpace(result.FinalResponse)
	if answer == "" {
		call.settle("failed", nil, fmt.Sprintf("chat %q finished without return_function_result or a reply", call.TargetLabel))
		return
	}
	call.settle("completed", map[string]interface{}{"answer": truncateTriggerTargetResult(answer)}, "")
}

// codeChatTurnRequest builds the turn in the target chat's existing
// conversation, re-checking that it is still that chat of the same Code.
func (api *StreamingAPI) codeChatTurnRequest(ctx context.Context, userID string, target triggerTarget, text, fromLabel string) (map[string]interface{}, string, error) {
	unavailable := fmt.Errorf("that chat is no longer available")
	if api == nil || api.agentProfiles == nil || target.Chat == nil {
		return nil, "", unavailable
	}
	profile, err := api.agentProfiles.Resolve(codeproduct.ProfileID, 0, userID)
	if err != nil {
		return nil, "", unavailable
	}
	binding, owned, err := resolveConversationBindingForUser(ctx, userID, profile, target.Chat.Key)
	if err != nil || !owned || !workspacePathsMatchForUser(userID, binding.WorkspacePath, target.Path) {
		return nil, "", unavailable
	}
	conversation, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, userID, profile, binding, "")
	if err != nil || conversation.SessionID != target.Chat.SessionID {
		return nil, "", unavailable
	}
	reqMap, sessionID, _, err := productBotTurnRequest(ctx, userID, profile, conversation, services.BotIncomingMessage{Text: text}, services.ThreadID{})
	if err != nil {
		return nil, "", err
	}
	// An interactive origin keeps the chat's coding session (no relaunch).
	reqMap["triggered_by"] = "code_chat"
	reqMap["triggered_by_label"] = "From " + fromLabel
	return reqMap, sessionID, nil
}

// registerCodeChatTools registers list_project_chats and ask_project_chat
// for one Code chat turn.
func (api *StreamingAPI) registerCodeChatTools(registrar definitionToolRegistrar, gate *productToolGate, userID, sessionID, workspacePath string) error {
	trusted := func(ctx context.Context) context.Context {
		return internalBotRequestContext(ctx, userID)
	}
	jsonOut := func(value interface{}) (string, error) {
		encoded, err := json.MarshalIndent(value, "", "  ")
		return string(encoded), err
	}
	describe := func(project codeChatProject) []map[string]interface{} {
		out := make([]map[string]interface{}, 0, len(project.Chats))
		for _, chat := range project.Chats {
			item := map[string]interface{}{"id": chat.ID, "name": chat.Name, "working": api.conversationTurnOccupied(chat.SessionID)}
			if chat.Key == project.Self.Key {
				item["this_chat"] = true
			}
			out = append(out, item)
		}
		return out
	}
	resolveCaller := crewTriggerLinkCaller(workspacePath)
	tools := []struct {
		name, description string
		parameters        map[string]interface{}
		execute           func(context.Context, map[string]interface{}) (string, error)
	}{
		{"list_project_chats", "List the chats of this Code: the main chat and its side chats (tabs), with id, name, whether each is working now, and which one is this chat. Use it before ask_project_chat.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			func(ctx context.Context, _ map[string]interface{}) (string, error) {
				ctx = trusted(ctx)
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				return jsonOut(map[string]interface{}{"chats": describe(project)})
			}},
		{"ask_project_chat", "Ask another chat (tab) of this Code, as a function call. It runs there as a turn, after that chat's current turn if it is busy, and the person sees it in that tab; that chat answers with return_function_result. Returns a call_id and status at once; by default the result arrives in this chat as an [AUTO-NOTIFICATION], so end your turn after asking. Check it with get_function_call(call_id). Set reply=false for a hand-off you do not wait for (no notification; the call is still recorded). The other chat shares this folder but not this conversation, so make the message self-contained. Only chats of this Code are reachable.", map[string]interface{}{
			"type": "object", "required": []string{"chat", "message"}, "properties": map[string]interface{}{
				"chat":            map[string]interface{}{"type": "string", "description": "The chat's id or name from list_project_chats (\"main\" for the main chat)."},
				"message":         map[string]interface{}{"type": "string", "description": "Self-contained question, request or hand-off."},
				"reply":           map[string]interface{}{"type": "boolean", "description": "Send its result back to this chat as an [AUTO-NOTIFICATION] (default true)."},
				"submission_id":   map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128, "description": "Stable ID for this intended ask; reuse it after an uncertain retry to get the original call_id."},
				"timeout_minutes": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": int(triggerTargetMaxTimeout / time.Minute), "description": "How long that chat may go without activity before you stop waiting (default 60); a late answer still arrives."},
			}},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				ctx = trusted(ctx)
				message, _ := args["message"].(string)
				if strings.TrimSpace(message) == "" {
					return "", fmt.Errorf("message is required")
				}
				if len([]rune(message)) > codeChatMessageMaxRune {
					return "", fmt.Errorf("message is longer than %d characters; put the detail in a file in the folder and point to it", codeChatMessageMaxRune)
				}
				wantReply := true
				if value, ok := args["reply"].(bool); ok {
					wantReply = value
				}
				timeout, err := triggerTargetTimeout(args["timeout_minutes"])
				if err != nil {
					return "", err
				}
				submissionID, _ := args["submission_id"].(string)
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				chatArg, _ := args["chat"].(string)
				chat, err := project.find(chatArg)
				if err != nil {
					return "", err
				}
				if chat.Key == project.Self.Key {
					return "", fmt.Errorf("a chat cannot ask itself")
				}
				base, err := resolveCaller(ctx)
				if err != nil {
					return "", err
				}
				caller, target := codeChatCaller(project, base), codeChatTarget(project, chat, agentProfileRuntimeWorkspace(userID, base.Path))
				if err := admitCodeChatAsk(project, time.Now()); err != nil {
					return "", err
				}
				busy := api.conversationTurnOccupied(chat.SessionID)
				call, err := api.startCrewFunctionCall(ctx, userID, caller, target, defaultAskCrewFunction(), map[string]interface{}{"message": strings.TrimSpace(message), "reply": wantReply}, timeout, submissionID)
				if err != nil {
					return "", err
				}
				recordCodeChatAsk(project, call.ID, time.Now())
				out := call.snapshot()
				addFunctionCallPending(out, call)
				select {
				case <-call.done: // already settled (a joined or resubmitted call)
					return jsonOut(out)
				default:
				}
				if _, joined := out["joined"]; !joined && busy {
					out["note"] = "That chat is busy; the ask runs right after its current turn."
				}
				if wantReply {
					executionID, watchErr := api.startCrewFunctionWatch(QueryRequest{SelectedFolder: workspacePath}, sessionID, userID, call, timeout)
					if watchErr != nil {
						out["auto_notification"] = "unavailable: " + watchErr.Error() + "; poll with get_function_call"
					} else {
						out["auto_notification"] = map[string]interface{}{"execution_id": executionID}
						out["next"] = "End your turn; the result arrives in this chat as an [AUTO-NOTIFICATION]."
					}
				}
				return jsonOut(out)
			}},
	}
	for _, tool := range tools {
		if gate != nil {
			gate.Declare(tool.name)
		}
		if err := registrar.RegisterCustomTool(tool.name, tool.description, tool.parameters, tool.execute, "code_chat_tools"); err != nil {
			return err
		}
	}
	return nil
}
