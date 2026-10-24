package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
)

// Running your own Code over the external API and MCP (owner decision 2026-10-10: test Code features, folder guards and
// the sandbox, without clicking). Four tools, merged into one `code` tool: list your Code projects, list a project's
// chats (tabs), ask a chat a question (a turn in that chat, like ask_crew) and read the project's state. They need the
// code:run scope, which is never in a default set, and they work on the caller's OWN Code only. Laptop tools are excluded; these turns run on the server. Every ask is logged.

func (api *StreamingAPI) externalCodeRunOwnProject(ctx context.Context, userID, projectID string) (productConversationBinding, string, bool) {
	projectID = strings.TrimSpace(projectID)
	if api == nil || api.agentProfiles == nil || projectID == "" || strings.ContainsAny(projectID, ":/") {
		return productConversationBinding{}, "", false
	}
	profile, err := api.agentProfiles.Resolve(workproduct.ProfileID, 0, userID)
	if err != nil {
		return productConversationBinding{}, "", false
	}
	binding, owned, err := resolveConversationBindingForUser(ctx, userID, profile, projectID)
	if err != nil || !owned {
		return productConversationBinding{}, "", false
	}
	raw, exists, err := readFileFromWorkspace(ctx, strings.TrimSuffix(strings.TrimSpace(binding.WorkspacePath), "/")+"/product.json")
	var manifest productProjectManifest
	if err != nil || !exists || json.Unmarshal([]byte(raw), &manifest) != nil || manifest.Product != workproduct.ProfileID || manifest.ID != projectID {
		return productConversationBinding{}, "", false
	}
	return binding, projectID, true
}

// externalCodeRunChats lists a project's chats: the main chat first, then the side chats, newest first.
func (api *StreamingAPI) externalCodeRunChats(ctx context.Context, userID, projectID string, binding productConversationBinding, profileIDs ...string) ([]codeChat, error) {
	profileID := workproduct.ProfileID
	if len(profileIDs) > 0 {
		profileID = profileIDs[0]
	}
	records, err := defaultProductConversationRegistryStore().projectChats(ctx, userID, profileID, projectID)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	if sessions, listErr := ListChatHistorySessions(userID, 200, 0, binding.WorkspacePath); listErr == nil {
		for _, session := range sessions {
			titles[session.SessionID] = strings.TrimSpace(session.Title)
		}
	}
	identities, _ := defaultProductConversationRegistryStore().agentIdentities(ctx, userID, profileID, projectID)
	var chats []codeChat
	for _, record := range records {
		if !workspacePathsMatchForUser(userID, record.WorkspacePath, binding.WorkspacePath) {
			continue
		}
		chat := codeChat{Key: record.ConversationKey, SessionID: record.SessionID, UpdatedAt: record.UpdatedAt}
		chat.ID = strings.TrimPrefix(record.ConversationKey, projectID+codeChatSideMarker)
		chat.IsMain = record.IsMainChat
		chat.Name = firstNonEmptyTrimmed(titles[record.SessionID], "Chat "+chat.ID)
		if chat.IsMain {
			chat.Name = "Main chat"
		}
		if identity, ok := identities[record.ConversationKey]; ok {
			chat.Name = firstNonEmptyTrimmed(identity.Name, chat.Name)
			chat.Purpose = identity.Purpose
		}
		chats = append(chats, chat)
	}
	sort.SliceStable(chats, func(i, j int) bool {
		if chats[i].IsMain != chats[j].IsMain {
			return chats[i].IsMain
		}
		return chats[i].UpdatedAt > chats[j].UpdatedAt
	})
	return chats, nil
}

// externalCodeRunRespond writes a recorded send's answer, adding fields to a successful JSON receipt.
func externalCodeRunRespond(w http.ResponseWriter, rec *externalMCPRecorder, extra map[string]any) {
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	var receipt map[string]any
	if status < 300 && extra != nil && json.Unmarshal(rec.body.Bytes(), &receipt) == nil {
		for k, v := range extra {
			receipt[k] = v
		}
		externalJSON(w, receipt)
		return
	}
	for k, v := range rec.header {
		w.Header()[k] = v
	}
	w.WriteHeader(status)
	_, _ = w.Write(rec.body.Bytes())
}

// codeTurnItem is one line of a Code chat as plain text.
type codeTurnItem struct {
	Seq    int    `json:"seq"`
	Turn   string `json:"turn,omitempty"`
	Role   string `json:"role"`
	Sender string `json:"sender,omitempty"` // you, assistant or tool
	Text   string `json:"text"`
	At     string `json:"at,omitempty"`
}

