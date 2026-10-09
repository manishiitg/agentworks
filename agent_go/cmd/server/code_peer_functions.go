package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// errCodeHasNoFunctions: Code is a private space, Crews and workflows are
// shared, and nothing shared reaches into a private space (owner, 2026-10-09).
var errCodeHasNoFunctions = fmt.Errorf("Code is private and has no functions: nothing outside a Code project can call it. To reach another chat of the same Code, use ask_project_chat")

// resolveFunctionTarget resolves a Crew or workflow. A Code target resolves
// only for a chat of that same Code (answering a call it made to a sibling
// chat); every other call into a Code project is refused.
func resolveFunctionTarget(ctx context.Context, claims *UserClaims, caller triggerLinkCaller, raw string) (triggerTarget, error) {
	query := strings.TrimPrefix(strings.TrimSpace(raw), "#")
	if strings.HasPrefix(strings.ToLower(query), "code:") || isCodeProjectPath(query) {
		if !strings.EqualFold(caller.Stamp.ProfileID, codeproduct.ProfileID) {
			return triggerTarget{}, errCodeHasNoFunctions
		}
		if strings.HasPrefix(strings.ToLower(query), "code:") {
			query = query[len("code:"):]
		}
		target, err := resolveOwnedCodePeer(ctx, claims, caller, strings.TrimSpace(query))
		if err != nil {
			return triggerTarget{}, err
		}
		if target.CrewID != caller.Stamp.ID {
			return triggerTarget{}, errCodeHasNoFunctions
		}
		return target, nil
	}
	return resolveTriggerTarget(ctx, claims, raw)
}

func resolveOwnedCodePeer(ctx context.Context, claims *UserClaims, caller triggerLinkCaller, query string) (triggerTarget, error) {
	denied := func() (triggerTarget, error) {
		return triggerTarget{}, fmt.Errorf("private Code target is unavailable or access denied")
	}
	if claims == nil {
		return denied()
	}
	ownerID := sanitizeUserIDForPath(claims.UserID)
	if strings.TrimSpace(query) == "" || authorizeOwnedCodeCaller(ctx, claims.UserID, ownerID, caller) != nil {
		return denied()
	}
	root := agentProfileRuntimeWorkspace(ownerID, codeproduct.ProjectsRoot)
	store := defaultProductProjectStore()
	paths, exists, err := store.listPaths(ctx, root)
	if err != nil || !exists {
		return denied()
	}
	want := strings.ToLower(strings.Trim(strings.TrimSpace(query), "/"))
	var found *triggerTarget
	for _, candidate := range paths {
		candidate = strings.Trim(strings.TrimSpace(candidate), "/")
		if !strings.HasPrefix(candidate, strings.Trim(root, "/")+"/") || !strings.HasSuffix(candidate, "/product.json") {
			continue
		}
		workspacePath := path.Dir(candidate)
		rawManifest, present, readErr := store.read(ctx, candidate)
		if readErr != nil || !present {
			continue
		}
		var manifest productProjectManifest
		if json.Unmarshal([]byte(rawManifest), &manifest) != nil || manifest.Product != codeproduct.ProfileID || strings.TrimSpace(manifest.ID) == "" {
			continue
		}
		logicalPath := normalizeConversationWorkspace(workspacePath)
		matches := false
		for _, value := range []string{manifest.ID, manifest.Title, path.Base(workspacePath), workspacePath, logicalPath} {
			if strings.ToLower(strings.TrimSpace(value)) == want {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		if !codeRoleFor(ctx, claims.UserID, ownerID, manifest.ID).atLeast(codeRoleEditor) {
			continue
		}
		if found != nil && found.Path != workspacePath {
			return triggerTarget{}, fmt.Errorf("private Code target %q is ambiguous; use its exact ID", query)
		}
		target := triggerTarget{Kind: triggerCallerCrew, Path: workspacePath, Label: "private Code workspace", CrewID: manifest.ID, CrewProfile: codeproduct.ProfileID, CrewOwner: ownerID}
		found = &target
	}
	if found == nil {
		return denied()
	}
	return *found, nil
}

// authorizeOwnedCodeCaller verifies the actual actor and a Code chat's source
// manifest. The only callers of a Code project are chats of that same Code
// (PLAT-648), so any other caller type is refused.
func authorizeOwnedCodeCaller(ctx context.Context, actorID, ownerID string, caller triggerLinkCaller) error {
	denied := fmt.Errorf("private Code target is unavailable or access denied")
	if !codeRoleFor(ctx, actorID, ownerID, "").atLeast(codeRoleOwner) || strings.TrimSpace(caller.Stamp.ID) == "" {
		return denied
	}
	claims := &UserClaims{UserID: actorID}
	ctx = context.WithValue(ctx, UserContextKey, claims)
	if !userAllowedProduct(claims, codeproduct.ProfileID) {
		return denied
	}
	if caller.Stamp.Type != triggerCallerCrew || caller.Stamp.ProfileID != codeproduct.ProfileID {
		return denied
	}
	root := canonicalCrewWorkspaceRoot(agentProfileRuntimeWorkspace(actorID, caller.Path))
	rootRef := workspaceref.MustParse(root)
	if projectsRoot, _, ok := rootRef.ProjectRoot(); !ok || projectsRoot != workspaceref.CodeProjectsRoot || !rootRef.OwnedBy(ownerID) || rootRef.String() != root {
		return denied
	}
	raw, present, err := defaultProductProjectStore().read(ctx, root+"/product.json")
	var manifest productProjectManifest
	if err != nil || !present || json.Unmarshal([]byte(raw), &manifest) != nil || manifest.Product != codeproduct.ProfileID || manifest.ID != caller.Stamp.ID {
		return denied
	}
	return nil
}
