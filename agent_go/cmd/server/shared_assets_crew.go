package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
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
	manifest, err := readCrewProjectManifests(context.Background(), "work", crewRoot)
	if err != nil {
		return false
	}
	crew, _, _, ok := api.externalCrewResolve(context.Background(), claims, manifest.ID)
	return ok && agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath) == crewRoot
}

// crewReaderSharedAsset recognizes shared and legacy Crew roots for every
// viewer, including the owner. Code retains its existing cross-owner rules.
// Returning the Crew root keeps file guards relative to the project rather
// than to its parent's Chats folder.
func (api *StreamingAPI) crewReaderSharedAsset(w http.ResponseWriter, r *http.Request, full string) (root, relative string, crewRootView, handled, ok bool) {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		return "", "", false, false, false
	}
	caller := publicWorkspaceUserID(r)
	clean, err := wf.CleanRelative(full)
	if err != nil || clean != full {
		return "", "", false, false, false
	}
	owner := strings.TrimSpace(r.URL.Query().Get("uid"))
	parts := strings.Split(clean, "/")
	if ref := workspaceref.MustParse(clean); ref.HasOwner() && ref.Logical() != "" {
		owner, parts = ref.Owner(), strings.Split(ref.Logical(), "/")
	}
	if owner == "" {
		owner = caller
	}
	var crewRoot, crewRelative string
	if len(parts) >= 2 && parts[0] == "Crew" && parts[1] != "" {
		crewRoot, crewRelative = strings.Join(parts[:2], "/"), strings.Join(parts[2:], "/")
	} else if len(parts) >= 4 && parts[0] == "Chats" && parts[1] == "Work" && parts[2] == "projects" && parts[3] != "" {
		if sanitizeUserIDForPath(owner) != owner {
			externalError(w, 403, "forbidden", "Crew owner is unavailable.")
			return "", "", false, true, false
		}
		crewRoot = workspaceref.PhysicalPathOf(owner, "Chats", "Work", "projects", parts[3])
		crewRelative = strings.Join(parts[4:], "/")
	} else {
		if !IsMultiUserMode() || owner == caller || sanitizeUserIDForPath(owner) != owner || len(parts) < 4 || parts[0] != "Chats" || parts[1] != "Code" || parts[2] != "projects" {
			return "", "", false, false, false
		}
		codeRoot := workspaceref.PhysicalPathOf(owner, "Chats", "Code", "projects", parts[3])
		if !codeLinkReadAllowed(r.Context(), claims, owner, codeRoot) {
			externalError(w, 403, "forbidden", "You do not have access to this Code workspace.")
			return "", "", false, true, false
		}
		if claims.AccessToken != nil && !claims.AccessToken.Allows("files:read") {
			externalError(w, 403, "insufficient_scope", "This token does not allow this asset.")
			return "", "", false, true, false
		}
		codeRelative := strings.Join(parts[4:], "/")
		if codeRelative != "" {
			if _, confined := confineSharedProjectPath(codeRoot, codeRelative); !confined {
				externalError(w, 403, "protected_path", "This part of the Code workspace is private to its owner.")
				return "", "", false, true, false
			}
		}
		relative = strings.Join(parts[1:], "/")
		if wf.Private(relative) {
			externalError(w, 403, "protected_path", "Private workspace files are not shareable.")
			return "", "", false, true, false
		}
		return workspaceref.PhysicalPathOf(owner, "Chats"), relative, codeRelative == "", true, true
	}
	if !crewLinkReadAllowed(api, claims, crewRoot) {
		externalError(w, 403, "forbidden", "You do not have access to this Crew.")
		return "", "", false, true, false
	}
	if token := claims.AccessToken; token != nil {
		manifest, err := readCrewProjectManifests(r.Context(), "work", crewRoot)
		if err != nil || !token.Allows("crews:read") || !token.AllowsCrew(manifest.ID) {
			externalError(w, 403, "insufficient_scope", "This token does not allow this Crew asset.")
			return "", "", false, true, false
		}
	}
	rootView := crewRelative == ""
	if rootView {
		crewRelative = "."
	} else if _, confined := confineSharedProjectPath(crewRoot, crewRelative); !confined {
		externalError(w, 403, "protected_path", "This part of the Crew is private to its owner.")
		return "", "", false, true, false
	}
	if wf.Private(crewRelative) || !externalFileGuard(r.Context(), false).Allows(crewRelative, false) {
		externalError(w, 403, "protected_path", "File is private or outside folder grants.")
		return "", "", false, true, false
	}
	return crewRoot, crewRelative, rootView, true, true
}

// crewRootListingVisible hides the crew's private areas from a reader's
// listing of the crew root. name is relative to the crew root.
func crewRootListingVisible(name string) bool {
	return !sharedProjectHiddenPath(name)
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
