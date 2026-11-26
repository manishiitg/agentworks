package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// Co-owners and Users of a server Crew. Co-owners build the Crew like its main owner and change everything (owner, 21
// Nov: "server Crews have co-owners and no Team"; crew_co_owners.go). Users work in it through chat with the setup the
// owners made and cannot change that setup (owner, 25 Nov; crew_setup_lock.go). Only the main owner adds or removes
// either. The Access panel (the dashboards endpoint: owners, owner_add, owner_remove, user_add, user_remove,
// user_files) and the MCP tool crew_owners reach the same code.
const externalCrewOwnersTool = "crew_owners"

const crewOwnersServerOnly = "Only a project on the server has co-owners and users. People work on a Mac project with you through its Team."

func externalCrewOwnersDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add(externalCrewOwnersTool, "List, add or remove the co-owners and users of a project on the server. Co-owners build it like its owner and change everything: files, agents, memory, instructions, skills, settings, automations and sharing; they see every chat in it. Users work in it through chat with the setup you made: they see only their own chats (the project's dashboard opens from its own link), run on the project's model, and cannot change its instructions, skills, settings, automations or sharing, nor the extra locked_paths you name (user_files). A User can also be a whole Team: team_add puts a Team you are in (from list's team_choices) into the Users, so everyone in it works here as a User and its people's agents can ask this project's agents; team_remove takes it out; list shows, for each Team, the project's agents it can use and who each is shared with, and new_agents (how your new agents here start for that Team); team_agents shares every agent you made here with that Team at once (all_now=whole_team or only_me) and/or sets how your new ones start (new_agents=whole_team or only_me). Only the project's owner adds or removes people; the owner or a co-owner adds or removes a Team they are in; the owner cannot be removed. Give the person's account email or user id, or the Team's team_id.", true, false, map[string]any{
		"crew_id":      externalString("Project ID from crew action=list."),
		"action":       map[string]any{"type": "string", "enum": []any{"list", "add", "remove", "user_add", "user_remove", "user_files", "team_add", "team_remove", "team_agents"}},
		"email":        externalString("The person's account email or user id; every action except list and the team ones."),
		"team_id":      externalString("team_add, team_remove, team_agents: the Team's team_id from list (teams or team_choices)."),
		"new_agents":   externalString("team_agents: how your new agents in this project start for the Team: whole_team or only_me."),
		"all_now":      externalString("team_agents: share every agent you made in this project with the whole Team (whole_team) or nobody (only_me) now."),
		"locked_paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "user_files only: more files or folders (relative to the project) this user cannot change, besides the instructions, skills and settings. An empty list keeps only those."},
	}, "crew_id", "action")
}

type crewOwnersState struct {
	Owner     runSharePerson   `json:"owner"`
	CoOwners  []runSharePerson `json:"co_owners"`
	CanManage bool             `json:"can_manage"`
	Note      string           `json:"note,omitempty"`
	// Users work through chat with the project's setup (crew_setup_lock.go), each with the extra paths locked for them;
	// AlwaysLocked is what is locked for every User.
	Users        []crewProjectUserState `json:"users"`
	AlwaysLocked []string               `json:"always_locked"`
	// Teams are the Teams in the project's Users (#4087); TeamChoices the Teams the viewer may add (only the owner gets any).
	Teams       []crewProjectInTeam `json:"teams"`
	TeamChoices []crewProjectInTeam `json:"team_choices"`
}

type crewProjectUserState struct {
	runSharePerson
	LockedPaths []string `json:"locked_paths"`
}

