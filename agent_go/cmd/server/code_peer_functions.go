package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// errCodeHasNoFunctions: Code is a private space, Crews and workflows are
// shared, and nothing shared reaches into a private space (owner, 2026-10-09).

// resolveFunctionTarget resolves a Crew or workflow. A Code target resolves
// only for a chat of that same Code (answering a call it made to a sibling
// chat); every other call into a Code project is refused.
func resolveFunctionTarget(ctx context.Context, claims *UserClaims, caller triggerLinkCaller, raw string) (triggerTarget, error) {
	return resolveTriggerTarget(ctx, claims, raw)
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
	if !userAllowedProduct(claims, workproduct.ProfileID) {
		return denied
	}
	if caller.Stamp.Type != triggerCallerCrew || caller.Stamp.ProfileID != workproduct.ProfileID {
		return denied
	}
	root := canonicalCrewWorkspaceRoot(agentProfileRuntimeWorkspace(actorID, caller.Path))
	rootRef := workspaceref.MustParse(root)
	if !isProjectWorkspacePath(root) || projectStorageOwner(root) != sanitizeUserIDForPath(ownerID) || rootRef.String() != root {
		return denied
	}
	raw, present, err := defaultProductProjectStore().read(ctx, root+"/product.json")
	var manifest productProjectManifest
	if err != nil || !present || json.Unmarshal([]byte(raw), &manifest) != nil || manifest.Product != workproduct.ProfileID || manifest.ID != caller.Stamp.ID {
		return denied
	}
	return nil
}
