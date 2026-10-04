// Package workspaceref is the one place that knows the two spellings of a
// workspace folder (PLAT-435).
//
// The same folder has a logical spelling that clients, manifests and
// profiles use ("Chats/Code/projects/p") and a physical spelling that
// storage and the runtime use ("_users/<owner>/Chats/Code/projects/p"). Both
// used to be plain strings and every caller stripped or compared the prefix
// on its own; a new caller that picked the wrong rule worked on a single-user
// laptop and broke only on multi-user servers. No code outside this package
// may split, strip, test or join the "_users" segment (a guard test in this
// package fails when it does); use Parse and a Ref.
//
// Two different questions, two differently named method families. They must
// never be confused:
//
//   - "Which product/project/shape is this path?" ignores who owns it. Use
//     Logical, Project, IsProject. Logical strips ANY owner's prefix.
//   - "Is this the same folder as that one, for this caller?" is identity and
//     is used for access. Use SameFor (relative to the authenticated user),
//     SameAs (strict, owner included) and OwnedBy. They never treat a path
//     owned by user A as equal to a public path seen by user B.
package workspaceref

import (
	"path"
	"regexp"
	"strings"
)

// UsersDir is the physical per-user directory below the document root. It
// is exported only for the storage layers that touch the file system; path
// logic must go through Ref.
const UsersDir = "_users"

// Project roots (logical, below a user's tree) of the project products Crew
// and Code. This is the single table: cmd/server's project products and
// internal/codeproduct take their root from here.
const (
	CrewProjectsRoot = "Chats/Work/projects"
	CodeProjectsRoot = "Chats/Code/projects"
)

// ProjectRoots lists every project product root, in lookup order.
var ProjectRoots = []string{CrewProjectsRoot, CodeProjectsRoot}

var safeUserID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// SanitizeUserID returns a user id safe to use as a path segment: "default"
// when empty, longer than 128 bytes or outside [a-zA-Z0-9_-]. This is the
// only implementation of the rule.
func SanitizeUserID(userID string) string {
	if userID == "" || len(userID) > 128 || !safeUserID.MatchString(userID) {
		return "default"
	}
	return userID
}

// Ref is a parsed workspace path.
type Ref struct {
	// owner is the "_users/<owner>/" segment as written (reserved
	// underscore users such as "_system_global_secrets" included), "" for a
	// logical path.
	owner string
	// logical is the clean path below the owner, no leading or trailing
	// slash, "" for the root.
	logical string
	// usersRoot marks the bare "_users" directory (no owner segment).
	usersRoot bool
}

// Parse accepts every spelling in use: logical, physical, an absolute path
// under a document root that contains "/_users/<owner>/", backslashes,
// leading/trailing slashes and "."/".." segments (cleaned). ok is false when
// a ".." escapes the root. An empty path is a valid empty Ref.
func Parse(p string) (Ref, bool) {
	s := strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	s = strings.Trim(s, "/")
	if s == "" {
		return Ref{}, true
	}
	s = path.Clean(s)
	if s == "." {
		return Ref{}, true
	}
	if s == ".." || strings.HasPrefix(s, "../") {
		return Ref{}, false
	}
	if s == UsersDir {
		return Ref{usersRoot: true}, true
	}
	marker := UsersDir + "/"
	for from := 0; from < len(s); {
		i := strings.Index(s[from:], marker)
		if i < 0 {
			break
		}
		i += from
		if i == 0 || s[i-1] == '/' {
			rest := s[i+len(marker):]
			owner, logical, _ := strings.Cut(rest, "/")
			return Ref{owner: owner, logical: logical}, true
		}
		from = i + 1
	}
	return Ref{logical: s}, true
}

// MustParse is Parse for callers that treat an invalid path as empty.
func MustParse(p string) Ref {
	r, _ := Parse(p)
	return r
}

// Logical is the path with ANY owner's prefix stripped. Use it to ask which
// product or project a path is, never to decide access.
func (r Ref) Logical() string { return r.logical }

// Owner returns the owner segment as written, "" when the path is logical.
func (r Ref) Owner() string { return r.owner }

// HasOwner reports whether the path carried a "_users/<owner>/" prefix.
func (r Ref) HasOwner() bool { return r.owner != "" }

