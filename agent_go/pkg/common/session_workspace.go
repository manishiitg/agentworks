package common

import (
	"context"
	"path"
	"regexp"
	"strings"
)

// Session workspace classification: the single place that knows the
// workspace path shapes. Every browser checkpoint (discovery, capture,
// recording, preview) must classify through here instead of hand-rolling
// Workflow/ prefix checks — each hand-rolled copy defaulted to
// Workflow-or-nothing and broke Crew sessions in a new place (PLAT-322,
// issue #210).
//
// Crew projects persist workflow.json through the shared project manifest,
// so manifest presence NEVER distinguishes them: the path does. Public
// clients say `Chats/Work/projects/<id>`; the runtime stores
// `_users/<owner>/Chats/Work/projects/<id>`. Both name the same project
// for the owning user, exactly like the discovery fix classifies them.

// SessionWorkspaceKind is the owning workspace kind of a session.
type SessionWorkspaceKind string

const (
	SessionWorkspaceUnknown     SessionWorkspaceKind = ""
	SessionWorkspaceWorkflow    SessionWorkspaceKind = "workflow"
	SessionWorkspaceCrewProject SessionWorkspaceKind = "crew"
)

// safeSessionUserIDForPath mirrors cmd/server's safeUserIDForPath. The two
// are pinned together by TestCanonicalSessionWorkspaceMatchesServer.
var safeSessionUserIDForPath = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func sanitizeSessionUserIDForPath(userID string) string {
	if userID == "" || len(userID) > 128 || !safeSessionUserIDForPath.MatchString(userID) {
		return "default"
	}
	return userID
}

// CanonicalSessionWorkspace normalizes a session workspace path and strips
// the caller's own `_users/<id>/` prefix. It mirrors cmd/server's
// canonicalChatHistoryWorkspacePath exactly (same trim, clean, traversal
// rejection, and prefix strip); the cmd/server equivalence test pins them
// together. It lives here because pkg/browser cannot import cmd/server.
func CanonicalSessionWorkspace(userID, workspacePath string) string {
	workspacePath = strings.TrimSpace(strings.Trim(workspacePath, "/"))
	if workspacePath == "" {
		return ""
	}
	cleaned := path.Clean(workspacePath)
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return ""
	}
	return strings.TrimPrefix(cleaned, path.Join("_users", sanitizeSessionUserIDForPath(userID))+"/")
}

// projectWorkspacePrefixes are the project products' roots: Crew and Code
// (cmd/server projectProducts). Both classify as a project workspace.
var projectWorkspacePrefixes = []string{"Chats/Work/projects/", "Chats/Code/projects/"}

// ClassifySessionWorkspace returns the owning kind and owning root of a
// session workspace: `Workflow/<name>` for workflows, or the Crew project
// root `Chats/Work/projects/<id>` for Crew sessions. Deeper working
// directories collapse to their root. Anything else is unknown with an
// empty root. The Crew rule matches cmd/server's
// isActiveWorkProjectWorkspace: same prefix, non-empty project remainder,
// under any owner's physical prefix (Crew Run mode: a Crew project is a
// Crew project whoever owns it).
func ClassifySessionWorkspace(userID, workspacePath string) (SessionWorkspaceKind, string) {
	canonical := CanonicalSessionWorkspace(userID, workspacePath)
	if canonical == "" {
		return SessionWorkspaceUnknown, ""
	}
	if canonical == "Workflow" || strings.HasPrefix(canonical, "Workflow/") {
		segments := strings.Split(canonical, "/")
		if len(segments) >= 2 && segments[1] != "" {
			return SessionWorkspaceWorkflow, "Workflow/" + segments[1]
		}
		return SessionWorkspaceUnknown, ""
	}
	crewCanonical := stripAnySessionUserPrefix(canonical)
	for _, crewPrefix := range projectWorkspacePrefixes {
		if !strings.HasPrefix(crewCanonical, crewPrefix) {
			continue
		}
		rest := strings.Trim(strings.TrimPrefix(crewCanonical, crewPrefix), "/")
		if rest == "" {
			return SessionWorkspaceUnknown, ""
		}
		project := rest
		if i := strings.Index(project, "/"); i >= 0 {
			project = project[:i]
		}
		return SessionWorkspaceCrewProject, crewPrefix + project
	}
	return SessionWorkspaceUnknown, ""
}

// stripAnySessionUserPrefix drops any owner's `_users/<id>/` prefix. It
// mirrors cmd/server's normalizeConversationWorkspace, which the server
// crew check applies after its own-prefix canonicalization.
func stripAnySessionUserPrefix(workspacePath string) string {
	clean := strings.Trim(strings.TrimSpace(workspacePath), "/")
	if index := strings.Index(clean, "_users/"); index >= 0 {
		rest := clean[index+len("_users/"):]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			return rest[slash+1:]
		}
	}
	return clean
}

// SessionUserIDFromContext reads the signed-in user for canonicalization.
// Empty when the context carries none; public-form paths still classify.
func SessionUserIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(UserIDKey).(string); ok {
		return id
	}
	return ""
}
