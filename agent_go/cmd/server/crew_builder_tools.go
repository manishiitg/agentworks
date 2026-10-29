package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// registerCrewBuilderTools gives the workflow Builder discovery and mutation
// tools for Crew triggers and read-only Crew attachments. It shares the
// registerWebhookTools Builder gate: interactive Builder chat with plan
// authoring. Crew access is enforced by user-scoped crew lookups on every
// call; workflow access by the bound workspace authorization.
func (api *StreamingAPI) registerCrewBuilderTools(reg definitionToolRegistrar, userID, workspace string) error {
	if api.projectActions == nil {
		return nil
	}
	authorize := func(ctx context.Context, readOnly bool) (context.Context, error) {
		ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: userID})
		if userAccessForClaims(&UserClaims{UserID: userID}).Disabled {
			return nil, fmt.Errorf("Access denied: disabled account")
		}
		if _, err := authorizeWorkflowContextPaths(ctx, []string{workspace}); err != nil {
			return nil, fmt.Errorf("Workflow is unavailable or access denied")
		}
		if !readOnly {
			manifest, exists, err := ReadWorkflowManifest(ctx, workspace)
			if err != nil || !exists || manifest == nil {
				return nil, fmt.Errorf("Workflow is unavailable or access denied")
			}
			if access := workflowAccessForManifest(GetUserFromContext(ctx), manifest); access != WorkflowAccessWrite && access != WorkflowAccessOwner {
				return nil, fmt.Errorf("Crew setup changes require current workflow write access")
			}
		}
		return ctx, nil
	}
	if err := reg.RegisterCustomTool("manage_crew_trigger", "List, create, update, or delete a Crew project's triggers from Builder chat. First list to discover a crew's triggers and their kind/caller bindings. Use kind=internal with a workflow caller to bind a calling workflow without a public URL or secret; the caller defaults to this workflow when omitted. Internal triggers never expose a URL or secret. create returns a one-time secret only for public triggers: provide it only to the requesting user, never shell logs. All calls enforce current crew and workflow permissions.", map[string]interface{}{
		"type": "object", "additionalProperties": false, "required": []string{"action", "crew_project_id"}, "properties": map[string]interface{}{
			"action":          map[string]interface{}{"type": "string", "enum": []string{"list", "create", "update", "delete"}},
			"crew_project_id": map[string]interface{}{"type": "string", "description": "Stable Crew project ID from list_accessible_workflows."},
			"crew_profile_id": map[string]interface{}{"type": "string", "description": "Crew product profile; defaults to work."},
			"id":              map[string]interface{}{"type": "string"}, "name": map[string]interface{}{"type": "string"}, "message": map[string]interface{}{"type": "string"},
			"auth_mode": map[string]interface{}{"type": "string", "enum": []string{"bearer", "github"}}, "enabled": map[string]interface{}{"type": "boolean"}, "rotate_secret": map[string]interface{}{"type": "boolean"},
			"run_destination": map[string]interface{}{"type": "string", "enum": []string{runDestinationChat, runDestinationIsolated}, "description": "Where trigger runs execute. chat (default) runs in the main Crew conversation; isolated gives this webhook its own continuing conversation. Internal project callers use the main chat when source and target share any owner; otherwise a separate continuing conversation."},
			"kind":            map[string]interface{}{"type": "string", "enum": []string{"internal"}},
			"caller":          triggerCallerToolSchema(triggerCallerWorkflow, triggerCallerCrew),
		}}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx, err := authorize(ctx, args["action"] == "list")
		if err != nil {
			return "", err
		}
		projectID, _ := args["crew_project_id"].(string)
		profileID, _ := args["crew_profile_id"].(string)
		if strings.TrimSpace(profileID) == "" {
			profileID = "work"
		}
		action, _ := args["action"].(string)
		switch action {
		case "list":
			triggers, err := api.projectActions.projectWebhookConfigs(ctx, userID, profileID, projectID)
			if err != nil {
				return "", err
			}
			out := make([]productWebhookResponse, 0, len(triggers))
			for _, trigger := range triggers {
				out = append(out, productWebhookDTO(trigger))
			}
			encoded, err := json.MarshalIndent(map[string]interface{}{"triggers": out}, "", "  ")
			return string(encoded), err
		case "create":
			name, _ := args["name"].(string)
			message, _ := args["message"].(string)
			authMode, _ := args["auth_mode"].(string)
			destination, _ := args["run_destination"].(string)
			kind, _ := args["kind"].(string)
			enabled, ok := args["enabled"].(bool)
			if !ok {
				enabled = true
			}
			caller := triggerCallerFromArgs(args)
			if isInternalTriggerKind(kind) && caller == nil {
				workflowID, err := boundWorkflowID(ctx, workspace)
				if err != nil {
					return "", err
				}
				caller = &triggerCaller{Type: triggerCallerWorkflow, ID: workflowID}
			}
			if err := authorizeTriggerCallerWorkflow(ctx, userID, caller); err != nil {
				return "", err
			}
			response, _, err := api.projectActions.saveProductWebhookConfig(ctx, userID, productWebhookRequest{ProfileID: profileID, ProjectID: projectID, Name: name, Message: message, AuthMode: authMode, Enabled: enabled, RunDestination: destination, Kind: kind, Caller: caller}, "")
			if err != nil {
				return "", err
			}
			encoded, err := json.MarshalIndent(response, "", "  ")
			return string(encoded), err
		case "update":
			id, _ := args["id"].(string)
			triggers, err := api.projectActions.projectWebhookConfigs(ctx, userID, profileID, projectID)
			if err != nil {
				return "", err
			}
			var current *productWebhookTrigger
			for i := range triggers {
				if triggers[i].ID == strings.TrimSpace(id) {
					current = &triggers[i]
					break
				}
			}
			if current == nil {
				return "", fmt.Errorf("trigger not found")
			}
			name, message, enabled, destination := current.Name, current.Message, current.Enabled, firstNonEmptyTrimmed(current.RunDestination, runDestinationChat)
			authMode := ""
			if current.Webhook != nil {
				authMode = current.Webhook.AuthMode
			}
			kind, caller := current.Kind, current.Caller
			if value, ok := args["name"].(string); ok && strings.TrimSpace(value) != "" {
				name = value
			}
			if value, ok := args["message"].(string); ok && strings.TrimSpace(value) != "" {
				message = value
			}
			if value, ok := args["auth_mode"].(string); ok && strings.TrimSpace(value) != "" {
				authMode = value
			}
			if value, ok := args["enabled"].(bool); ok {
				enabled = value
			}
			if value, ok := args["run_destination"].(string); ok && strings.TrimSpace(value) != "" {
				destination = value
			}
			if value, ok := args["kind"].(string); ok && strings.TrimSpace(value) != "" {
				kind = value
			}
			if updated := triggerCallerFromArgs(args); updated != nil {
				caller = updated
			}
			if err := authorizeTriggerCallerWorkflow(ctx, userID, caller); err != nil {
				return "", err
			}
			rotate, _ := args["rotate_secret"].(bool)
			response, _, err := api.projectActions.saveProductWebhookConfig(ctx, userID, productWebhookRequest{ProfileID: profileID, ProjectID: projectID, Name: name, Message: message, AuthMode: authMode, Enabled: enabled, RotateSecret: rotate, RunDestination: destination, Kind: kind, Caller: caller}, current.ID)
			if err != nil {
				return "", err
			}
			encoded, err := json.MarshalIndent(response, "", "  ")
			return string(encoded), err
		case "delete":
			id, _ := args["id"].(string)
			if err := api.projectActions.deleteProductWebhookConfig(ctx, userID, profileID, projectID, strings.TrimSpace(id)); err != nil {
				return "", err
			}
			return "Trigger deleted.", nil
		default:
			return "", fmt.Errorf("Unknown crew trigger action")
		}
	}, "workflow_webhooks"); err != nil {
		return err
	}

	if err := api.registerCrewAttachmentTool(reg, userID, workspace, authorize); err != nil {
		return err
	}
	return nil
}

