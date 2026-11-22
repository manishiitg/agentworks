package server

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Who an In-team agent is shared with (owner, 15 Nov): the whole Team (the default) or chosen people in it. The
// choice lives on the agent's own rule; crewTeamReachRow and the Team's per-person agent rows read it, so discovery,
// every send/ask/read, the right panel, the live line and chat viewing all follow the same check. A person taken off
// the list loses access on their next request. The agent's own person always sees it.
const (
	crewTeamAudienceWholeTeam = "whole_team" // as tools and screens name it; stored as ""
	crewTeamAudienceChosen    = "chosen"
	crewTeamAudienceMaxPeople = 200
)

var (
	errCrewTeamAudienceUnknown = errors.New("choose whole_team or chosen")
	errCrewTeamAudiencePerson  = errors.New("choose people who are in this Team")
)

// chosen: the viewer may reach this agent under its audience. The whole Team is everyone; a chosen list is exactly
// those people (an empty chosen list is nobody else, never the whole Team).
func (r crewTeamAgentRule) chosen(viewer string) bool {
	return r.Audience != crewTeamAudienceChosen || slices.Contains(r.People, viewer)
}

// empty: no choice was made on this agent (every part at its default).
func (r crewTeamAgentRule) empty() bool {
	return r.WhenOthersAsk == "" && r.ChatShare == "" && r.Audience == "" && len(r.People) == 0
}

// audienceName is the audience as tools and screens name it.
func (r crewTeamAgentRule) audienceName() string {
	if r.Audience == crewTeamAudienceChosen {
		return crewTeamAudienceChosen
	}
	return crewTeamAudienceWholeTeam
}

// crewTeamAudiencePerson is one chosen person as the agent's own person sees the list.
type crewTeamAudiencePerson struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

// audiencePeople is the chosen list for display: only current members (people who left drop out on read).
func (t crewTeam) audiencePeople(r crewTeamAgentRule) []crewTeamAudiencePerson {
	out := []crewTeamAudiencePerson{}
	if r.Audience != crewTeamAudienceChosen {
		return out
	}
	labels := crewTeamPersonLabels(t)
	for _, id := range r.People {
		if t.member(id) {
			out = append(out, crewTeamAudiencePerson{UserID: id, Name: firstNonEmptyTrimmed(labels[id], crewPersonShortName(id))})
		}
	}
	return out
}

// agentChatShareFor is what this viewer sees of the agent's chat: its own choice, or Hidden when the viewer is not
// among its chosen people.
func (t crewTeam) agentChatShareFor(ctx context.Context, viewer, person, key string) string {
	rule := t.agentRule(person, crewTeamRuleKey(ctx, t, person, strings.TrimSpace(key)))
	if viewer != person && !rule.chosen(viewer) {
		return crewChatShareAgent
	}
	return rule.ChatShare
}

// setCrewTeamAgentAudience saves who one of the person's own agents is shared with. audience is whole_team or chosen
// ("" keeps it); people, when given, replaces the list and must name current members other than the person.
func setCrewTeamAgentAudience(ctx context.Context, project, person, key, audience string, people []string, setPeople bool) (crewTeamAgentRule, error) {
	if audience != "" && audience != crewTeamAudienceWholeTeam && audience != crewTeamAudienceChosen {
		return crewTeamAgentRule{}, errCrewTeamAudienceUnknown
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		return crewTeamAgentRule{}, err
	}
	unlock, err := wf.LockWorkspace(ctx, dir)
	if err != nil {
		return crewTeamAgentRule{}, err
	}
	defer unlock()
	t, stored, err := crewTeamOwnAgent(ctx, project, person, key)
	if err != nil {
		return crewTeamAgentRule{}, err
	}
	rule := t.agentRule(person, stored)
	before := rule
	if setPeople {
		list := []string{}
		for _, id := range people {
			id = strings.TrimSpace(id)
			if id == "" || id == person || slices.Contains(list, id) {
				continue
			}
			if !t.member(id) {
				return crewTeamAgentRule{}, errCrewTeamAudiencePerson
			}
			list = append(list, id)
		}
		if len(list) > crewTeamAudienceMaxPeople {
			return crewTeamAgentRule{}, errCrewTeamAudiencePerson
		}
		rule.People = list
	}
	switch audience {
	case crewTeamAudienceWholeTeam:
		rule.Audience, rule.People = "", nil
	case crewTeamAudienceChosen:
		rule.Audience = crewTeamAudienceChosen
	}
	if rule.Audience != crewTeamAudienceChosen {
		rule.People = nil // a list without Chosen people would be a second, unused audience
	}
	if err = updateCrewTeamRegisteredAgent(project, person, stored, func(a *crewTeamRegisteredAgent) { a.Rule = rule }); err != nil {
		return crewTeamAgentRule{}, err
	}
	crewMacTeamTabsCache.Delete(project + "\x00" + person)
	log.Printf("[CREW_TEAM] project=%s event=agent_audience user=%s agent=%s audience=%s people=%d revision=%d", project, logUsernameForUserID(person), stored, rule.audienceName(), len(rule.People), t.Revision)
	publishCrewPanelChanged(project)
	if !reflect.DeepEqual(rule, before) {
		noteCrewTeamAgentChanged(project, person, stored)
	}
	return rule, nil
}

// crewTeamNotChosenForAddress: the address is an In-team agent in one of the viewer's open Teams whose chosen people
// leave the viewer out. Only used to name a refusal's reason; it never admits anything.
func crewTeamNotChosenForAddress(ctx context.Context, viewer, address string) bool {
	root, err := crewroot.StateRoot()
	if err != nil || viewer == "" || address == "" {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(root, "team-folders"))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t, err := readCrewTeam(entry.Name())
		if err != nil || !crewTeamReachOpen(t, viewer) {
			continue
		}
		for _, a := range t.Agents {
			if !a.InTeam || a.OwnerID == viewer || a.Rule.chosen(viewer) {
				continue
			}
			if crewTeamAgentBridgeAddress(a.OwnerID, a.ID) == address {
				return true
			}
			if a.Kind == "terminal" {
				if named, err := currentCrewTeamTerminal(ctx, t, a); err == nil && "agent:"+shareOwnerLabel(a.OwnerID)+"/"+named.Name == address {
					return true
				}
			}
		}
	}
	return false
}
