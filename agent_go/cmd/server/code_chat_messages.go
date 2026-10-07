package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Chats of one Code talk to each other (PLAT-648). A Code has a main chat and
// side chats (`<projectId>:chat:<id>`, PLAT-571), each a full Builder chat.
// One chat can list its sibling chats and send one a message, which runs as
// a turn in that chat (queued behind its current turn, visible in its tab).
// By default the target's final reply comes back to the sender as an
// [AUTO-NOTIFICATION], the same way a Crew's answer to call_function does.
//
// The sender and the project come from the trusted turn (its session and
// verified workspace), never from tool arguments, and targets are read from
// the owner's own conversation registry: only chats of the same Code, of the
// same person (a Code is owner-only), are reachable.
//
// Loops: a chain of messages may not come back to a chat already in it, may
// be at most codeChatMaxHops long, and one Code sends at most
// codeChatRateLimit messages per codeChatRateWindow.

const (
	codeChatMainName       = "main"
	codeChatSideMarker     = ":chat:"
	codeChatMaxHops        = 3
	codeChatRateLimit      = 20
	codeChatRateWindow     = time.Hour
	codeChatTurnLimit      = 4 * time.Hour
	codeChatMessageMaxRune = 20000
)

// codeChatRelay holds the in-flight chains (by receiving session) and the
// recent sends per Code, for the loop guards.
var codeChatRelay = struct {
	sync.Mutex
	inbound map[string][]*codeChatInbound
	sent    map[string][]time.Time
}{inbound: map[string][]*codeChatInbound{}, sent: map[string][]time.Time{}}

// codeChatInbound is the chain a delivered message carries into its turn.
type codeChatInbound struct{ chain []string }

// codeChatTurn, when set (tests), replaces the delivered turn.
var codeChatTurn func(api *StreamingAPI, ctx context.Context, reqMap map[string]interface{}, sessionID, userID string) (internalSessionTurnResult, error)

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

func codeChatChainKey(ownerID, conversationKey string) string {
	return ownerID + "/" + conversationKey
}

// admitCodeChatMessage applies the loop guards and, on success, records the
// send and the chain the target turn will carry. release forgets the chain.
func admitCodeChatMessage(project codeChatProject, target codeChat, now time.Time) (chain []string, release func(), err error) {
	from := codeChatChainKey(project.OwnerID, project.Self.Key)
	to := codeChatChainKey(project.OwnerID, target.Key)
	if from == to {
		return nil, nil, fmt.Errorf("a chat cannot message itself")
	}
	codeChatRelay.Lock()
	defer codeChatRelay.Unlock()
	var base []string
	for _, inbound := range codeChatRelay.inbound[project.Self.SessionID] {
		if len(inbound.chain) > len(base) {
			base = inbound.chain
		}
	}
	chain = append([]string(nil), base...)
	if len(chain) == 0 || chain[len(chain)-1] != from {
		chain = append(chain, from)
	}
	for _, key := range chain {
		if key == to {
			return nil, nil, fmt.Errorf("refused: %q already took part in this exchange; your final reply goes back to it on its own", target.Name)
		}
	}
	if len(chain) >= codeChatMaxHops {
		return nil, nil, fmt.Errorf("refused: this exchange already passed through %d chats", len(chain))
	}
	rateKey := codeChatChainKey(project.OwnerID, project.ProjectID)
	recent := codeChatRelay.sent[rateKey][:0]
	for _, at := range codeChatRelay.sent[rateKey] {
		if now.Sub(at) < codeChatRateWindow {
			recent = append(recent, at)
		}
	}
	if len(recent) >= codeChatRateLimit {
		codeChatRelay.sent[rateKey] = recent
		return nil, nil, fmt.Errorf("refused: the chats of this Code already sent %d messages to each other in the last hour", codeChatRateLimit)
	}
	codeChatRelay.sent[rateKey] = append(recent, now)
	entry := &codeChatInbound{chain: append(chain, to)}
	codeChatRelay.inbound[target.SessionID] = append(codeChatRelay.inbound[target.SessionID], entry)
	release = func() {
		codeChatRelay.Lock()
		defer codeChatRelay.Unlock()
		kept := codeChatRelay.inbound[target.SessionID][:0]
		for _, inbound := range codeChatRelay.inbound[target.SessionID] {
			if inbound != entry {
				kept = append(kept, inbound)
			}
		}
		if len(kept) == 0 {
			delete(codeChatRelay.inbound, target.SessionID)
		} else {
			codeChatRelay.inbound[target.SessionID] = kept
		}
	}
	return entry.chain, release, nil
}

func codeChatMessageText(from codeChat, message string, wantReply bool) string {
	ending := "Your final reply in this turn is sent back to that chat automatically; make it a clear, self-contained answer. Do not message it back with message_project_chat."
	if !wantReply {
		ending = "No reply is expected; it will not see your answer here."
	}
	return fmt.Sprintf("[Message from %q, another chat of this Code (same folder, same person). %s]\n\n%s", from.Name, ending, strings.TrimSpace(message))
}

