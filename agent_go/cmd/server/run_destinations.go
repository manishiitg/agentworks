package server

import (
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

// Where the messages of a schedule or a trigger run. One list, read by the screens
// and by the chat tools, so they offer the same choices:
//   - new: a new conversation for every run
//   - chat: the main chat, or one of the Crew's own chats (chat_id)
//   - isolated: one conversation of its own, kept for that schedule or trigger
//
// Saved runs with no messages have no chat, so none of this applies to them.
const (
	runDestinationNew      = "new"
	runDestinationChat     = "chat"
	runDestinationIsolated = "isolated"
)

type runDestinationChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

var (
	runDestinationChoiceNew      = runDestinationChoice{ID: runDestinationNew, Label: "New conversation each time"}
	runDestinationChoiceChat     = runDestinationChoice{ID: runDestinationChat, Label: "Main chat"}
	runDestinationChoiceIsolated = runDestinationChoice{ID: runDestinationIsolated, Label: "Its own chat"}
	// runDestinationChoices are the choices in screen order. The Crew's own chats fall between chat and isolated.
	runDestinationChoices = []runDestinationChoice{runDestinationChoiceNew, runDestinationChoiceChat, runDestinationChoiceIsolated}
)

// runDestinationEnum is the run_destination values a tool accepts.
func runDestinationEnum() []string {
	values := make([]string, 0, len(runDestinationChoices))
	for _, choice := range runDestinationChoices {
		values = append(values, choice.ID)
	}
	return values
}

// runDestinationEnumAny is the same values for the schemas that take []any.
func runDestinationEnumAny() []any {
	values := runDestinationEnum()
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

// runDestinationHelp is the one description every tool gives the run_destination argument.
const runDestinationHelp = `Where the messages run. "chat" (default): the Crew's main chat, or one of its chats with chat_id. "new": a new conversation for every run. "isolated": one conversation of its own, kept for this schedule or trigger.`

// A Crew schedule that sends a message offers two places (owner, 9 Nov): its own chat, a new chat for this schedule
// kept across runs and listed with the Crew's chats (the default), or an existing chat (Main or one of the Crew's
// chats). "new" still works for schedules saved with it, and for functions and webhooks, but is not offered here.
const messageScheduleDestinationHelp = `Where the message goes. "isolated" (the default): its own chat, a new chat for this schedule that every run continues, listed with the Crew's chats. "chat": an existing chat, the main chat, or one of the Crew's chats with chat_id. When the person did not say, ask them first: its own chat, or an existing chat (which one)?`

// messageScheduleDestinationEnum is the run_destination values the schedule tools offer for a message schedule.
func messageScheduleDestinationEnum() []string {
	return []string{runDestinationIsolated, runDestinationChat}
}

// crewMessageScheduleDefaultsToOwnChat reports a new Crew message schedule that names no place: no run_destination,
// no chat_id and no workflow. It then gets its own chat.
func crewMessageScheduleDefaultsToOwnChat(profileID, projectID, workflowID, destination, chatID string) bool {
	return isProjectProfileID(profileID) && strings.TrimSpace(projectID) != "" && strings.TrimSpace(workflowID) == "" &&
		strings.TrimSpace(destination) == "" && strings.TrimSpace(chatID) == ""
}

// runDestinationSetting is what a destination means for a saved schedule. A new
// conversation each time is an isolated conversation whose key changes every run.
type runDestinationSetting struct {
	Isolated bool
	New      bool
}

func parseRunDestination(value string) (runDestinationSetting, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", runDestinationChat:
		return runDestinationSetting{}, nil
	case runDestinationIsolated:
		return runDestinationSetting{Isolated: true}, nil
	case runDestinationNew:
		return runDestinationSetting{Isolated: true, New: true}, nil
	default:
		return runDestinationSetting{}, fmt.Errorf("run_destination must be %q, %q or %q", runDestinationNew, runDestinationChat, runDestinationIsolated)
	}
}

// value is the run_destination a setting is shown and saved as.
func (d runDestinationSetting) value() string {
	switch {
	case d.New:
		return runDestinationNew
	case d.Isolated:
		return runDestinationIsolated
	default:
		return runDestinationChat
	}
}

func (d runDestinationSetting) apply(schedule *productschedule.Schedule) {
	schedule.Isolated, schedule.New = d.Isolated, d.New
}

// runDestinationFromConversation is the run_destination a saved entry's conversation field stands for.
func runDestinationFromConversation(conversation string) string {
	switch conversation {
	case runDestinationNew, runDestinationIsolated:
		return conversation
	default:
		return runDestinationChat
	}
}

func scheduleRunDestination(schedule productschedule.Schedule) string {
	return runDestinationSetting{Isolated: schedule.Isolated, New: schedule.New}.value()
}
