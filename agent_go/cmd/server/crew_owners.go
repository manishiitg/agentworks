package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// Co-owners of a server Crew (owner, 21 Nov: "server Crews have co-owners and no Team"). Co-owners build the Crew like
// its main owner (crew_co_owners.go); only the main owner adds or removes them. The Access panel's Co-owners section
// (the dashboards endpoint: owners, owner_add, owner_remove) and the MCP tool crew_owners reach the same code.
const externalCrewOwnersTool = "crew_owners"

const crewOwnersServerOnly = "Only a Crew on the server has co-owners. People work on a Mac Crew with you through its Team."

func externalCrewOwnersDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add(externalCrewOwnersTool, "List, add or remove the co-owners of a Crew on the server. Co-owners build it like its owner: files, agents, memory, instructions and automations, and they see every chat in it. Only the Crew's owner adds or removes co-owners; the owner cannot be removed. Turns still run in the owner's server account.", true, false, map[string]any{
		"crew_id": externalString("Crew ID from crew action=list."),
		"action":  map[string]any{"type": "string", "enum": []any{"list", "add", "remove"}},
		"email":   externalString("The person's account email; add and remove only."),
	}, "crew_id", "action")
}

type crewOwnersState struct {
	Owner     runSharePerson   `json:"owner"`
	CoOwners  []runSharePerson `json:"co_owners"`
	CanManage bool             `json:"can_manage"`
	Note      string           `json:"note,omitempty"`
}

func crewOwnersStateFor(root, ownerID, callerID string) crewOwnersState {
	dir, _ := readUserDirectoryFile()
	state := crewOwnersState{CanManage: crewProjectOwnedByCallerStrict(callerID, root), CoOwners: []runSharePerson{}}
	if people := peopleForIDs(dir, []string{ownerID}); len(people) == 1 {
		state.Owner = people[0]
	}
	if people := peopleForIDs(dir, crewCoOwnerIDs(root)); people != nil {
		state.CoOwners = people
	}
	return state
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
		return crewOwnersState{}, http.StatusForbidden, "owner_only", "Only the Crew's owner adds or removes co-owners. Ask " + crewOwnerDisplayName(ownerID) + "."
	}
	if email == "" {
		return crewOwnersState{}, http.StatusBadRequest, "invalid_arguments", "Give the person's email."
	}
	dir, _ := readUserDirectoryFile()
	people, unknown := resolveShareEmails(dir, []string{email})
	if len(people) != 1 {
		return crewOwnersState{}, http.StatusNotFound, "not_found", strings.Join(unknown, ", ") + " hasn't signed in to AgentWorks yet. Ask them to sign in once, then add them."
	}
	target := people[0]
	if action == "add" && !crewTeamAccountActive(target.ID) {
		return crewOwnersState{}, http.StatusConflict, "inactive", firstNonEmptyTrimmed(target.Username, target.Email) + " can't use Crews on this server, so they can't be a co-owner. Ask an admin to give them Crew access first."
	}
	if sanitizeUserIDForPath(target.ID) == sanitizeUserIDForPath(ownerID) {
		if action == "remove" {
			return crewOwnersState{}, http.StatusConflict, "creator", "The Crew's owner cannot be removed."
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
		state.Note = fmt.Sprintf("%s can now build this Crew.", firstNonEmptyTrimmed(target.Username, target.Email))
	}
	return state, 0, "", ""
}

func (api *StreamingAPI) handleDashboardCrewOwners(w http.ResponseWriter, r *http.Request, claims *UserClaims, target dashboardTarget, action string, emails []string) {
	ref, ok := resolveCrewPath(r.Context(), claims.UserID, target.Root)
	if !ok || crewAccessFor(claims, ref) != crewAccessOwner {
		externalError(w, http.StatusNotFound, "not_found", "Crew not found or not allowed.")
		return
	}
	if !crewLivesOnServer(target.ID) {
		externalError(w, http.StatusConflict, "mac_crew", crewOwnersServerOnly)
		return
	}
	state := crewOwnersStateFor(ref.Root, ref.OwnerID, claims.UserID)
	if dashboardOwnersWrite(action) {
		if !dashboardAuthoringAllowed(claims, &target) {
			externalError(w, http.StatusForbidden, "insufficient_scope", "Changing co-owners needs the Crew's owner and existing authoring consent.")
			return
		}
		if len(emails) == 0 {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "Give the person's email.")
			return
		}
		for _, email := range emails {
			next, status, code, message := api.changeCrewOwners(claims, ref.Root, ref.OwnerID, strings.TrimPrefix(action, "owner_"), strings.TrimSpace(email))
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
	write := action == "add" || action == "remove"
	if action != "list" && !write {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "action must be list, add or remove.")
		return
	}
	if t := claims.AccessToken; t != nil {
		scope := "crews:read"
		if write {
			scope = "crews:write"
		}
		if !t.Allows(scope) || !t.AllowsCrew(crewID) {
			externalError(w, http.StatusForbidden, "insufficient_scope", "This connection does not allow "+action+" on this Crew's co-owners.")
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
		externalError(w, http.StatusNotFound, "not_found", "Crew not found or not allowed for this connection.")
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
	state, status, code, message := api.changeCrewOwners(claims, root, crew.OwnerID, action, email)
	if status != 0 {
		externalError(w, status, code, message)
		return
	}
	externalJSON(w, state)
}

// crewOwnerNamesFor names a server Crew's owner and co-owners other than the viewer, for the side panel's line.
func crewOwnerNamesFor(projectID, viewer string) []string {
	root := crewProjectRootByID(projectID)
	if root == "" {
		return nil
	}
	names := []string{}
	owner := ownerOfCrewProjectID(projectID)
	for _, id := range append([]string{owner}, crewCoOwnerIDs(root)...) {
		if id == "" || sanitizeUserIDForPath(id) == sanitizeUserIDForPath(viewer) {
			continue
		}
		names = append(names, crewOwnerDisplayName(id))
	}
	return names
}
