package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

// Builder panels reuse the external API's schemas, dispatcher and website
// handlers. The active project is supplied by the server, never by the model.
func (api *StreamingAPI) registerWorkspacePanelChatTools(reg definitionToolRegistrar, gate *productToolGate, user, session, root, product string) error {
	names := []string{"settings", "workflow_team", "manage_project", "browser_connections", "manage_messaging", "manage_pulse"}
	if product == "relays" {
		names = []string{"settings", "manage_project", "browser_connections", "manage_messaging", "list_relay_runs"}
	}
	if product == "work" {
		// Browser and model chat tools already exist for Crews.
		names = []string{"settings", "crew_owners", "manage_messaging"}
	}
	catalog, err := externalTools()
	if err != nil {
		return err
	}
	for _, name := range names {
		var tool externalTool
		for _, candidate := range catalog {
			if candidate.Name == name {
				tool = candidate
				break
			}
		}
		if tool.Name == "" {
			return fmt.Errorf("panel tool %s is unavailable", name)
		}
		// Clone: removing selectors from a chat schema must not mutate MCP's
		// catalog. Its original validator still checks the bound request.
		raw, _ := json.Marshal(tool.InputSchema)
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			return err
		}
		props := schema["properties"].(map[string]any)
		delete(props, "workflow_id")
		delete(props, "crew_id")

		if required, ok := schema["required"].([]any); ok {
			kept := []any{}
			for _, field := range required {
				if field != "workflow_id" && field != "crew_id" {
					kept = append(kept, field)
				}
			}
			schema["required"] = kept
		}
		if gate != nil {
			gate.Declare(name)
		}
		description := "For this project's Builder chat only; the project is bound by the server. " + tool.Description
		if err := reg.RegisterCustomToolWithTimeout(name, description, schema, func(ctx context.Context, args map[string]any) (out string, err error) {
			defer func() { log.Printf("[PANEL_SETTINGS] tool=%s session=%s success=%t", name, session, err == nil) }()
			claims := GetUserFromContext(ctx)
			if claims == nil || claims.UserID != user || !userAccessForClaims(claims).CanEdit {
				return "", errors.New("Use your own Builder chat to change this project.")
			}
			api.lastQueryMu.RLock()
			turn := api.lastQueryRequests[session]
			api.lastQueryMu.RUnlock()
			if turn.userID != user || !workspacePathsMatchForUser(user, turn.SelectedFolder, root) || api.isSyntheticTurn(session) {
				return "", errors.New("These settings need your current interactive Builder turn in this project.")
			}
			if api.eventStore != nil {
				if owner := api.eventStore.GetSessionOwner(session); owner != "" && owner != user {
					return "", errors.New("This chat's caller changed; start a new Builder turn.")
				}
			}
			clean := map[string]any{}
			for key := range props {
				if value, exists := args[key]; exists {
					clean[key] = value
				}
			}
			if product == "work" {
				if status, message := externalCrewAuthorGate(claims); status != 0 {
					return "", errors.New(message)
				}
				ref, found := resolveCrewPath(ctx, user, root)
				if !found || crewAccessFor(claims, ref) != crewAccessOwner {
					return "", errors.New("Only this Crew's Builder can change its settings.")
				}
				manifest, err := readCrewProjectManifests(ctx, crewProfileID, root)
				if err != nil {
					return "", err
				}
				clean["crew_id"] = manifest.ID
			} else {
				access, manifest := workflowAccessForWorkspacePath(ctx, claims, root)
				if manifest == nil || (manifest.Kind == "relay") != (product == "relays") || access != WorkflowAccessOwner && access != WorkflowAccessWrite || !userAllowedProduct(claims, product) {
					return "", errors.New("Only this project's Builder can change its settings.")
				}
				if claims.AccessToken != nil {
					if _, err := validateBuilderGrant(ctx, claims, manifest.ID, root); err != nil {
						return "", err
					}
				}
				clean["workflow_id"] = manifest.ID
			}

			data, err := json.Marshal(map[string]any{"name": name, "arguments": clean})
			if err != nil {
				return "", err
			}
			request := httptest.NewRequest(http.MethodPost, "/api/external/call", bytes.NewReader(data)).WithContext(ctx)
			recorder := httptest.NewRecorder()
			api.dispatchExternalCall(recorder, request, false)
			body := strings.TrimSpace(recorder.Body.String())
			if recorder.Code >= 400 {
				return "", errors.New(laptopActionErrorText(body))
			}
			return body, nil
		}, time.Minute, "workspace_advanced"); err != nil {
			return err
		}
	}
	return nil
}
