package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Sharing a dashboard (PLAT-750, share = dashboard). There is one access source
// per product and this edits it, so Ask, functions, dashboards and MCP keep
// using their existing checks and no second path can drift:
//
//   - Crew: the owner registry's Private flag (everyone who uses the Crew
//     product, or the owner alone).
//   - Workflow and Relay: WorkflowAccess.Readers (the people listed) plus
//     RunEveryone (every account that may use that product).
//
// A share includes the dashboard, Ask, published functions and existing MCP
// reads of product files, runs and logs. Secret values and private logs remain
// excluded. Nothing becomes visible to more people unless its owner shares it.

const runShareAccessDescription = "People you share with open its dashboard, can ask it, use its published functions, read and message selected existing agents, and read its files, runs and logs through MCP. Secret values and private logs stay excluded."

const (
	runShareEveryone = "everyone"
	runSharePeople   = "people"
	runSharePrivate  = "private"
)

type runSharePerson struct {
	ID       string `json:"id"`
	Email    string `json:"email,omitempty"`
	Username string `json:"username,omitempty"`
}

type runShareAccount struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Use  string `json:"use"`
}

type runShareState struct {
	Accounts []runShareAccount `json:"accounts,omitempty"`
	Mode     string            `json:"mode"`
	People   []runSharePerson  `json:"people,omitempty"`
	Unknown  []string          `json:"unknown_emails,omitempty"`
	// Previous is who had access before this change, so the owner or agent sees exactly who was removed.
	Previous *runShareState `json:"previous,omitempty"`
	// CanShare tells the screen whether to offer sharing: only the project's owner can.
	CanShare       bool `json:"can_share"`
	NeedsDashboard bool `json:"needs_dashboard,omitempty"`
	// Note says anything the owner should know about what the share did.
	Note   string            `json:"note,omitempty"`
	Agents []sharedCrewAgent `json:"agents,omitempty"`
}

func dashboardShareAction(action string) bool { return action == "share" || action == "share_status" }

// resolveShareEmails maps emails to active accounts. An email with no active
// account is reported back, never dropped silently.
func resolveShareEmails(dir *userDirectory, emails []string) (people []runSharePerson, unknown []string) {
	seen := map[string]bool{}
	for _, raw := range emails {
		email := strings.ToLower(strings.TrimSpace(raw))
		if email == "" || seen[email] {
			continue
		}
		seen[email] = true
		var user *UserRecord
		if dir != nil {
			user = dir.byEmail(email)
		}
		if user == nil || user.Disabled {
			unknown = append(unknown, email)
			continue
		}
		people = append(people, runSharePerson{ID: user.ID, Email: user.Email, Username: user.Username})
	}
	sort.Strings(unknown)
	return people, unknown
}

func peopleForIDs(dir *userDirectory, ids []string) []runSharePerson {
	out := make([]runSharePerson, 0, len(ids))
	for _, id := range ids {
		person := runSharePerson{ID: id}
		if dir != nil {
			if user := dir.byID(id); user != nil {
				person.Email, person.Username = user.Email, user.Username
			}
		}
		out = append(out, person)
	}
	return out
}

// workflowRunEveryone: a workflow or Relay its owner opened to every account that may use the product.
func workflowRunEveryone(claims *UserClaims, m *WorkflowManifest, product string) bool {
	if claims == nil || m == nil || m.Access == nil || !m.Access.RunEveryone {
		return false
	}
	access := userAccessForClaims(claims)
	return !access.Disabled && access.Known && userAllowedProduct(claims, product)
}

// dashboardShareOwner reports whether the caller owns the project behind the dashboard (sharing is the owner's act).
func (api *StreamingAPI) dashboardShareOwner(ctx context.Context, claims *UserClaims, target dashboardTarget) bool {
	switch target.Kind {
	case "crew":
		crew, ok := resolveCrewPath(ctx, claims.UserID, target.Root)
		return ok && crewAccessFor(claims, crew) == crewAccessOwner
	case "workflow", "relay":
		access, _ := workflowAccessForWorkspacePath(ctx, claims, target.Root)
		return access == WorkflowAccessOwner
	}
	return false
}

