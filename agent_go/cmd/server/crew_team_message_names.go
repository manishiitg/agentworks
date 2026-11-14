package server

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Names select only current admitted rows in this Crew. A suggestion never
// changes a target: ambiguous or misspelled names require another explicit send.
func crewTeamMessageAgent(ctx context.Context, project, caller, requested string) (crewTeamAgent, string, error) {
	t, err := readCrewTeam(project)
	if err != nil || !crewTeamAgentsVisible(t, caller) {
		return crewTeamAgent{}, "", fmt.Errorf("This Crew's Team is not available to you")
	}
	type choice struct {
		agent        crewTeamAgent
		owner, label string
		distance     int
	}
	choices, matches := []choice{}, []choice{}
	requested = strings.TrimSpace(requested)
	if len([]rune(requested)) > 256 {
		return crewTeamAgent{}, "", fmt.Errorf("Use a Team agent name or id from messages action=list_agents")
	}
	normalized := func(s string) string { return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "’", "'")) }
	labels := crewTeamPersonLabels(t)
	for _, p := range teamAgentsByPerson(ctx, t, caller) {
		for _, a := range p.Agents {
			if !a.InTeam {
				continue
			}
			person := firstNonEmptyTrimmed(labels[p.UserID], p.Name)
			label := person + "'s " + firstNonEmptyTrimmed(a.Name, "Main")
			c := choice{agent: a, owner: p.UserID, label: label, distance: crewTeamNameDistance(normalized(requested), normalized(label))}
			if strings.HasPrefix(requested, crewTeamAgentIDPrefix) {
				c.distance = crewTeamNameDistance(requested, a.ID)
			}
			choices = append(choices, c)
			// p.You && requested == a.Tab: your own agent named by its tab id (a Mac agent messaging its online agent).
			if requested == a.ID || p.You && requested == a.Tab || normalized(requested) == normalized(a.Name) || normalized(requested) == normalized(label) || p.You && normalized(requested) == normalized("Your "+a.Name) {
				matches = append(matches, c)
			}
		}
	}
	slices.SortFunc(choices, func(a, b choice) int {
		if a.distance != b.distance {
			return a.distance - b.distance
		}
		return strings.Compare(a.label, b.label)
	})
	if len(matches) == 1 {
		return matches[0].agent, matches[0].owner, nil
	}
	describe := func(rows []choice) string {
		result := []string{}
		for _, c := range rows {
			result = append(result, fmt.Sprintf("%s (%s)", c.label, c.agent.ID))
			if len(result) == 3 {
				break
			}
		}
		return strings.Join(result, "; ")
	}
	if len(matches) > 1 {
		slices.SortFunc(matches, func(a, b choice) int { return strings.Compare(a.label+a.agent.ID, b.label+b.agent.ID) })
		return crewTeamAgent{}, "", fmt.Errorf("That name matches more than one Team agent. Choose: %s", describe(matches))
	}
	if len(choices) > 0 {
		return crewTeamAgent{}, "", fmt.Errorf("No current Team agent matches that address. Did you mean %s? Run messages action=list_agents for the current list", describe(choices))
	}
	return crewTeamAgent{}, "", fmt.Errorf("No agents are shared in this Crew's Team now. Run messages action=list_agents for the current list")
}

func crewTeamNameDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	previous := make([]int, len(y)+1)
	for i := range previous {
		previous[i] = i
	}
	for i, c := range x {
		next := make([]int, len(y)+1)
		next[0] = i + 1
		for j, d := range y {
			cost := 0
			if c != d {
				cost = 1
			}
			next[j+1] = min(previous[j+1]+1, next[j]+1, previous[j]+cost)
		}
		previous = next
	}
	return previous[len(y)]
}

func crewTeamShortMessageName(raw string) bool {
	raw = strings.TrimSpace(raw)
	return raw != "" && !strings.ContainsAny(raw, ":/\\")
}
