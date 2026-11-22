package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// When the owner changes one of their agents' Team settings (shared or Only me, Who, what
// teammates see, the owner's note), the agent's own chat gets one server-written line, so the agent knows things
// changed (owner, 16 Nov: "the agent should know things changed"). It starts no turn: the owner sees the line in the
// chat now, and the agent reads it with its next turn. Rapid changes become one line (a screen saves several fields
// in a row). Every save path ends in setCrewTeamAgentRule, setCrewTeamAgentAudience or setCrewTeamAgentChoice, so every
// screen and tool is covered. An agent on a Mac keeps its chat on the Mac and is not covered here.
const crewTeamNoticeWindow = 10 * time.Second

var crewTeamNotices = struct {
	mu      sync.Mutex
	timers  map[string]*time.Timer
	waiting map[string]string // session -> line the agent reads with its next turn
}{timers: map[string]*time.Timer{}, waiting: map[string]string{}}

// crewTeamAgentSettingsSentence is the agent's current Team settings in the plain words of the agent header's
// summary (crewShareAgentSummary, the one function for it). That summary is written to the owner, so the agent is
// told whose "you" it is.
func crewTeamAgentSettingsSentence(t crewTeam, person, key, name string) string {
	rule := t.agentRule(person, key)
	agent := crewTeamTabAgent{Name: name, InTeam: t.agentInTeam(person, key), Audience: rule.Audience, People: t.audiencePeople(rule), ChatShare: rule.ChatShare, WhenOthersAsk: rule.WhenOthersAsk}
	sentence := "Your owner changed your Team settings. Their summary (\"you\" is your owner): " + crewShareAgentSummary(t.On, agent)
	if note := strings.TrimSpace(rule.WhenOthersAsk); note != "" {
		sentence += " Your owner's note: " + note
	}
	return sentence
}

// noteCrewTeamAgentChanged is called after a saved change; the line is written once the owner has stopped changing
// settings for crewTeamNoticeWindow.
func noteCrewTeamAgentChanged(project, person, storedKey string) {
	key := project + "\x00" + person + "\x00" + storedKey
	crewTeamNotices.mu.Lock()
	defer crewTeamNotices.mu.Unlock()
	if timer := crewTeamNotices.timers[key]; timer != nil {
		timer.Reset(crewTeamNoticeWindow)
		return
	}
	crewTeamNotices.timers[key] = time.AfterFunc(crewTeamNoticeWindow, func() {
		crewTeamNotices.mu.Lock()
		delete(crewTeamNotices.timers, key)
		crewTeamNotices.mu.Unlock()
		postCrewTeamAgentNotice(project, person, storedKey)
	})
}

func postCrewTeamAgentNotice(project, person, storedKey string) {
	ctx := context.Background()
	t, err := readCrewTeam(project)
	registered, ok := t.Agents[storedKey]
	if err != nil || !ok || runningServerAPI == nil || runningServerAPI.eventStore == nil {
		log.Printf("[CREW_TEAM] project=%s event=agent_rule_notice agent=%s posted=false reason=unavailable", project, storedKey)
		return
	}
	session := ""
	for _, row := range memberTeamAgents(ctx, t, person, person) {
		if row.ChatKey == registered.ChatKey {
			session = row.sessionID
			break
		}
	}
	if session == "" {
		log.Printf("[CREW_TEAM] project=%s event=agent_rule_notice agent=%s posted=false reason=chat_not_on_server", project, storedKey)
		return
	}
	sentence := crewTeamAgentSettingsSentence(t, person, storedKey, registered.Name)
	now := time.Now()
	runningServerAPI.eventStore.AddEvent(session, storeevents.Event{
		ID: fmt.Sprintf("team-settings-notice:%d", now.UnixNano()), Type: "team_settings_notice", SessionID: session, Timestamp: now,
		ExecutionKind: "main_agent", ExecutionID: "main:" + session, ParentExecutionID: "session:" + session,
		Data: unifiedevents.NewAgentEvent(&unifiedevents.GenericEventData{
			BaseEventData: unifiedevents.BaseEventData{Timestamp: now},
			Data:          map[string]interface{}{"message": sentence},
		}),
	})
	crewTeamNotices.mu.Lock()
	crewTeamNotices.waiting[session] = sentence
	crewTeamNotices.mu.Unlock()
	log.Printf("[CREW_TEAM] project=%s event=agent_rule_notice agent=%s posted=true", project, storedKey)
}

// takeCrewTeamAgentNotice is the line the agent has not read yet, once.
func takeCrewTeamAgentNotice(session string) string {
	crewTeamNotices.mu.Lock()
	defer crewTeamNotices.mu.Unlock()
	line := crewTeamNotices.waiting[session]
	delete(crewTeamNotices.waiting, session)
	return line
}
