package security

import (
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
	"github.com/manishiitg/coding-agent-loop/workspace/gogconfig"
	"os"
	"path/filepath"
	"strings"
)

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
		env = append(clean, browserconfig.ProfileEnv+"="+browserconfig.SharedProfile(), "AGENT_BROWSER_SOCKET_DIR=/tmp/.agent-browser", "TZ=UTC")
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
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/tmp",
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

	return env
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

	return strings.Join(parts, ":")
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
