package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

// Website, MCP and trusted Builder factories create through the same native
// service. The current caller creates their own project, including co-owners
// whose current turn uses the main owner's provider account.
func projectCreationGate(claims *UserClaims, profileID string) (int, string) {
	if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		return http.StatusUnauthorized, "Sign in to create a Crew."
	}
	access := userAccessForClaims(claims)
	if !isProjectProfileID(profileID) || !workspaceOriginAllowed(claims, profileID) || access.Disabled || !access.CanEdit || !access.CanCreateIn(profileID) || !workspaceCreateTokenAllowed(claims) {
		return http.StatusForbidden, "This account cannot create a Crew here. An administrator can change its create permissions."
	}
	return 0, ""
}

func projectCreationWarning(err error) string {
	return "The Crew was created, but some setup needs attention: " + err.Error() + ". Open it to finish setup; you do not need to create it again."
}

func crewCreationPath(root, name, suffix string) string {
	return root + "/" + slugifyCrewTitle(name) + "-" + suffix
}

// A display-only preview. It neither reserves an identity nor probes folders.
func (api *StreamingAPI) handleProjectCreationLocation(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := GetUserFromContext(r.Context())
	profileID := strings.TrimSpace(mux.Vars(r)["id"])
	if status, message := projectCreationGate(claims, profileID); status != 0 {
		writeAgentProfileError(w, status, message)
		return
	}
	if api == nil || api.agentProfiles == nil {
		writeAgentProfileError(w, http.StatusNotFound, "Project creation is unavailable")
		return
	}
	profile, err := api.agentProfiles.Resolve(profileID, 0, claims.UserID)
	if err != nil {
		writeAgentProfileError(w, http.StatusNotFound, "Project creation is unavailable")
		return
	}
	root, err := projectProfileProjectsRoot(profile, claims.UserID)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "The project folder could not be resolved")
		return
	}
	root = crewCreationRoot(profileID, agentProfileRuntimeWorkspace(claims.UserID, root))
	if IsLocalMode() {
		root = filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(root))
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]any{
		"folder":            crewCreationPath(root, r.URL.Query().Get("name"), "…"),
		"can_choose_folder": IsLocalMode(),
	})
}

func (api *StreamingAPI) createdProjectResponse(ctx context.Context, claims *UserClaims, profileID, id string) (map[string]any, error) {
	profile, err := api.agentProfiles.Resolve(profileID, 0, claims.UserID)
	if err != nil {
		return nil, err
	}
	binding, err := resolveProductProjectBindingWithStore(ctx, claims.UserID, profile, id, defaultProductProjectStore())
	if err != nil {
		return nil, err
	}
	root := binding.WorkspacePath
	read := func(name string) (map[string]any, error) {
		raw, exists, err := readFileFromWorkspace(ctx, root+"/"+name)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("created Crew settings are unavailable")
		}
		var doc map[string]any
		err = json.Unmarshal([]byte(raw), &doc)
		return doc, err
	}
	manifest, err := read("product.json")
	if err != nil {
		return nil, err
	}
	runtime, err := read("workflow.json")
	if err != nil {
		return nil, err
	}
	return map[string]any{"workspace_id": profileID + ":" + id, "id": id, "workspace_path": root, "files_path": crewFilesPath(root), "product_manifest": manifest, "runtime_manifest": runtime}, nil
}

func (api *StreamingAPI) handleCreateAgentProfileProject(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := GetUserFromContext(r.Context())
	profileID := strings.TrimSpace(mux.Vars(r)["id"])
	if status, message := projectCreationGate(claims, profileID); status != 0 {
		writeAgentProfileError(w, status, message)
		return
	}
	if api == nil || api.agentProfiles == nil {
		writeAgentProfileError(w, http.StatusNotFound, "Crew creation is unavailable")
		return
	}
	var spec crewSpec
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&spec); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "Send a Crew name and its optional settings.")
		return
	}
	id, setupErr := api.createWorkspaceFromSpec(r.Context(), claims, profileID, spec, false)
	if setupErr != nil && id == "" {
		writeAgentProfileError(w, http.StatusBadRequest, externalCrewCreateError(id, setupErr))
		return
	}
	response, err := api.createdProjectResponse(r.Context(), claims, profileID, id)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, externalCrewCreateError(id, err))
		return
	}
	if setupErr != nil {
		response["warnings"] = []string{projectCreationWarning(setupErr)}
	}
	api.refreshAgentWorksSoon(claims.UserID)
	writeAgentProfileJSON(w, http.StatusCreated, response)
}

func (api *StreamingAPI) projectCreationTool(profileID, userID, sessionID string, binding agentprofiles.ToolBinding, turnClaims ...*UserClaims) func(context.Context, map[string]any) (string, error) {
	var captured *UserClaims
	if len(turnClaims) > 0 && turnClaims[0] != nil {
		copy := *turnClaims[0]
		captured = &copy
	}
	return func(ctx context.Context, args map[string]any) (string, error) {
		claims := captured
		if claims == nil {
			claims = GetUserFromContext(ctx)
		}
		if claims == nil || claims.UserID != userID {
			return "", fmt.Errorf("open your Crew's Builder to create another Crew")
		}
		ctx = context.WithValue(ctx, UserContextKey, claims)
		spec := crewSpec{Name: externalArg(args, "name"), Icon: externalArg(args, "icon"), Purpose: externalArg(args, "description"), RootPath: externalArg(args, "root_path")}
		id, setupErr := api.createWorkspaceFromSpec(ctx, claims, profileID, spec, false)
		if setupErr != nil && id == "" {
			return "", fmt.Errorf("%s", externalCrewCreateError(id, setupErr))
		}
		response, err := api.createdProjectResponse(ctx, claims, profileID, id)
		if err != nil {
			return "", err
		}
		kind := "project_created"
		if binding.Interaction != nil && strings.TrimSpace(binding.Interaction.Kind) != "" {
			kind = strings.TrimSpace(binding.Interaction.Kind)
		}
		api.emitAgentProfileEvent(sessionID, &orchestratorevents.ProductInteractionEvent{Product: profileID, Kind: kind, Payload: map[string]any{"operation": "created", "project_id": id, "name": spec.Name, "icon": spec.Icon, "workspace_path": response["workspace_path"]}})
		response["status"], response["project_id"], response["name"] = "created", id, spec.Name
		if setupErr != nil {
			response["warnings"] = []string{projectCreationWarning(setupErr)}
		}
		response["message"] = "The Crew is ready. Select it from the project menu to open it."
		delete(response, "product_manifest")
		delete(response, "runtime_manifest")
		data, err := json.Marshal(response)
		return string(data), err
	}
}
