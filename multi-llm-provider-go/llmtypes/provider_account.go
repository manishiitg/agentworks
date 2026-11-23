package llmtypes

import (
	"sort"
	"strings"
)

const providerAccountEnvironmentKey = "provider_account_environment"

// WithProviderAccountEnvironment is for server-resolved runtime paths, separate
// from tool secrets. Never populate it from a browser-supplied environment.
func WithProviderAccountEnvironment(environment map[string]string) CallOption {
	copy := map[string]string{}
	for key, value := range environment {
		if providerAccountPathKey(key) && value != "" {
			copy[key] = value
		}
	}
	return func(opts *CallOptions) {
		if opts.Metadata == nil {
			opts.Metadata = &Metadata{Custom: map[string]interface{}{}}
		}
		if opts.Metadata.Custom == nil {
			opts.Metadata.Custom = map[string]interface{}{}
		}
		opts.Metadata.Custom[providerAccountEnvironmentKey] = copy
		if opts.CLISecurity != nil && copy["HOME"] != "" && opts.CLISecurity.LandlockEnforced() {
			// Confined: the account home is only the credential source; the
			// CLI's home is the sandbox's private one.
			policy := opts.CLISecurity.Clone()
			policy.CredentialHome = copy["HOME"]
			policy.CredentialEnv = map[string]string{}
			for key, value := range copy {
				policy.CredentialEnv[key] = value
			}
			opts.CLISecurity = &policy
		} else if opts.CLISecurity != nil && copy["HOME"] != "" {
			// Seatbelt (a Mac): the account home is the CLI's home and also its only login source. Without this the
			// sandbox fell back to the person's own home and linked their own login (~/.codex/auth.json) into the
			// account, even copying an account's newer login over the person's.
			policy := opts.CLISecurity.Clone()
			policy.PrivateHome = copy["HOME"]
			policy.CredentialHome = copy["HOME"]
			policy.CredentialEnv = map[string]string{}
			for key, value := range copy {
				policy.CredentialEnv[key] = value
			}
			policy.HostReadPaths = append(policy.HostReadPaths, copy["HOME"])
			policy.HostWritePaths = append(policy.HostWritePaths, copy["HOME"])
			opts.CLISecurity = &policy
		}
	}
}
func providerAccountPathKey(key string) bool {
	switch key {
	case "HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR", MuseBaseURLKey, AnthropicBaseURLKey, CursorBackendURLKey, CodexBaseURLKey:
		return true
	}
	return false
}

// MuseBaseURLKey is the account's Meta API address for Muse (passed as --base-url), for an account that reaches
// Meta through another server rather than directly. Muse has no setting or variable for it.
const MuseBaseURLKey = "MUSE_BASE_URL"

// AnthropicBaseURLKey is the account's Anthropic API address for Claude Code, for an account that reaches Anthropic
// through another server. Claude Code reads it from its environment like the other account paths.
const AnthropicBaseURLKey = "ANTHROPIC_BASE_URL"

// CodexBaseURLKey is the account's OpenAI API address for Codex, for an account that reaches OpenAI through another
// server (a login the server holds). Codex reads it only from its config, so it is passed as -c openai_base_url.
const CodexBaseURLKey = "CODEX_ACCOUNT_BASE_URL"

// CursorBackendURLKey is the account's Cursor backend address, for an account that reaches Cursor through another
// server. The Cursor SDK reads it from its environment.
const CursorBackendURLKey = "CURSOR_BACKEND_URL"

func ProviderAccountEnvironment(opts *CallOptions) map[string]string {
	result := map[string]string{}
	if opts == nil || opts.Metadata == nil {
		return result
	}
	source, _ := opts.Metadata.Custom[providerAccountEnvironmentKey].(map[string]string)
	for key, value := range source {
		if providerAccountPathKey(key) && value != "" {
			result[key] = value
		}
	}
	return result
}
func mergeProviderAccountEnvironment(base []string, opts *CallOptions) []string {
	env := ProviderAccountEnvironment(opts)
	// Which credentials the CLI receives is decided by the real account alone.
	accountBound := len(env) > 0
	if accountBound {
		for key, value := range providerAccountCredentials(opts) {
			env[key] = value
		}
	}
	// A confined CLI's home replaces the account's (or server's) paths.
	for key, value := range SandboxHomeEnvironment(opts) {
		env[key] = value
	}
	out := make([]string, 0, len(base)+len(env))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if env[key] == "" && !(accountBound && providerAccountCredentialKey(key)) {
			out = append(out, entry)
		}
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

// WithProviderAccountCredentials binds credentials resolved by the server. It
// also prevents an account with file-based login from inheriting a server key.
func WithProviderAccountCredentials(credentials map[string]string) CallOption {
	copied := map[string]string{}
	for key, value := range credentials {
		if providerAccountCredentialKey(key) && value != "" {
			copied[key] = value
		}
	}
	return func(opts *CallOptions) {
		if opts.Metadata == nil {
			opts.Metadata = &Metadata{}
		}
		if opts.Metadata.Custom == nil {
			opts.Metadata.Custom = map[string]interface{}{}
		}
		opts.Metadata.Custom["provider_account_credentials"] = copied
	}
}
func providerAccountCredentials(opts *CallOptions) map[string]string {
	if opts == nil || opts.Metadata == nil {
		return nil
	}
	values, _ := opts.Metadata.Custom["provider_account_credentials"].(map[string]string)
	return values
}
func ProviderAccountCredentialNames() []string {
	return []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN", "CURSOR_API_KEY", "CURSOR_AUTH_TOKEN", "META_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "PI_API_KEY", "PI_CODING_AGENT_DIR"}
}
func providerAccountCredentialKey(key string) bool {
	for _, name := range ProviderAccountCredentialNames() {
		if name == key {
			return true
		}
	}
	return false
}