// boundWorkflowID resolves the workflow ID of the Builder's workspace so
// internal trigger bindings default their caller to the workflow being built.
func boundWorkflowID(ctx context.Context, workspace string) (string, error) {
	manifest, exists, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil || !exists || manifest == nil {
		return "", fmt.Errorf("Workflow is unavailable or access denied")
	}
	if strings.TrimSpace(manifest.ID) == "" {
		return "", fmt.Errorf("workflow manifest has no ID")
	}
	return manifest.ID, nil
}

// authorizeTriggerCallerWorkflow verifies that an explicit workflow caller
// names a real workflow the user may access, so bindings cannot be minted
// for foreign workflows.
func authorizeTriggerCallerWorkflow(ctx context.Context, userID string, caller *triggerCaller) error {
	if caller == nil || !strings.EqualFold(strings.TrimSpace(caller.Type), triggerCallerWorkflow) {
		return nil
	}
	workspacePath, _, err := findWorkflowManifestByID(ctx, caller.ID)
	if err != nil {
		return fmt.Errorf("caller workflow %q does not exist", strings.TrimSpace(caller.ID))
	}
	ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: userID})
	if _, err := authorizeWorkflowContextPaths(ctx, []string{workspacePath}); err != nil {
		return fmt.Errorf("caller workflow %q is unavailable or access denied", strings.TrimSpace(caller.ID))
	}
	return nil
}

