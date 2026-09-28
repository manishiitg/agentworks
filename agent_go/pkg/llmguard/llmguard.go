// Package llmguard enforces that agents run only through coding-agent CLIs.
package llmguard

import (
	"fmt"
	"os"
	"strings"

	"github.com/manishiitg/mcpagent/llm"
)

// IsCodingAgentProvider reports whether provider names a coding-agent CLI.
func IsCodingAgentProvider(provider string) bool {
	return llm.IsCodingAgentProvider(llm.Provider(normalize(provider)), "")
}

// CodingAgentProviders lists the coding-agent CLI providers, sorted.
func CodingAgentProviders() []string {
	contracts := llm.CodingAgentProviderContracts()
	providers := make([]string, 0, len(contracts))
	seen := make(map[string]bool, len(contracts))
	for _, contract := range contracts {
		name := string(contract.Provider)
		if !seen[name] {
			seen[name] = true
			providers = append(providers, name)
		}
	}
	return providers
}

// RequireCodingAgentProvider rejects direct-API providers (openai, anthropic,
// vertex, bedrock, ...). Their native loop is unmaintained and no longer offered.
func RequireCodingAgentProvider(provider string) error {
	if normalize(provider) == "agy-cli" && !AgyAlphaEnabled() {
		return fmt.Errorf("LLM provider %q is unavailable: AGY alpha requires AGY_ALPHA=1 in single-user mode", strings.TrimSpace(provider))
	}
	if IsCodingAgentProvider(provider) {
		return nil
	}
	return fmt.Errorf("LLM provider %q is not supported: agents run only through coding-agent CLIs (%s)",
		strings.TrimSpace(provider), strings.Join(CodingAgentProviders(), ", "))
}

// AgyAlphaEnabled is the runtime gate, shared by publication and execution.
func AgyAlphaEnabled() bool {
	return strings.TrimSpace(os.Getenv("AGY_ALPHA")) == "1" && os.Getenv("MULTI_USER_MODE") != "true"
}

// ServerDefaultConnectionPrefix marks a model that names no account, so it
// runs on the server account. WithServerAccountAdmission sets it only on the
// config handed to InitializeLLM, so the server's connection resolver runs
// (and admits or refuses the server account) for every model, not only for
// models that name an account. It is never stored.
const ServerDefaultConnectionPrefix = "server-default:"

// WithServerAccountAdmission routes a config without a connection through
// the resolver attached to its keys, when there is one.
func WithServerAccountAdmission(config llm.Config) llm.Config {
	if strings.TrimSpace(config.ConnectionID) == "" && config.APIKeys != nil && config.APIKeys.ResolveConnection != nil && strings.TrimSpace(string(config.Provider)) != "" {
		config.ConnectionID = ServerDefaultConnectionPrefix + string(config.Provider)
	}
	return config
}

func normalize(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}
