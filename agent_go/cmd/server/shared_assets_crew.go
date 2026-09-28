package server

import (
	"context"
	"net/http"
	"path"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Crews are shared like workflows: anyone who may open a crew (Crew Run
// mode — the owner, or any signed-in user with the Crew product) may open
// its file and folder links read-only. The crew's private areas stay
// hidden exactly as in the mediated crew reader (crew_directory.go):
// builder/ transcripts, db/ run databases, and the raw root manifests.

// crewLinkReadAllowed reports whether claims may read the crew rooted at
// crewRoot ("_users/<owner>/Chats/Work/projects/<p>"). Tests replace it.
var crewLinkReadAllowed = func(api *StreamingAPI, claims *UserClaims, crewRoot string) bool {
	return api.crewBrowserAccess(claims, crewRoot) != WorkflowAccessNone
}

// crewReaderSharedAsset resolves a link to another owner's crew for a
// signed-in reader. handled is false when the link is not a cross-user crew
// link, so the caller applies the ordinary rules. crewRootView is true when
// the link addresses the crew root itself, whose listing must be filtered.
func (api *StreamingAPI) crewReaderSharedAsset(w http.ResponseWriter, r *http.Request, full string) (root, relative string, crewRootView, handled, ok bool) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || !IsMultiUserMode() {
		return "", "", false, false, false
	}
	caller := publicWorkspaceUserID(r)
	clean, err := wf.CleanRelative(full)
	if err != nil || clean != full {
		return "", "", false, false, false
	}
	owner := strings.TrimSpace(r.URL.Query().Get("uid"))
	parts := strings.Split(clean, "/")
	if parts[0] == "_users" && len(parts) >= 3 {
		owner, parts = parts[1], parts[2:]
	}
	if owner == "" || owner == caller || sanitizeUserIDForPath(owner) != owner {
		return "", "", false, false, false
	}
	if len(parts) < 4 || parts[0] != "Chats" || (parts[1] != "Work" && parts[1] != "Code") || parts[2] != "projects" || parts[3] == "" {
		return "", "", false, false, false
	}
	crewRoot := path.Join("_users", owner, "Chats", parts[1], "projects", parts[3])
	if parts[1] == "Code" {
		// A Code link opens for the people its owner shared it with.
		if !codeLinkReadAllowed(r.Context(), claims, owner, crewRoot) {
			externalError(w, 403, "forbidden", "You do not have access to this Code workspace.")
			return "", "", false, true, false
		}
	} else if !crewLinkReadAllowed(api, claims, crewRoot) {
		externalError(w, 403, "forbidden", "You do not have access to this crew.")
		return "", "", false, true, false
	}
	if claims.AccessToken != nil && !claims.AccessToken.Allows("files:read") {
		externalError(w, 403, "insufficient_scope", "This token does not allow this asset.")
		return "", "", false, true, false
	}
	crewRelative := strings.Join(parts[4:], "/")
	if crewRelative != "" {
		if _, confined := confineSharedProjectPath(crewRoot, crewRelative); !confined {
			externalError(w, 403, "protected_path", "This part of the crew is private to its owner.")
			return "", "", false, true, false
		}
	}
	relative = strings.Join(parts[1:], "/")
	if wf.Private(relative) {
		externalError(w, 403, "protected_path", "Private workspace files are not shareable.")
		return "", "", false, true, false
	}
	return path.Join("_users", owner, "Chats"), relative, crewRelative == "", true, true
}

// crewRootListingVisible hides the crew's private areas from a reader's
// listing of the crew root. name is relative to the crew root.
func crewRootListingVisible(name string) bool {
	name = strings.Trim(name, "/")
	if sharedProjectExcludedRootFiles[name] {
		return false
	}
	top := name
	if index := strings.IndexByte(name, '/'); index >= 0 {
		top = name[:index]
	}
	return !sharedProjectExcludedTopSegments[top]
}

// codeLinkReadAllowed reports whether claims may read the Code rooted at
// codeRoot ("_users/<owner>/Chats/Code/projects/<p>"): any role its owner
// granted them. The project id comes from the Code's own product.json.
var codeLinkReadAllowed = func(ctx context.Context, claims *UserClaims, ownerID, codeRoot string) bool {
	if claims == nil {
		return false
	}
	manifest, err := readCrewProjectManifests(ctx, codeproduct.ProfileID, codeRoot)
	if err != nil {
		return false
	}
	return codeRoleFor(ctx, claims.UserID, ownerID, manifest.ID) != codeRoleNone
}
