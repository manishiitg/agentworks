package shelllaunch

import (
	"context"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	EnvShellMode = "CODING_AGENT_SHELL_MODE"
	EnvShellPath = "CODING_AGENT_LOGIN_SHELL"
)

// Command returns a tmux shell-command that starts a coding CLI from the
// caller's login shell. This lets GUI/DMG-launched servers pick up the same
// shell initialization a user expects when launching the CLI from Terminal.
func Command(args []string, workingDir string) string {
	args = managedCLIArgs(args)
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		workingDir = mustGetwd()
	}

	if directModeEnabled() {
		return DirectCommand(args, workingDir)
	}

	shellPath, kind, ok := resolveLoginShell()
	if !ok {
		return DirectCommand(args, workingDir)
	}

	switch kind {
	case "fish":
		return Join(append([]string{
			shellPath,
			"-lic",
			"cd $argv[1]; or exit; exec $argv[2..-1]",
			workingDir,
		}, args...))
	default:
		return Join(append([]string{
			shellPath,
			"-ilc",
			`cd "$1" || exit; shift; exec "$@"`,
			"coding-agent",
			workingDir,
		}, args...))
	}
}

func DirectCommand(args []string, workingDir string) string {
	args = managedCLIArgs(args)
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		workingDir = mustGetwd()
	}
	return "cd " + Quote(workingDir) + " && exec " + Join(args)
}

// CommandWithEnv returns a tmux shell-command that launches args with env
// variables without placing KEY=VALUE pairs in the tmux new-session argv. When
// env is non-empty it writes a 0600 self-deleting wrapper script containing the
// exports, then runs that script via /bin/sh. Call cleanup if tmux fails before
// the script starts; on successful launch the script removes itself.
func CommandWithEnv(args []string, workingDir string, env []string) (string, func(), error) {
	if err := slotfs.CheckLaunch(strings.TrimSpace(workingDir)); err != nil {
		return "", nil, err // a slot the host does not confirm: never the app account instead (PLAT-451)
	}
	entries, err := parseEnvEntries(env)
	if err != nil {
		return "", nil, err
	}
	if len(entries) == 0 {
		return Command(args, workingDir), func() {}, nil
	}

	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		workingDir = mustGetwd()
	}

	script := launchScript(args, workingDir, entries)
	file, err := slotfs.CreateTemp(workingDir, "mlp-coding-agent-launch-*.sh")
	if err != nil {
		return "", nil, fmt.Errorf("create launch script: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := file.Chmod(slotfs.Mode(workingDir, 0o600)); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, fmt.Errorf("chmod launch script: %w", err)
	}
	if _, err := file.WriteString(script); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, fmt.Errorf("write launch script: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close launch script: %w", err)
	}
	return Join([]string{"/bin/sh", path}), cleanup, nil
}

// CommandWithFinalEnv returns a self-deleting launch wrapper like
// CommandWithEnv, but applies the requested environment after the login shell
// has initialized and immediately before the coding CLI is exec'd. This is for
// authentication boundaries where shell startup files must not be able to
// restore an ambient credential that the caller explicitly removed.
//
// Values are kept out of the tmux/parent command line. In login-shell mode the
// wrapper carries them under private temporary names, then the inner shell
// unsets ambient variables, installs the final values, removes the private
// names, and execs the requested command.
func CommandWithFinalEnv(args []string, workingDir string, env, unset []string) (string, func(), error) {
	return CommandWithScopedEnv(args, workingDir, env, unset, nil)
}

// CommandWithScopedEnv is CommandWithFinalEnv plus a dynamic credential scrub
// evaluated against the environment that actually exists at exec time. See
// ScopeScrub for why an explicit unset list cannot cover tmux-server drift.
func CommandWithScopedEnv(args []string, workingDir string, env, unset []string, scrub *ScopeScrub) (string, func(), error) {
	if err := slotfs.CheckLaunch(strings.TrimSpace(workingDir)); err != nil {
		return "", nil, err // a slot the host does not confirm: never the app account instead (PLAT-451)
	}
	entries, err := parseEnvEntries(env)
	if err != nil {
		return "", nil, err
	}
	unsetKeys, err := parseUnsetKeys(unset)
	if err != nil {
		return "", nil, err
	}
	if len(entries) == 0 && len(unsetKeys) == 0 && scrub.empty() {
		return Command(args, workingDir), func() {}, nil
	}

	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		workingDir = mustGetwd()
	}

	script := launchScriptWithFinalEnv(args, workingDir, entries, unsetKeys, scrub)
	file, err := slotfs.CreateTemp(workingDir, "mlp-coding-agent-launch-*.sh")
	if err != nil {
		return "", nil, fmt.Errorf("create launch script: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := file.Chmod(slotfs.Mode(workingDir, 0o600)); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, fmt.Errorf("chmod launch script: %w", err)
	}
	if _, err := file.WriteString(script); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, fmt.Errorf("write launch script: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close launch script: %w", err)
	}
	return Join([]string{"/bin/sh", path}), cleanup, nil
}