func (api *StreamingAPI) readRunShare(ctx context.Context, claims *UserClaims, target dashboardTarget) (runShareState, error) {
	state := runShareState{Mode: runSharePrivate, NeedsDashboard: target.Kind == "crew" && !hasPublishedDashboard(ctx, target.Root)}
	switch target.Kind {
	case "crew":
		if crewSharedWithOthers(target.Root) {
			state.Mode = runShareEveryone
		} else if id, ok := projectIdentityOf(target.Root); ok && isProjectProfileID(id.Product) {
			if rec, found := defaultProjectOwners().Lookup(id.Product, id.Folder); found && len(rec.RunPeople) > 0 {
				state.Mode = runSharePeople
				dir, _ := readUserDirectoryFile()
				state.People = peopleForIDs(dir, rec.RunPeople)
			}
		}
	case "workflow", "relay":
		manifest, found, err := ReadWorkflowManifest(ctx, target.Root)
		if err != nil || !found || manifest == nil {
			return state, dashboardFailure(404, "project unavailable")
		}
		dir, _ := readUserDirectoryFile()
		switch {
		case manifest.Access != nil && manifest.Access.RunEveryone:
			state.Mode = runShareEveryone
		case len(manifest.effectiveReaders()) > 0:
			state.Mode = runSharePeople
			state.People = peopleForIDs(dir, manifest.effectiveReaders())
		}
	default:
		return state, dashboardFailure(404, "sharing is not available for this kind of project")
	}
	if target.Kind == "crew" {
		var err error
		state.Agents, err = crewShareAgents(ctx, claims.UserID, target.Root, crewProjectOwnedByCaller(claims.UserID, target.Root))
		if err != nil {
			return state, err
		}
	}
	if (target.Kind == "crew") && api.dashboardShareOwner(ctx, claims, target) {
		state.Accounts = api.workspaceShareAccounts(ctx, target)
	}
	return state, nil
}

func logDashboardShareRefusal(claims *UserClaims, target dashboardTarget, mode, stage string, err, cause error) {
	user := ""
	if claims != nil {
		user = sanitizeUserIDForPath(claims.UserID)
	}
	if cause == nil {
		cause = err
	}
	log.Printf("[RUN_SHARE] refused user_id=%q kind=%q target=%q root=%q mode=%q stage=%q status=%d cause_status=%d reason=%q", user, target.Kind, target.ID, target.Root, mode, stage, wf.StatusCode(err), wf.StatusCode(cause), cause.Error())
}

func (api *StreamingAPI) applyRunShare(ctx context.Context, claims *UserClaims, target dashboardTarget, mode string, emails []string, createDashboard bool, changes ...crewAgentShareChange) (state runShareState, err error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	stage := "mode"
	var cause error
	defer func() {
		if err != nil {
			logDashboardShareRefusal(claims, target, mode, stage, err, cause)
		}
	}()
	if mode != runShareEveryone && mode != runSharePeople && mode != runSharePrivate {
		return runShareState{}, dashboardFailure(400, "mode must be everyone, people or private")
	}
	// Named people with nobody named is the owners alone: the screen sends the list, the server decides the mode.
	if mode == runSharePeople && !slices.ContainsFunc(emails, func(email string) bool { return strings.TrimSpace(email) != "" }) {
		mode = runSharePrivate
	}
	stage = "owner"

	if !api.dashboardShareOwner(ctx, claims, target) {
		return runShareState{}, dashboardFailure(403, "only the owner can share this project")
	}
	// Sharing is sharing the dashboard: there must be one published to share. Making it private needs none.
	madeDefault := false
	stage = "dashboard"
	if mode != runSharePrivate && !hasPublishedDashboard(ctx, target.Root) {
		if target.Kind == "crew" && !createDashboard {
			return runShareState{NeedsDashboard: true}, dashboardFailure(409, "Create a dashboard for this Crew before sharing it. Ask the owner whether to create a standard dashboard, then retry with create_dashboard=true only if they agree.")
		}
		if _, generated, defaultErr := api.ensureDefaultDashboard(ctx, claims, target); defaultErr != nil || !hasPublishedDashboard(ctx, target.Root) {
			cause = defaultErr
			return runShareState{}, dashboardFailure(409, "this project has no published dashboard and a standard one could not be made; build and publish a dashboard, then share it")
		} else {
			madeDefault = generated
		}
	}
	before, _ := api.readRunShare(ctx, claims, target)
	stage = "save"
	state, err = api.applyRunShareChange(ctx, claims, target, mode, emails, changes...)
	if err == nil {
		state.Previous = &before
		if madeDefault {
			state.Note = "Shared its standard dashboard; you can build your own later."
		}
	}
	return state, err
}