// deliverCodeChatMessage runs the message as a turn in the target chat and
// returns its final reply.
func (api *StreamingAPI) deliverCodeChatMessage(ctx context.Context, userID string, project codeChatProject, target codeChat, text string) (string, error) {
	profile, err := api.agentProfiles.Resolve(codeproduct.ProfileID, 0, userID)
	if err != nil {
		return "", err
	}
	binding, owned, err := resolveConversationBindingForUser(ctx, userID, profile, target.Key)
	if err != nil || !owned || !workspacePathsMatchForUser(userID, binding.WorkspacePath, project.Workspace) {
		return "", fmt.Errorf("that chat is no longer available")
	}
	conversation, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, userID, profile, binding, "")
	if err != nil || conversation.SessionID != target.SessionID {
		return "", fmt.Errorf("that chat is no longer available")
	}
	reqMap, sessionID, _, err := productBotTurnRequest(ctx, userID, profile, conversation, services.BotIncomingMessage{Text: text}, services.ThreadID{})
	if err != nil {
		return "", err
	}
	// An interactive origin keeps the chat's coding session (no relaunch).
	reqMap["triggered_by"] = "code_chat"
	reqMap["triggered_by_label"] = "From " + project.Self.Name
	var result internalSessionTurnResult
	if codeChatTurn != nil {
		result, err = codeChatTurn(api, ctx, reqMap, sessionID, userID)
	} else {
		result, err = api.startSessionInternalWithResult(ctx, reqMap, sessionID, userID, nil)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.FinalResponse), nil
}

// registerCodeChatTools registers list_project_chats and message_project_chat
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
	tools := []struct {
		name, description string
		parameters        map[string]interface{}
		execute           func(context.Context, map[string]interface{}) (string, error)
	}{
		{"list_project_chats", "List the chats of this Code: the main chat and its side chats (tabs), with id, name, whether each is working now, and which one is this chat. Use it before message_project_chat.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			func(ctx context.Context, _ map[string]interface{}) (string, error) {
				ctx = trusted(ctx)
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				return jsonOut(map[string]interface{}{"chats": describe(project)})
			}},
		{"message_project_chat", "Send a message to another chat (tab) of this Code. It runs there as a turn, after that chat's current turn if it is busy, and the person sees it in that tab. The other chat shares this folder but not this conversation, so make the message self-contained. By default its final reply comes back to this chat as an [AUTO-NOTIFICATION]; end your turn after sending. Set reply=false for a hand-off that needs no answer. Only chats of this Code are reachable.", map[string]interface{}{
			"type": "object", "required": []string{"chat", "message"}, "properties": map[string]interface{}{
				"chat":    map[string]interface{}{"type": "string", "description": "The chat's id or name from list_project_chats (\"main\" for the main chat)."},
				"message": map[string]interface{}{"type": "string", "description": "Self-contained message or request."},
				"reply":   map[string]interface{}{"type": "boolean", "description": "Send its final reply back to this chat (default true)."},
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
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				chatArg, _ := args["chat"].(string)
				target, err := project.find(chatArg)
				if err != nil {
					return "", err
				}
				_, release, err := admitCodeChatMessage(project, target, time.Now())
				if err != nil {
					return "", err
				}
				busy := api.conversationTurnOccupied(target.SessionID)
				var notify func(string, error)
				if wantReply {
					notify, err = api.beginCodeChatReplyNotification(workspacePath, sessionID, userID, project, target)
					if err != nil {
						release()
						return "", err
					}
				}
				text := codeChatMessageText(project.Self, message, wantReply)
				go func() {
					defer release()
					runCtx, cancel := context.WithTimeout(internalBotRequestContext(context.Background(), userID), codeChatTurnLimit)
					defer cancel()
					reply, deliverErr := api.deliverCodeChatMessage(runCtx, userID, project, target, text)
					if deliverErr != nil {
						log.Printf("[CODE_CHAT] message from %s to %s failed: %v", project.Self.Key, target.Key, deliverErr)
					}
					if notify != nil {
						notify(reply, deliverErr)
					}
				}()
				out := map[string]interface{}{"to": map[string]interface{}{"id": target.ID, "name": target.Name}, "status": "delivered"}
				if busy {
					out["status"] = "queued"
					out["note"] = "That chat is busy; the message runs right after its current turn."
				}
				if wantReply {
					out["next"] = "End your turn; its reply arrives in this chat as an [AUTO-NOTIFICATION]."
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

// beginCodeChatReplyNotification registers a background execution in the
// sending chat; calling the returned func resumes that chat with the reply.
func (api *StreamingAPI) beginCodeChatReplyNotification(workspacePath, sessionID, userID string, project codeChatProject, target codeChat) (func(string, error), error) {
	if api == nil || api.bgAgentRegistry == nil {
		return nil, fmt.Errorf("replies cannot reach this chat; send with reply=false")
	}
	name := "Reply from " + target.Name
	executionID := "code-chat-" + api.bgAgentRegistry.NextID(name)
	parentExecutionID := api.currentConversationTurnExecutionID(sessionID)
	if strings.TrimSpace(parentExecutionID) == "" {
		parentExecutionID = "session:" + sessionID
	}
	notifier := &workshopExecutionBgNotifier{api: api, sessionID: sessionID, workspacePath: workspacePath, userID: userID}
	notifier.OnExecutionStart(todo_creation_human.WorkshopExecutionStart{
		ID: executionID, ParentExecutionID: parentExecutionID, Name: name, Kind: "trigger_auto_notify",
		// No Cancel: the other chat's turn is its own and is stopped in its tab.
		Metadata: map[string]string{"execution_type": "code-chat-reply", "target_chat": target.ID},
	})
	if registered := api.bgAgentRegistry.Get(sessionID, executionID); registered == nil || registered.GetStatus() == BGAgentCanceled {
		return nil, fmt.Errorf("replies cannot reach this chat; send with reply=false")
	}
	return func(reply string, err error) {
		header := fmt.Sprintf("Reply from %q (another chat of this Code)", target.Name)
		switch {
		case err != nil:
			notifier.OnExecutionComplete(executionID, name, "", nil, fmt.Errorf("%s: the message could not be answered: %w", header, err))
		case reply == "":
			notifier.OnExecutionComplete(executionID, name, "", nil, fmt.Errorf("%s: it finished without a reply", header))
		default:
			notifier.OnExecutionComplete(executionID, name, header+":\n\n"+truncateTriggerTargetResult(reply), nil, nil)
		}
	}, nil
}
