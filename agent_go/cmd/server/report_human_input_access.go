package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// reportHumanInputAccess is what a caller may do with the decisions stored
// for one workspace (its db/db.sqlite): read them, or answer, dismiss and
// create them. Workflows follow their access record (readers read; owners
// and editors write). A Crew's decisions, including the suggestions its
// other users leave, belong to its owner alone: one Crew user never sees
// another's suggestions.
//
// Before this the decisions routes checked nothing but a signed-in user, so
// anyone could read, answer or dismiss another workflow's decisions, some of
// which authorize changes (found building Crew suggestions, 2026-09-28).
func reportHumanInputAccess(ctx context.Context, workspacePath string) (read, write bool) {
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.UserID == "" {
		return false, false
	}
	if _, m := workflowAccessForWorkspacePath(ctx, claims, workspacePath); m != nil && m.Team != nil {
		if claims.UserID == m.Team.LeaderID {
			return true, userAccessForClaims(claims).CanEdit
		}
		if member := m.Team.member(claims.UserID); member != nil {
			return true, userAccessForClaims(claims).CanEdit && !userAccessForClaims(claims).Disabled
		}
		return userAccessForClaims(claims).Admin, false
	}
	if userAccessForClaims(claims).Admin {
		return true, true
	}
	if ref, ok := resolveCrewPath(ctx, claims.UserID, workspacePath); ok {
		owner := crewAccessFor(claims, ref) == crewAccessOwner
		return owner, owner
	}
	switch level, _ := workflowAccessForWorkspacePath(ctx, claims, workspacePath); level {
	case WorkflowAccessOwner, WorkflowAccessWrite:
		return true, true
	case WorkflowAccessRead:
		return true, false
	}
	return false, false
}

// requireReportHumanInputAccess guards a decisions route by the
// workspace_path it names (query string or JSON body).
func requireReportHumanInputAccess(write bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next(w, r)
			return
		}
		read, canWrite := reportHumanInputAccess(r.Context(), requestWorkflowWorkspacePath(r))
		if !read || (write && !canWrite) {
			http.Error(w, "you do not have access to this workspace's decisions", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// Approval delegation names decisions explicitly; edit permission alone never
// authorizes an answer. Other writable decision operations remain available to
// Contributors, so they can propose and record verified outcomes.
func requireTeamDecisionApproval(ctx context.Context, root, id string) error {
	team, e := localDecisionTeam(root)
	if e != nil {
		return e
	}
	if team == nil {
		return nil
	}

	actor := teamActor(ctx)
	a := userAccessForClaims(&UserClaims{UserID: actor})
	if a.Disabled || !a.CanEdit || IsMultiUserMode() && !a.Known {
		return fmt.Errorf("account cannot approve decisions")
	}
	if actor == team.LeaderID {
		return nil
	}
	member := team.member(actor)
	if member != nil && member.Approve {
		for _, approved := range member.ApprovalIDs {
			if approved == id {
				return nil
			}
		}
	}
	return fmt.Errorf("this decision requires the Leader or explicit delegated approval")
}

// Decisions are in the workflow's local managed DB. Read its co-located
// manifest directly, rather than depending on a remote workspace HTTP service
// to answer a DB decision. Missing manifests preserve legacy standalone DBs.
func localDecisionTeam(root string) (*WorkflowTeam, error) {
	normalized, _, e := reportHumanInputDBPath(root)
	if e != nil {
		return nil, e
	}
	raw, e := os.ReadFile(filepath.Join(getWorkspaceDocsAbsPath(), filepath.FromSlash(normalized), "workflow.json"))
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var m WorkflowManifest
	if e = json.Unmarshal(raw, &m); e != nil {
		return nil, e
	}
	return m.Team, nil
}

func authorizeTeamRecordActor(ctx context.Context, team *WorkflowTeam) error {
	actor := teamActor(ctx)
	if actor != team.LeaderID && team.member(actor) == nil {
		return fmt.Errorf("shared record requires a team member")
	}
	a := userAccessForClaims(&UserClaims{UserID: actor})
	if a.Disabled || !a.CanEdit || IsMultiUserMode() && !a.Known {
		return fmt.Errorf("account cannot update shared records")
	}
	return nil
}
