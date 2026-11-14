package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A Team works with one Crew setup (owner, 5 Nov): the Crew's chat instructions and selected skills travel in
// team-setup.json at the Crew folder's top, and its own skills in skills/. Both are always part of what a Team shares,
// PROJECT_INSTRUCTIONS.md is also always shared; MEMORY.md and memory/ stay personal.
// Whatever folders it chose, every copy has them and edits by anyone sync both ways with the store's history and
// clash handling. Each copy (the owner's Mac, a member's Mac, a web member's server copy) writes the file when its
// Crew's setup changes and applies it when someone else's edit arrives, so a web member's edit reaches the owner's Mac.
// The model, apps and secrets stay as they are (the owner's, lent to the Team).

const crewTeamSetupFile = crewroot.TeamSetupFile

type crewTeamSetup struct {
	ChatInstructions string   `json:"chat_instructions"`
	SelectedSkills   []string `json:"selected_skills"`
}

// crewTeamSetupPath: always shared with the Team (crewTeamFolder.holds).
func crewTeamSetupPath(rel string) bool {
	return rel == "PROJECT_INSTRUCTIONS.md" || rel == crewTeamSetupFile || rel == "skills" || strings.HasPrefix(rel, "skills/")
}

func (s crewTeamSetup) encode() []byte {
	s.SelectedSkills = slices.Clone(s.SelectedSkills)
	if s.SelectedSkills == nil {
		s.SelectedSkills = []string{}
	}
	slices.Sort(s.SelectedSkills)
	s.SelectedSkills = slices.Compact(s.SelectedSkills)
	raw, _ := json.MarshalIndent(s, "", "  ")
	return append(raw, '\n')
}

func crewTeamSetupHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// crewSetupOf reads this Crew's setup as the person given (its owner where it lives).
func (api *StreamingAPI) crewSetupOf(ctx context.Context, person, project string) (crewTeamSetup, string, context.Context, bool) {
	claims := &UserClaims{UserID: person, Username: person}
	ctx = context.WithValue(ctx, UserContextKey, claims)
	binding, manifest, ok := api.resolveExternalWorkspace(ctx, claims, project)
	if !ok {
		return crewTeamSetup{}, "", ctx, false
	}
	return crewTeamSetup{ChatInstructions: manifest.ChatInstructions, SelectedSkills: manifest.Capabilities.SelectedSkills}, binding.Binding.WorkspacePath, ctx, true
}

// reconcileCrewTeamSetup brings this Crew and its team-setup.json together. state is where the last applied version
// is kept; write is false until a copy has received the team folder, so a new copy never replaces the Team's setup.
func (api *StreamingAPI) reconcileCrewTeamSetup(ctx context.Context, person, project, root, state string, write bool) {
	api.reconcileCrewTeamSetupMapped(ctx, person, project, root, state, write, nil)
}

func (api *StreamingAPI) reconcileCrewTeamSetupMapped(ctx context.Context, person, project, root, state string, write bool, copy *crewTeamLocal) {
	if project == "" || root == "" || state == "" {
		return
	}
	setup, workspace, ctx, ok := api.crewSetupOf(ctx, person, project)
	if !ok {
		return
	}
	mark := filepath.Join(state, "setup-applied")
	last, _ := os.ReadFile(mark)
	path := filepath.Join(root, crewTeamSetupFile)
	file, err := os.ReadFile(path)
	currentSetup := setup
	if copy != nil && copy.Mounted {
		var shared crewTeamSetup
		if err == nil {
			_ = json.Unmarshal(file, &shared)
		}
		currentSetup.SelectedSkills = copy.exportSelectedSkills(setup.SelectedSkills, shared.SelectedSkills)
	}
	current := currentSetup.encode()

	switch {
	case os.IsNotExist(err):
		if !write {
			return
		}
		if err = os.WriteFile(path, current, 0o600); err == nil {
			_ = os.WriteFile(mark, []byte(crewTeamSetupHash(current)), 0o600)
			log.Printf("[CREW_TEAM] project=%s event=setup_written reason=first", project)
		}
	case err != nil:
		return
	case crewTeamSetupHash(file) != strings.TrimSpace(string(last)):
		// Someone's edit arrived (or this copy is new): the Crew takes it.
		var in crewTeamSetup
		if json.Unmarshal(file, &in) != nil {
			log.Printf("[CREW_TEAM] project=%s event=setup_unreadable", project)
			return
		}
		next := crewTeamSetup{ChatInstructions: in.ChatInstructions, SelectedSkills: in.SelectedSkills}
		localSkills := next.SelectedSkills
		if copy != nil && copy.Mounted {
			localSkills = copy.importSelectedSkills(in.SelectedSkills, setup.SelectedSkills)
		}
		if next.ChatInstructions != setup.ChatInstructions || !slices.Equal(localSkills, setup.SelectedSkills) {
			if next.ChatInstructions != setup.ChatInstructions {
				if err = updateCrewChatInstructions(ctx, workspace, next.ChatInstructions); err != nil {
					log.Printf("[CREW_TEAM] project=%s event=setup_apply_failed reason=%q", project, err)
					return
				}
			}
			if err = updateProductSelectedSkills(ctx, crewProfileID, workspace, func([]string) []string { return localSkills }); err != nil {
				log.Printf("[CREW_TEAM] project=%s event=setup_apply_failed reason=%q", project, err)
				return
			}
			log.Printf("[CREW_TEAM] project=%s event=setup_applied skills=%d", project, len(next.SelectedSkills))
		}
		_ = os.WriteFile(mark, []byte(crewTeamSetupHash(file)), 0o600)
	case write && string(file) != string(current):
		// This Crew's setup changed here: the Team gets it with the next upload.
		if err = os.WriteFile(path, current, 0o600); err == nil {
			_ = os.WriteFile(mark, []byte(crewTeamSetupHash(current)), 0o600)
			log.Printf("[CREW_TEAM] project=%s event=setup_written reason=changed", project)
		}
	}
}

// crewTeamLocalCrew is the Crew on this Mac that a Team folder copy belongs to: the owner's own Crew, or the member's
// working copy (crew_team_where.go).
func crewTeamLocalCrew(b crewTeamLocal) string {
	if b.LocalProject != "" {
		return b.LocalProject
	}
	return crewTeamMemberCopy(b.Project)
}

func (b crewTeamLocal) privateSkill(name string) bool {
	if filepath.Base(name) != name {
		return false
	}
	if b.remoteRel("skills/"+name) != "" {
		return false
	}
	_, err := os.Lstat(filepath.Join(b.diskRoot("skills/"+name), "skills", name))
	return err == nil
}

func (b crewTeamLocal) exportSelectedSkills(local []string, shared ...[]string) []string {
	common := []string{}
	if len(shared) > 0 {
		common = shared[0]
	}
	out := []string{}
	for _, name := range local {
		if remote := b.remoteRel("skills/" + name); remote != "" {
			out = append(out, strings.TrimPrefix(remote, "skills/"))
		} else if slices.Contains(common, name) && !b.privateSkill(name) {
			out = append(out, name)
		}
	}
	return out
}

func (b crewTeamLocal) importSelectedSkills(team, local []string) []string {
	out := []string{}
	for _, name := range local {
		if b.remoteRel("skills/"+name) == "" {
			out = append(out, name)
		}
	}
	for _, name := range team {
		if disk := b.diskRel("skills/" + name); disk != "" {
			out = append(out, strings.TrimPrefix(disk, "skills/"))
		} else {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