func crewOwnersStateFor(root, ownerID, callerID string) crewOwnersState {
	dir, _ := readUserDirectoryFile()
	state := crewOwnersState{CanManage: crewProjectOwnedByCallerStrict(callerID, root), CoOwners: []runSharePerson{}, Users: []crewProjectUserState{}, AlwaysLocked: append([]string{}, crewSetupLockedDefaultPaths...)}
	if people := peopleForIDs(dir, []string{ownerID}); len(people) == 1 {
		state.Owner = people[0]
	}
	if people := peopleForIDs(dir, crewCoOwnersOnly(root)); people != nil {
		state.CoOwners = people
	}
	// The Users list shows the people added one by one; a Team's people show as the Team.
	rec, _ := crewOwnerRecordAt(root)
	direct := []string{}
	for _, id := range crewUserIDs(root) {
		if containsID(rec.Users, id) {
			direct = append(direct, id)
		}
	}
	for _, person := range peopleForIDs(dir, direct) {
		_, paths := crewSetupLockFor(root, person.ID)
		state.Users = append(state.Users, crewProjectUserState{runSharePerson: person, LockedPaths: append([]string{}, paths...)})
	}
	state.Teams, state.TeamChoices = crewProjectInTeams(rec.ProjectID, callerID), crewProjectTeamChoices(rec.ProjectID, callerID)
	if state.Teams == nil {
		state.Teams = []crewProjectInTeam{}
	}
	return state
}

// changeCrewTeamUsers adds a Team the owner or a co-owner is in to the project's Users, or removes one (#4087). The Team record keeps
// it (changeCrewTeamProject), with the same checks the Team panel's Remove uses.
func (api *StreamingAPI) changeCrewTeamUsers(claims *UserClaims, root, ownerID, action, team string) (crewOwnersState, int, string, string) {
	rec, found := crewOwnerRecordAt(root)
	if !found || !crewLivesOnServer(rec.ProjectID) {
		return crewOwnersState{}, http.StatusConflict, "mac_crew", crewOwnersServerOnly
	}
	team = strings.TrimSpace(team)
	if team == "" {
		return crewOwnersState{}, http.StatusBadRequest, "invalid_arguments", "Pick the Team (its team_id from list)."
	}
	verb := strings.TrimPrefix(action, "team_")
	if _, status, code, refusal := changeCrewTeamProject(team, claims.UserID, verb, rec.ProjectID); refusal != "" {
		return crewOwnersState{}, status, code, refusal
	}
	if verb == "remove" {
		if t, err := readCrewTeam(team); err == nil {
			for _, id := range append([]string{t.OwnerID}, crewTeamJoinedPeople(t)...) {
				if !crewIsProjectUser(root, id) && !crewIsCoOwner(root, id) {
					api.stopRemovedOwnerSessions(id, root)
				}
			}
		}
	}
	log.Printf("[CREW_OWNERS] project=%s event=%s team=%s by=%s", rec.ProjectID, action, team, logUsernameForUserID(claims.UserID))
	state := crewOwnersStateFor(root, ownerID, claims.UserID)
	name := crewTeamProjectName(team)
	if verb == "add" {
		state.Note = "Everyone in " + name + " can now work in this project through chat, as Users, and their agents can ask its agents."
	} else {
		state.Note = name + " is no longer in this project's Users."
	}
	return state, 0, "", ""
}

// resolveCrewPerson finds one active account by email or user id.
func resolveCrewPerson(value string) (runSharePerson, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return runSharePerson{}, false
	}
	dir, _ := readUserDirectoryFile()
	if people, _ := resolveShareEmails(dir, []string{value}); len(people) == 1 {
		return people[0], true
	}
	if dir != nil {
		if user := dir.byID(value); user != nil && !user.Disabled {
			return runSharePerson{ID: user.ID, Email: user.Email, Username: user.Username}, true
		}
	}
	return runSharePerson{}, false
}

