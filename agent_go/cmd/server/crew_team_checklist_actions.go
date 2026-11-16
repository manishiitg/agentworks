package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"sort"
	"unicode/utf8"
)

func crewChecklistAction(action string) bool {
	switch action {
	case "setup_list", "setup_get", "setup_share", "setup_edit", "setup_checklist", "setup_targets", "setup_apply":
		return true
	}
	return false
}

type crewChecklistInput struct {
	Title       string                   `json:"title"`
	Instruction crewChecklistInstruction `json:"instruction"`
	Items       []crewChecklistItem      `json:"items"`
	Keys        []crewChecklistKey       `json:"keys"`
	Assignees   []string                 `json:"assignees"`
}

// The HTTP/native/MCP adapters all reach this service after resolving the
// caller. The agent actor is a separate trusted argument, never an input field.
func (api *StreamingAPI) crewChecklistAction(ctx context.Context, claims *UserClaims, project, action string, args map[string]any, actor crewChecklistActor) (out any, err error) {
	defer func() {
		if err != nil {
			person := ""
			if claims != nil {
				person = claims.UserID
			}
			checklistOutcome(project, "", person, action, err)
		}
	}()
	if len(externalArg(args, "setup_id")) > 128 {
		return nil, fmt.Errorf("Choose a setup from setup_list")
	}
	write := action == "setup_share" || action == "setup_edit" || action == "setup_checklist" || action == "setup_apply"
	team, err := crewChecklistTeam(claims, project, write)
	if err != nil {
		return nil, err
	}
	switch action {
	case "setup_targets":
		targets, err := crewChecklistTargets(ctx, claims, project)
		if err == nil {
			log.Printf("[TEAM_SETUP] project=%q person=%q operation=targets outcome=done count=%d", project, claims.UserID, len(targets))
		}
		return map[string]any{"agents": targets}, err
	case "setup_apply":
		return api.applyCrewChecklist(ctx, claims, project, args)
	case "setup_share", "setup_edit":
		raw, err := json.Marshal(args["setup"])
		if err != nil {
			return nil, err
		}
		var input crewChecklistInput
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&input); err != nil {
			return nil, fmt.Errorf("Give the setup title, shared instruction file and checklist")
		}
		id := externalArg(args, "setup_id")
		if action == "setup_share" {
			id = ""
		}
		if action == "setup_edit" && id == "" {
			return nil, fmt.Errorf("Choose a setup to edit")
		}
		revision, err := crewChecklistUint(args, "setup_revision")
		if err != nil {
			return nil, err
		}
		value, err := saveCrewChecklist(claims, project, crewChecklist{ID: id, Title: input.Title, Instruction: input.Instruction, Items: input.Items, Keys: input.Keys, Assignees: input.Assignees}, revision)
		if err != nil {
			return nil, err
		}
		publishCrewPanelChanged(project)
		return map[string]any{"setup_id": value.ID, "revision": value.Revision}, nil
	case "setup_checklist":
		revision, err := crewChecklistUint(args, "setup_revision")
		if err != nil {
			return nil, err
		}
		instruction, err := crewChecklistUint(args, "instruction_revision")
		if err != nil {
			return nil, err
		}
		var done map[string]bool
		raw, _ := json.Marshal(args["done"])
		if json.Unmarshal(raw, &done) != nil || len(done) == 0 {
			return nil, fmt.Errorf("Give the checklist item IDs and whether each is done")
		}
		result, err := writeCrewChecklistProgress(claims, project, externalArg(args, "setup_id"), actor, revision, instruction, done)
		if err != nil {
			return nil, err
		}
		publishCrewPanelChanged(project)
		return result, nil
	case "setup_list", "setup_get":
		data, err := readCrewChecklists(project)
		if err != nil {
			return nil, err
		}
		state, err := readCrewTeamCurrentState(project)
		if err != nil {
			return nil, err
		}
		list := []map[string]any{}
		todo := 0
		// Resolve admitted count-only identities once per batch, never per setup.
		members := map[string]string{}
		candidates := append([]crewTeamPerson{{UserID: team.OwnerID}}, team.People...)
		for _, member := range candidates {
			if !member.Invited && team.member(member.UserID) && crewTeamAccountActive(member.UserID) {
				members[member.UserID] = shareOwnerLabel(member.UserID)
			}
		}
		for _, setup := range sortedCrewChecklists(data) {
			if token := claims.AccessToken; token != nil && token.FileGuard != nil && !token.FileGuard.Allows(setup.Instruction.Path, false) {
				continue
			}
			current, versionErr := crewChecklistInstructionVersion(claims, team, setup.Instruction.Path, state)
			if versionErr != nil {
				current = 0
			}
			done := crewChecklistDone(setup, setup.Progress[claims.UserID], current)
			forYou := crewChecklistFor(setup, claims.UserID)
			if forYou && done < len(setup.Items) {
				todo++
			}
			summary := map[string]any{"id": setup.ID, "title": setup.Title, "revision": setup.Revision, "instruction_revision": current, "done": done, "total": len(setup.Items), "instructions_available": current != 0, "for_you": forYou}
			people := []map[string]any{}
			assigned := []string{} // names; empty means everyone
			for person, name := range members {
				if !crewChecklistFor(setup, person) {
					continue
				}
				if len(setup.Assignees) > 0 {
					assigned = append(assigned, name)
				}
				people = append(people, map[string]any{"person_id": person, "name": name, "done": crewChecklistDone(setup, setup.Progress[person], current), "total": len(setup.Items)})
			}
			sort.Slice(people, func(i, j int) bool { return people[i]["person_id"].(string) < people[j]["person_id"].(string) })
			sort.Strings(assigned)
			summary["people"] = people
			summary["assigned_to"] = assigned
			if action == "setup_list" {
				list = append(list, summary)
				continue
			}
			if setup.ID != externalArg(args, "setup_id") {
				continue
			}
			if versionErr != nil {
				return nil, fmt.Errorf("This setup is unavailable; ask the sharer to restore its shared instruction file")
			}
			text, err := crewChecklistReadInstructions(project, setup.Instruction.Path, state)
			if err != nil {
				return nil, err
			}
			mine := map[string]bool{}
			p := setup.Progress[claims.UserID]
			if p.SetupRevision == setup.Revision && p.InstructionRevision == current {
				for key, value := range p.Done {
					mine[key] = value
				}
			}

			summary["shared_by"] = shareOwnerLabel(setup.SharedBy)
			summary["updated_at"] = setup.UpdatedAt
			summary["instruction"] = crewChecklistInstruction{Path: setup.Instruction.Path, Revision: current}
			summary["instructions"] = text
			summary["items"] = setup.Items
			summary["keys"] = setup.Keys
			summary["key_status"] = api.crewChecklistKeyStates(ctx, project, claims.UserID, setup.Keys)
			summary["my_items"] = mine
			// Presence of a required key reference does not claim it is configured.
			// Agents inspect way/Vault through their existing admitted tools.
			log.Printf("[TEAM_SETUP] project=%q setup=%q person=%q operation=get outcome=done", project, setup.ID, claims.UserID)
			return summary, nil
		}
		if action == "setup_get" {
			return nil, fmt.Errorf("That Team setup is unavailable")
		}
		log.Printf("[TEAM_SETUP] project=%q person=%q operation=list outcome=done count=%d", project, claims.UserID, len(list))
		return map[string]any{"setups": list, "to_do": todo}, nil
	}
	return nil, fmt.Errorf("Choose list, get, share, edit or update checklist")
}

func crewChecklistUint(args map[string]any, key string) (uint64, error) {
	if args[key] == nil {
		return 0, nil
	}
	raw, err := json.Marshal(args[key])
	if err != nil {
		return 0, err
	}
	var value uint64
	if json.Unmarshal(raw, &value) != nil {
		return 0, fmt.Errorf("Use the current setup revision from get")
	}
	return value, nil
}

func crewChecklistReadInstructions(project, rel string, state crewTeamFileState) (string, error) {
	dir, err := crewTeamContentDir(project)
	if err != nil {
		return "", err
	}
	file, err := openCrewTeamFile(filepath.Join(dir, "files"), rel, state.onPurpose())
	if err != nil {
		return "", fmt.Errorf("The shared instruction file is unavailable")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 64<<10+1))
	if err != nil {
		return "", err
	}
	if len(raw) > 64<<10 {
		return "", fmt.Errorf("Use an instruction document smaller than 64 KB")
	}
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return "", fmt.Errorf("Use UTF-8 Markdown instructions")
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != state.Versions[rel].File.Hash {
		return "", errCrewChecklistChanged
	}
	return string(raw), nil
}