// crewAttachmentReadRoots returns the crew workspace roots attached to a
// workflow, for session read grants. Each root is granted only when the
// stored attachment still equals the freshly authorized project binding:
// shape checks alone cannot tell `_users/owner/.../projects/server A` from
// `_users/other/.../projects/server A`, so the grant is derived from the
// binding the access check just authorized, never from the stored path.
// Missing manifests, revoked crews, and retargeted roots yield no grant.
// A nil service or empty user grants nothing; crew features require a user.
func crewAttachmentReadRoots(ctx context.Context, svc *ProjectActionDispatcher, userID, workspace string) []string {
	if svc == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	manifest, exists, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil || !exists || manifest == nil {
		return nil
	}
	roots := make([]string, 0, len(manifest.CrewAttachments))
	for _, attachment := range liveCrewAttachmentBindings(manifest.CrewAttachments) {
		profileID := normalizeInternalProfileID(attachment.CrewProfileID)
		_, binding, _, _, err := svc.projectManifestAnyOwner(ctx, userID, profileID, strings.TrimSpace(attachment.CrewProjectID))
		if err != nil {
			continue
		}
		authorized := workflowtypes.CanonicalCrewAttachmentRoot(binding.WorkspacePath)
		if authorized == "" || workflowtypes.CanonicalCrewAttachmentRoot(attachment.CrewWorkspacePath) != authorized {
			continue
		}
		roots = append(roots, authorized)
	}
	return roots
}

// attachCrewProjectToWorkflow records a read-only crew attachment on a
// workflow and reconciles live sessions. It returns the stored root and
// whether the attachment was newly created: attaching the same crew under
// the same alias adopts the existing record (created=false) so retried
// creation flows converge; every other conflict is an error.
func attachCrewProjectToWorkflow(ctx context.Context, svc *ProjectActionDispatcher, userID, workspace, profileID, projectID, alias string) (string, bool, error) {
	alias = strings.TrimSpace(alias)
	if err := workflowtypes.ValidateCrewAttachmentAlias(alias); err != nil {
		return "", false, err
	}
	projectID = strings.TrimSpace(projectID)
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		profileID = "work"
	}
	if projectID == "" {
		return "", false, fmt.Errorf("crew_project_id is required")
	}
	manifest, exists, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil || !exists || manifest == nil {
		return "", false, fmt.Errorf("Workflow is unavailable or access denied")
	}
	for _, attachment := range manifest.CrewAttachments {
		sameAlias := attachment.Alias == alias
		sameCrew := strings.TrimSpace(attachment.CrewProfileID) == profileID && strings.TrimSpace(attachment.CrewProjectID) == projectID
		if sameAlias && sameCrew {
			return strings.TrimSpace(attachment.CrewWorkspacePath), false, nil
		}
		if sameAlias {
			return "", false, fmt.Errorf("alias %q is already attached", alias)
		}
		if sameCrew {
			return "", false, fmt.Errorf("crew project %q is already attached as %q", projectID, attachment.Alias)
		}
	}
	if svc == nil {
		return "", false, fmt.Errorf("Crew project is unavailable or access denied")
	}
	_, binding, _, err := svc.projectManifest(ctx, userID, profileID, projectID)
	if err != nil {
		return "", false, fmt.Errorf("Crew project is unavailable or access denied")
	}
	previousRoots := crewAttachmentStoredRoots(manifest.CrewAttachments)
	root := filepath.ToSlash(binding.WorkspacePath)
	manifest.CrewAttachments = append(manifest.CrewAttachments, workflowtypes.CrewAttachment{
		ID: uuid.NewString(), Alias: alias, CrewProfileID: profileID, CrewProjectID: projectID,
		CrewWorkspacePath: root, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err := writeWorkflowManifest(ctx, workspace, manifest); err != nil {
		return "", false, err
	}
	live := liveCrewAttachmentBindings(manifest.CrewAttachments)
	common.ReconcileSessionCrewAttachments(workspace, previousRoots, crewAttachmentStoredRoots(live), workflowtypes.CrewAttachmentEnvKeys(live))
	return root, true, nil
}

// liveCrewAttachmentBindings drops attachments whose stored binding no
// longer validates, so grants and session env only ever carry live roots.
func liveCrewAttachmentBindings(attachments []workflowtypes.CrewAttachment) []workflowtypes.CrewAttachment {
	live := make([]workflowtypes.CrewAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		if err := workflowtypes.ValidateCrewAttachmentBinding(attachment); err != nil {
			continue
		}
		live = append(live, attachment)
	}
	return live
}

// crewAttachmentStoredRoots lists the stored workspace roots of the given
// attachments. Previous-root snapshots use this on the unfiltered list so
// revocation also catches roots that no longer validate.
func crewAttachmentStoredRoots(attachments []workflowtypes.CrewAttachment) []string {
	roots := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		if root := strings.TrimSpace(attachment.CrewWorkspacePath); root != "" {
			roots = append(roots, root)
		}
	}
	return roots
}

