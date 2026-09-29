package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// resolveFunctionTarget keeps Codes out of the public Crew resolver. Only a
// Code whose caller can edit it can find another editable Code under that
// same owner's tree; all other callers still see only Crews and workflows.
func resolveFunctionTarget(ctx context.Context, claims *UserClaims, caller triggerLinkCaller, raw string) (triggerTarget, error) {
	query := strings.TrimPrefix(strings.TrimSpace(raw), "#")
	explicitCode := strings.HasPrefix(strings.ToLower(query), "code:") || isCodeProjectPath(query)
	if explicitCode {
		if strings.HasPrefix(strings.ToLower(query), "code:") {
			query = query[len("code:"):]
		}
		return resolveOwnedCodePeer(ctx, claims, caller, strings.TrimSpace(query))
	}
	regular, regularErr := resolveTriggerTarget(ctx, claims, raw)
	if !strings.EqualFold(caller.Stamp.ProfileID, codeproduct.ProfileID) {
		return regular, regularErr
	}
	peer, peerErr := resolveOwnedCodePeer(ctx, claims, caller, query)
	if regularErr == nil && peerErr == nil {
		return triggerTarget{}, fmt.Errorf("%q matches a Crew/workflow and a private Code; use #code:<id> to select the Code", raw)
	}
	if regularErr == nil {
		return regular, nil
	}
	if peerErr == nil {
		return peer, nil
	}
	return triggerTarget{}, regularErr
}

