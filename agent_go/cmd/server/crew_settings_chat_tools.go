package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// Crew chat uses the same settings services as the website and external MCP.
// Project and conversation selectors come from the turn, never tool arguments.
func (api *StreamingAPI) registerCrewSettingsChatTools(reg definitionToolRegistrar, gate *productToolGate, profile *resolvedAgentProfile, claims *UserClaims, readOnly bool, sessionID, root, baseURL string) error {
	if profile == nil || !isProjectProfileID(profile.Definition.ID) || readOnly || claims == nil || claims.UserID == "" {
		return nil
	}
	ref, ok := parseCrewPath(claims.UserID, root)
	if !ok || ref.Rest != "" {
		return nil
	}
	requestFor := func(ctx context.Context) (*http.Request, error) {
		if current := GetUserFromContext(ctx); current != nil && current.UserID != claims.UserID {
			return nil, errors.New("Use your own Crew chat to change its settings.")
		}
		// The authenticated turn owns consent. A native executor can supply
		// same-user context without its token, but cannot widen this turn.
		caller := claims
		if status, _ := externalCrewAuthorGate(caller); status != 0 {
			return nil, errors.New("Open a Crew you can build, or ask its owner to change these settings.")
		}
		current, found := resolveCrewPath(ctx, caller.UserID, ref.Root)
		if !found || crewAccessFor(caller, current) != crewAccessOwner {
			return nil, errors.New("Open a Crew you can build, or ask its owner to change these settings.")
		}
		return httptest.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/", nil).WithContext(context.WithValue(ctx, UserContextKey, caller)), nil
	}
	register := func(name, description string, props map[string]any, required []string, execute func(*http.Request, map[string]any) (string, error)) error {
		gate.Declare(name)
		schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		return reg.RegisterCustomToolWithTimeout(name, description, schema, func(ctx context.Context, args map[string]any) (out string, err error) {
			defer func() {
				log.Printf("[CREW_SETTINGS] tool=%s user=%q workspace=%q session=%q success=%t", name, claims.UserID, ref.Root, sessionID, err == nil)
			}()
			r, err := requestFor(ctx)
			if err != nil {
				return "", err
			}
			publicName := name
			if name == "manage_crew_model" {
				publicName = "manage_crew_chats"
			}
			// Keep the public dispatcher's connection consent as well as the
			// canonical handler's action-specific checks. Native registration
			// must not turn a run-only connection into browser/cost read access.
			if !externalTokenAllows(GetUserFromContext(r.Context()), externalTool{Name: publicName}) {
				return "", errors.New("This connection does not allow this operation. Use a connection with the same access as the Crew's settings page.")
			}
			manifest, err := readCrewProjectManifests(r.Context(), crewProfileID, ref.Root)
			if err != nil {
				return "", errors.New("This Crew is unavailable. Reopen it and try again.")
			}
			crew, _, _, found := api.externalCrewResolve(r.Context(), GetUserFromContext(r.Context()), manifest.ID)
			if !found || !workspacePathsMatchForUser(claims.UserID, crew.Binding.WorkspacePath, ref.Root) {
				return "", errors.New("This Crew is unavailable. Reopen it and try again.")
			}
			// Project selectors and unknown fields cannot change the binding,
			// even for a caller that bypasses schema validation.
			clean := map[string]any{}
			for key := range props {
				if value, exists := args[key]; exists {
					clean[key] = value
				}
			}
			clean["crew_id"] = manifest.ID
			return execute(r, clean)
		}, time.Minute, "workspace_advanced")
	}
	responseText := func(w *httptest.ResponseRecorder) (string, error) {
		body := strings.TrimSpace(w.Body.String())
		if w.Code >= 400 {
			return "", errors.New(laptopActionErrorText(body))
		}
		return body, nil
	}
	if _, bound, rootErr := localCrewRoots(ref.Root); bound && rootErr == nil {
		if err := register("manage_crew_chats", "Recognize this existing local folder (project_context), list your own native Claude/Codex conversations (native_list), or reopen an explicitly chosen native_id in its own private tab (native_resume). Found files are not a claim that they were loaded. The original account and current permissions apply; opening never starts a model turn.", map[string]any{
			"action":    map[string]any{"type": "string", "enum": []string{"project_context", "native_list", "native_resume"}},
			"native_id": externalString("Opaque id from native_list; required for native_resume."),
		}, []string{"action"}, func(r *http.Request, args map[string]any) (string, error) {
			switch externalArg(args, "action") {
			case "project_context", "native_list", "native_resume":
			default:
				return "", errors.New("Choose project_context, native_list or native_resume.")
			}
			w := httptest.NewRecorder()
			api.externalCrewChatsCall(w, r, args)
			return responseText(w)
		}); err != nil {
			return err
		}
	}
	if agentprofiles.HasFeature(profile.Definition, "browser") {
		props := map[string]any{
			"action":     map[string]any{"type": "string", "enum": []string{"list", "setup", "select", "disconnect"}},
			"browser_id": externalString("A browser from list; required for select."),
		}
		if err := register("browser_connections", "List your own saved Chrome and Edge connections, get their setup link, select one for this Crew, or disconnect it from this Crew. Connect the browser once on the website if needed; disconnecting this Crew leaves your other projects connected. No browser credentials or tabs are returned.", props, []string{"action"}, func(r *http.Request, args map[string]any) (string, error) {
			switch externalArg(args, "action") {
			case "list", "setup", "select", "disconnect":
			default:
				return "", errors.New("Choose list, setup, select or disconnect.")
			}
			w := httptest.NewRecorder()
			api.externalBrowserConnectionsCall(w, r, args)
			return responseText(w)
		}); err != nil {
			return err
		}
	}
	if agentprofiles.HasFeature(profile.Definition, "costs") {
		if err := register("get_crew_costs", "Read this Crew's Costs and usage: total spend, tokens and calls, by day, activity and model. Uses the app's ledger and existing Crew owner access, including co-owners. days defaults to 30 (at most 90); before pages back by UTC date.", map[string]any{"days": externalInteger(1, 90), "before": externalString("Only spend before this UTC date (YYYY-MM-DD).")}, nil, func(r *http.Request, args map[string]any) (string, error) {
			w := httptest.NewRecorder()
			api.externalCrewCosts(w, r, args)
			return responseText(w)
		}); err != nil {
			return err
		}
	}
	if agentprofiles.HasFeature(profile.Definition, "models") {
		return register("manage_crew_model", "Read the model choices and saved model of this chat (action=list), or change this chat's model when the person asks (action=set). Changes apply from the next message; this reply still uses the current model. Use an engine/model_id and connection_id returned by list. Choose your own personal account or a company account available to you for this tab. An empty engine returns the chat to the Crew's default. Other chats and the Crew default stay unchanged. list also shows this chat's context: tokens used of its window, its account's plan limits and the offered switches. set takes context_1m and idle_compaction (without engine only they change). compact: summarise this chat's conversation once this reply ends, so the next message costs less. fresh: the next message starts a new conversation carrying a short summary of this one; the history stays.", map[string]any{
			"action":           map[string]any{"type": "string", "enum": []string{"list", "set", "compact", "fresh"}},
			"context_1m":       map[string]any{"type": "boolean", "description": "For set: run this chat's Claude Opus or Sonnet model with its 1M-token context window (true) or the normal window (false)."},
			"idle_compaction":  map[string]any{"type": "boolean", "description": "For set: Auto-compact when idle. Long chats are summarised after about 50 minutes idle, so the next message costs less."},
			"engine":           map[string]any{"type": "string", "description": "Provider option from list. For set, an empty string uses the Crew default."},
			"connection_id":    externalString("Your personal or available company account ID from provider_connections in list."),
			"model_id":         externalString("Model from that provider's offered models."),
			"reasoning_effort": externalString("Optional reasoning level offered for that provider."),
		}, []string{"action"}, func(r *http.Request, args map[string]any) (string, error) {
			crewID := externalArg(args, "crew_id")
			project, err := api.codeChatsFor(r.Context(), claims.UserID, sessionID, ref.Root)
			if err != nil {
				return "", err
			}
			choices, err := api.agentProfiles.Resolve(crewProfileID, 0, claims.UserID)
			if err != nil {
				return "", err
			}
			choices = limitProfileModelsToAccount(r.Context(), claims.UserID, profileWithAvailableProviders(choices))
			payload := map[string]any{"crew_id": crewID, "action": "list"}
			owner := productWorkspaceUserID(r.Context())
			context1M, idleCompaction, argErr := chatContextArgs(args)
			if argErr != nil {
				return "", argErr
			}
			contextReply := func(input chatContextRequest) (string, error) {
				// The same connection consent as the MCP chat actions: changing a chat needs crews:write.
				if caller := GetUserFromContext(r.Context()); caller != nil && caller.AccessToken != nil && !caller.AccessToken.Allows(crewChatActionScope(firstNonEmptyTrimmed(input.Action, "set_model"), "")) {
					return "", errors.New("This connection does not allow changing this chat. Use a connection with the same access as the Crew's settings page.")
				}
				input.SessionID = project.Self.SessionID
				status, out := api.applyChatContext(r.Context(), owner, input, true)
				if status >= http.StatusBadRequest {
					message, _ := out["error"].(string)
					return "", errors.New(message)
				}
				encoded, err := json.Marshal(out)
				return string(encoded), err
			}
			switch externalArg(args, "action") {
			case "list":
			case "compact", "fresh":
				return contextReply(chatContextRequest{Action: externalArg(args, "action")})
			case "set":
				_, hasEngine := args["engine"]
				if !hasEngine && (context1M != nil || idleCompaction != nil) {
					return contextReply(chatContextRequest{Context1M: context1M, IdleCompaction: idleCompaction})
				}
				if !hasEngine {
					return "", errors.New("Choose an engine from list, or send an empty engine to use the Crew default.")
				}
				if engine := externalArg(args, "engine"); engine != "" {
					option, offered := findProviderOptionByID(choices.Runtime.ProviderOptions, engine)
					if !offered || (externalArg(args, "model_id") != "" && !providerOptionOffersModel(option, externalArg(args, "model_id"))) || (externalArg(args, "reasoning_effort") != "" && !reasoningEffortOffered(option, externalArg(args, "reasoning_effort"))) {
						return "", errors.New("Choose a provider, model and reasoning level offered by action=list.")
					}
				}
				payload = map[string]any{"crew_id": crewID, "action": "set_model", "chat_id": project.Self.SessionID, "engine": externalArg(args, "engine"), "model_id": externalArg(args, "model_id"), "reasoning_effort": externalArg(args, "reasoning_effort"), "connection_id": externalArg(args, "connection_id")}
			default:
				return "", errors.New("Choose list or set.")
			}
			w := httptest.NewRecorder()
			api.externalCrewChatsCall(w, r, payload)
			if w.Code >= 400 {
				return "", errors.New(laptopActionErrorText(w.Body.String()))
			}
			var state map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
				return "", err
			}
			accounts, err := api.listProviderAccountViews(r.Context(), claims.UserID, false, providerAccountScope{Principal: claims.UserID, WorkspacePath: ref.Root, Product: profile.Definition.Product})
			if err != nil {
				return "", err
			}
			state["provider_connections"] = accounts
			state["provider_options"] = choices.Runtime.ProviderOptions
			state["this_chat"] = project.Self.Key
			state["next"] = "A saved model change applies from the next message."
			if context1M != nil || idleCompaction != nil {
				if _, err := contextReply(chatContextRequest{Context1M: context1M, IdleCompaction: idleCompaction}); err != nil {
					return "", err
				}
			}
			if tab, own := chatContextTabForSession(r.Context(), owner, project.Self.SessionID); own {
				state["context"] = api.chatContextViewFor(owner, tab)
			}
			encoded, err := json.Marshal(state)
			return string(encoded), err
		})
	}
	return nil
}
