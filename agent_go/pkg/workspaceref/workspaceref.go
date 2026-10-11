// Package workspaceref is the one place that knows the two spellings of a workspace folder (PLAT-435, PLAT-442).
//
// The implementation lives in the workspace module (github.com/manishiitg/coding-agent-loop/workspace/workspaceref)
// so the workspace service and the agent server share one parser; this package re-exports it, so agent_go's
// imports stay as they were. Read the implementation's package comment for the rules: no code outside it may
// split, strip, test or join the "_users" segment, and the guard test in this directory fails when it does,
// in agent_go and in the workspace module.
package workspaceref

import w "github.com/manishiitg/coding-agent-loop/workspace/workspaceref"

// Ref is a parsed workspace path.
type Ref = w.Ref

// UsersDir is the physical per-user directory below the document root (storage layers only).
const UsersDir = w.UsersDir

// Project roots (logical, below a user's tree) of Crew and Code.
const (
	CrewProjectsRoot = w.CrewProjectsRoot
	CodeProjectsRoot = w.CodeProjectsRoot
)

// SharedCrewRoot is the shared Crew root "Crew" (PLAT-442 step 4); a path in it names no owner.
const SharedCrewRoot = w.SharedCrewRoot

// SharedProjectPath builds "Crew/<project>/<rel...>".
func SharedProjectPath(project string, rel ...string) string {
	return w.SharedProjectPath(project, rel...)
}

// ProjectRoots lists every project product root, in lookup order.
var ProjectRoots = w.ProjectRoots

// SanitizeUserID returns a user id safe to use as a path segment ("default" when invalid).
func SanitizeUserID(userID string) string { return w.SanitizeUserID(userID) }

// Parse accepts every spelling in use; see the implementation.
func Parse(p string) (Ref, bool) { return w.Parse(p) }

// MustParse is Parse for callers that treat an invalid path as empty.
func MustParse(p string) Ref { return w.MustParse(p) }

// PhysicalPath builds "_users/<sanitized user>/<rel...>".
func PhysicalPath(user string, rel ...string) string { return w.PhysicalPath(user, rel...) }

// UserRoot is "_users/<sanitized user>".
func UserRoot(user string) string { return w.UserRoot(user) }

// CanonicalFor is the identity key of a workspace path for user.
func CanonicalFor(user, p string) string { return w.CanonicalFor(user, p) }

// PhysicalPathOf builds "_users/<owner>/<rel...>" for an already validated owner.
func PhysicalPathOf(owner string, rel ...string) string { return w.PhysicalPathOf(owner, rel...) }
