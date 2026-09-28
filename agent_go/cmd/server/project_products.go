package server

import (
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
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
}

const crewProfileID = "work"

var projectProducts = []projectProduct{
	{ProfileID: crewProfileID, ProjectsRoot: "Chats/Work/projects"},
	{ProfileID: codeproduct.ProfileID, ProjectsRoot: codeproduct.ProjectsRoot},
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
	canonical := normalizeConversationWorkspace(workspacePath)
	for _, product := range projectProducts {
		prefix := product.ProjectsRoot + "/"
		if !strings.HasPrefix(canonical, prefix) {
			continue
		}
		project := strings.SplitN(strings.Trim(strings.TrimPrefix(canonical, prefix), "/"), "/", 2)[0]
		if project == "" {
			return projectProduct{}, "", false
		}
		return product, project, true
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
