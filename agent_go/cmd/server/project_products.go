package server

import (
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// projectProduct is a product whose conversations live in per-project
// folders under the owner's tree: Crew (profile work) and Code. Both are built
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

var projectProducts = []projectProduct{
	{ProfileID: crewProfileID, ProjectsRoot: workspaceref.CrewProjectsRoot, FallbackName: "Crew"},
	{ProfileID: codeproduct.ProfileID, ProjectsRoot: codeproduct.ProjectsRoot, FallbackName: "Code"},
}

// isProjectProfileID reports whether profileID is a project product.
func isProjectProfileID(profileID string) bool {
	_, ok := projectProductForProfile(profileID)
	return ok
}

func projectProductForProfile(profileID string) (projectProduct, bool) {
	profileID = strings.TrimSpace(profileID)
	for _, product := range projectProducts {
		if strings.EqualFold(product.ProfileID, profileID) {
			return product, true
		}
	}
	return projectProduct{}, false
}

// projectProductForPath returns the project product whose projects root
// contains workspacePath (logical or physical _users/<owner>/ form) and the
// project folder name. The path must address a project, not the root.
func projectProductForPath(workspacePath string) (projectProduct, string, bool) {
	root, project, ok := workspaceref.MustParse(workspacePath).Project()
	if !ok {
		return projectProduct{}, "", false
	}
	for _, product := range projectProducts {
		if product.ProjectsRoot == root {
			return product, project, true
		}
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
func isCodeProjectPath(workspacePath string) bool {
	product, _, ok := projectProductForPath(workspacePath)
	return ok && product.ProfileID == codeproduct.ProfileID
}

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
