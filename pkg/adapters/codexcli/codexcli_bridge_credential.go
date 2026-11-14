package codexcli

import (
	"encoding/json"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// AppBridgeCredential is host-owned, exact-session authority. Its token is never
// logged. Started follows a successful process start; Release is idempotent and
// generation-bound, so an old process cannot revoke its replacement.
type AppBridgeCredential struct {
	Token          string
	Started        func()
	Release        func()
	ReleasePending func() // preflight cleanup: must never revoke a live generation
}

// AppBridgeCredentialForOwner is installed once by the host. Unregistered
// owners (including children) return nil and retain their own credential scope.
var AppBridgeCredentialForOwner func(owner, scope string) *AppBridgeCredential

func appBridgeCredential(owner string, opts *llmtypes.CallOptions) *AppBridgeCredential {
	if AppBridgeCredentialForOwner == nil {
		return nil
	}
	var config map[string]any
	if json.Unmarshal([]byte(codexOptionString(opts, MetadataKeyMCPServers)), &config) != nil {
		return nil
	}
	servers := config
	if nested, ok := config["mcpServers"].(map[string]any); ok {
		servers = nested
	}
	bridge, _ := servers["api-bridge"].(map[string]any)
	env, _ := bridge["env"].(map[string]any)
	if env["MCP_SESSION_ID"] != owner {
		return nil
	}
	scope, _ := env["MCP_VIRTUAL_SCOPE_ID"].(string)
	return AppBridgeCredentialForOwner(owner, scope)
}

// Refresh only the host bridge's authentication. Preserve all other effective
// child environment and policy fields as real process-relaunch gates.
func codexBridgeCredentialOptions(owner string, opts *llmtypes.CallOptions) (*llmtypes.CallOptions, *AppBridgeCredential, error) {
	credential := appBridgeCredential(owner, opts)
	if credential == nil || credential.Token == "" {
		return opts, nil, nil
	}
	copy := *opts
	if opts.Metadata != nil {
		metadata := *opts.Metadata
		metadata.Custom = make(map[string]any, len(opts.Metadata.Custom))
		for key, value := range opts.Metadata.Custom {
			metadata.Custom[key] = value
		}
		copy.Metadata = &metadata
	}
	environment := llmtypes.CodingAgentSecretEnvironmentFromOptions(opts)
	if environment["MCP_SESSION_ID"] == owner {
		environment["MCP_API_TOKEN"] = credential.Token
		environment["MCP_AUTH"] = "Authorization: Bearer " + credential.Token
		llmtypes.WithCodingAgentSecretEnvironment(environment)(&copy)
	}
	raw := codexOptionString(opts, MetadataKeyMCPServers)
	if raw == "" {
		return &copy, credential, nil
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return nil, credential, err
	}
	servers := config
	if nested, ok := config["mcpServers"].(map[string]any); ok {
		servers = nested
	}
	if bridge, ok := servers["api-bridge"].(map[string]any); ok {
		if env, ok := bridge["env"].(map[string]any); ok && env["MCP_SESSION_ID"] == owner {
			env["MCP_API_TOKEN"] = credential.Token
			if _, present := env["MCP_AUTH"]; present {
				env["MCP_AUTH"] = "Authorization: Bearer " + credential.Token
			}
			updated, err := json.Marshal(config)
			if err != nil {
				return nil, credential, err
			}
			WithMCPServers(string(updated))(&copy)
		}
	}
	return &copy, credential, nil
}
