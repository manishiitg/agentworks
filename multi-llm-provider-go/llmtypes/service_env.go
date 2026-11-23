package llmtypes

import (
	"os"
	"sort"
	"strings"
)

// IsServiceOnlyEnvKey reports whether an environment variable belongs to the host service alone: its signing
// secret, login password and user lists, server and bridge tokens, the deployment's global secrets, and the
// platform's own back-end keys (Supabase, Vault, the keyring password, the database). Nothing launched for a
// user or an agent needs one of these.
//
// It is wider than the per-launch scrub (isServerOwnedSecretKey): it is what a long-lived process that hosts
// launches -- the tmux server -- must not keep in its own environment, because tmux hands its global
// environment to every new pane and prints it to anything that reaches its socket (`tmux show-environment -g`,
// PLAT-663). A CLI's own login (provider keys, CLAUDE_CODE_OAUTH_TOKEN) and the per-chat scope (SECRET_*,
// MCP_*) are deliberately not here: the launch step decides those per call. Host-specific additions come
// from AGENTWORKS_CLI_ENV_DENY (comma separated), the same setting the Landlock launcher honours.
func IsServiceOnlyEnvKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	if isServerOwnedSecretKey(key) {
		return true
	}
	for _, prefix := range []string{"GLOBAL_SECRET_", "SUPABASE_", "VAULT_", "LANGFUSE_"} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	switch key {
	case "AUTH_ALLOWED_EMAILS", "ADMIN_USERS", "GOG_KEYRING_PASSWORD", "CAPLAYER_SERVICE_TOKEN_FILE",
		"GATEWAY_HUMAN_TOKEN", "JWT_SECRET", "DATABASE_URL", "AGENTWORKS_SLOT_CLI_USERS":
		return true
	}
	for _, name := range strings.Split(os.Getenv("AGENTWORKS_CLI_ENV_DENY"), ",") {
		if strings.TrimSpace(name) == key {
			return true
		}
	}
	return false
}

// ServiceOnlyEnvNames returns the sorted, de-duplicated NAMES in env that IsServiceOnlyEnvKey matches. Values
// are never returned.
func ServiceOnlyEnvNames(env []string) []string {
	seen := map[string]bool{}
	var names []string
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if !seen[key] && IsServiceOnlyEnvKey(key) {
			seen[key] = true
			names = append(names, key)
		}
	}
	sort.Strings(names)
	return names
}
