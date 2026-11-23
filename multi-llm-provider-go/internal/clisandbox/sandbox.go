package clisandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/internal/shelllaunch"
	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var ErrUnsupported = errors.New("CLI security policy is not supported on this runtime")

// PrepareCodexCommand returns a command string that runs inside the tmux pane.
// This placement is load-bearing: wrapping the tmux client would not sandbox a
// pane created by an already-running tmux server.
func PrepareCodexCommand(policy *llmtypes.CLISecurityPolicy, args []string, workingDir string, runtimeReadPaths []string) (string, func(), error) {
	return PrepareCodexCommandScoped(policy, args, workingDir, runtimeReadPaths, nil, nil, nil)
}

// PrepareCodexCommandScoped is PrepareCodexCommand plus the caller's scoped
// credential environment.
//
// Codex was outside the isolation guarantee entirely. Compatibility mode --
// the default -- handed the child a plain command with full ambient
// inheritance, so it read whatever credentials were on the backend. The
// sandboxed modes were already stronger than the other providers (env -i with
// an explicit allowlist), but neither mode ever INJECTED the scope the caller
// declared, so a child could not receive its own credentials either.
//
// scopedEnv is applied in both modes; scrub only matters in compatibility mode,
// since env -i has already discarded everything the allowlist did not name.
func PrepareCodexCommandScoped(policy *llmtypes.CLISecurityPolicy, args []string, workingDir string, runtimeReadPaths []string, scopedEnv, unset []string, scrub *shelllaunch.ScopeScrub) (string, func(), error) {
	if policy == nil || llmtypes.NormalizeCLISecurityMode(policy.Mode) == llmtypes.CLISecurityModeCompatibility {
		if len(scopedEnv) == 0 && len(unset) == 0 && scrub == nil {
			if err := slotfs.CheckLaunch(strings.TrimSpace(workingDir)); err != nil {
				return "", nil, err // PLAT-451: never the app account instead of the declared slot
			}
			return shelllaunch.Command(args, workingDir), func() {}, nil
		}
		return shelllaunch.CommandWithScopedEnv(args, workingDir, scopedEnv, unset, scrub)
	}
	// The host's lock (Landlock on Linux, Seatbelt on a Mac) confines Codex
	// like every other CLI.
	if policy.Confined() {
		wrapped, cleanupPolicy, err := LandlockArgs(policy, args, workingDir, append(runtimeReadPaths, ArgFilePaths(args)...), nil)
		if err != nil {
			return "", nil, err
		}
		command, cleanup, err := shelllaunch.CommandWithScopedEnv(wrapped, workingDir, scopedEnv, unset, scrub)
		if err != nil {
			cleanupPolicy()
			return "", nil, err
		}
		return command, func() { cleanup(); cleanupPolicy() }, nil
	}
	if runtime.GOOS != "darwin" {
		return "", nil, fmt.Errorf("%w: %s requires macOS sandbox-exec", ErrUnsupported, policy.Mode)
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		return "", nil, fmt.Errorf("%w: sandbox-exec is unavailable", ErrUnsupported)
	}
	if strings.TrimSpace(policy.Provider) != "codex-cli" {
		return "", nil, fmt.Errorf("%w: provider %q", ErrUnsupported, policy.Provider)
	}
	if len(args) == 0 {
		return "", nil, errors.New("Codex launch command is empty")
	}

	resolvedArgs := append([]string(nil), args...)
	executable, err := exec.LookPath(resolvedArgs[0])
	if err != nil {
		return "", nil, fmt.Errorf("resolve Codex executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", nil, fmt.Errorf("resolve absolute Codex executable: %w", err)
	}
	resolvedArgs[0] = executable

	profile, err := codexProfile(policy, executable, workingDir, runtimeReadPaths)
	if err != nil {
		return "", nil, err
	}
	file, err := os.CreateTemp("", "agentworks-codex-sandbox-*.sb")
	if err != nil {
		return "", nil, fmt.Errorf("create Codex sandbox profile: %w", err)
	}
	profilePath := file.Name()
	cleanup := func() { _ = os.Remove(profilePath) }
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, fmt.Errorf("protect Codex sandbox profile: %w", err)
	}
	if _, err := file.WriteString(profile); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, fmt.Errorf("write Codex sandbox profile: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close Codex sandbox profile: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("resolve Codex home: %w", err)
	}
	if policy.Mode == llmtypes.CLISecurityModeIsolated {
		home = canonical(policy.PrivateHome)
		privateHome := home
		if privateHome == "" {
			cleanup()
			return "", nil, errors.New("isolated CLI security requires a private home")
		}
		if err := os.MkdirAll(privateHome, 0o700); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("create isolated Codex home: %w", err)
		}
	}
	envArgs := []string{"/usr/bin/env", "-i"}
	for _, key := range []string{"PATH", "TERM", "LANG", "LC_ALL", "TMPDIR"} {
		if value := os.Getenv(key); value != "" {
			envArgs = append(envArgs, key+"="+value)
		}
	}
	envArgs = append(envArgs, "HOME="+home)
	envArgs = append(envArgs, "CODEX_HOME="+filepath.Join(home, ".codex"))
	for _, key := range policy.EnvironmentVariables {
		key = strings.TrimSpace(key)
		if key == "" || strings.Contains(key, "=") {
			continue
		}
		if value, ok := os.LookupEnv(key); ok {
			envArgs = append(envArgs, key+"="+value)
		}
	}
	// The declared scope is appended last so it wins over anything the
	// allowlist above pulled in from the ambient environment.
	envArgs = append(envArgs, scopedEnv...)
	resolvedArgs = append(envArgs, resolvedArgs...)
	direct := shelllaunch.DirectCommand(resolvedArgs, workingDir)
	command := shelllaunch.Join([]string{"/usr/bin/sandbox-exec", "-f", profilePath, "/bin/sh", "-c", direct})
	return command, cleanup, nil
}

