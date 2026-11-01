package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// A Goal schedule that sends messages may run in one of its owner's Builder
// tabs or in its own conversation, with the same choices as a Crew schedule
// (projectRunDestinations). Plan, step, function and webhook runs always start
// a new run. Unlike a Crew, the Goal default stays a new conversation each fire
// (PLAT-871-28).

// runDestinationNew is the Goal default: a new conversation for every fire.
const runDestinationNew = "new"

// goalScheduleOwner is the person a Goal schedule runs as; its chats are theirs.
func goalScheduleOwner(manifest *WorkflowManifest, sched WorkflowSchedule) string {
	return firstNonEmptyTrimmed(sched.RunAs, workflowExecutionOwnerUserID(manifest))
}

type goalScheduleChat struct {
	Key, Name, SessionID string
	Main                 bool
}

// goalScheduleChats lists the owner's own Builder tabs of this Goal, main first.
func goalScheduleChats(ctx context.Context, owner, presetID, workspace string) ([]goalScheduleChat, error) {
	store := defaultProductConversationRegistryStore()
	records, err := store.projectChats(ctx, owner, goalChatProfile, presetID)
	if err != nil {
		return nil, err
	}
	identities, err := store.agentIdentities(ctx, owner, goalChatProfile, presetID)
	if err != nil {
		return nil, err
	}
	mainKey := crewOwnerDefaultChatKey(presetID, owner)
	history := chatHistorySessionsByID(owner, workspace)
	out := []goalScheduleChat{}
	for _, record := range records {
		if strings.Trim(record.WorkspacePath, "/") != strings.Trim(workspace, "/") || record.SessionID == "" {
			continue
		}
		name, _ := goalTabPresentation(record, identities[record.ConversationKey], history[record.SessionID])
		out = append(out, goalScheduleChat{Key: record.ConversationKey, Name: name, SessionID: record.SessionID, Main: record.ConversationKey == mainKey})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Main != out[j].Main {
			return out[i].Main
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// applyGoalScheduleDestination sets where a Goal schedule runs from the same
// run_destination and chat_id a Crew schedule takes, plus "new". Nothing sent
// leaves it unchanged. chat_id "" or "main" is the owner's main Builder chat.
func applyGoalScheduleDestination(ctx context.Context, manifest *WorkflowManifest, workspace string, sched *WorkflowSchedule, destination string, chatID *string) error {
	if manifest != nil && manifest.crewExecutable != nil {
		if destination != "" && destination != runDestinationChat {
			return fmt.Errorf("Crew automations always target a saved chat")
		}
		if chatID != nil {
			sched.ChatID = strings.TrimSpace(*chatID)
		}
		if sched.ChatID == "" {
			sched.ChatID = "main"
		}
		return validateExecutableSchedule(ctx, buildScheduleContext(workspace, manifest, *sched))
	}
	destination = strings.ToLower(strings.TrimSpace(destination))
	if destination == "" && chatID == nil {
		return nil
	}
	if destination == "" {
		destination = runDestinationChat
	}
	switch destination {
	case runDestinationNew:
		sched.ChatKey, sched.ChatID, sched.Conversation = "", "", ""
		return nil
	case runDestinationChat, runDestinationIsolated:
	default:
		return fmt.Errorf("run_destination must be %q, %q or %q", runDestinationNew, runDestinationChat, runDestinationIsolated)
	}
	if len(sched.Messages) == 0 {
		return fmt.Errorf("only a schedule that sends messages can run in a chat; a plan run always starts a new run")
	}
	if destination == runDestinationIsolated {
		sched.ChatKey, sched.ChatID, sched.Conversation = "", "", runDestinationIsolated
		return nil
	}
	owner := goalScheduleOwner(manifest, *sched)
	id := ""
	if chatID != nil {
		id = strings.TrimPrefix(strings.TrimSpace(*chatID), manifest.ID+codeChatSideMarker)
	}
	if id == "" || id == "main" {
		// The main chat is created on first use of the Builder; until then the schedule runs in a new conversation.
		sched.ChatKey = crewOwnerDefaultChatKey(manifest.ID, owner)
		sched.ChatID, sched.Conversation = sched.ChatKey, ""
		return nil
	}
	chats, err := goalScheduleChats(ctx, owner, manifest.ID, workspace)
	if err != nil {
		return err
	}
	for _, chat := range chats {
		if chat.Key == manifest.ID+codeChatSideMarker+id {
			sched.ChatKey, sched.ChatID, sched.Conversation = chat.Key, chat.Key, ""
			return nil
		}
	}
	return fmt.Errorf("chat_id %q is not one of the schedule owner's tabs of this Goal; list the tabs first, and use main for the main chat", id)
}

// goalScheduleDestinationView is what the Schedules screen and MCP show for a
// Goal message schedule; nothing for other schedules.
func goalScheduleDestinationView(manifest *WorkflowManifest, workspace string, sched WorkflowSchedule) (destination, chatKey, chatID string, choices []ScheduleRunDestination) {
	if manifest != nil && manifest.crewExecutable != nil {
		chatID = firstNonEmptyTrimmed(sched.ChatID, "main")
		choices = crewExecutableChatChoices(manifest)
		return runDestinationChat, "", chatID, choices
	}
	if manifest == nil || len(sched.Messages) == 0 || sched.ScheduleType == "webhook" || manifest.Kind == "relay" {
		return "", "", "", nil
	}
	owner := goalScheduleOwner(manifest, sched)
	mainKey := crewOwnerDefaultChatKey(manifest.ID, owner)
	switch {
	case sched.Conversation == runDestinationIsolated:
		destination = runDestinationIsolated
	case sched.ChatKey != "":
		destination, chatKey = runDestinationChat, sched.ChatKey
		if sched.ChatKey != mainKey {
			chatID = strings.TrimPrefix(sched.ChatKey, manifest.ID+codeChatSideMarker)
		}
	default:
		destination = runDestinationNew
	}
	choices = []ScheduleRunDestination{{ID: runDestinationNew, Label: "New conversation each time"}, {ID: "main", Label: "Main chat"}}
	if chats, err := goalScheduleChats(context.Background(), owner, manifest.ID, workspace); err == nil {
		for _, chat := range chats {
			if !chat.Main {
				choices = append(choices, ScheduleRunDestination{ID: strings.TrimPrefix(chat.Key, manifest.ID+codeChatSideMarker), Label: chat.Name})
			}
		}
	}
	return destination, chatKey, chatID, append(choices, ScheduleRunDestination{ID: runDestinationIsolated, Label: "Own conversation"})
}

// goalScheduleConversation is the session a Goal message schedule runs in when
// it has a chat or its own conversation; "" keeps a new session per fire. tab
// reports delivery into an owner's Builder tab. A tab that no longer exists
// falls back to a new conversation: a Crew falls back to its main chat, but a
// new conversation is the Goal default.
func goalScheduleConversation(ctx context.Context, sctx *ScheduleContext) (sessionID, title string, tab bool) {
	sched := sctx.Schedule
	if sctx.WebhookInput != nil || len(sched.Messages) == 0 || sched.WorkshopMode != "workshop" {
		return "", "", false
	}
	if sched.Conversation == runDestinationIsolated {
		// One stable session per schedule, so its history carries across fires.
		return "schedule-own--" + sched.ID + "_0", "", false
	}
	if sched.ChatKey == "" {
		return "", "", false
	}
	chats, err := goalScheduleChats(ctx, sctx.OwnerUserID, sctx.WorkflowID, sctx.WorkspacePath)
	if err == nil {
		for _, chat := range chats {
			if chat.Key == sched.ChatKey {
				return chat.SessionID, chat.Name, true
			}
		}
	}
	scheduleLogf("[SCHEDULER] schedule %s: chat %s is gone (err=%v); running in a new conversation", sched.ID, sched.ChatKey, err)
	return "", "", false
}
