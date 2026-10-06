package security

import "strings"

// SlotShellEnv keeps only what a person's own commands need (their Code terminal and the agent's shell as their slot
// account, PLAT-622). The service environment is shared by every user, so a slot command gets an allowlist instead of
// the deny-list alone: a new server variable stays out of user shells until someone adds it here. Per-call values
// (MergeExtraEnv) and the slot's own HOME/XDG (SlotHomeEnv) are added after this.
func SlotShellEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if slotShellEnvAllowed(key) {
			out = append(out, entry)
		}
	}
	return out
}

var slotShellEnvExact = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true, "PWD": true,
	"LANG": true, "LANGUAGE": true, "TZ": true, "TMPDIR": true, "TMP": true, "TEMP": true,
	"COLORTERM": true, "EDITOR": true, "VISUAL": true, "PAGER": true, "PROMPT_COMMAND": true,
	"DOCKER_HOST": true, "DBUS_SESSION_BUS_ADDRESS": true,
	"MCP_API_URL": true, "WORKSPACE_API_URL": true, "WORKSPACE_DOCS_PATH": true, "PUBLIC_URL": true,
	"PI_BIN": true, "NATIVE_WORKSPACE": true, "MULTI_USER_MODE": true, "JAVA_HOME": true,
	"GOPATH": true, "GOROOT": true, "GOCACHE": true, "GOMODCACHE": true, "GOFLAGS": true, "GOPROXY": true, "GOTOOLCHAIN": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "http_proxy": true, "https_proxy": true, "no_proxy": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "REQUESTS_CA_BUNDLE": true, "CURL_CA_BUNDLE": true, "NODE_EXTRA_CA_CERTS": true,
}

var slotShellEnvPrefixes = []string{
	"AGENT_", "AGENTWORKS_", "CODING_AGENT_", "SANDBOX_", "GIT_", "PIP_", "PYTHON", "npm_config_", "NPM_", "NODE_",
	"UV_", "CARGO_", "RUSTUP_", "XDG_", "LC_", "TERM", "TMUX",
}

func slotShellEnvAllowed(key string) bool {
	if key == "AGENTWORKS_SLOT_CLI_USERS" {
		return false // other people's account ids
	}
	if slotShellEnvExact[key] {
		return true
	}
	for _, prefix := range slotShellEnvPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}