// changeCrewUsers adds or removes a User, or sets a User's extra locked paths, by the Crew's main owner only.
func (api *StreamingAPI) changeCrewUsers(claims *UserClaims, root, ownerID, action, who string, lockedPaths []string) (crewOwnersState, int, string, string) {
	id, ok := projectIdentityOf(root)
	if !ok || !isProjectProfileID(id.Product) {
		return crewOwnersState{}, http.StatusConflict, "unsupported", crewOwnersServerOnly
	}
	rec, found := defaultProjectOwners().Lookup(id.Product, id.Folder)
	if !found || !crewLivesOnServer(rec.ProjectID) {
		return crewOwnersState{}, http.StatusConflict, "mac_crew", crewOwnersServerOnly
	}
	if !crewProjectOwnedByCallerStrict(claims.UserID, root) {
		return crewOwnersState{}, http.StatusForbidden, "owner_only", "Only the project's owner adds or removes users. Ask " + crewOwnerDisplayName(ownerID) + "."
	}
	target, ok := resolveCrewPerson(who)
	if !ok {
		return crewOwnersState{}, http.StatusNotFound, "not_found", "That person hasn't signed in to this server yet. Ask them to sign in once, then add them."
	}
	name := firstNonEmptyTrimmed(target.Username, target.Email)
	if sanitizeUserIDForPath(target.ID) == sanitizeUserIDForPath(ownerID) {
		return crewOwnersState{}, http.StatusConflict, "creator", "The project's owner builds it and cannot be a user."
	}
	if action == "user_add" && !crewTeamAccountActive(target.ID) {
		return crewOwnersState{}, http.StatusConflict, "inactive", name + " can't use projects on this server. Ask an admin to give them project access first."
	}
	storedID := target.ID
	for _, existing := range rec.Users {
		if sanitizeUserIDForPath(existing) == sanitizeUserIDForPath(target.ID) {
			storedID = existing
		}
	}
	var changed bool
	var err error
	switch action {
	case "user_add":
		changed, err = defaultProjectOwners().AddUser(id.Product, id.Folder, target.ID)
	case "user_remove":
		changed, err = defaultProjectOwners().RemoveUser(id.Product, id.Folder, storedID)
	case "user_files":
		if !crewIsProjectUser(root, target.ID) {
			return crewOwnersState{}, http.StatusNotFound, "not_user", name + " is not a user of this project. Add them as a user first."
		}
		changed, err = true, defaultProjectOwners().SetUserLockedPaths(id.Product, id.Folder, storedID, cleanCrewLockedPaths(lockedPaths))
	default:
		return crewOwnersState{}, http.StatusBadRequest, "invalid_arguments", "action must be user_add, user_remove or user_files."
	}
	if err != nil {
		log.Printf("[CREW_OWNERS] project=%s event=failed action=%s reason=%q", rec.ProjectID, action, err.Error())
		return crewOwnersState{}, http.StatusInternalServerError, "failed", "The change could not be saved. Try again."
	}
	log.Printf("[CREW_OWNERS] project=%s event=%s user=%s locked_paths=%d by=%s changed=%t", rec.ProjectID, action, logUsernameForUserID(target.ID), len(lockedPaths), logUsernameForUserID(claims.UserID), changed)
	if changed && action != "user_files" {
		if action == "user_remove" {
			api.stopRemovedOwnerSessions(target.ID, root)
		}
		// Moving someone from co-owner to user also takes them off the Crew vault's owners.
		if vaultConfigured() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			if _, err := api.ensureCrewVault(ctx, rec.ProjectID); err != nil {
				log.Printf("[CREW_VAULT] project=%s owners not updated after %s, queued: %v", rec.ProjectID, action, err)
				api.requestCrewVault(rec.ProjectID)
			}
			cancel()
		}
	}
	state := crewOwnersStateFor(root, ownerID, claims.UserID)
	switch {
	case action == "user_add" && !changed:
		state.Note = name + " is already a user."
	case action == "user_add":
		state.Note = name + " can now work in this project through chat, with the setup as you made it."
	case action == "user_files":
		state.Note = "Saved what " + name + " cannot change."
	}
	return state, 0, "", ""
}