// IsEmpty reports a path that names nothing (empty, ".", or the bare users root).
func (r Ref) IsEmpty() bool { return r.owner == "" && r.logical == "" }

// IsUsersRoot reports the bare "_users" directory.
func (r Ref) IsUsersRoot() bool { return r.usersRoot }

// Physical is the path stored under the given user: "_users/<user>/<logical>",
// the user passed through SanitizeUserID. The ref's own owner is ignored; use
// PhysicalKeepOwner to keep an explicit owner.
func (r Ref) Physical(user string) string {
	return PhysicalPath(user, r.logical)
}

// PhysicalKeepOwner is the physical path of the ref's own owner when it has
// one, else the path under user. ("An explicit _users/<owner>/ path is kept,
// a logical one is placed under the caller.")
func (r Ref) PhysicalKeepOwner(user string) string {
	if r.HasOwner() {
		return path.Join(UsersDir, r.owner, r.logical)
	}
	return r.Physical(user)
}

// PhysicalPath builds "_users/<sanitized user>/<rel...>".
func PhysicalPath(user string, rel ...string) string {
	return path.Join(append([]string{UsersDir, SanitizeUserID(user)}, rel...)...)
}

// UserRoot is "_users/<sanitized user>".
func UserRoot(user string) string { return PhysicalPath(user) }

// effectiveOwner is the owner the path denotes for the given caller: its own
// prefix, else the caller (a public path means the caller's folder).
func (r Ref) effectiveOwner(user string) string {
	if r.HasOwner() {
		return r.owner
	}
	return SanitizeUserID(user)
}

// OwnedBy reports an explicit "_users/<user>/" prefix for that user.
func (r Ref) OwnedBy(user string) bool {
	return r.HasOwner() && r.owner == SanitizeUserID(user)
}

// OwnedByOrUnowned is true for a logical path and for a path with the
// user's own prefix; false for another user's physical path.
func (r Ref) OwnedByOrUnowned(user string) bool {
	return !r.HasOwner() || r.owner == SanitizeUserID(user)
}

// SameAs is strict identity: same logical path and same owner (both
// unowned counts as the same). A physical path is never SameAs a logical one.
func (r Ref) SameAs(other Ref) bool {
	return r.owner == other.owner && r.logical == other.logical && r.usersRoot == other.usersRoot
}

// SameFor is identity for access, relative to the authenticated user: only
// that user's own prefix is dropped, so a path owned by user A is never equal
// to a public path seen by user B.
func (r Ref) SameFor(user string, other Ref) bool {
	return r.logical == other.logical && r.effectiveOwner(user) == other.effectiveOwner(user) &&
		r.usersRoot == other.usersRoot
}

// Project returns the project root (one of ProjectRoots) the logical path is
// inside and the project folder name. The path must address a project, not
// the root itself. The owner is ignored.
func (r Ref) Project() (root, project string, ok bool) {
	for _, candidate := range ProjectRoots {
		prefix := candidate + "/"
		if !strings.HasPrefix(r.logical, prefix) {
			continue
		}
		project, _, _ = strings.Cut(strings.Trim(strings.TrimPrefix(r.logical, prefix), "/"), "/")
		if project == "" {
			return "", "", false
		}
		return candidate, project, true
	}
	return "", "", false
}

// IsProject reports whether the path is inside a Crew or Code project.
func (r Ref) IsProject() bool {
	_, _, ok := r.Project()
	return ok
}

// CanonicalFor is the identity key of a workspace path for user: logical when
// the path is public or carries the user's own prefix, the clean physical
// spelling when another user owns it, "" for an empty or invalid path. Two
// paths are the same folder for user exactly when their keys are equal. Use
// SameFor to compare; use this only where a string key is needed (maps,
// stored fields).
func CanonicalFor(user, p string) string {
	r, ok := Parse(p)
	switch {
	case !ok:
		return ""
	case r.usersRoot:
		return UsersDir
	case r.OwnedByOrUnowned(user):
		return r.logical
	}
	return path.Join(UsersDir, r.owner, r.logical)
}

// WithLogical returns a Ref with the same owner and another logical path
// (for example the project root of a path inside a project).
func (r Ref) WithLogical(logical string) Ref {
	return Ref{owner: r.owner, logical: strings.Trim(path.Clean("/"+logical), "/")}
}
