package server

import (
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// projectProduct is a product whose conversations live in per-project
// folders under the owner's tree: Crew (profile work). Both are built
// from the same shared features, so owner-side project behavior (workspace
// resolution, sandbox, keyed chat, files, tools) applies to either.
//
// Being a project product says nothing about who else may open or call a
// project. Every non-owner path (Crew Run-mode readers, ask_crew, functions,
// triggers, # references, list_crews) stays keyed to the Crew profile, so a
// Code is owner-only and never a call target unless code says otherwise.
type projectProduct struct {
	ProfileID    string
	ProjectsRoot string // logical, below _users/<owner>/
	// FallbackName is shown when the profile is not registered; the name in
	// the product's product.yaml wins otherwise.
	FallbackName string
}

const crewProfileID = "work"

// crewProduct is the only project product.
var crewProduct = projectProduct{ProfileID: crewProfileID, ProjectsRoot: workspaceref.SharedCrewRoot, FallbackName: "Crew"}

// isProjectProfileID reports whether profileID is a project product.
func isProjectProfileID(profileID string) bool {
	_, ok := projectProductForProfile(profileID)
	return ok
}

func projectProductForProfile(profileID string) (projectProduct, bool) {
	if strings.EqualFold(crewProduct.ProfileID, strings.TrimSpace(profileID)) {
		return crewProduct, true
	}
	return projectProduct{}, false
}

// projectProductForPath returns the project product whose projects root
// contains workspacePath (logical or physical _users/<owner>/ form) and the
// project folder name. The path must address a project, not the root.
func projectProductForPath(workspacePath string) (projectProduct, string, bool) {
	ref := workspaceref.MustParse(workspacePath)
	// A Crew at the shared root (Crew/<id>, PLAT-442 step 4) is a Crew project like any other.
	if project, shared := ref.SharedProject(); shared {
		if rec, found := defaultProjectOwners().sharedRootRecord(project); found {
			product, ok := projectProductForProfile(rec.Product)
			return product, project, ok
		}
		return crewProduct, project, true
	}
	return projectProduct{}, "", false
}

// isProjectWorkspacePath reports whether workspacePath is inside a Crew or
// Code project.
func isProjectWorkspacePath(workspacePath string) bool {
	_, _, ok := projectProductForPath(workspacePath)
	return ok
}

// isCodeProjectPath reports whether workspacePath is inside a Code project.

// projectProductName is the display name its product.yaml gives the project
// product that owns workspacePath (profile.name: "Crew", "Code"). It falls
// back to FallbackName when the profile is not registered.
func (api *StreamingAPI) projectProductName(userID, workspacePath string) string {
	product, _, ok := projectProductForPath(workspacePath)
	if !ok {
		return "project"
	}
	if api != nil && api.agentProfiles != nil {
		if profile, err := api.agentProfiles.Resolve(product.ProfileID, 0, userID); err == nil && strings.TrimSpace(profile.Name) != "" {
			return strings.TrimSpace(profile.Name)
		}
	}
	return product.FallbackName
}

// Inventory/creation roots are configuration, never a selectable chat workspace.
func projectProfileProjectsRoot(profile agentprofiles.Profile, userID string) (string, error) {
	if isProjectProfileID(profile.ID) {
		return workspaceref.SharedCrewRoot, nil
	}
	return cleanAgentProfileWorkspace(profile.Runtime.Workspace.ProjectsRoot, userID)
}