func (api *StreamingAPI) applyRunShareChange(ctx context.Context, claims *UserClaims, target dashboardTarget, mode string, emails []string, changes ...crewAgentShareChange) (runShareState, error) {
	switch target.Kind {
	case "crew":
		crew, ok := resolveCrewPath(ctx, claims.UserID, target.Root)
		if !ok {
			return runShareState{}, dashboardFailure(404, "project unavailable")
		}
		id, ok := projectIdentityOf(crew.Root)
		if !ok || !isProjectProfileID(id.Product) {
			return runShareState{}, dashboardFailure(404, "project unavailable")
		}
		var ids []string
		var unknown []string
		if mode == runSharePeople {
			dir, _ := readUserDirectoryFile()
			people, missing := resolveShareEmails(dir, emails)
			unknown = missing
			if len(people) == 0 {
				return runShareState{Mode: runSharePeople, Unknown: unknown}, dashboardFailure(400, "none of those emails match an active account"+unknownSuffix(unknown))
			}
			for _, person := range people {
				ids = append(ids, person.ID)
			}
		}
		keys := crewSharedAgentKeys(target.Root)
		if len(changes) > 0 && (changes[0].Agents != nil || len(changes[0].Purposes) > 0) {
			var err error
			keys, err = validateCrewAgentShare(ctx, claims.UserID, target.Root, changes[0])
			if err != nil {
				return runShareState{}, dashboardFailure(400, err.Error())
			}
		}
		registry := defaultProjectOwners()
		if err := registry.SetAudience(id.Product, id.Folder, mode != runShareEveryone, ids, keys); err != nil {
			return runShareState{}, dashboardFailure(500, "could not save the sharing choice")
		}
		log.Printf("[RUN_SHARE] %s set Crew %s/%s share=%s", sanitizeUserIDForPath(claims.UserID), id.Product, id.Folder, mode)
		state, err := api.readRunShare(ctx, claims, target)
		state.Unknown = unknown
		return state, err
	case "workflow", "relay":
		manifest, found, err := ReadWorkflowManifest(ctx, target.Root)
		if err != nil || !found || manifest == nil || manifest.Access == nil {
			return runShareState{}, dashboardFailure(404, "project unavailable")
		}
		var unknown []string
		switch mode {
		case runShareEveryone:
			manifest.Access.RunEveryone = true
		case runSharePrivate:
			manifest.Access.RunEveryone = false
			manifest.Access.Readers = nil
		case runSharePeople:
			dir, _ := readUserDirectoryFile()
			people, missing := resolveShareEmails(dir, emails)
			unknown = missing
			if len(people) == 0 {
				return runShareState{Mode: runSharePeople, Unknown: unknown}, dashboardFailure(400, "none of those emails match an active account"+unknownSuffix(unknown))
			}
			ids := []string{}
			for _, person := range people {
				if containsID(manifest.effectiveOwners(), person.ID) {
					continue // already an owner
				}
				ids = append(ids, person.ID)
			}
			manifest.Access.RunEveryone = false
			manifest.Access.Readers = ids
		}
		if err := WriteWorkflowManifest(ctx, target.Root, manifest); err != nil {
			log.Printf("[RUN_SHARE] save failed kind=%q root=%q mode=%q: %v", target.Kind, target.Root, mode, err)
			return runShareState{}, dashboardFailure(500, "could not save the sharing choice")
		}
		log.Printf("[RUN_SHARE] %s set %s %s share=%s", sanitizeUserIDForPath(claims.UserID), target.Kind, target.ID, mode)
		state, err := api.readRunShare(ctx, claims, target)
		state.Unknown = unknown
		return state, err
	default:
		return runShareState{}, dashboardFailure(404, "sharing is not available for this kind of project")
	}
}

func hasPublishedDashboard(ctx context.Context, root string) bool {
	listed, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "list"})
	if err != nil {
		return false
	}
	for _, d := range listed.Dashboards {
		if d.Managed && d.Published != "" {
			return true
		}
	}
	return false
}

func unknownSuffix(unknown []string) string {
	if len(unknown) == 0 {
		return ""
	}
	return fmt.Sprintf(" (no account: %s)", strings.Join(unknown, ", "))
}

func (api *StreamingAPI) handleDashboardShare(w http.ResponseWriter, r *http.Request, claims *UserClaims, target dashboardTarget, action, mode string, emails []string, createDashboard bool, changes ...crewAgentShareChange) {
	var (
		state runShareState
		err   error
	)
	if action == "share" {
		if !dashboardAuthoringAllowed(claims, &target) {
			logDashboardShareRefusal(claims, target, mode, "authoring_scope", dashboardFailure(403, "sharing requires owner and project authoring consent"), nil)
			externalError(w, 403, "insufficient_scope", "Sharing needs the project's owner and existing authoring consent for this project.")
			return
		}
		state, err = api.applyRunShare(r.Context(), claims, target, mode, emails, createDashboard, changes...)
	} else {
		state, err = api.readRunShare(r.Context(), claims, target)
	}
	if err != nil {
		if state.NeedsDashboard {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "dashboard_required": true})
			return
		}
		if len(state.Unknown) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "unknown_emails": state.Unknown})
			return
		}
		dashboardError(w, err)
		return
	}
	state.CanShare = api.dashboardShareOwner(r.Context(), claims, target) && dashboardAuthoringAllowed(claims, &target)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}
