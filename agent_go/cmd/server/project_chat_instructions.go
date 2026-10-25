package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// The app and native chat adapt the existing settings service; all three
// products store the same optional field alongside their purpose.
func (api *StreamingAPI) projectChatInstructions(w http.ResponseWriter, r *http.Request, root, action string, text *string) {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		externalError(w, 401, "authentication_required", "Sign in to open settings.")
		return
	}
	target, err := api.dashboardTarget(r.Context(), claims, root)
	if err != nil {
		dashboardError(w, err)
		return
	}
	args := map[string]any{}
	name := "get_settings"
	if action == "set" {
		if text == nil {
			externalError(w, 400, "invalid_arguments", "Chat instructions must be text.")
			return
		}
		name = "update_settings"
		args["chat_instructions"] = *text
	} else if action != "get" {
		externalError(w, 400, "invalid_arguments", "Choose get or set.")
		return
	}
	if !externalTokenAllows(claims, externalTool{Name: name}) || !userAllowedProduct(claims, map[string]string{"crew": "work", "workflow": "agentworks", "relay": "relays"}[target.Kind]) {
		externalError(w, 403, "forbidden", "This connection cannot use these settings.")
		return
	}
	recorder := httptest.NewRecorder()
	switch target.Kind {
	case "crew":
		args["crew_id"] = target.ID
		api.externalCrewSettingsCall(recorder, r, name, args)
	case "workflow", "relay":
		access, manifest := workflowAccessForWorkspacePath(r.Context(), claims, target.Root)
		if manifest == nil || access == WorkflowAccessNone {
			externalError(w, 404, "not_found", "Project unavailable.")
			return
		}
		api.externalSettingsCall(recorder, r, name, args, DiscoveredWorkflow{WorkspacePath: target.Root, Manifest: manifest}, access)
	default:
		externalError(w, 404, "not_found", "Project unavailable.")
		return
	}
	if recorder.Code >= 400 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
		return
	}
	var result struct {
		ChatInstructions string `json:"chat_instructions"`
		Access           string `json:"my_access"`
	}
	if json.Unmarshal(recorder.Body.Bytes(), &result) != nil {
		externalError(w, 502, "invalid_settings", "Settings could not be read.")
		return
	}
	externalJSON(w, map[string]any{"chat_instructions": result.ChatInstructions, "can_edit": result.Access == "owner", "max_length": workflowtypes.ChatInstructionsLimit})
}

func (api *StreamingAPI) handleProjectChatInstructions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		api.projectChatInstructions(w, r, r.URL.Query().Get("workspace"), "get", nil)
		return
	}
	var body struct {
		Workspace        string  `json:"workspace"`
		ChatInstructions *string `json:"chat_instructions"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
		externalError(w, 400, "invalid_arguments", "Invalid Chat instructions.")
		return
	}
	api.projectChatInstructions(w, r, body.Workspace, "set", body.ChatInstructions)
}

func updateCrewChatInstructions(ctx context.Context, root, text string) error {
	file := root + "/product.json"
	lock := productConversationRegistryMutex(file)
	lock.Lock()
	defer lock.Unlock()
	raw, found, err := readFileFromWorkspace(ctx, file)
	if err != nil || !found {
		return firstError(err, fmt.Errorf("project unavailable"))
	}
	var manifest map[string]any
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return err
	}
	manifest["chat_instructions"] = text
	manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, file, string(encoded)+"\n")
}

func projectChatInstructionsKey(root string) string {
	manifest, found, err := ReadWorkflowManifest(context.Background(), root)
	if err != nil || !found || strings.TrimSpace(manifest.ChatInstructions) == "" {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(manifest.ChatInstructions))))
}
