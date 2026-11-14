package server

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

const crewTeamJoinInstructionsNotice = "Your current project instructions will be kept as a backup and replaced by the Team's."
const crewTeamJoinAlreadyOnline = "Already online with another setup. Turn Run online off for it first, or pick New project."

type crewTeamJoinTarget struct {
	ProjectID string        `json:"project_id"`
	Name      string        `json:"name"`
	CanJoin   bool          `json:"can_join"`
	Reason    string        `json:"reason,omitempty"`
	Agents    []macAgentRow `json:"agents"`
}

// The same admission is used to list targets and immediately before accepting.
// Project ownership and saved Team/online state are authoritative, not the picker.
func (api *StreamingAPI) crewTeamJoinTarget(ctx context.Context, claims *UserClaims, project string) (crewTeamJoinTarget, crewroot.Roots, error) {
	out := crewTeamJoinTarget{ProjectID: project, Agents: []macAgentRow{}}
	if !IsLocalMode() || ownerOfCrewProjectID(project) != claims.UserID {
		return out, crewroot.Roots{}, fmt.Errorf("Choose one of your own projects on this Mac.")
	}
	if agentWorksPrivateProject(ctx, project) {
		return out, crewroot.Roots{}, fmt.Errorf("Choose a project that can join a Team.")
	}
	_, manifest, ok := api.resolveExternalWorkspace(ctx, claims, project)
	if !ok {
		return out, crewroot.Roots{}, fmt.Errorf("This project could not be opened. Pick another project.")
	}
	out.Name = firstNonEmptyTrimmed(manifest.Title, manifest.Label, project)
	state := readCrewTeamState(project)
	team, err := readCrewTeam(project)
	if err != nil && !os.IsNotExist(err) {
		return out, crewroot.Roots{}, fmt.Errorf("This project's Team settings could not be checked.")
	}
	if state.TeamOn || state.MemberOf != "" || team.On {
		return out, crewroot.Roots{}, fmt.Errorf("This project already has a Team. Pick a project with Team off.")
	}
	binding, err := crewroot.ReadSync(project)
	if err != nil {
		return out, crewroot.Roots{}, fmt.Errorf("This project's online setup could not be checked.")
	}
	if binding != nil {
		out.Reason = crewTeamJoinAlreadyOnline
		return out, crewroot.Roots{}, nil
	}
	roots, err := api.crewSyncRoots(ctx, claims, project)
	if err != nil {
		return out, roots, err
	}
	out.Agents, err = api.macAgentRows(ctx, claims, project)
	if err != nil {
		return out, roots, err
	}
	out.CanJoin = true
	return out, roots, nil
}

func (api *StreamingAPI) crewTeamJoinTargets(ctx context.Context, claims *UserClaims) ([]crewTeamJoinTarget, error) {
	records, err := defaultProjectOwners().All()
	if err != nil {
		return nil, err
	}
	out := []crewTeamJoinTarget{}
	for _, record := range records {
		if record.Product != crewProfileID || record.OwnerID != claims.UserID {
			continue
		}
		target, _, err := api.crewTeamJoinTarget(ctx, claims, record.ProjectID)
		if err == nil {
			out = append(out, target)
		}
	}
	slices.SortFunc(out, func(a, b crewTeamJoinTarget) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}
