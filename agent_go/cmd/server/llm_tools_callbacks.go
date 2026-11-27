package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/mcpagent/llm"
)

// buildLLMToolsCallbacks creates LLMToolsCallbacks that bridge the workshop tools
// to the published LLM list, model metadata catalog, and provider validation logic.
func (api *StreamingAPI) buildLLMToolsCallbacks() *todo_creation_human.LLMToolsCallbacks {
	return &todo_creation_human.LLMToolsCallbacks{
		ListPublishedLLMs: func(ctx context.Context) (string, error) {
			llms, err := LoadPublishedLLMsWithAuto(ctx)
			if err != nil {
				return "", fmt.Errorf("failed to load published LLMs: %w", err)
			}
			return prettyJSON(map[string]interface{}{
				"count": len(llms),
				"llms":  llms,
				"note":  "These are the published models available for workflow tier configuration.",
			}), nil
		},
		ListProviderModels: func(_ context.Context, provider string) (string, error) {
			return listProviderModelsJSON(provider), nil
		},
		ValidateLLM: func(ctx context.Context, args map[string]interface{}) (string, error) {
			provider := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", args["provider"])))
			modelID, _ := args["model_id"].(string)
			apiKey, _ := args["api_key"].(string)
			endpoint, _ := args["endpoint"].(string)
			region, _ := args["region"].(string)
			apiVersion, _ := args["api_version"].(string)
			options, _ := args["options"].(map[string]interface{})

			if provider == "" {
				return "provider is required.", nil
			}
			if !isPublishedLLMProviderAllowed(provider) {
				return fmt.Sprintf("unsupported chat LLM provider %q. Use coding agents or direct API providers: codex-cli, cursor-cli, pi-cli, claude-code, bedrock, openai, anthropic, vertex, or azure.", provider), nil
			}

			validationOptions := cloneOptionsMap(options)

			// Use workspace-backed auth if no explicit key provided
			usedWorkspaceAuth := false
			if strings.TrimSpace(apiKey) == "" {
				keys, err := LoadProviderKeys(ctx)
				if err == nil && keys != nil {
					if value := getStoredProviderAPIKey(keys, provider); value != "" {
						apiKey = value
						usedWorkspaceAuth = true
					}
					switch provider {
					case "bedrock":
						if keys.Bedrock != nil && keys.Bedrock.Region != "" {
							region = keys.Bedrock.Region
							usedWorkspaceAuth = true
						}
					case "azure":
						if keys.Azure != nil {
							if keys.Azure.APIKey != "" {
								apiKey = keys.Azure.APIKey
								usedWorkspaceAuth = true
							}
							if endpoint == "" && keys.Azure.Endpoint != "" {
								endpoint = keys.Azure.Endpoint
							}
							if apiVersion == "" && keys.Azure.APIVersion != "" {
								apiVersion = keys.Azure.APIVersion
							}
						}
					}
				}
			}

			if endpoint != "" || region != "" || apiVersion != "" {
				if validationOptions == nil {
					validationOptions = map[string]interface{}{}
				}
				if endpoint != "" {
					validationOptions["endpoint"] = endpoint
				}
				if region != "" {
					validationOptions["region"] = region
				}
				if apiVersion != "" {
					validationOptions["api_version"] = apiVersion
				}
			}
			req := llm.APIKeyValidationRequest{
				Provider: provider,
				ModelID:  modelID,
				APIKey:   apiKey,
				Options:  validationOptions,
			}

			resp := validateProviderConfig(req)
			return prettyJSON(map[string]interface{}{
				"provider":            provider,
				"model_id":            modelID,
				"valid":               resp.Valid,
				"message":             resp.Message,
				"error":               resp.Error,
				"corrected_options":   resp.CorrectedOptions,
				"used_workspace_auth": usedWorkspaceAuth,
			}), nil
		},
	}
}