func Join(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = Quote(arg)
	}
	return strings.Join(quoted, " ")
}

func Quote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

type envEntry struct {
	key   string
	value string
}

func parseEnvEntries(env []string) ([]envEntry, error) {
	entries := make([]envEntry, 0, len(env))
	for _, raw := range env {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, fmt.Errorf("invalid env entry %q: missing '='", key)
		}
		if !validEnvKey(key) {
			return nil, fmt.Errorf("invalid env key %q", key)
		}
		entries = append(entries, envEntry{key: key, value: value})
	}
	return entries, nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		ch := key[i]
		if i == 0 {
			if !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || ch == '_') {
				return false
			}
			continue
		}
		if !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return false
		}
	}
	return true
}

func parseUnsetKeys(keys []string) ([]string, error) {
	seen := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if !validEnvKey(key) {
			return nil, fmt.Errorf("invalid env key %q", key)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out, nil
}

func launchScript(args []string, workingDir string, entries []envEntry) string {
	args = managedCLIArgs(args)
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("rm -f \"$0\"\n")
	for _, entry := range entries {
		b.WriteString("export ")
		b.WriteString(entry.key)
		b.WriteString("=")
		b.WriteString(Quote(entry.value))
		b.WriteString("\n")
	}

	if !directModeEnabled() {
		if shellPath, kind, ok := resolveLoginShell(); ok {
			switch kind {
			case "fish":
				b.WriteString("exec ")
				b.WriteString(Join(append([]string{
					shellPath,
					"-lic",
					"cd $argv[1]; or exit; exec $argv[2..-1]",
					workingDir,
				}, args...)))
				b.WriteString("\n")
				return b.String()
			default:
				b.WriteString("exec ")
				b.WriteString(Join(append([]string{
					shellPath,
					"-ilc",
					`cd "$1" || exit; shift; exec "$@"`,
					"coding-agent",
					workingDir,
				}, args...)))
				b.WriteString("\n")
				return b.String()
			}
		}
	}

	b.WriteString("cd ")
	b.WriteString(Quote(workingDir))
	b.WriteString(" || exit\n")
	b.WriteString("exec ")
	b.WriteString(Join(args))
	b.WriteString("\n")
	return b.String()
}

// ScopeScrub describes a DYNAMIC credential scrub to run at the launch
// boundary. It exists because an explicit unset list cannot be complete: the
// caller enumerates keys from its own environment, while a tmux pane inherits
// the long-lived tmux SERVER's environment. After a backend restart those sets
// diverge, and precisely the drifted keys -- the ones the caller cannot see --
// are the ones that leak. Matching by pattern against the environment that
// actually exists at exec time removes the enumeration problem instead of
// relocating it.
//
// Keep lists the names that must survive: the caller's declared scope plus any
// credential the adapter derived itself. Everything else matching Prefixes or
// Names is unset.
type ScopeScrub struct {
	Prefixes []string
	Names    []string
	Keep     []string
}

func (s *ScopeScrub) empty() bool {
	return s == nil || (len(s.Prefixes) == 0 && len(s.Names) == 0)
}

// scrubScript emits POSIX sh that enumerates the REAL environment and unsets
// every scoped credential outside Keep. awk's ENVIRON is used rather than
// parsing `env` output, because a value containing a newline would otherwise
// be misread as another variable name.
func (s *ScopeScrub) scrubScript() string {
	if s.empty() {
		return ""
	}
	var patterns []string
	for _, prefix := range s.Prefixes {
		if prefix = strings.TrimSpace(prefix); prefix != "" {
			patterns = append(patterns, prefix+"*")
		}
	}
	for _, name := range s.Names {
		if name = strings.TrimSpace(name); name != "" {
			patterns = append(patterns, name)
		}
	}
	if len(patterns) == 0 {
		return ""
	}
	var keep []string
	for _, name := range s.Keep {
		if name = strings.TrimSpace(name); name != "" {
			keep = append(keep, name)
		}
	}

	var b strings.Builder
	b.WriteString("__mlp_keep=" + Quote(" "+strings.Join(keep, " ")+" ") + "; ")
	b.WriteString("for __mlp_k in $(awk 'BEGIN{for (k in ENVIRON) print k}' </dev/null); do ")
	b.WriteString("case \"$__mlp_k\" in ")
	b.WriteString(strings.Join(patterns, "|"))
	b.WriteString(") ")
	b.WriteString("case \"$__mlp_keep\" in *\" $__mlp_k \"*) ;; *) unset \"$__mlp_k\" ;; esac ")
	b.WriteString(";; esac; ")
	b.WriteString("done; unset __mlp_k __mlp_keep; ")
	return b.String()
}

// scrubScriptFish is the fish-shell form of scrubScript. fish has no `case`
// and uses `set -e` rather than `unset`, so the same policy needs its own
// rendering rather than a shared string.
func (s *ScopeScrub) scrubScriptFish() string {
	if s.empty() {
		return ""
	}
	var patterns []string
	for _, prefix := range s.Prefixes {
		if prefix = strings.TrimSpace(prefix); prefix != "" {
			patterns = append(patterns, prefix+"*")
		}
	}
	for _, name := range s.Names {
		if name = strings.TrimSpace(name); name != "" {
			patterns = append(patterns, name)
		}
	}
	if len(patterns) == 0 {
		return ""
	}
	var keep []string
	for _, name := range s.Keep {
		if name = strings.TrimSpace(name); name != "" {
			keep = append(keep, name)
		}
	}

	var b strings.Builder
	b.WriteString("set -l __mlp_keep " + fishQuoteList(keep) + "; ")
	b.WriteString("for __mlp_k in (set -n); ")
	b.WriteString("if contains -- $__mlp_k $__mlp_keep; continue; end; ")
	b.WriteString("switch $__mlp_k; case " + fishQuoteList(patterns) + "; set -e $__mlp_k; end; ")
	b.WriteString("end; set -e __mlp_k __mlp_keep; ")
	return b.String()
}

func fishQuoteList(values []string) string {
	if len(values) == 0 {
		// `contains -- $x` with an empty list is false, and an empty `case`
		// matches nothing, which is the intended behavior for both uses.
		return "''"
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "'"+strings.ReplaceAll(value, "'", `\'`)+"'")
	}
	return strings.Join(quoted, " ")
}

