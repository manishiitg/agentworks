package server

import (
	"strings"
	"unicode"
)

// Labels are scoped to this Team view. Account IDs, logins and admission never
// depend on these display labels. Load the directory once, not once per agent.
func crewTeamPersonLabels(t crewTeam) map[string]string {
	raw := map[string]string{}
	label := shareOwnerLabeler()
	raw[t.OwnerID] = label(t.OwnerID)
	for _, p := range t.People {
		raw[p.UserID] = label(p.UserID)
	}
	return crewTeamDistinctNames(raw)
}

func crewTeamDistinctNames(raw map[string]string) map[string]string {
	labels, counts := map[string]string{}, map[string]int{}
	for id, name := range raw {
		labels[id] = crewTeamFirstName(name)
		counts[strings.ToLower(labels[id])]++
	}
	for id, name := range raw {
		if counts[strings.ToLower(labels[id])] < 2 {
			continue
		}
		words := strings.Fields(name)
		if len(words) > 1 {
			initial := []rune(words[len(words)-1])
			labels[id] += " " + string(unicode.ToUpper(initial[0])) + "."
		} else {
			labels[id] = strings.SplitN(name, "@", 2)[0]
		}
	}
	return labels
}