func (api *StreamingAPI) registerMultiAgentMCPServerTools(registrar interface {
	RegisterCustomTool(string, string, map[string]interface{}, func(context.Context, map[string]interface{}) (string, error), string) error
}, disabled func(string) bool) error {
	if err := api.registerMCPCallTool(registrar, disabled); err != nil {
		return err
	}
	registerTool := func(name, description string, params map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error)) error {
		if disabled != nil && disabled(name) {
			return nil
		}
		return registrar.RegisterCustomTool(name, description, params, execute, "mcp_server_tools")
	}
	stringField := func(description string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": description}
	}
	properties := map[string]interface{}{
		"name":           stringField("Exact saved vault_<connection ID>, catalog name, or a name for a new custom MCP."),
		"server_name":    stringField("Exact Vault connection ID; same as name for discovery."),
		"catalog":        stringField("Catalog provider from list_mcp_servers."),
		"label":          stringField("A label creates a separate account. Omit when reconnecting an existing exact ID."),
		"url":            stringField("Verified provider MCP endpoint for a custom connection."),
		"vault_id":       stringField("Optional vault you own; defaults to your accounts vault."),
		"transport":      map[string]interface{}{"type": "string", "enum": []string{"http", "sse"}},
		"api_key":        stringField("User-provided key; stored encrypted in Vault. Never repeat it."),
		"api_key_header": stringField("Provider-documented key header; defaults to Authorization."),
		"api_key_format": stringField("Header format with {}; default Bearer {} for Authorization, otherwise {}."),
		"credential":     map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "string"}, "description": "Advanced: user-provided credential headers. Stored encrypted; never repeat values."},
		"client_id":      stringField("OAuth app client ID if the provider requires an app."),
		"client_secret":  stringField("User-provided OAuth app secret with client_id. Encrypted; never repeat."),
		"reconnect":      map[string]interface{}{"type": "boolean"},
	}
	descriptions := map[string]string{
		"list_mcp_servers":      "List the current caller's permitted Vault connections, owned vaults and provider catalog. Metadata only; no credentials.",
		"install_mcp_server":    "Connect or reuse an exact Vault account through this chat, complete key/OAuth setup, return a real provider sign-in link, verify discovery and select it for the current project. Never echo credentials. Existing Builder, ownership, sharing and Vault grants apply.",
		"add_mcp_server":        "Connect a verified custom HTTP/SSE MCP through Vault in this chat, with the same behavior as install_mcp_server.",
		"edit_mcp_server":       "Update an owned Vault connection's label, URL or credentials. Use its exact saved ID, preserve its grants and finish any required sign-in through chat.",
		"remove_mcp_server":     "Deselect an exact Vault connection from this project. Outside a project, remove an owned Vault account. Unrelated accounts and grants stay unchanged.",
		"trigger_mcp_discovery": "Verify and refresh an exact permitted Vault connection's live tools. Pass its saved name. Report the real discovery result before claiming it connected.",
	}
	for _, name := range []string{"list_mcp_servers", "install_mcp_server", "add_mcp_server", "edit_mcp_server", "remove_mcp_server", "trigger_mcp_discovery"} {
		params := map[string]interface{}{"type": "object", "additionalProperties": false, "properties": properties}
		if name != "list_mcp_servers" {
			params["anyOf"] = []any{map[string]any{"required": []string{"name"}}, map[string]any{"required": []string{"catalog"}}, map[string]any{"required": []string{"server_name"}}}
		}
		if err := registerTool(name, descriptions[name], params, func(ctx context.Context, args map[string]interface{}) (string, error) {
			person, err := api.mcpToolUserID(ctx)
			if err != nil {
				return "", err
			}
			if GetUserFromContext(ctx) == nil {
				ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: person})
			}
			return api.mcpConnectionTool(ctx, person, name, args)
		}); err != nil {
			return err
		}
	}
	if err := registerTool(
		"search_mcp_catalog",
		"Search for an MCP (Model Context Protocol) server for a given service or capability — e.g. \"is there an MCP for ClickUp?\". Searches our curated catalog, GitHub's public MCP Registry, and the official MCP Registry. Registries supply metadata, not a required hosting or authorization service. Prefer the provider's own documented endpoint. If no suitable MCP is found, continue with an internet search using available web/search/browser tools and verify the provider's official documentation or repository. A registry miss does not establish that no MCP exists; never invent an endpoint. Catalog hits report whether the user has already connected them; public registry hits are NOT vetted — do not assume they are safe to add sight-unseen, and note to the user that adding one (especially an OAuth or locally-run package one) needs the same scrutiny a human would give any third-party integration. Use install_mcp_server for a verified provider URL; add_mcp_server is for a custom configuration whose auth is already known.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Service or capability to search for, e.g. \"clickup\", \"playwright\", \"vector database\".",
				},
			},
			"required": []string{"query"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			query, _ := args["query"].(string)
			query = strings.TrimSpace(query)
			if query == "" {
				return "query is required.", nil
			}
			needle := strings.ToLower(query)

			if _, err := api.mcpToolUserID(ctx); err != nil {
				return "", err
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("## MCP search: %q\n\n", query))

			// Our own catalog first: every entry here was hand-verified (OAuth
			// shape, live-probed), unlike the public registries below.
			var catalogHits []string
			for name, server := range api.baseMCPConfig().MCPServers {
				if !strings.Contains(strings.ToLower(name), needle) {
					continue
				}
				status := "available template — connect it through chat using Vault"
				if server.OAuth == nil {
					status = "available template — add it through chat using Vault"
				}

				catalogHits = append(catalogHits, fmt.Sprintf("- **%s** [our catalog, vetted] — %s", name, status))
			}
			sort.Strings(catalogHits)
			sb.WriteString("### Our catalog\n\n")
			if len(catalogHits) == 0 {
				sb.WriteString("No match.\n\n")
			} else {
				for _, line := range catalogHits {
					sb.WriteString(line + "\n")
				}
				sb.WriteString("\n")
			}

			appendExternal := func(title string, results []services.MCPRegistryResult, searchErr error) {
				sb.WriteString(fmt.Sprintf("### %s (unvetted — review before adding)\n\n", title))
				if searchErr != nil {
					sb.WriteString(fmt.Sprintf("Search failed: %v\n\n", searchErr))
					return
				}
				if len(results) == 0 {
					sb.WriteString("No match.\n\n")
					return
				}
				for _, r := range results {
					kind := "requires a local package run (npm/npx, docker, etc.)"
					if r.Remote {
						kind = "remote HTTP server"
					}
					desc := r.Description
					if desc == "" {
						desc = "(no description provided)"
					}
					sb.WriteString(fmt.Sprintf("- **%s**", r.Name))

					sb.WriteString(fmt.Sprintf(" — %s. %s", desc, kind))
					if r.Link != "" {
						sb.WriteString(fmt.Sprintf(" (endpoint or repository: %s)", r.Link))
						if r.RepositoryURL != "" && r.RepositoryURL != r.Link {
							sb.WriteString(fmt.Sprintf("; source: %s", r.RepositoryURL))
						}
					}
					sb.WriteString("\n")
				}
				sb.WriteString("\n")
			}

			githubResults, githubErr := services.SearchGitHubMCPRegistry(ctx, query, 10)
			appendExternal("GitHub MCP Registry", githubResults, githubErr)

			officialResults, officialErr := services.SearchOfficialMCPRegistry(ctx, query, 10)
			appendExternal("Official MCP Registry", officialResults, officialErr)

			sb.WriteString("### Check official providers on the internet\n\nAlso search the internet for the provider's official MCP documentation or source repository using available web/search/browser tools. Registry listings can be incomplete and do not prove provider ownership. Verify and prefer the provider's direct endpoint before proposing a connection; explain any hosted intermediary. If no suitable match appears here, continue the internet search rather than concluding that no MCP exists.\n")

			return sb.String(), nil
		},
	); err != nil {
		return err
	}
	if err := registerTool(
		"get_mcp_server_logs",
		"Show recent logs for a specific MCP server, or list which servers currently have logs.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Optional server name. If omitted, the tool lists servers that currently have stored logs.",
				},
			},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			name, _ := args["name"].(string)
			name = strings.TrimSpace(name)

			api.serverLogsMux.RLock()
			defer api.serverLogsMux.RUnlock()

			if name == "" {
				if len(api.serverLogs) == 0 {
					return "No MCP server logs are currently stored.", nil
				}
				names := make([]string, 0, len(api.serverLogs))
				for serverName := range api.serverLogs {
					names = append(names, serverName)
				}
				sort.Strings(names)
				return fmt.Sprintf("Servers with stored MCP logs: %s", strings.Join(names, ", ")), nil
			}

			logs := api.serverLogs[name]
			if len(logs) == 0 {
				return fmt.Sprintf("No stored logs for MCP server %q.", name), nil
			}

			start := 0
			if len(logs) > 20 {
				start = len(logs) - 20
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("## MCP Server Logs: %s\n\n", name))
			for _, entry := range logs[start:] {
				sb.WriteString(fmt.Sprintf("- %s [%s] %s\n", entry.Timestamp.Format(time.RFC3339), entry.Level, entry.Message))
			}
			return sb.String(), nil
		},
	); err != nil {
		return err
	}
	return nil
}