func launchScriptWithFinalEnv(args []string, workingDir string, entries []envEntry, unsetKeys []string, scrub *ScopeScrub) string {
	args = managedCLIArgs(args)
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("rm -f \"$0\"\n")

	privateNames := make([]string, len(entries))
	for i, entry := range entries {
		privateName := fmt.Sprintf("__MLP_CODING_AGENT_ENV_%d", i)
		privateNames[i] = privateName
		b.WriteString("export ")
		b.WriteString(privateName)
		b.WriteString("=")
		b.WriteString(Quote(entry.value))
		b.WriteString("\n")
	}

	if !directModeEnabled() {
		if shellPath, kind, ok := resolveLoginShell(); ok {
			var inner strings.Builder
			switch kind {
			case "fish":
				inner.WriteString(scrub.scrubScriptFish())
				for _, key := range unsetKeys {
					inner.WriteString("set -e ")
					inner.WriteString(key)
					inner.WriteString("; ")
				}
				for i, entry := range entries {
					inner.WriteString("set -gx ")
					inner.WriteString(entry.key)
					inner.WriteString(" \"")
					inner.WriteString("$")
					inner.WriteString(privateNames[i])
					inner.WriteString("\"; set -e ")
					inner.WriteString(privateNames[i])
					inner.WriteString("; ")
				}
				inner.WriteString("cd $argv[1]; or exit; exec $argv[2..-1]")
				b.WriteString("exec ")
				b.WriteString(Join(append([]string{shellPath, "-lic", inner.String(), workingDir}, args...)))
				b.WriteString("\n")
				return b.String()
			default:
				inner.WriteString(scrub.scrubScript())
				for _, key := range unsetKeys {
					inner.WriteString("unset ")
					inner.WriteString(key)
					inner.WriteString("; ")
				}
				for i, entry := range entries {
					inner.WriteString("export ")
					inner.WriteString(entry.key)
					inner.WriteString("=\"$")
					inner.WriteString(privateNames[i])
					inner.WriteString("\"; unset ")
					inner.WriteString(privateNames[i])
					inner.WriteString("; ")
				}
				inner.WriteString(`cd "$1" || exit; shift; exec "$@"`)
				b.WriteString("exec ")
				b.WriteString(Join(append([]string{shellPath, "-ilc", inner.String(), "coding-agent", workingDir}, args...)))
				b.WriteString("\n")
				return b.String()
			}
		}
	}

	if line := scrub.scrubScript(); line != "" {
		b.WriteString(line)
		b.WriteString("\n")
	}
	for _, key := range unsetKeys {
		b.WriteString("unset ")
		b.WriteString(key)
		b.WriteString("\n")
	}
	for _, entry := range entries {
		b.WriteString("export ")
		b.WriteString(entry.key)
		b.WriteString("=")
		b.WriteString(Quote(entry.value))
		b.WriteString("\n")
	}
	for _, privateName := range privateNames {
		b.WriteString("unset ")
		b.WriteString(privateName)
		b.WriteString("\n")
	}
	b.WriteString("cd ")
	b.WriteString(Quote(workingDir))
	b.WriteString(" || exit\n")
	b.WriteString("exec ")
	b.WriteString(Join(args))
	b.WriteString("\n")
	return b.String()
}

func directModeEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvShellMode))) {
	case "direct", "none", "off", "false", "0":
		return true
	default:
		return false
	}
}

func resolveLoginShell() (string, string, bool) {
	candidates := []string{
		os.Getenv(EnvShellPath),
		os.Getenv("SHELL"),
		darwinUserShell(),
		passwdUserShell(),
		"/bin/zsh",
		"/bin/bash",
		"/bin/sh",
	}

	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if !isExecutableAbsolutePath(candidate) {
			continue
		}
		kind := shellKind(candidate)
		if kind == "" {
			continue
		}
		return candidate, kind, true
	}
	return "", "", false
}

func shellKind(shellPath string) string {
	name := filepath.Base(shellPath)
	switch {
	case name == "fish":
		return "fish"
	case name == "zsh", name == "bash", name == "sh", name == "dash", name == "ksh":
		return "posix"
	default:
		return ""
	}
}

func isExecutableAbsolutePath(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

func darwinUserShell() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	current, err := user.Current()
	if err != nil || strings.TrimSpace(current.Username) == "" {
		return ""
	}
	username := strings.TrimPrefix(current.Username, "uid:")
	if idx := strings.LastIndex(username, `\`); idx >= 0 {
		username = username[idx+1:]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "dscl", ".", "-read", "/Users/"+username, "UserShell").Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	return strings.TrimSpace(strings.TrimPrefix(line, "UserShell:"))
}

func passwdUserShell() string {
	current, err := user.Current()
	if err != nil || strings.TrimSpace(current.Username) == "" {
		return ""
	}
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	username := current.Username
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, username+":") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) >= 7 {
			return strings.TrimSpace(fields[6])
		}
	}
	return ""
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// Resolve owned CLI shims before entering a login shell: shell rc files may
// reset PATH, and a pre-existing tmux server has an older environment. Explicit
// absolute paths (including PI_BIN) always retain the caller's selection.
func managedCLIArgs(args []string) []string {
	if len(args) == 0 || filepath.Base(args[0]) != args[0] {
		return args
	}
	switch args[0] {
	case "codex", "claude", "pi", "cursor-agent", "agent":
	default:
		return args
	}
	root := os.Getenv("AGENTWORKS_MANAGED_CLI_BIN")
	if !filepath.IsAbs(root) {
		return args
	}
	path := filepath.Join(root, args[0])
	if !isExecutableAbsolutePath(path) {
		return args
	}
	copy := append([]string(nil), args...)
	copy[0] = path
	return copy
}