// changeCrewOwners applies one add or remove by the Crew's main owner. It returns the new state, or a refusal (status,
// code, plain message).
func (api *StreamingAPI) changeCrewOwners(claims *UserClaims, root, ownerID, action, email string) (crewOwnersState, int, string, string) {
	id, ok := projectIdentityOf(root)
	if !ok || !isProjectProfileID(id.Product) {
		return crewOwnersState{}, http.StatusConflict, "unsupported", crewOwnersServerOnly
	}
	rec, found := defaultProjectOwners().Lookup(id.Product, id.Folder)
	if !found || !crewLivesOnServer(rec.ProjectID) {
		return crewOwnersState{}, http.StatusConflict, "mac_crew", crewOwnersServerOnly
	}
	if !crewProjectOwnedByCallerStrict(claims.UserID, root) {
		return crewOwnersState{}, http.StatusForbidden, "owner_only", "Only the project's owner adds or removes co-owners. Ask " + crewOwnerDisplayName(ownerID) + "."
	}
	if email == "" {
		return crewOwnersState{}, http.StatusBadRequest, "invalid_arguments", "Give the person's email."
	}
	target, found := resolveCrewPerson(email)
	if !found {
		return crewOwnersState{}, http.StatusNotFound, "not_found", "That person hasn't signed in to this server yet. Ask them to sign in once, then add them."
	}
	if action == "add" && !crewTeamAccountActive(target.ID) {
		return crewOwnersState{}, http.StatusConflict, "inactive", firstNonEmptyTrimmed(target.Username, target.Email) + " can't use projects on this server, so they can't be a co-owner. Ask an admin to give them project access first."
	}
	if sanitizeUserIDForPath(target.ID) == sanitizeUserIDForPath(ownerID) {
		if action == "remove" {
			return crewOwnersState{}, http.StatusConflict, "creator", "The project's owner cannot be removed."
		}
		return crewOwnersStateFor(root, ownerID, claims.UserID), 0, "", ""
	}
	var changed bool
	var err error
	if action == "add" {
		changed, err = defaultProjectOwners().AddCoOwner(id.Product, id.Folder, target.ID)
	} else {
		changed, err = defaultProjectOwners().RemoveCoOwner(id.Product, id.Folder, target.ID)
	}
	if err != nil {
		log.Printf("[CREW_OWNERS] project=%s event=failed action=%s reason=%q", rec.ProjectID, action, err.Error())
		return crewOwnersState{}, http.StatusInternalServerError, "failed", "The co-owners could not be changed. Try again."
	}
	log.Printf("[CREW_OWNERS] project=%s event=%s co_owner=%s by=%s changed=%t", rec.ProjectID, action, logUsernameForUserID(target.ID), logUsernameForUserID(claims.UserID), changed)
	if action == "remove" && changed {
		api.stopRemovedOwnerSessions(target.ID, root)
	}
	if changed && vaultConfigured() {
		// The Crew vault's owners follow at once, so a removed co-owner cannot change its keys afterwards. One Vault write
		// for one person's action; on failure the background queue tries again.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		if _, err := api.ensureCrewVault(ctx, rec.ProjectID); err != nil {
			log.Printf("[CREW_VAULT] project=%s owners not updated after %s, queued: %v", rec.ProjectID, action, err)
			api.requestCrewVault(rec.ProjectID)
		}
		cancel()
	}
	state := crewOwnersStateFor(root, ownerID, claims.UserID)
	if action == "add" && !changed {
		state.Note = fmt.Sprintf("%s is already a co-owner.", target.Email)
	} else if action == "add" {
		state.Note = fmt.Sprintf("%s can now build this project.", firstNonEmptyTrimmed(target.Username, target.Email))
	}
	return state, 0, "", ""
}

