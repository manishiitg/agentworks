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

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Builder panels reuse the external API's schemas, dispatcher and website
// handlers. The active project is supplied by the server, never by the model.
func (api *StreamingAPI) registerWorkspacePanelChatTools(reg definitionToolRegistrar, gate *productToolGate, user, session, root, product string) error {
	names := []string{"settings", "models", "workflow_team", "manage_project", "browser_connections", "manage_messaging"}
	if product == "relays" {
		names = []string{"settings", "models", "manage_project", "browser_connections", "manage_messaging", "list_relay_runs"}
	}
	if product == "work" {
		// Browser and model chat tools already exist for Crews.
		names = []string{"settings", "models", "crew_owners", "manage_messaging", "list_crew_snapshots", "compare_crew_snapshot", "review_crew_snapshot", "undo_crew_reply", "crew_git_status", "crew_git_diff", "crew_git_branch_compare", "crew_git_branch_diff"}
	}
	names = append(names, "chat_preferences")
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
		delete(props, "workspace_id")

		if required, ok := schema["required"].([]any); ok {
			kept := []any{}
			for _, field := range required {
				if field != "workflow_id" && field != "crew_id" && field != "workspace_id" {
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
			// A refusal from the dispatched call is already logged by [EXTERNAL_CALL] (envelopes only, never
			// echoed arguments); log this panel's own refusal text here so a failed call is never blind.
			dispatched := false
			defer func() {
				reason := ""
				if err != nil && !dispatched {
					reason = fmt.Sprintf(" reason=%q", err.Error())
				}
				log.Printf("[PANEL_SETTINGS] tool=%s session=%s success=%t%s", name, session, err == nil, reason)
			}()
			claims := GetUserFromContext(ctx)
			if name == "chat_preferences" {
				if claims == nil || claims.UserID != user {
					return "", errors.New("Use your own chat to change your display.")
				}
				var update *bool
				if show, ok := args["show_context_indicator"].(bool); ok {
					update = &show
				}
				recorder := httptest.NewRecorder()
				api.chatPreferences(recorder, httptest.NewRequest(http.MethodGet, "/api/chat-preferences", nil).WithContext(ctx), update)
				if recorder.Code >= 400 {
					return "", errors.New(strings.TrimSpace(recorder.Body.String()))
				}
				return strings.TrimSpace(recorder.Body.String()), nil
			}
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
			// Translated: the server chose this tool and project after the caller checks above. Panel tools such as
			// crew_owners are internal members of a merged MCP tool (workspace action=owners), so an untranslated
			// call was refused as unknown_tool (PLAT-856).
			dispatched = true
			api.dispatchExternalCall(recorder, request, true)
			body := strings.TrimSpace(recorder.Body.String())
			if recorder.Code >= 400 {
				return "", errors.New(laptopActionErrorText(body))
			}
			if (name == "settings" && clean["action"] == "update" || name == "models" && clean["action"] == "set") && product != "work" {
				if err := api.refreshBoundWorkflowSettings(ctx, user, session, root, clean); err != nil {
					const note = "Saved. Reopen this chat to use the new settings."
					var saved map[string]any
					if json.Unmarshal([]byte(body), &saved) == nil && saved != nil {
						saved["note"] = note
						data, _ := json.Marshal(saved)
						return string(data), nil
					}
					return body + "\n" + note, nil
				}
			}
			return body, nil
		}, time.Minute, "workspace_advanced"); err != nil {
			return err
		}
	}
	return nil
}

// Refresh only the native session already bound by the panel's caller checks.
// External MCP saves have no native session to retarget.
func (api *StreamingAPI) refreshBoundWorkflowSettings(ctx context.Context, user, session, root string, args map[string]any) error {
	cached, exists := api.workshopChatSessions.Load(session)
	if !exists {
		return nil
	}
	live := cached.(*stepworkflow.WorkshopChatSession)
	cfg := live.GetConfig()
	if cfg == nil || !workspacePathsMatchForUser(user, cfg.WorkspacePath, root) {
		return errors.New("Reopen this project's Builder chat to use its saved settings.")
	}
	manifest, found, err := ReadWorkflowManifest(ctx, root)
	if err != nil || !found || manifest == nil {
		return errors.New("Saved settings could not be reloaded. Reopen this project's Builder chat.")
	}
	caps := manifest.Capabilities
	scoped := api.loadSelectedSecrets(ctx, user, root, caps.SelectedSecrets)
	resolved := api.mergeGlobalSecretsFor(ctx, api.projectSecretsOwner(user, root), scoped, caps.SelectedGlobalSecretNames)
	entries := make([]orchestrator.SecretEntry, 0, len(resolved))
	for _, secret := range resolved {
		entries = append(entries, orchestrator.SecretEntry{Name: secret.Name, Value: secret.Value})
	}
	useKB := true
	if caps.LLMConfig != nil && caps.LLMConfig.UseKnowledgebase != nil {
		useKB = *caps.LLMConfig.UseKnowledgebase
	}
	live.UpdatePresetSettings(caps.RuntimeServers(), caps.SelectedTools, true, caps.UseCodeExecutionMode, useKB, caps.SelectedSkills, true, entries)
	mode := strings.ToLower(strings.TrimSpace(caps.BrowserMode))
	port := caps.BrowserCDPPort
	live.UpdateBrowserRuntime(mode, configuredCDPPortsForMode(mode, &port, nil))
	common.SetSessionBrowserMode(session, mode)
	stepworkflow.RefreshWorkflowFolderAccessSession(session, root)
	if caps.LLMConfig != nil {
		modelCtx := withModelLimitPerson(ctx, user)
		locked := lockedPresetLLMConfig(caps.LLMConfig)
		primary, tiered := workshopResolveLLMConfigFor(modelCtx, locked)
		live.UpdatePresetLLMConfigs(primary, workshopResolveBackgroundLLMConfigFor(modelCtx, locked))
		live.UpdateTieredConfig(tiered)
	}
	if notifications, ok := args["notifications"].(map[string]any); ok {
		if sender, changed := notifications["gmail_connection_id"].(string); changed {
			virtualtools.UpdateSessionGmailSender(session, root, sender)
			if dest, ok := ctx.Value(virtualtools.BotNotificationDestinationKey).(*services.NotificationDestination); ok && dest != nil && dest.WorkspacePath == root {
				if dest.Gmail == nil {
					dest.Gmail = &services.GmailDest{}
				}
				dest.Gmail.Workspace, dest.Gmail.ConnectionIDs = root, nil
				if sender = strings.TrimSpace(sender); sender != "" {
					dest.Gmail.ConnectionIDs = []string{sender}
				}
			}
		}
	}
	log.Printf("[PANEL_SETTINGS] live_refresh session=%s success=true", session)
	return nil
}