func codexProfile(policy *llmtypes.CLISecurityPolicy, executable, workingDir string, runtimeReadPaths []string) (string, error) {
	mode := llmtypes.NormalizeCLISecurityMode(policy.Mode)
	if mode != llmtypes.CLISecurityModeVerified && mode != llmtypes.CLISecurityModeIsolated {
		return "", fmt.Errorf("%w: mode %q", ErrUnsupported, mode)
	}

	readPaths := []string{
		"/System",
		"/usr",
		"/bin",
		"/sbin",
		"/Library/Apple",
		"/private/etc",
		"/private/var/db/timezone",
		"/opt/homebrew",
		executable,
		workingDir,
	}
	writePaths := []string{workingDir}
	readPaths = append(readPaths, policy.WorkspaceReadPaths...)
	readPaths = append(readPaths, policy.WorkspaceWritePaths...)
	readPaths = append(readPaths, policy.HostReadPaths...)
	readPaths = append(readPaths, policy.HostWritePaths...)
	readPaths = append(readPaths, runtimeReadPaths...)
	writePaths = append(writePaths, policy.WorkspaceWritePaths...)
	writePaths = append(writePaths, policy.HostWritePaths...)

	if mode == llmtypes.CLISecurityModeIsolated {
		readPaths = append(readPaths, policy.PrivateHome)
		writePaths = append(writePaths, policy.PrivateHome)
	}

	readPaths = canonicalUnique(readPaths)
	writePaths = canonicalUnique(writePaths)
	if len(readPaths) == 0 || len(writePaths) == 0 {
		return "", errors.New("Codex sandbox requires explicit read and write paths")
	}

	var profile strings.Builder
	profile.WriteString("(version 1)\n")
	profile.WriteString("(allow default)\n")
	// Deny user-data roots, then add narrow provider/workspace grants below.
	// Starting from deny-default is not viable for current macOS CLI runtimes:
	// dyld, networking, locale, TTY, and system services have undocumented
	// filesystem dependencies. These broad user-data denies are the enforceable
	// boundary; system/runtime paths remain compatible.
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		profile.WriteString("(deny file-read* file-write* (subpath \"" + sandboxQuote(canonical(home)) + "\"))\n")
	}
	profile.WriteString("(deny file-read* file-write* (subpath \"/Users\"))\n")
	profile.WriteString("(deny file-read* file-write* (subpath \"/Volumes\"))\n")
	profile.WriteString("(deny file-read* file-write* (subpath \"/Network\"))\n")
	profile.WriteString("(allow file-read-metadata\n")
	for _, path := range ancestorPaths(append(append([]string(nil), readPaths...), writePaths...)) {
		profile.WriteString("  (literal \"" + sandboxQuote(path) + "\")\n")
	}
	profile.WriteString(")\n")
	profile.WriteString("(allow file-read*\n")
	for _, path := range readPaths {
		profile.WriteString("  (subpath \"" + sandboxQuote(path) + "\")\n")
	}
	profile.WriteString(")\n")
	profile.WriteString("(allow file-read* file-write*\n")
	for _, path := range writePaths {
		profile.WriteString("  (subpath \"" + sandboxQuote(path) + "\")\n")
	}
	profile.WriteString(")\n")
	// TTYs, pipes, and null devices are needed by tmux and the Codex TUI.
	profile.WriteString("(allow file-read* file-write* (subpath \"/dev\"))\n")
	return profile.String(), nil
}

func Fingerprint(policy *llmtypes.CLISecurityPolicy) string {
	if policy == nil {
		return string(llmtypes.CLISecurityModeCompatibility)
	}
	cloned := policy.Clone()
	sort.Strings(cloned.WorkspaceReadPaths)
	sort.Strings(cloned.WorkspaceWritePaths)
	sort.Strings(cloned.HostReadPaths)
	sort.Strings(cloned.HostWritePaths)
	sort.Strings(cloned.EnvironmentVariables)
	sort.Strings(cloned.ApprovedCapabilities)
	data, _ := json.Marshal(cloned)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func canonicalUnique(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = canonical(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func ancestorPaths(paths []string) []string {
	var ancestors []string
	for _, path := range canonicalUnique(paths) {
		for current := filepath.Dir(path); current != "."; current = filepath.Dir(current) {
			ancestors = append(ancestors, current)
			if current == string(filepath.Separator) {
				break
			}
		}
	}
	return canonicalUnique(ancestors)
}

func canonical(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func sandboxQuote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}
