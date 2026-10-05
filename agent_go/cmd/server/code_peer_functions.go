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
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Code targets are resolved privately, under the actual actor's own tree.
// The public Crew/workflow resolver and catalog never enumerate Codes.
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

// authorizeCodePeerIDs checks that the actor owns both private Codes.
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

// authorizeOwnedCodeCaller verifies the actual actor and the source manifest,
// never substituting a shared Crew/workflow's owner for its calling person.
// Path and ID must both match. Workflow access requires explicit ownership;
// legacy access, editors, readers and a general admin bypass are insufficient.
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
	switch caller.Stamp.Type {
	case triggerCallerCrew:
		profileID := caller.Stamp.ProfileID
		if profileID != crewProfileID && profileID != codeproduct.ProfileID {
			return denied
		}
		if !userAllowedProduct(claims, profileID) {
			return denied
		}
		root := canonicalCrewWorkspaceRoot(agentProfileRuntimeWorkspace(actorID, caller.Path))
		wantRoot := workspaceref.CrewProjectsRoot
		if profileID == codeproduct.ProfileID {
			wantRoot = workspaceref.CodeProjectsRoot
		}
		rootRef := workspaceref.MustParse(root)
		if folder, shared := rootRef.SharedProjectRoot(); shared && profileID == crewProfileID {
			// A Crew at the shared root: its owner is the server's registry (PLAT-442 step 4).
			if sharedCrewOwner(folder) != ownerID {
				return denied
			}
		} else if projectsRoot, _, ok := rootRef.ProjectRoot(); !ok || projectsRoot != wantRoot || !rootRef.OwnedBy(ownerID) || rootRef.String() != root {
			return denied
		}
		raw, present, err := defaultProductProjectStore().read(ctx, root+"/product.json")
		var manifest productProjectManifest
		if err != nil || !present || json.Unmarshal([]byte(raw), &manifest) != nil || manifest.Product != profileID || manifest.ID != caller.Stamp.ID {
			return denied
		}
	case triggerCallerWorkflow:
		if caller.Stamp.ProfileID != "" || !userAllowedProduct(claims, "agentworks") {
			return denied
		}
		if _, err := authorizeWorkflowContextPaths(ctx, []string{caller.Path}); err != nil {
			return denied
		}
		manifest, present, err := ReadWorkflowManifest(ctx, caller.Path)
		if err != nil || !present || manifest == nil || manifest.ID != caller.Stamp.ID ||
			!containsID(manifest.effectiveOwners(), actorID) || workflowAccessForManifest(claims, manifest) != WorkflowAccessOwner {
			return denied
		}
	default:
		return denied
	}
	return nil
}

// connectCodePeerTarget writes a hidden internal binding for an owned source.
// Code's public trigger API cannot create these bindings.
func (api *StreamingAPI) connectCodePeerTarget(ctx context.Context, actorID string, caller triggerLinkCaller, target triggerTarget) (string, bool, error) {
	ownerID := target.CrewOwner
	if target.CrewProfile != codeproduct.ProfileID ||
		(caller.Stamp.ProfileID == codeproduct.ProfileID && caller.Stamp.ID == target.CrewID) ||
		authorizeOwnedCodeCaller(ctx, actorID, ownerID, caller) != nil {
		return "", false, fmt.Errorf("private Code target is unavailable or access denied")
	}
	sourcePath := canonicalCrewWorkspaceRoot(agentProfileRuntimeWorkspace(actorID, caller.Path))
	productWebhookConfigMu.Lock()
	defer productWebhookConfigMu.Unlock()
	_, binding, manifest, err := api.productSchedules.codePeerProject(ctx, ownerID, target.CrewID)
	if err != nil {
		return "", false, err
	}
	for _, trigger := range manifest.Triggers {
		if trigger.IsInternal() && trigger.Enabled && trigger.Caller.matchesAnyPresented(caller.Stamp) && (canonicalCrewWorkspaceRoot(agentProfileRuntimeWorkspace(actorID, trigger.PrivateCallerPath)) == sourcePath || (trigger.PrivateCallerPath == "" && caller.Stamp.ProfileID == codeproduct.ProfileID)) {
			return trigger.ID, false, nil
		}
	}
	stamp := caller.Stamp
	trigger := productWebhookTrigger{ID: uuid.NewString(), Name: "Called by " + caller.Label, Enabled: true,
		Message: crewTargetMessage(caller), Kind: triggerKindInternal, Caller: &stamp, PrivateCallerPath: sourcePath, RunDestination: runDestinationIsolated}
	if err := validateProductWebhook(trigger); err != nil {
		return "", false, err
	}
	manifest.Triggers = append(manifest.Triggers, trigger)
	if err := api.productSchedules.writeProjectManifest(ctx, binding, manifest); err != nil {
		return "", false, err
	}
	return trigger.ID, true, nil
}