// codeChatTranscript reads a Code chat's saved events as plain lines: what was asked, the answer of each turn and the
// names of the tools used (never their output). after is the last seq already seen; the returned last is the newest seq.
func codeChatTranscript(ctx context.Context, userID, sessionID, workspacePath string, after, limit int) ([]codeTurnItem, int, error) {
	data, err := ReadChatHistoryConversation(userID, sessionID, workspacePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(strings.ToLower(err.Error()), "not found") {
			return nil, after, nil // a tab nobody has written to yet has nothing to read
		}
		return nil, after, err
	}
	var doc struct {
		Events []struct {
			Sequence  int    `json:"sequence"`
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Data      struct {
				Data struct {
					Content     string `json:"content"`
					FinalResult string `json:"final_result"`
					ToolName    string `json:"tool_name"`
					ServerName  string `json:"server_name"`
					Metadata    struct {
						TurnID         string `json:"turn_id"`
						SenderLabel    string `json:"sender_label"`
						DisplayContent string `json:"display_content"`
						Source         string `json:"source"`
					} `json:"metadata"`
				} `json:"data"`
			} `json:"data"`
		} `json:"ui_events"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, after, err
	}
	clip := func(s string) string {
		r := []rune(strings.TrimSpace(s))
		if len(r) > 8000 {
			return string(r[:8000]) + "…"
		}
		return string(r)
	}
	var items []codeTurnItem
	last := after
	for _, e := range doc.Events {
		if e.Sequence > last {
			last = e.Sequence
		}
		if e.Sequence <= after {
			continue
		}
		d := e.Data.Data
		switch e.Type {
		case "user_message":
			text, sender := d.Content, ""
			if d.Metadata.Source == "agent_message" {
				text, sender = d.Metadata.DisplayContent, d.Metadata.SenderLabel
			}
			items = append(items, codeTurnItem{Seq: e.Sequence, Turn: d.Metadata.TurnID, Role: "you", Sender: sender, Text: clip(text), At: e.Timestamp})
		case "unified_completion":
			if strings.TrimSpace(d.FinalResult) != "" {
				items = append(items, codeTurnItem{Seq: e.Sequence, Turn: d.Metadata.TurnID, Role: "assistant", Text: clip(d.FinalResult), At: e.Timestamp})
			}
		case "tool_call_start":
			if name := firstNonEmptyTrimmed(d.ToolName, d.ServerName); name != "" {
				items = append(items, codeTurnItem{Seq: e.Sequence, Turn: d.Metadata.TurnID, Role: "tool", Text: name, At: e.Timestamp})
			}
		}
	}
	if limit > 0 && len(items) > limit {
		items = items[len(items)-limit:]
	}
	return items, last, nil
}

// waitForCodeTurn waits for the turn that carries messageID (the delivered text names it) to answer, up to wait.
// It logs when the turn starts and when it answers, so a resume of a closed session is visible in the log.
func (api *StreamingAPI) waitForCodeTurn(ctx context.Context, userID, sessionID, workspacePath, projectID, chatID string, after int, messageID string, wait time.Duration) ([]codeTurnItem, bool) {
	started := time.Now()
	deadline := started.Add(wait)
	turn, announced := "", false
	for {
		items, _, err := codeChatTranscript(ctx, userID, sessionID, workspacePath, after, 0)
		if err == nil {
			for _, it := range items {
				if turn == "" && it.Role == "you" && messageID != "" && strings.Contains(it.Text, messageID) {
					turn = it.Turn
					if !announced {
						announced = true
						log.Printf("[CODE_RUN] turn started in Code %s chat %s for message %s after %s (turn %s)", projectID, chatID, messageID, time.Since(started).Round(time.Millisecond), turn)
					}
				}
				if turn != "" && it.Role == "assistant" && it.Turn == turn {
					log.Printf("[CODE_RUN] turn answered in Code %s chat %s for message %s after %s", projectID, chatID, messageID, time.Since(started).Round(time.Millisecond))
					return items, true
				}
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, false
		}
		time.Sleep(700 * time.Millisecond)
	}
}

// findCodeChat picks a tab by id or by name (case-insensitive). A name that two tabs share picks nothing.
func findCodeChat(chats []codeChat, key string) *codeChat {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	for i := range chats {
		if strings.EqualFold(key, codeChatMainName) && chats[i].IsMain {
			alias := chats[i]
			alias.Key, _, _ = strings.Cut(alias.Key, codeChatSideMarker)
			return &alias
		}
		if strings.EqualFold(chats[i].ID, key) {
			return &chats[i]
		}
	}
	var found *codeChat
	for i := range chats {
		if strings.EqualFold(strings.TrimSpace(chats[i].Name), key) {
			if found != nil {
				return nil
			}
			found = &chats[i]
		}
	}
	return found
}

// codeChatNameTaken reports whether a tab already has this name (any case), so two tabs are never one agent.
func codeChatNameTaken(chats []codeChat, name, exceptID string) bool {
	for _, c := range chats {
		if !strings.EqualFold(c.ID, exceptID) && strings.EqualFold(strings.TrimSpace(c.Name), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

// externalCallerTabName names the tab a call without a tab gets: the attached agent's name, else the app that signed in.
func externalCallerTabName(ctx context.Context, claims *UserClaims) string {
	token := claims.AccessToken
	if token == nil {
		return "Website"
	}
	name := strings.TrimSpace(token.BoundAgentName)
	if name == "" && strings.HasPrefix(token.ID, "oauth-") {
		if store, err := openMCPOAuthStore(); err == nil {
			defer store.Close()
			if grant, e := store.ActiveFamily(ctx, strings.TrimPrefix(token.ID, "oauth-")); e == nil {
				if grant.ClientID == cliOAuthClientID {
					name = "AgentWorks CLI"
				} else if client, e2 := store.Client(ctx, grant.ClientID); e2 == nil {
					name = strings.TrimSpace(client.Name)
				}
			}
		}
	}
	name = firstNonEmptyTrimmed(name, token.Name, "AI app")
	if title, err := normalizeChatHistoryTitle(name); err == nil && title != "" {
		return title
	}
	return "AI app"
}

// externalCodeRunOpenTab opens a new side tab, optionally named, as the + in the app does. It returns the tab id, or an
// HTTP status and plain message when it cannot.
func (api *StreamingAPI) externalCodeRunOpenTab(r *http.Request, claims *UserClaims, binding productConversationBinding, projectID string, chats []codeChat, chatName string, profileIDs ...string) (string, int, string) {
	profileID := workproduct.ProfileID
	if len(profileIDs) > 0 {
		profileID = profileIDs[0]
	}
	ids := make([]string, len(chats))
	mainID := codeChatMainName
	names := []string{}
	for i, c := range chats {
		ids[i] = c.ID
		if c.IsMain {
			mainID = c.ID
		} else {
			names = append(names, c.Name)
		}
	}
	if sideChatsOf(ids, mainID) >= externalCrewMaxSideChats {
		return "", http.StatusConflict, fmt.Sprintf("This project already has %d side tabs. Close one first (code action=close_chat); they are: %s.", externalCrewMaxSideChats, strings.Join(names, ", "))
	}
	if strings.TrimSpace(chatName) == "" {
		return "", http.StatusBadRequest, "Give the new tab a name, a few words saying what this agent is for."
	}
	if codeChatNameTaken(chats, chatName, "") {
		return "", http.StatusConflict, "A tab named " + chatName + " already exists in this project; use it, or pick another name."
	}
	raw := make([]byte, 4)
	_, _ = rand.Read(raw)
	id := hex.EncodeToString(raw)
	// The conversation route names the new tab (its agent identity and history title).
	status, body := scheduleHandler(r, api.handleResolveAgentProfileConversation, http.MethodPost, "/api/agent-profiles/code/conversation", map[string]string{"id": profileID}, nil, AgentProfileConversationRequest{ConversationKey: projectID + codeChatSideMarker + id, Name: chatName})
	if status >= 400 {
		return "", status, string(body)
	}
	return id, 0, ""
}

// setCodeTabName gives a Code tab its name as an agent identity, which is what the tab list and name lookups use.
func setCodeTabName(ctx context.Context, userID, conversationKey, name string, profileIDs ...string) error {
	profileID := workproduct.ProfileID
	if len(profileIDs) > 0 {
		profileID = profileIDs[0]
	}
	_, err := defaultProductConversationRegistryStore().setAgentIdentity(ctx, userID, profileID, conversationKey, &name, nil, "tab name")
	return err
}

func externalCodeRunAllowed(c *UserClaims) bool {
	return c != nil && userAllowedProduct(c, crewProfileID) && (c.AccessToken == nil || c.AccessToken.Allows("crews:write"))
}