func (api *StreamingAPI) handleDashboardCrewOwners(w http.ResponseWriter, r *http.Request, claims *UserClaims, target dashboardTarget, action string, emails []string, lockedPaths []string) {
	ref, ok := resolveCrewPath(r.Context(), claims.UserID, target.Root)
	if !ok || crewAccessFor(claims, ref) != crewAccessOwner {
		externalError(w, http.StatusNotFound, "not_found", "Project not found or not allowed.")
		return
	}
	if !crewLivesOnServer(target.ID) {
		externalError(w, http.StatusConflict, "mac_crew", crewOwnersServerOnly)
		return
	}
	state := crewOwnersStateFor(ref.Root, ref.OwnerID, claims.UserID)
	if dashboardOwnersWrite(action) {
		if !dashboardAuthoringAllowed(claims, &target) {
			externalError(w, http.StatusForbidden, "insufficient_scope", "Changing co-owners and users needs the project's owner and existing authoring consent.")
			return
		}
		if len(emails) == 0 {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "Pick the person.")
			return
		}
		for _, who := range emails {
			var next crewOwnersState
			var status int
			var code, message string
			if strings.HasPrefix(action, "team_") {
				next, status, code, message = api.changeCrewTeamUsers(claims, ref.Root, ref.OwnerID, action, who)
			} else if strings.HasPrefix(action, "user_") {
				next, status, code, message = api.changeCrewUsers(claims, ref.Root, ref.OwnerID, action, strings.TrimSpace(who), lockedPaths)
			} else {
				next, status, code, message = api.changeCrewOwners(claims, ref.Root, ref.OwnerID, strings.TrimPrefix(action, "owner_"), strings.TrimSpace(who))
			}
			if status != 0 {
				externalError(w, status, code, message)
				return
			}
			state = next
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

func (api *StreamingAPI) externalCrewOwnersCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	crewID, action, email := externalArg(args, "crew_id"), externalArg(args, "action"), strings.TrimSpace(externalArg(args, "email"))
	write := action == "add" || action == "remove" || strings.HasPrefix(action, "user_") || action == "team_add" || action == "team_remove"
	if action != "list" && !write {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "action must be list, add, remove, user_add, user_remove, user_files, team_add, team_remove or team_agents.")
		return
	}
	if t := claims.AccessToken; t != nil {
		scope := "crews:read"
		if write {
			scope = "crews:write"
		}
		if !t.Allows(scope) || !t.AllowsCrew(crewID) {
			externalError(w, http.StatusForbidden, "insufficient_scope", "This connection does not allow "+action+" on this project's co-owners and users.")
			return
		}
	}
	if write {
		if status, message := externalCrewAuthorGate(claims); status != 0 {
			externalError(w, status, "forbidden", message)
			return
		}
	}
	crew, _, _, ok := api.externalCrewResolve(r.Context(), claims, crewID)
	if !ok || !crew.OwnedByCaller {
		externalError(w, http.StatusNotFound, "not_found", "Project not found or not allowed for this connection.")
		return
	}
	root := crew.Binding.WorkspacePath
	if !write {
		if rec, found := crewOwnerRecordAt(root); !found || !crewLivesOnServer(rec.ProjectID) {
			externalError(w, http.StatusConflict, "mac_crew", crewOwnersServerOnly)
			return
		}
		externalJSON(w, crewOwnersStateFor(root, crew.OwnerID, claims.UserID))
		return
	}
	var state crewOwnersState
	var status int
	var code, message string
	if action == "team_agents" {
		rec, found := crewOwnerRecordAt(root)
		if !found || !crewLivesOnServer(rec.ProjectID) {
			externalError(w, http.StatusConflict, "mac_crew", crewOwnersServerOnly)
			return
		}
		_, err := setCrewTeamOnlineAgentsSharing(r.Context(), externalArg(args, "team_id"), rec.ProjectID, claims.UserID, externalArg(args, "new_agents"), externalArg(args, "all_now"))
		switch {
		case errors.Is(err, errCrewTeamOnlineAgentsChoice):
			externalError(w, http.StatusBadRequest, "invalid_arguments", "Give new_agents and/or all_now as whole_team or only_me.")
			return
		case err != nil:
			externalError(w, http.StatusForbidden, "not_in_team", "Choose a Team from list's teams that uses this project.")
			return
		}
		state = crewOwnersStateFor(root, crew.OwnerID, claims.UserID)
	} else if strings.HasPrefix(action, "team_") {
		state, status, code, message = api.changeCrewTeamUsers(claims, root, crew.OwnerID, action, externalArg(args, "team_id"))
	} else if strings.HasPrefix(action, "user_") {
		var paths []string
		if list, ok := args["locked_paths"].([]any); ok {
			for _, item := range list {
				if p, ok := item.(string); ok {
					paths = append(paths, p)
				}
			}
		}
		state, status, code, message = api.changeCrewUsers(claims, root, crew.OwnerID, action, email, paths)
	} else {
		state, status, code, message = api.changeCrewOwners(claims, root, crew.OwnerID, action, email)
	}
	if status != 0 {
		externalError(w, status, code, message)
		return
	}
	externalJSON(w, state)
}

// crewOwnerNamesFor names a server Crew's owner and co-owners (not its Users) other than the viewer, for the side panel's line.
func crewOwnerNamesFor(projectID, viewer string) []string {
	root := crewProjectRootByID(projectID)
	if root == "" {
		return nil
	}
	names := []string{}
	owner := ownerOfCrewProjectID(projectID)
	for _, id := range append([]string{owner}, crewCoOwnersOnly(root)...) {
		if id == "" || sanitizeUserIDForPath(id) == sanitizeUserIDForPath(viewer) {
			continue
		}
		names = append(names, crewOwnerDisplayName(id))
	}
	return names
}
