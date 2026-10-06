package security

import (
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
	"github.com/manishiitg/coding-agent-loop/workspace/gogconfig"
	"os"
	"path/filepath"
	"strings"
)

// browserSocketDir is where the shared-profile agent-browser daemons keep
// their sockets. It is the one /tmp folder sandboxed commands may write.
const browserSocketDir = "/tmp/.agent-browser"

// sandboxSharedHome is the HOME the Docker-mode environment starts with. It is
// shared by every command, so ExecuteIsolated replaces it with a private one.
const sandboxSharedHome = "/tmp"

// BuildSafeEnvironment returns a sanitized set of environment variables.
// In Docker mode, this is a strict whitelist to prevent secret leakage.
// In native mode, it inherits the host environment but strips known secrets,
// so host-installed tools (aws, node, python, etc.) and their config remain accessible.
func BuildSafeEnvironment() []string {
	var env []string
	if os.Getenv("NATIVE_WORKSPACE") == "true" {
		env = buildNativeEnvironment()
	} else {
		env = buildDockerEnvironment()
	}
	if browserconfig.SharedEnabled() {
		clean := make([]string, 0, len(env)+3)
		for _, entry := range env {
			if !strings.HasPrefix(entry, "TZ=") && !strings.HasPrefix(entry, "AGENT_BROWSER_SOCKET_DIR=") && !strings.HasPrefix(entry, browserconfig.ProfileEnv+"=") {
				clean = append(clean, entry)
			}
		}
		env = append(clean, browserconfig.ProfileEnv+"="+browserconfig.SharedProfile(), "AGENT_BROWSER_SOCKET_DIR="+browserSocketDir, "TZ=UTC")
	}
	// A rootless Docker socket controls only containers owned by this service
	// account. Never forward an arbitrary endpoint (especially the host Docker
	// socket or tcp://), because access to the host daemon is equivalent to root.
	if dockerHost := configuredRootlessDockerHost(); dockerHost != "" {
		env = append(env, "DOCKER_HOST="+dockerHost)
	}
	return gogconfig.Environment(env, false)
}

func configuredRootlessDockerHost() string {
	expected := fmt.Sprintf("unix:///run/user/%d/docker.sock", os.Getuid())
	if strings.TrimSpace(os.Getenv("DOCKER_HOST")) == expected {
		return expected
	}
	return ""
}

// buildDockerEnvironment returns a strict whitelist for Docker containers.
func buildDockerEnvironment() []string {
	browserExecutable := configuredBrowserExecutable()
	env := []string{
		// Essential shell variables
		"PATH=" + browserCLIPath("/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"),
		"HOME=" + sandboxSharedHome,
		"USER=agent",
		"SHELL=/bin/sh",

		// Locale settings
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",

		// Browser automation and HyperFrames must resolve the same deployment-
		// managed headless binary. These are paths, not credentials.
		"AGENT_BROWSER_EXECUTABLE_PATH=" + browserExecutable,
		"HYPERFRAMES_BROWSER_PATH=" + browserExecutable,

		// Python: disable output buffering so stdout/stderr are captured even if the process is killed (timeout/signal)
		"PYTHONUNBUFFERED=1",

		// Allow pip install when Python is externally managed (PEP 668); avoids "break system packages" errors in LLM-run shells
		"PIP_BREAK_SYSTEM_PACKAGES=1",

		// HOME is /tmp here, so the AWS CLI and boto3 would look for profiles in
		// /tmp/.aws. Point both at the system config the box installs
		// (deploy/aws-ec2/install-system-tools.sh), where the named "RTS" profile
		// and the default both resolve to the instance role. A path, not a secret.
		"AWS_CONFIG_FILE=/usr/local/etc/aws/config",

		// DO NOT include:
		// - DATABASE_URL
		// - API_KEYS
		// - JWT_SECRET
		// - Any other secrets from parent process
	}

	if dir := configuredBrowserCLIDir(); dir != "" {
		env = append(env, "AGENT_BROWSER_CLI_DIR="+dir)
	}
	return env
}

// This path comes only from deployment configuration, never a shell request.
// Docker-mode sanitization must not silently select an older system CLI.
func configuredBrowserCLIDir() string {
	dir := strings.TrimSpace(os.Getenv("AGENT_BROWSER_CLI_DIR"))
	if !filepath.IsAbs(dir) || filepath.Clean(dir) == string(filepath.Separator) {
		return ""
	}
	return filepath.Clean(dir)
}

func browserCLIPath(existing string) string {
	if dir := configuredBrowserCLIDir(); dir != "" {
		return dir + string(os.PathListSeparator) + existing
	}
	return existing
}

func configuredBrowserExecutable() string {
	if configured := strings.TrimSpace(os.Getenv("AGENT_BROWSER_EXECUTABLE_PATH")); configured != "" {
		return configured
	}
	return "/usr/bin/chromium"
}

