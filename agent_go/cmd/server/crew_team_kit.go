package server

import (
	"context"
	"fmt"
	"log"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// The Team kit (#3141, owner 18 Nov): one read-only view of everything a teammate's agent needs to work the Team's way:
// the Team instructions, skills, tools and keys, and setup steps. Each item says who shared it and who has it. It only
// reads what the Team already stores (the shared files, the Team's way and its setups); nothing here changes them, so
// adding, removing and progress stay with way_* and setup_*.

type crewKitPerson struct {
	ID   string `json:"person_id"`
	Name string `json:"name"`
	You  bool   `json:"you,omitempty"`
}

// crewKitHas is one person's state for one item: have, missing or unknown (their agent has not checked yet). Steps also
// carry how many checklist items are done.
type crewKitHas struct {
	Person string `json:"person_id"`
	State  string `json:"state"`
	Done   int    `json:"done,omitempty"`
	Total  int    `json:"total,omitempty"`
}

type crewKitItem struct {
	// Section is instructions, skills, tools or steps; Kind says what it is inside it: instructions, skill, mcp, app,
	// key, sign_in or step.
	Section string `json:"section"`
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Line    string `json:"line,omitempty"`
	By      string `json:"by,omitempty"`
	ByVerb  string `json:"by_verb,omitempty"`
	At      string `json:"at,omitempty"`
	// Everyone means every agent in the Team has it without doing anything (Team instructions, the Crew's skills, a
	// Team key or app); People is then empty.
	Everyone bool         `json:"everyone,omitempty"`
	People   []crewKitHas `json:"people,omitempty"`
	// Mine is the asker's own state: auto, have, missing, unknown, or empty when it is not for them.
	Mine     string   `json:"mine,omitempty"`
	SetupIDs []string `json:"setup_ids,omitempty"`
}

type crewKitView struct {
	People []crewKitPerson `json:"people"`
	Items  []crewKitItem   `json:"items"`
	// Have and Total count the items for the asker that need something; Missing is what Apply asks their agent to do.
	Have     int    `json:"have"`
	Total    int    `json:"total"`
	Missing  int    `json:"missing"`
	ApplyAsk string `json:"apply_ask,omitempty"`
}

// crewKitWorse keeps the state that still needs work when two setups list the same thing: missing, then unknown, then have.
func crewKitWorse(a, b string) string {
	rank := map[string]int{"": 0, "have": 1, "unknown": 2, "missing": 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func crewKitState(state string) string {
	switch state {
	case "installed", "added", "connected":
		return "have"
	case "missing":
		return "missing"
	}
	return "unknown"
}

func (api *StreamingAPI) crewTeamKit(ctx context.Context, claims *UserClaims, project string, team crewTeam) (crewKitView, error) {
	view := crewKitView{People: []crewKitPerson{}, Items: []crewKitItem{}}
	label := shareOwnerLabeler()
	members := []string{}
	for _, person := range append([]crewTeamPerson{{UserID: team.OwnerID}}, team.People...) {
		if !person.Invited && team.member(person.UserID) && crewTeamAccountActive(person.UserID) && !slices.Contains(members, person.UserID) {
			members = append(members, person.UserID)
		}
	}
	sort.SliceStable(members, func(i, j int) bool { return members[i] == claims.UserID && members[j] != claims.UserID })
	for _, person := range members {
		view.People = append(view.People, crewKitPerson{ID: person, Name: label(person), You: person == claims.UserID})
	}
	state, err := readCrewTeamCurrentState(project)
	if err != nil {
		return view, err
	}
	allowed := func(rel string) bool {
		token := claims.AccessToken
		return token == nil || token.FileGuard == nil || token.FileGuard.Allows(rel, false)
	}
	firstBy := func(prefix string) string {
		for _, event := range state.Events {
			if strings.HasPrefix(event.Path, prefix) && event.Actor != "" {
				return event.Actor
			}
		}
		return ""
	}

	// Team instructions: the Team's file, edited by any member, read live by every agent.
	if allowed(crewTeamInstructionsFile) {
		item := crewKitItem{Section: "instructions", Kind: "instructions", ID: crewTeamInstructionsFile, Name: "Team instructions", Everyone: true, Mine: "auto"}
		if v, ok := state.Versions[crewTeamInstructionsFile]; ok && v.File != nil {
			item.Line, item.By, item.ByVerb, item.At = "Every agent in the Team follows these.", label(v.Author), "Written by", v.ModifiedAt
		} else {
			item.Line = "None yet. The Team owner adds them by asking their agent."
		}
		view.Items = append(view.Items, item)
	}

	// The Crew's own skills travel with the Team, so every agent has them.
	skills := map[string]bool{}
	if setup, _, _, ok := api.crewSetupOf(ctx, team.OwnerID, project); ok {
		for _, name := range setup.SelectedSkills {
			skills[name] = true
		}
	}
	for rel, v := range state.Versions {
		if rest, ok := strings.CutPrefix(rel, "skills/"); ok && v.File != nil {
			if name, _, found := strings.Cut(rest, "/"); found && name != "" {
				skills[name] = true
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(skills)) {
		item := crewKitItem{Section: "skills", Kind: "skill", ID: "crew:" + name, Name: name, Line: "Part of this Crew; every agent in the Team has it.", Everyone: true, Mine: "auto"}
		if v, ok := state.Versions["skills/"+name+"/SKILL.md"]; ok {
			item.At = v.ModifiedAt
			if by := firstBy("skills/" + name + "/"); by != "" {
				item.By, item.ByVerb = label(by), "Shared by"
			} else if v.Author != "" {
				item.By, item.ByVerb = label(v.Author), "Changed by"
			}
		}
		view.Items = append(view.Items, item)
	}

	// Tools and keys the Team holds or each person signs in to (the Team's way).
	index := map[string]int{}
	if way, err := api.teamWayView(ctx, project, claims.UserID); err == nil {
		var signIns []teamWayItem
		for _, w := range way.Items {
			switch w.Kind {
			case "app":
				index["app:"+w.ID] = len(view.Items)
				view.Items = append(view.Items, crewKitItem{Section: "tools", Kind: "app", ID: "app:" + w.ID, Name: w.Name, Line: "The Team's own connection; every agent can use it.", By: w.AddedBy, ByVerb: "Added by", Everyone: true, Mine: "auto"})
			case "secret":
				index["key:"+w.ID] = len(view.Items)
				view.Items = append(view.Items, crewKitItem{Section: "tools", Kind: "key", ID: "key:" + w.ID, Name: w.Name, Line: "The Team holds this key; agents use it by name and nobody sees the value.", By: w.AddedBy, ByVerb: "Added by", Everyone: true, Mine: "auto"})
			case "needs_sign_in":
				signIns = append(signIns, w)
			}
		}
		if len(signIns) > 0 {
			vaults := crewKitPersonalVaults(ctx, members)
			for _, w := range signIns {
				item := crewKitItem{Section: "tools", Kind: "sign_in", ID: "sign_in:" + strings.ToLower(strings.TrimPrefix(w.ID, "custom:")), Name: w.Name, Line: "Each person signs in with their own account.", By: w.AddedBy, ByVerb: "Added by"}
				for _, person := range members {
					has := crewKitHas{Person: person, State: "unknown"}
					if list, ok := vaults[person]; ok {
						has.State = map[bool]string{true: "have", false: "missing"}[teamWayConnected(list, w.ID)]
					}
					item.People = append(item.People, has)
					if person == claims.UserID {
						item.Mine = has.State
					}
				}
				index[item.ID] = len(view.Items)
				view.Items = append(view.Items, item)
			}
		}
	}

	// Setups: each is a step, and the MCPs, skills, keys and sign-ins it lists are kit items of their own.
	data, err := readCrewChecklists(project)
	if err != nil {
		return view, err
	}
	var steps []crewKitItem
	for _, setup := range sortedCrewChecklists(data) {
		if !allowed(setup.Instruction.Path) {
			continue
		}
		current, versionErr := crewChecklistInstructionVersion(claims, team, setup.Instruction.Path, state)
		if versionErr != nil {
			current = 0
		}
		forPeople := []string{}
		for _, person := range members {
			if crewChecklistFor(setup, person) {
				forPeople = append(forPeople, person)
			}
		}
		at := ""
		if !setup.UpdatedAt.IsZero() {
			at = setup.UpdatedAt.UTC().Format(time.RFC3339)
		}
		step := crewKitItem{Section: "steps", Kind: "step", ID: "step:" + setup.ID, Name: setup.Title, By: label(setup.SharedBy), ByVerb: "Shared by", At: at, SetupIDs: []string{setup.ID}}
		if current == 0 {
			step.Line = "Instructions unavailable; ask the person who shared it."
		} else {
			step.Line = fmt.Sprintf("%d steps", len(setup.Items))
		}
		for _, person := range forPeople {
			done := crewChecklistDone(setup, setup.Progress[person], current)
			has := crewKitHas{Person: person, State: "missing", Done: done, Total: len(setup.Items)}
			if done >= len(setup.Items) {
				has.State = "have"
			}
			step.People = append(step.People, has)
			if person == claims.UserID {
				step.Mine = has.State
			}
		}
		steps = append(steps, step)
		if len(setup.Keys) == 0 || len(forPeople) == 0 {
			continue
		}
		needs := api.crewChecklistNeedStates(ctx, project, setup, forPeople, current)
		for k, key := range setup.Keys {
			kind, section, id, name := "key", "tools", "", key.Name
			switch {
			case key.Kind == "skill":
				kind, section = "skill", "skills"
			case key.Kind == "mcp":
				kind = "mcp"
			case key.Provider != "":
				kind, name = "sign_in", key.Provider
			}
			id = kind + ":" + strings.ToLower(strings.TrimPrefix(name, "custom:"))
			pos, seen := index[id]
			if !seen {
				pos = len(view.Items)
				index[id] = pos
				item := crewKitItem{Section: section, Kind: kind, ID: id, Name: name, By: label(setup.SharedBy), ByVerb: "Asked for by", At: at}
				switch kind {
				case "skill":
					item.Line = "Each person's agent installs it in their own app."
				case "mcp":
					item.Line = "An MCP tool each person's agent installs in their own app."
				case "sign_in":
					item.Line = "Each person signs in with their own account."
				default:
					item.Line = "A key each person keeps in their own Vault."
				}
				view.Items = append(view.Items, item)
			}
			item := &view.Items[pos]
			if !slices.Contains(item.SetupIDs, setup.ID) {
				item.SetupIDs = append(item.SetupIDs, setup.ID)
			}
			if item.Everyone {
				continue
			}
			for _, person := range forPeople {
				states := needs[person]
				if k >= len(states) {
					continue
				}
				next := crewKitState(states[k].State)
				i := slices.IndexFunc(item.People, func(h crewKitHas) bool { return h.Person == person })
				if i < 0 {
					item.People = append(item.People, crewKitHas{Person: person, State: next})
				} else {
					item.People[i].State = crewKitWorse(item.People[i].State, next)
				}
				if person == claims.UserID {
					item.Mine = crewKitWorse(item.Mine, next)
				}
			}
		}
	}
	view.Items = append(view.Items, steps...)

	for _, item := range view.Items {
		switch item.Mine {
		case "have":
			view.Have++
			view.Total++
		case "missing", "unknown":
			view.Missing++
			view.Total++
		}
	}
	if view.Missing > 0 {
		view.ApplyAsk = fmt.Sprintf("Set me up with the Team kit of Team %s. Read team_sync action=setup_kit (MCP: workspace action=team_setup_kit, crew_id=%s) to see what I am missing. For each setup step that is not done, read setup_get and do it; install the MCPs and skills it lists in my own app; connect the sign-ins I lack. Record what you checked with setup_checklist. Ask me only for a sign-in or key that only I can give, by name. Tell me what you set up and what still needs me.", project, project)
	}
	log.Printf("[TEAM_KIT] project=%q person=%q items=%d people=%d missing=%d", project, claims.UserID, len(view.Items), len(view.People), view.Missing)
	return view, nil
}

// crewKitPersonalVaults reads each member's own Vault list (names and sign-in states only, never values); a person
// whose Vault cannot be read is left out, so their sign-ins show as unknown.
func crewKitPersonalVaults(ctx context.Context, people []string) map[string]personalVaultList {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out := map[string]personalVaultList{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	for _, person := range people {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			if list, err := personalVaults(ctx, person); err == nil {
				mu.Lock()
				out[person] = list
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}
