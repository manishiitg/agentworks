package server

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// crew_owners: who builds a Crew (crew_co_owners.go). One tool with an action, like Share. Listing is open to anyone who
// can see the Crew; adding and removing is for the MAIN owner only (owner, 2026-10-16) with crews:write consent.
// The main owner (the creator) can never be removed, so a Crew always has an owner.
const externalCrewOwnersTool = "crew_owners"

func externalCrewOwnersDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add(externalCrewOwnersTool, "List a Crew's owners (action=list), add a co-owner by email (action=add), remove one (action=remove), or see who is editing it now (action=lock_status; locks are kept in memory, so a server restart frees them). One owner edits at a time: the lock is taken by an owner's Builder chat or first change, freed when their chat ends or after 15 minutes without a change; memory, run results, asking and running are never locked. Co-owners build the Crew with its main owner: they change its instructions, skills, functions, settings and knowledge, share it, and can delete it. Only the main owner adds or removes owners. Everything still runs as the main owner. The main owner (the creator) cannot be removed. Listing needs crews:read; add and remove need crews:write and must come from the main owner.", true, false, map[string]any{
		"crew_id": externalString("Crew ID from crew action=list."),
		"action":  map[string]any{"type": "string", "enum": []any{"list", "add", "remove", "lock_status"}},
		"email":   externalString("The person's account email; add and remove only."),
	}, "crew_id", "action")
}

type crewOwnersState struct {
	Owner     runSharePerson   `json:"owner"`
	CoOwners  []runSharePerson `json:"co_owners"`
	CanManage bool             `json:"can_manage"`
	Note      string           `json:"note,omitempty"`
}

func crewOwnersStateFor(crew crewProjectBinding, callerID string) crewOwnersState {
	dir, _ := readUserDirectoryFile()
	state := crewOwnersState{CanManage: crewProjectOwnedByCallerStrict(callerID, crew.Binding.WorkspacePath), CoOwners: []runSharePerson{}}
	if people := peopleForIDs(dir, []string{crew.OwnerID}); len(people) == 1 {
		state.Owner = people[0]
	}
	state.CoOwners = peopleForIDs(dir, crewCoOwnerIDs(crew.Binding.WorkspacePath))
	return state
}

func (api *StreamingAPI) externalCrewOwnersCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	crewID, action, email := externalArg(args, "crew_id"), externalArg(args, "action"), strings.TrimSpace(externalArg(args, "email"))
	write := action == "add" || action == "remove"
	if action != "list" && action != "lock_status" && !write {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "action must be list, add, remove or lock_status.")
		return
	}
	if t := claims.AccessToken; t != nil {
		scope := "crews:read"
		if write {
			scope = "crews:write"
		}
		if !t.Allows(scope) || !t.AllowsCrew(crewID) {
			externalError(w, http.StatusForbidden, "insufficient_scope", "This connection does not allow "+action+" on this Crew's owners.")
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
	if !ok {
		externalError(w, http.StatusNotFound, "not_found", "Crew not found or not allowed for this connection.")
		return
	}
	if action == "lock_status" {
		externalJSON(w, crewEditLockStatus(crew.Binding.WorkspacePath, time.Now()))
		return
	}
	if !write {
		externalJSON(w, crewOwnersStateFor(crew, claims.UserID))
		return
	}
	state, status, code, message := api.changeCrewOwners(claims, crew, action, email)
	if status != 0 {
		externalError(w, status, code, message)
		return
	}
	externalJSON(w, state)
}

// changeCrewOwners applies one add or remove by the main owner. It returns the new state, or a refusal (status, code, plain message).
func (api *StreamingAPI) changeCrewOwners(claims *UserClaims, crew crewProjectBinding, action, email string) (crewOwnersState, int, string, string) {
	if !crewProjectOwnedByCallerStrict(claims.UserID, crew.Binding.WorkspacePath) {
		return crewOwnersState{}, http.StatusForbidden, "forbidden", "Only the main owner of this Crew can add or remove owners."
	}
	id, ok := projectIdentityOf(crew.Binding.WorkspacePath)
	if !ok || id.Product != "work" || !id.Shared {
		return crewOwnersState{}, http.StatusConflict, "unsupported", "This Crew is not at the shared Crew root yet, so it cannot have co-owners."
	}
	if email == "" {
		return crewOwnersState{}, http.StatusBadRequest, "invalid_arguments", "Give the person's email."
	}
	dir, _ := readUserDirectoryFile()
	people, unknown := resolveShareEmails(dir, []string{email})
	if len(people) != 1 {
		return crewOwnersState{}, http.StatusNotFound, "not_found", "No active account has the email " + strings.Join(unknown, ", ") + "."
	}
	target := people[0]
	if sanitizeUserIDForPath(target.ID) == sanitizeUserIDForPath(crew.OwnerID) {
		if action == "remove" {
			return crewOwnersState{}, http.StatusConflict, "creator", "The creator of a Crew cannot be removed as an owner."
		}
		return crewOwnersStateFor(crew, claims.UserID), 0, "", ""
	}
	registry := defaultProjectOwners()
	var changed bool
	var err error
	if action == "add" {
		changed, err = registry.AddCoOwner("work", id.Folder, target.ID)
	} else {
		changed, err = registry.RemoveCoOwner("work", id.Folder, target.ID)
	}
	if err != nil {
		log.Printf("[CREW_OWNERS] %s %s on %s failed: %v", action, target.ID, id.Folder, err)
		return crewOwnersState{}, http.StatusInternalServerError, "failed", "The owners could not be changed. Try again."
	}
	log.Printf("[CREW_OWNERS] crew=%s %s co_owner=%s by=%s changed=%t", id.Folder, action, target.ID, claims.UserID, changed)
	if action == "remove" && changed {
		crewEditLockRelease(crew.Binding.WorkspacePath, target.ID)
		api.stopRemovedOwnerSessions(target.ID, crew.Binding.WorkspacePath)
	}
	state := crewOwnersStateFor(crew, claims.UserID)
	if action == "add" && !changed {
		state.Note = fmt.Sprintf("%s is already an owner.", target.Email)
	}
	return state, 0, "", ""
}