// buildNativeEnvironment inherits the host environment but strips secrets.
// This preserves PATH, HOME, and tool configs (AWS, Node, Go, etc.) so
// host-installed CLIs work normally, while preventing accidental leakage
// of server-internal secrets to agent-executed shell commands.
func buildNativeEnvironment() []string {
	// Env var names (case-insensitive prefix match) that must NOT leak to shell commands.
	// These are server-internal secrets, not user/agent credentials.
	blockedPrefixes := []string{
		"GLOBAL_SECRET_",
		"CAPLAYER_SERVICE_",
		"GATEWAY_HUMAN_TOKEN",
		"DATABASE_URL",
		"JWT_SECRET",
		"LANGFUSE_",
		"SUPABASE_",
		"OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
		"AZURE_OPENAI_",
		"GOOGLE_AI_",
		"BEDROCK_",
		"OPENROUTER_",
		"AGENT_PROVIDER",
		"AGENT_MODEL",
		"DEEP_SEARCH_",
		"MULTI_USER_",
		// Per-deployment global secrets (GLOBAL_SECRET_<NAME>) belong to workflows that declare them.
		"GLOBAL_SECRET_",
		// Sign-in configuration (who may sign in, providers) and the gateway's settings are the server's, not a
		// shell's: AUTH_ALLOWED_EMAILS listed every allowed person in each user's Code terminal (Excellence 2026-10-06).
		"AUTH_",
		"GATEWAY_",
	}

	// Exact env var names to block
	blockedExact := map[string]bool{
		"MCP_API_TOKEN":       true,
		"WORKSPACE_API_TOKEN": true,
		// The server's API token, and the secret that signs per-session bridge
		// tokens when an operator pins it: a shell holding the secret could
		// mint a token for any session.
		"MCP_SERVER_API_TOKEN":    true,
		"MCP_BRIDGE_TOKEN_SECRET": true,
		// The JWT signing and stored-secret encryption key: a shell holding
		// it could forge sessions and decrypt stored secrets.
		"AUTH_SECRET": true,
		// The app's global login password and the legacy user list (emails and password hashes or
		// passwords): a shell holding either could sign in as anyone.
		"ACCESS_PASSWORD": true,
		"AUTH_USERS":      true,
		// Other people's account ids, the service's SSH agent and systemd bookkeeping.
		"AGENTWORKS_SLOT_CLI_USERS": true,
		"SSH_AUTH_SOCK":             true,
		"MEMORY_PRESSURE_WRITE":     true,
		"MEMORY_PRESSURE_WATCH":     true,
		"NOTIFY_SOCKET":             true,
		"INVOCATION_ID":             true,
		"JOURNAL_STREAM":            true,
		"SYSTEMD_EXEC_PID":          true,
	}

	var env []string
	pathValue := ""
	for _, kv := range os.Environ() {
		key := kv
		value := ""
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			key = kv[:idx]
			value = kv[idx+1:]
		}

		if blockedExact[key] {
			continue
		}

		blocked := false
		keyUpper := strings.ToUpper(key)
		for _, prefix := range blockedPrefixes {
			if strings.HasPrefix(keyUpper, prefix) {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}

		if key == "PATH" {
			pathValue = value
			continue
		}

		env = append(env, kv)
	}

	env = append(env, "PATH="+buildNativePath(pathValue))

	// Ensure Python output buffering is disabled
	env = append(env, "PYTHONUNBUFFERED=1")

	// Allow pip install when Python is externally managed (PEP 668); avoids
	// "externally-managed-environment" errors in LLM-run shells. Native mode
	// runs inside a per-step sandbox (Landlock/mount-namespace/Seatbelt), not
	// the operator's real system Python, so bypassing this guard here is safe
	// the same way it already is in buildDockerEnvironment above.
	env = append(env, "PIP_BREAK_SYSTEM_PACKAGES=1")

	return env
}

func buildNativePath(existing string) string {
	parts := splitPath(existing)
	add := func(path string) {
		if path == "" || containsPath(parts, path) {
			return
		}
		parts = append(parts, path)
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(filepath.Join(home, ".local", "bin"))
		add(filepath.Join(home, "go", "bin"))
		add(filepath.Join(home, ".cargo", "bin"))
		add(filepath.Join(home, ".bun", "bin"))
	}

	add("/opt/homebrew/bin")
	add("/opt/homebrew/sbin")
	add("/usr/local/bin")
	add("/usr/local/sbin")
	add("/usr/bin")
	add("/bin")
	add("/usr/sbin")
	add("/sbin")

	return browserCLIPath(strings.Join(parts, ":"))
}

func splitPath(pathValue string) []string {
	if strings.TrimSpace(pathValue) == "" {
		return nil
	}
	raw := strings.Split(pathValue, ":")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part == "" || containsPath(parts, part) {
			continue
		}
		parts = append(parts, part)
	}
	return parts
}

func containsPath(paths []string, target string) bool {
	for _, path := range paths {
		if path == target {
			return true
		}
	}
	return false
}