// codePeerRunBinding runs a function in the owner's isolated target chat.
func codePeerRunBinding(ctx context.Context, actorID string, profile agentprofiles.Profile, targetID, targetPath, triggerID, title string) (productConversationBinding, error) {
	project, err := resolveCrewProjectBinding(ctx, actorID, profile, targetID, targetPath)
	if err != nil || !workspacePathsMatchForUser(actorID, project.Binding.WorkspacePath, targetPath) ||
		!codeRoleFor(ctx, actorID, project.OwnerID, targetID).atLeast(codeRoleEditor) {
		return productConversationBinding{}, fmt.Errorf("private Code target is unavailable or access denied")
	}
	return isolateProjectAutomationBinding(project.Binding, targetID, "trigger", triggerID, title)
}

// This is the queued execution boundary. Re-read the binding and source
// ownership instead of trusting authorization captured at submission.
func (s *ProductScheduleService) codeFunctionRunBinding(ctx context.Context, job productScheduleJob, title string) (productConversationBinding, error) {
	ownerID, ok := crewProjectOwnerID(job.WorkspacePath)
	if !ok || job.CodeCaller == nil {
		return productConversationBinding{}, ErrInternalTriggerNotFound
	}
	_, target, _, trigger, err := s.findInternalProductTrigger(ctx, job.UserID, codeproduct.ProfileID, job.ProjectID, job.Schedule.ID, codePeerTriggerAccess{ownerID, *job.CodeCaller})
	if err != nil {
		return productConversationBinding{}, err
	}
	if canonicalCrewWorkspaceRoot(target.WorkspacePath) != canonicalCrewWorkspaceRoot(job.WorkspacePath) || trigger.PrivateCallerPath != job.CodeCallerPath {
		return productConversationBinding{}, ErrInternalTriggerNotFound
	}
	if !job.Schedule.Isolated {
		return resolveProductConversationBinding(ctx, job.UserID, job.Profile, job.ProjectID)
	}
	return codePeerRunBinding(ctx, job.UserID, job.Profile, job.ProjectID, job.WorkspacePath, job.Schedule.ID, title)
}

func codePeerPrivateRunsWorkspace(actorID, targetPath, targetID string) string {
	ownerID, _ := crewProjectOwnerID(targetPath)
	key := sha256.Sum256([]byte(ownerID + "\x00" + targetID))
	return workspaceref.PhysicalPath(actorID, "chat_history", "code-peer-runs", hex.EncodeToString(key[:]))
}

func isPrivateCodePeerTrigger(profileID string, trigger productWebhookTrigger) bool {
	return profileID == codeproduct.ProfileID && trigger.IsInternal() && trigger.Caller != nil &&
		(trigger.Caller.Type == triggerCallerWorkflow || (trigger.Caller.Type == triggerCallerCrew && (trigger.Caller.ProfileID == codeproduct.ProfileID || trigger.Caller.ProfileID == crewProfileID)))
}
