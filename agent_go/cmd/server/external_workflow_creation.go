package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

const externalWorkflowCreatorDescription = "Create a workflow you own using the same creator as app chat. Requires account creation rights, AgentWorks product access and builder:chat covering all workflows. Supply folder_name (kebab-case), workflow_json and plan_json; the complete plan graph is validated before writing and existing folders/IDs are never overwritten. Ownership is assigned to the authenticated user. Configure folder/KB/project access separately after creation. Returns workflow_id for builder_chat, KB bindings and run tools. Structure creation does not run the workflow or author scripted-step code."

func externalWorkflowCreationAllowed(claims *UserClaims) bool {
	return externalBuilderEnabled() && claims != nil && claims.AccessToken != nil &&
		claims.AccessToken.BuilderAccess() && claims.AccessToken.AllWorkflows &&
		userAccessForClaims(claims).CanCreate && userAllowedProduct(claims, "agentworks")
}

func (api *StreamingAPI) externalCreateWorkflow(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if !externalWorkflowCreationAllowed(claims) {
		externalError(w, 403, "forbidden", "Workflow creation requires account creation rights and unrestricted Builder permission.")
		return
	}
	// Recheck local-token revocation or the OAuth grant before privileged writes.
	live, err := activeExternalGrantClaims(r.Context(), claims.AccessToken.ID)
	if err != nil || live.UserID != claims.UserID || !externalWorkflowCreationAllowed(live) {
		externalError(w, 403, "grant_unavailable", "Workflow creation grant is no longer active.")
		return
	}
	ctx := context.WithValue(r.Context(), UserContextKey, live)
	workflow, _ := args["workflow_json"].(map[string]any)
	if workflow == nil {
		externalError(w, 400, "invalid_arguments", "workflow_json must be an object.")
		return
	}
	// Creation cannot bypass the separate host-folder approvals or KB/project
	// binding checks. Empty fields from exported manifests are harmless.
	for _, field := range []string{"folder_access", "folder_access_requests", "shared_knowledgebase", "knowledgebase_sources", "crew_attachments", "workflow_context_paths", "knowledgebase_contract_history", "knowledgebase_migration"} {
		for key, value := range workflow {
			if !strings.EqualFold(key, field) {
				continue
			}
			encoded, _ := json.Marshal(value)
			if value != nil && string(encoded) != "[]" && string(encoded) != "{}" {
				externalError(w, 400, "invalid_arguments", field+" must be configured through its authorized access tools after creation.")
				return
			}
		}
	}
	if kind, _ := workflow["kind"].(string); kind != "" {
		externalError(w, 400, "invalid_arguments", "Use create_relay to create a Relay.")
		return
	}
	workflowID, _ := workflow["id"].(string)
	if strings.TrimSpace(workflowID) != workflowID || workflowID == "" {
		externalError(w, 400, "invalid_arguments", "workflow_json.id must be a non-empty ID without surrounding whitespace.")
		return
	}
	if !userAllowedWorkflowID(live, workflowID) {
		externalError(w, 403, "forbidden", "The account workflow allowlist does not permit this workflow ID.")
		return
	}
	workflow["created_by"] = live.UserID
	workflow["access"] = map[string]any{"owners": []any{live.UserID}, "readers": []any{}}
	defaultWorkflowCreatorGlobalSecretsToNone(workflow)
	encoded, err := json.Marshal(workflow)
	var manifest WorkflowManifest
	if err == nil {
		err = json.Unmarshal(encoded, &manifest)
	}
	if err == nil && manifest.Kind != "" {
		externalError(w, 400, "invalid_arguments", "Use create_relay to create a Relay.")
		return
	}
	if err == nil {
		err = ValidateManifest(&manifest)
	}
	if err == nil {
		err = enforceDeploymentBrowserCapability(&manifest.Capabilities)
	}
	if err == nil {
		err = validateWorkflowSlackConnectionID(manifest.Capabilities.SlackConnectionID)
	}
	if err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	// Match the app creation path: pin the current product default model,
	// and persist any deployment normalization of browser capabilities.
	if manifest.Capabilities.LLMConfig == nil {
		manifest.Capabilities.LLMConfig = productDefaultWorkflowLLMConfig(ctx)
	}
	capabilitiesJSON, err := json.Marshal(manifest.Capabilities)
	if err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	var capabilities map[string]any
	if err := json.Unmarshal(capabilitiesJSON, &capabilities); err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	workflow["capabilities"] = capabilities
	result, err := api.handleWorkflowCreatorTool(ctx, args)
	if err != nil {
		status, code := 400, "creation_failed"
		if errors.Is(err, errWorkflowCreationConflict) {
			status, code = 409, "creation_conflict"
		}
		externalError(w, status, code, err.Error())
		return
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(result), &response); err != nil {
		externalError(w, 500, "creation_failed", "Workflow was created but its response could not be read.")
		return
	}
	externalJSON(w, response)
}