func resolveOwnedCodePeer(ctx context.Context, claims *UserClaims, caller triggerLinkCaller, query string) (triggerTarget, error) {
	denied := func() (triggerTarget, error) {
		return triggerTarget{}, fmt.Errorf("private Code target is unavailable or access denied")
	}
	if claims == nil || !strings.EqualFold(caller.Stamp.ProfileID, codeproduct.ProfileID) {
		return denied()
	}
	ownerID, ok := crewProjectOwnerID(caller.Path)
	if !ok && isCodeProjectPath(caller.Path) {
		ownerID, ok = sanitizeUserIDForPath(claims.UserID), true
	}
	if !ok || strings.TrimSpace(query) == "" || !codeRoleFor(ctx, claims.UserID, ownerID, caller.Stamp.ID).atLeast(codeRoleEditor) {
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

// authorizeCodePeerIDs rechecks both grants at each phase of an internal
// call. A revoked editor must not keep a binding, poll a result or steer a run.
func authorizeCodePeerIDs(ctx context.Context, actorID, ownerID, sourceID, targetID string) error {
	denied := fmt.Errorf("private Code target is unavailable or access denied")
	if actorID == "" || ownerID == "" || sourceID == "" || targetID == "" || sourceID == targetID {
		return denied
	}
	if !codeRoleFor(ctx, actorID, ownerID, sourceID).atLeast(codeRoleEditor) || !codeRoleFor(ctx, actorID, ownerID, targetID).atLeast(codeRoleEditor) {
		return denied
	}
	return nil
}

func (s *ProductScheduleService) codePeerProject(ctx context.Context, ownerID, targetID string) (agentprofiles.Profile, productConversationBinding, productProjectManifest, error) {
	denied := fmt.Errorf("private Code target is unavailable or access denied")
	if s == nil || s.registry == nil || ownerID == "" || targetID == "" {
		return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, denied
	}
	profile, err := s.registry.Resolve(codeproduct.ProfileID, 0, ownerID)
	if err != nil {
		return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, denied
	}
	binding, err := resolveProductProjectBinding(ctx, ownerID, profile, targetID)
	if err != nil {
		return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, denied
	}
	raw, exists, err := s.readFile(ctx, binding.ManifestPath)
	if err != nil || !exists {
		return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, denied
	}
	var manifest productProjectManifest
	if json.Unmarshal([]byte(raw), &manifest) != nil || manifest.Product != codeproduct.ProfileID || manifest.ID != targetID {
		return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, denied
	}
	return profile, binding, manifest, nil
}

// connectCodePeerTarget writes only a hidden internal caller binding. Code's
// public schedule/webhook APIs remain disabled; both Codes must stay editable.
func (api *StreamingAPI) connectCodePeerTarget(ctx context.Context, actorID string, caller triggerLinkCaller, target triggerTarget) (string, bool, error) {
	ownerID := target.CrewOwner
	if caller.Stamp.Type != triggerCallerCrew || caller.Stamp.ProfileID != codeproduct.ProfileID ||
		ownerID == "" || target.CrewProfile != codeproduct.ProfileID {
		return "", false, fmt.Errorf("private Code target is unavailable or access denied")
	}
	if sourceOwner, ok := crewProjectOwnerID(caller.Path); ok && sourceOwner != ownerID {
		return "", false, fmt.Errorf("private Code target is unavailable or access denied")
	} else if !ok && sanitizeUserIDForPath(actorID) != ownerID {
		return "", false, fmt.Errorf("private Code target is unavailable or access denied")
	}
	if err := authorizeCodePeerIDs(ctx, actorID, ownerID, caller.Stamp.ID, target.CrewID); err != nil {
		return "", false, err
	}
	// Verify the source really exists under the same owner, not just a stale
	// grant record or a forged caller stamp.
	_, sourceBinding, _, err := api.productSchedules.codePeerProject(ctx, ownerID, caller.Stamp.ID)
	if err != nil || canonicalCrewWorkspaceRoot(sourceBinding.WorkspacePath) != canonicalCrewWorkspaceRoot(agentProfileRuntimeWorkspace(actorID, caller.Path)) {
		return "", false, fmt.Errorf("private Code target is unavailable or access denied")
	}
	productWebhookConfigMu.Lock()
	defer productWebhookConfigMu.Unlock()
	_, binding, manifest, err := api.productSchedules.codePeerProject(ctx, ownerID, target.CrewID)
	if err != nil {
		return "", false, err
	}
	for _, trigger := range manifest.Triggers {
		if trigger.IsInternal() && trigger.Enabled && trigger.Caller.matchesAnyPresented(caller.Stamp) {
			return trigger.ID, false, nil
		}
	}
	stamp := caller.Stamp
	trigger := productWebhookTrigger{ID: uuid.NewString(), Name: "Called by private Code workspace", Enabled: true,
		Message: crewTargetMessage(caller), Kind: triggerKindInternal, Caller: &stamp, RunDestination: runDestinationIsolated}
	if err := validateProductWebhook(trigger); err != nil {
		return "", false, err
	}
	manifest.Triggers = append(manifest.Triggers, trigger)
	if err := api.productSchedules.writeProjectManifest(ctx, binding, manifest); err != nil {
		return "", false, err
	}
	return trigger.ID, true, nil
}

// codePeerRunBinding gives each editor their own isolated target chat.
func codePeerRunBinding(ctx context.Context, actorID string, profile agentprofiles.Profile, targetID, targetPath, triggerID, title string) (productConversationBinding, error) {
	project, err := resolveCrewProjectBinding(ctx, actorID, profile, targetID, targetPath)
	if err != nil || !workspacePathsMatchForUser(actorID, project.Binding.WorkspacePath, targetPath) ||
		!codeRoleFor(ctx, actorID, project.OwnerID, targetID).atLeast(codeRoleEditor) {
		return productConversationBinding{}, fmt.Errorf("private Code target is unavailable or access denied")
	}
	return isolateProjectAutomationBinding(project.Binding, targetID, "trigger", triggerID, title)
}

func codePeerPrivateRunsWorkspace(actorID, targetPath, targetID string) string {
	ownerID, _ := crewProjectOwnerID(targetPath)
	key := sha256.Sum256([]byte(ownerID + "\x00" + targetID))
	return "_users/" + sanitizeUserIDForPath(actorID) + "/chat_history/code-peer-runs/" + hex.EncodeToString(key[:])
}

func isPrivateCodePeerTrigger(profileID string, trigger productWebhookTrigger) bool {
	return profileID == codeproduct.ProfileID && trigger.IsInternal() && trigger.Caller != nil &&
		trigger.Caller.Type == triggerCallerCrew && trigger.Caller.ProfileID == codeproduct.ProfileID
}
