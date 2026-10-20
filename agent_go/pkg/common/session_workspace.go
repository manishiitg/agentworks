package common

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
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

// CanonicalSessionWorkspace normalizes a session workspace path and strips
// the caller's own `_users/<id>/` prefix. It is cmd/server's
// canonicalChatHistoryWorkspacePath: both call workspaceref.CanonicalFor, the
// one implementation (PLAT-435). It lives here because pkg/browser cannot
// import cmd/server.
func CanonicalSessionWorkspace(userID, workspacePath string) string {
	return workspaceref.CanonicalFor(userID, workspacePath)
}

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
	ref := workspaceref.MustParse(canonical)
	// A Crew at the shared root (PLAT-442 step 4) is a Crew project like any other.
	if project, ok := ref.SharedProject(); ok {
		return SessionWorkspaceCrewProject, workspaceref.SharedProjectPath(project)
	}
	return SessionWorkspaceUnknown, ""
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

// GoogleWorkspaceFromContext names the target configured by the server. Attached
// folders and tool arguments never choose which account a run can use.
func GoogleWorkspaceFromContext(ctx context.Context) (string, error) {
	sessionID, _ := ctx.Value(ChatSessionIDKey).(string)
	user := SessionUserIDFromContext(ctx)
	cfg := GetSessionShellConfig(sessionID)
	if cfg == nil {
		return "", fmt.Errorf("Open this item's chat to use its Google accounts.")
	}
	for _, candidate := range []string{cfg.WorkflowPath, cfg.WorkingDir} {
		kind, root := ClassifySessionWorkspace(user, candidate)
		if kind == SessionWorkspaceWorkflow {
			return root, nil
		}
		if kind == SessionWorkspaceCrewProject {
			ref := workspaceref.MustParse(candidate)
			if _, shared := ref.SharedProject(); shared {
				return root, nil
			}
			return ref.WithLogical(root).PhysicalKeepOwner(user), nil
		}
	}
	return "", nil // legacy non-product chats retain company accounts only
}