// writeWorkflowManifest persists a mutated workflow manifest.
func writeWorkflowManifest(ctx context.Context, workspace string, manifest *WorkflowManifest) error {
	manifest.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode workflow manifest: %w", err)
	}
	if err := writeFileToWorkspace(ctx, manifestPath(workspace), string(data)); err != nil {
		return fmt.Errorf("cannot save workflow manifest: %w", err)
	}
	return nil
}

// registerCrewAttachmentTool is shared by the native Builder and direct MCP.
// The transport supplies its current, bound workflow authorization.
func (api *StreamingAPI) registerCrewAttachmentTool(reg definitionToolRegistrar, userID, workspace string, authorize func(context.Context, bool) (context.Context, error)) error {
	return reg.RegisterCustomTool("manage_crew_attachment", "Attach a Crew project's workspace to this workflow read-only so downstream steps can read crew files as <alias>/<crew-relative path>. First discover crews with list_accessible_workflows. Attach validates crew access and alias uniqueness, then records the fixed crew workspace path; reads re-resolve through the alias on every access while run-start and per-step checks fail fast on revoked access. Detach removes the alias immediately.", map[string]interface{}{
		"type": "object", "additionalProperties": false, "required": []string{"action"}, "properties": map[string]interface{}{
			"action":          map[string]interface{}{"type": "string", "enum": []string{"list", "attach", "detach"}},
			"alias":           map[string]interface{}{"type": "string", "description": "Short lowercase alias for crew file reads, for example reviewer-crew."},
			"crew_project_id": map[string]interface{}{"type": "string", "description": "Stable Crew project ID from list_accessible_workflows."},
			"crew_profile_id": map[string]interface{}{"type": "string", "description": "Crew product profile; defaults to work."},
		}}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx, err := authorize(ctx, args["action"] == "list")
		if err != nil {
			return "", err
		}
		action, _ := args["action"].(string)
		manifest, exists, err := ReadWorkflowManifest(ctx, workspace)
		if err != nil || !exists || manifest == nil {
			return "", fmt.Errorf("Workflow is unavailable or access denied")
		}
		switch action {
		case "list":
			encoded, err := json.MarshalIndent(map[string]interface{}{"attachments": manifest.CrewAttachments}, "", "  ")
			return string(encoded), err
		case "attach":
			alias := strings.TrimSpace(fmt.Sprint(args["alias"]))
			projectID := strings.TrimSpace(fmt.Sprint(args["crew_project_id"]))
			profileID := strings.TrimSpace(fmt.Sprint(args["crew_profile_id"]))
			_, created, err := attachCrewProjectToWorkflow(ctx, api.projectActions, userID, workspace, profileID, projectID, alias)
			if err != nil {
				return "", err
			}
			if !created {
				return "", fmt.Errorf("alias %q is already attached", alias)
			}
			return fmt.Sprintf("Crew project %q attached as %q (read-only). Downstream steps read crew files as %s/<path>.", projectID, alias, alias), nil
		case "detach":
			alias := strings.TrimSpace(fmt.Sprint(args["alias"]))
			kept := make([]workflowtypes.CrewAttachment, 0, len(manifest.CrewAttachments))
			found := false
			for _, attachment := range manifest.CrewAttachments {
				if attachment.Alias == alias {
					found = true
					continue
				}
				kept = append(kept, attachment)
			}
			if !found {
				return "", fmt.Errorf("alias %q is not attached", alias)
			}
			previousRoots := crewAttachmentStoredRoots(manifest.CrewAttachments)
			manifest.CrewAttachments = kept
			if err := writeWorkflowManifest(ctx, workspace, manifest); err != nil {
				return "", err
			}
			live := liveCrewAttachmentBindings(manifest.CrewAttachments)
			common.ReconcileSessionCrewAttachments(workspace, previousRoots, crewAttachmentStoredRoots(live), workflowtypes.CrewAttachmentEnvKeys(live))
			return fmt.Sprintf("Alias %q detached. Reads through it stop resolving immediately.", alias), nil
		default:
			return "", fmt.Errorf("Unknown crew attachment action")
		}
	}, "workflow_webhooks")
}
