package clisandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// landlockPolicy is the launcher's config format (workspace/security
// LandlockPolicy). The launcher reads it, deletes it, applies Landlock and
// execs the command, so the CLI is the pane's process as before.
type landlockPolicy struct {
	ReadPaths  []string `json:"read_paths"`
	WritePaths []string `json:"write_paths"`
	WorkDir    string   `json:"work_dir"`
	// ListPaths may be listed (folder names) but not read. A confined CLI
	// gets "/": Muse opens every folder from / down to its workspace at
	// start, and Landlock cannot grant one folder without the ones below it.
	ListPaths []string `json:"list_paths,omitempty"`
	// Agy native shells require PTYs; only grant an isolated devpts instance.
	PrivatePTS bool `json:"private_pts,omitempty"`
	// HiddenPaths and ReadOnlyOverlays are blocked paths inside a grant that the
	// launcher mounts over in the command's own namespaces (shell wrapping
	// starts them): hidden ones can be neither read nor changed, read-only ones
	// can be read but not changed.
	HiddenPaths      []string `json:"hidden_paths,omitempty"`
	ReadOnlyOverlays []string `json:"read_only_overlays,omitempty"`
	// ShellWrap: every shell the CLI starts runs under Inner, without the CLI's logins (PLAT-872-84).
	ShellWrap *landlockShellWrap `json:"shell_wrap,omitempty"`
	// OwnLoginEnv is the only login variables the CLI keeps; the launcher removes every other login-like variable
	// before it starts (PLAT-872-91). Always sent: a CLI with no list keeps no login variables.
	OwnLoginEnv *[]string `json:"own_login_env,omitempty"`
}

// ownLoginEnv is each CLI's own login variables: one CLI never carries another provider's company login (a Muse
// launch held the company Claude and Codex logins and its shells read them, PLAT-872-91). Pi sends its own per turn
// (policy.OwnLoginEnv: the chosen model's provider key). A CLI with no entry keeps none.
var ownLoginEnv = map[string][]string{
	"claude-code": {"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"},
	"codex-cli":   {"CODEX_API_KEY", "OPENAI_API_KEY"},
	"cursor-cli":  {"CURSOR_API_KEY"},
	"muse-cli":    {"META_API_KEY"},
	"agy-cli":     {"GEMINI_API_KEY", "GOOGLE_API_KEY"},
}

type landlockShellWrap struct {
	Inner    landlockPolicy `json:"inner"`
	ScrubEnv []string       `json:"scrub_env,omitempty"`
}

// shellWrapEnabled turns shell wrapping on for confined launches (PLAT-872-84). It needs a launcher whose wrapper
// lets the inner layer run the real shells in its private /tmp.
var shellWrapEnabled = true

// cliLoginEnv are the environment variables a coding CLI logs in with. A shell the CLI starts never needs them, so
// the launcher's shell wrapper removes them; the CLI itself keeps them.
var cliLoginEnv = []string{
	"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
	"CODEX_API_KEY", "OPENAI_API_KEY",
	"CURSOR_API_KEY", "META_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENROUTER_API_KEY",
}

// withoutPaths is paths without any of drop (canonical spelling).
func withoutPaths(paths, drop []string) []string {
	skip := map[string]bool{}
	for _, path := range drop {
		skip[canonical(path)] = true
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if !skip[canonical(path)] {
			out = append(out, path)
		}
	}
	return out
}

// credentialFile is one CLI login file: where it goes in the private home
// (Rel, which the sandbox's HOME/XDG/CODEX_HOME/CLAUDE_CONFIG_DIR layout also
// points at), and where the account keeps it: under the account's EnvKey
// folder when that is set (a server whose XDG_CONFIG_HOME is not ~/.config),
// else at Rel under the account home.
type credentialFile struct {
	Rel    string
	EnvKey string
	Sub    string
}

// credentialFiles are the only part of the account (or server) home a
// confined CLI can reach.
var credentialFiles = map[string][]credentialFile{
	"claude-code": {{Rel: ".claude/.credentials.json", EnvKey: "CLAUDE_CONFIG_DIR", Sub: ".credentials.json"}},
	"codex-cli":   {{Rel: ".codex/auth.json", EnvKey: "CODEX_HOME", Sub: "auth.json"}},
	"cursor-cli": {
		{Rel: ".config/cursor/auth.json", EnvKey: "XDG_CONFIG_HOME", Sub: "cursor/auth.json"},
		{Rel: ".cursor/cli-config.json"},
		{Rel: ".cursor/sdk/auth.json"},
	},
	"muse-cli": {{Rel: ".config/muse/auth.json", EnvKey: "XDG_CONFIG_HOME", Sub: "muse/auth.json"}},
}

// credentialSource is where the account keeps one login file.
func credentialSource(file credentialFile, home string, env func(string) string) string {
	if file.EnvKey != "" {
		if root := strings.TrimSpace(env(file.EnvKey)); root != "" {
			return filepath.Join(root, file.Sub)
		}
	}
	return filepath.Join(home, file.Rel)
}

// LandlockArgs wraps a coding CLI's argv so it starts confined: by the host's
// Landlock launcher on Linux, or by Seatbelt on a Mac. It returns args
// unchanged when the policy does not confine the CLI on this host.
// runtimeReadPaths/runtimeWritePaths are files the adapter prepared for this
// launch (MCP config, settings, hooks, status files) that live outside the
// granted folders.
// LandlockWritePaths is every folder a Landlock-confined CLI may write, before
// blocked paths are split out of them: its working folder, its private home,
// the policy's write grants and the launch's own write paths. A file the CLI
// must not change (a request it would act on as its person) must lie outside
// all of them; WritableUnder says whether one does.
func LandlockWritePaths(policy *llmtypes.CLISecurityPolicy, workingDir string, runtimeWritePaths []string) []string {
	write := []string{workingDir, canonical(policy.PrivateHome)}
	// Only Cursor gets the shared /tmp: it keeps sockets at fixed /tmp paths
	// (cursor-askpass-*.sock, and .cursor/<project> when its home path is too long for a socket).
	// Every other CLI uses the private TMPDIR from CLIHomeEnvironment and starts without it (Muse,
	// Pi and Agy verified under the real launcher; Claude and Codex ran confined without it), so
	// one CLI's temp files are not readable by another. See docs/DECISIONS.md in the builder.
	if strings.EqualFold(strings.TrimSpace(policy.Provider), "cursor-cli") {
		write = append(write, "/tmp")
	}
	write = append(write, policy.WorkspaceWritePaths...)
	write = append(write, policy.HostWritePaths...)
	return append(write, runtimeWritePaths...)
}

// WritableUnder reports whether path is one of the write paths or inside one.
func WritableUnder(writePaths []string, path string) bool {
	target := canonicalThroughExisting(path)
	for _, write := range writePaths {
		dir := canonicalThroughExisting(write)
		if dir == "" {
			continue
		}
		if target == dir || strings.HasPrefix(target, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func LandlockArgs(policy *llmtypes.CLISecurityPolicy, args []string, workingDir string, runtimeReadPaths, runtimeWritePaths []string) ([]string, func(), error) {
	return landlockArgs(policy, args, workingDir, runtimeReadPaths, runtimeWritePaths, nil, nil)
}

// landlockArgs is LandlockArgs for a command whose environment is known: the sandbox's git settings are added after
// the git settings env already carries (GIT_CONFIG_COUNT/KEY_n/VALUE_n), so neither replaces the other.
func landlockArgs(policy *llmtypes.CLISecurityPolicy, args []string, workingDir string, runtimeReadPaths, runtimeWritePaths, env, launchCredentials []string) ([]string, func(), error) {
	noop := func() {}
	if policy.SeatbeltEnforced() {
		return SeatbeltArgs(policy, args, workingDir, SeatbeltGrants{ReadPaths: runtimeReadPaths, WritePaths: runtimeWritePaths})
	}
	if !policy.LandlockEnforced() {
		return args, noop, nil
	}
	if len(args) == 0 {
		return nil, noop, errors.New("coding CLI launch command is empty")
	}
	runner := strings.TrimSpace(policy.LandlockRunner)
	if !filepath.IsAbs(runner) {
		return nil, noop, fmt.Errorf("%w: Landlock launcher path must be absolute", ErrUnsupported)
	}
	if _, err := os.Stat(runner); err != nil {
		return nil, noop, fmt.Errorf("%w: Landlock launcher unavailable: %w", ErrUnsupported, err)
	}
	home := canonical(policy.PrivateHome)
	// A launch the application declared for a slot the host does not confirm is refused here, before any file is
	// prepared: it must not run as the app account instead (PLAT-451).
	if err := slotfs.CheckLaunch(home); err != nil {
		return nil, noop, err
	}
	for _, dir := range []string{home, filepath.Join(home, ".config"), filepath.Join(home, ".local", "share"), filepath.Join(home, ".local", "state"), filepath.Join(home, ".cache"), filepath.Join(home, "tmp")} {
		if err := os.MkdirAll(dir, slotfs.Mode(home, 0o700)); err != nil {
			return nil, noop, fmt.Errorf("create private CLI home: %w", err)
		}
	}
	// A slot runtime folder (<slot state>/<slot>/cli-runtimes/v1/<digest>) is the slot's to use: the server's umask
	// left the folders it made (the private home's parents, tool_output_folder) owner-only, so the slot could not
	// enter its own home.
	if runtime := canonical(workingDir); strings.Contains(runtime, string(filepath.Separator)+filepath.Join("cli-runtimes", "v1")+string(filepath.Separator)) && strings.HasPrefix(home, runtime+string(filepath.Separator)) {
		if err := slotfs.ShareTree(home, runtime); err != nil {
			return nil, noop, fmt.Errorf("give the user's account its CLI runtime: %w", err)
		}
	}
	adoptResumedSession(policy, args)
	if err := flushClaudeCredentialSync(policy); err != nil {
		return nil, noop, err
	}
	credentials, err := linkCredentialFiles(policy, home)
	if err != nil {
		return nil, noop, err
	}
	// Login files the adapter made for this launch: the CLI reads them, its shells never.
	credentials = append(credentials, existing(canonicalUnique(launchCredentials))...)

	launchedAs := "app"
	if slot, ok := slotfs.SlotOf(home); ok {
		launchedAs = slot
	}
	log.Printf("[CLI_LANDLOCK] launch provider=%s launched_as=%s home=%s", strings.TrimSpace(policy.Provider), launchedAs, home)
	if slot, ok := slotfs.SlotOf(home); ok {
		// The CLI runs as the user's own account: give it the files the adapter prepared for this launch.
		for _, path := range runtimeReadPaths {
			if err := slotfs.GrantRuntime(slot, path, false); err != nil {
				return nil, noop, fmt.Errorf("give the user's account launch files: %w", err)
			}
		}
		for _, path := range runtimeWritePaths {
			// A per-launch folder the platform built (an isolated CLI home, a config root) is made the
			// slot group's to use, whatever mode its adapter gave it.
			if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
				if own, owned := slotfs.SlotOf(path); !owned || own != slot {
					if err := slotfs.ShareTree(home, path); err != nil {
						return nil, noop, fmt.Errorf("give the user's account launch files: %w", err)
					}
				}
			}
			if err := slotfs.GrantRuntime(slot, path, true); err != nil {
				return nil, noop, fmt.Errorf("give the user's account launch files: %w", err)
			}
		}
		for _, path := range executableDirs(args) {
			if err := slotfs.GrantRuntime(slot, path, false); err != nil {
				return nil, noop, fmt.Errorf("give the user's account its CLI install: %w", err)
			}
		}
		// Folders other people share with this user (a shared Code, a Crew): the launch policy already
		// lists exactly what this user may reach, so give this slot the same access at the file level.
		// The user's own folders belong to the slot already.
		for _, shared := range []struct {
			paths []string
			write bool
		}{{policy.WorkspaceReadPaths, false}, {policy.WorkspaceWritePaths, true}} {
			for _, path := range shared.paths {
				if own, ok := slotfs.SlotOf(path); ok && own == slot {
					continue
				}
				// A Crew's folders already belong to its owner's slot group; a file another account owns cannot take
				// an ACL from the platform (setfacl: Operation not permitted). Launch anyway: the
				// slot reaches what its group allows.
				provider, folder := strings.TrimSpace(policy.Provider), path
				slotfs.GrantSharedOnce(slot, path, shared.write, func(err error) {
					log.Printf("[CLI_LANDLOCK] launch provider=%s as %s: shared folder %s not fully granted (granted once per server run; the next restart tries again): %v", provider, slot, folder, err)
				})
			}
		}
	}

	read := append([]string{}, policy.WorkspaceReadPaths...)
	read = append(read, policy.HostReadPaths...)
	read = append(read, runtimeReadPaths...)
	read = append(read, executableDirs(args)...)
	write := append(LandlockWritePaths(policy, workingDir, runtimeWritePaths), credentials...)
	// Blocked paths inside a grant (planning/, the raw database, a Crew's
	// settings/, AGENTS.md): Landlock cannot take them back. With the launcher's
	// own namespaces an existing one is mounted over (hidden, or read-only for a
	// blocked write) and the grant stays whole, so the CLI can still create files
	// beside it (PLAT-746-924); a missing one, or any without those namespaces,
	// splits the grant around it as before. Mounting covers the CLI's own
	// working folder too (a server Crew's settings/ sits inside it). That folder
	// is never split: for Code it is the project, and splitting it would stop
	// the CLI creating files there; a missing blocked path inside it is left to
	// the bridge, as before.
	cwd := canonical(workingDir)
	read, write = canonicalUnique(read), canonicalUnique(write)
	blockedRead, blockedWrite := canonicalUnique(policy.BlockedPaths), canonicalUnique(policy.BlockedWritePaths)
	var hidden, readOnly []string
	if shellWrapEnabled {
		hidden, readOnly, blockedRead, blockedWrite = mountedBlocks(read, write, blockedRead, blockedWrite)
	}
	read, write = splitAroundBlocked(read, write, outside(blockedRead, cwd), outside(blockedWrite, cwd))
	config := landlockPolicy{
		ReadPaths:        existing(canonicalUnique(read)),
		WritePaths:       existing(canonicalUnique(write)),
		WorkDir:          canonical(workingDir),
		ListPaths:        []string{"/"},
		PrivatePTS:       strings.TrimSpace(policy.Provider) == "agy-cli",
		HiddenPaths:      hidden,
		ReadOnlyOverlays: readOnly,
	}
	own := append([]string{}, policy.OwnLoginEnv...)
	if len(own) == 0 {
		own = append(own, ownLoginEnv[strings.TrimSpace(policy.Provider)]...)
	}
	config.OwnLoginEnv = &own
	log.Printf("[CLI_LOGIN_ENV] provider=%s keeps only %v of the login variables", policy.Provider, own)
	// The CLI's shells get the same grants without its login files, and no login variables, in the CLI's own private
	// namespace (a private /tmp, the account's service manager hidden). Muse too since PLAT-872-91: unwrapped, its
	// shells reached the app account's unconfined tmux server in /tmp (its environment held the company logins, and a
	// new window ran outside the sandbox) and escaped through systemd-run --user.
	if shellWrapEnabled {
		config.ShellWrap = &landlockShellWrap{
			Inner: landlockPolicy{ReadPaths: withoutPaths(config.ReadPaths, credentials), WritePaths: withoutPaths(config.WritePaths, credentials),
				WorkDir: config.WorkDir, ListPaths: config.ListPaths},
			ScrubEnv: cliLoginEnv,
		}
	}
	if config.ShellWrap != nil && strings.TrimSpace(policy.Provider) == "claude-code" {
		// A refresh replaces the login symlink with a regular file. Shells must
		// never read that transient file or sibling refresh temporaries either.
		inner := &config.ShellWrap.Inner
		inner.ReadPaths, inner.WritePaths = splitAroundBlocked(inner.ReadPaths, inner.WritePaths, []string{filepath.Join(home, ".claude")}, nil)
	}
	file, err := slotfs.CreateTemp(home, "agentworks-cli-landlock-*.json")
	if err != nil {
		return nil, noop, fmt.Errorf("create CLI Landlock policy: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	err = file.Chmod(slotfs.Mode(home, 0o600))
	if err == nil {
		err = json.NewEncoder(file).Encode(config)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		cleanup()
		return nil, noop, fmt.Errorf("write CLI Landlock policy: %w", err)
	}
	// The launcher execs argv[0] without a PATH search; /usr/bin/env resolves
	// the CLI (and `env VAR=… cli` forms) inside the sandbox.
	wrapped := []string{runner, "--config", path, "--", "/usr/bin/env"}
	if slotfs.IsSlotLaunch(home) {
		// The folders belong to the platform account with the slot's group, so git refuses them as
		// "dubious ownership" (and a CLI such as Muse fails to find its project root). The folders a
		// confined CLI can reach are only the ones it was granted.
		// safe.directory=* (the folders are platform-owned), and the two repository settings that run code
		// unasked, hooks and fsmonitor, switched off: a shared folder's author cannot get code run as the
		// user through git (same set as the shell tool's; docs/DECISIONS.md in the builder).
		wrapped = append(wrapped, appendGitConfig(env, "safe.directory", "*", "core.hooksPath", "/dev/null", "core.fsmonitor", "false")...)
	} else {
		// Private project runtimes can trust exact mixed-UID repositories in
		// their own Git config. Keep repository hooks and fsmonitor disabled
		// at command scope, where repository-local config cannot override them.
		wrapped = append(wrapped, appendGitConfig(env, "core.hooksPath", "/dev/null", "core.fsmonitor", "false")...)
	}
	// The shim is a convenience: a home that cannot take it (read-only, full) launches without it.
	if shimEnv, err := nodeMemoryShimEnv(home, env); err != nil {
		log.Printf("[CLI_LANDLOCK] launch provider=%s without the Node memory shim: %v", strings.TrimSpace(policy.Provider), err)
	} else {
		wrapped = append(wrapped, shimEnv)
	}
	wrapped = append(wrapped, args...)
	stopRefresh, err := beginClaudeCredentialSync(policy, home)
	if err != nil {
		cleanup()
		return nil, noop, err
	}
	originalCleanup := cleanup
	return wrapped, func() { stopRefresh(); originalCleanup() }, nil
}

// nodeMemoryShim keeps process.memoryUsage() working inside the lock. The launcher grants /proc/self as the
// launcher's own pid, so a node started from the CLI's shell (a new pid) cannot read its /proc/self and the call
// throws EACCES (uv_resident_set_memory): `next build` failed in every sandboxed Crew. On that error it answers
// from V8's own heap numbers, which need no /proc; any other error, and every unblocked call, is unchanged.
const nodeMemoryShim = `"use strict";
const v8 = require("v8");
const original = process.memoryUsage;
const blocked = (err) => err && err.code === "EACCES";
const estimate = () => {
  const heap = v8.getHeapStatistics();
  return { rss: heap.total_heap_size + heap.external_memory, heapTotal: heap.total_heap_size, heapUsed: heap.used_heap_size, external: heap.external_memory, arrayBuffers: 0 };
};
const memoryUsage = function memoryUsage() {
  try { return original.call(process); } catch (err) { if (blocked(err)) return estimate(); throw err; }
};
memoryUsage.rss = function rss() {
  try { return original.rss(); } catch (err) { if (blocked(err)) return estimate().rss; throw err; }
};
process.memoryUsage = memoryUsage;
`

// nodeMemoryShimEnv writes the shim into the private home (readable inside the lock) and returns the NODE_OPTIONS
// that preloads it, after any NODE_OPTIONS the launch already carries.
func nodeMemoryShimEnv(home string, env []string) (string, error) {
	path := filepath.Join(home, ".agentworks-node-memory.js")
	if err := os.WriteFile(path, []byte(nodeMemoryShim), 0o644); err != nil {
		return "", fmt.Errorf("write the sandbox Node memory shim: %w", err)
	}
	options := ""
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "NODE_OPTIONS="); ok {
			options = strings.TrimSpace(value)
		}
	}
	require := "--require=" + path
	if strings.ContainsAny(path, " \t\"") {
		require = "--require=" + strconv.Quote(path)
	}
	return "NODE_OPTIONS=" + strings.TrimSpace(options+" "+require), nil
}

// linkCredentialFiles gives the private home a link to each of the CLI's
// login files in CredentialHome, never a copy: logins rotate their refresh
// tokens, so every session on one account must share the one file. If the
// CLI replaced the link with its own file (write-and-rename), that newer
// file is moved back to the account first so no session loses a refresh.
// It returns the credential files to grant.
func linkCredentialFiles(policy *llmtypes.CLISecurityPolicy, home string) ([]string, error) {
	// A CLI run as a person's slot reaches the server's shared login the same way as its own account's: a link to
	// the one login file plus that slot's grant on it, so token rotations stay in one file. The shells it starts
	// never see it: the paths returned here join the Landlock layer that keeps logins out of shell commands
	// (PLAT-872-84, owner 2 Nov: "hide the login via landlock only", PLAT-872-86).
	source := canonical(policy.CredentialHome)
	if source == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			source = canonical(userHome)
		}
	}
	if source == "" || source == home {
		return nil, nil
	}
	var granted []string
	// The server account's logins are where this process's environment says;
	// an account's are where its own path environment says.
	env := os.Getenv
	if strings.TrimSpace(policy.CredentialHome) != "" {
		env = func(key string) string { return policy.CredentialEnv[key] }
	}
	for _, file := range credentialFiles[strings.TrimSpace(policy.Provider)] {
		src := credentialSource(file, source, env)
		dst := filepath.Join(home, file.Rel)
		if info, err := os.Lstat(dst); err == nil && info.Mode().IsRegular() {
			if srcInfo, err := os.Stat(src); strings.TrimSpace(policy.Provider) != "claude-code" && (err != nil || info.ModTime().After(srcInfo.ModTime())) {
				if err := copyFileAtomic(dst, src, home); err != nil {
					return nil, fmt.Errorf("return refreshed CLI login to its account: %w", err)
				}
			}
		}
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if target, err := os.Readlink(dst); err != nil || target != src {
			if err := os.MkdirAll(filepath.Dir(dst), slotfs.Mode(home, 0o700)); err != nil {
				return nil, err
			}
			_ = os.Remove(dst)
			if err := os.Symlink(src, dst); err != nil {
				return nil, fmt.Errorf("link CLI login into private home: %w", err)
			}
		}
		if slot, ok := slotfs.SlotOf(home); ok {
			if err := slotfs.GrantAccess(slot, src); err != nil {
				return nil, fmt.Errorf("give the user's account access to the CLI login: %w", err)
			}
			log.Printf("[CLI_LOGIN] provider=%s slot=%s action=grant", strings.TrimSpace(policy.Provider), slot)
		}
		granted = append(granted, src)
	}
	return granted, nil
}

func copyFileAtomic(from, to, hint string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(to), slotfs.Mode(hint, 0o700)); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(to), ".agentworks-login-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Chmod(slotfs.Mode(hint, 0o600)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), to)
}

// executableDirs grants the CLI's install folder read-only when it lives
// outside the launcher's system baseline (e.g. ~/.local/bin/cursor-agent ->
// ~/.local/share/cursor-agent/versions/…): the link's folder and the real
// binary's folder tree.
func executableDirs(args []string) []string {
	var dirs []string
	// A launch may start with a shell prelude, `sh -c <script> <name> <arg> <real command...>` (Muse sweeps its probe folders first): the CLI is
	// the command after it, not the shell, whose folder the baseline already grants.
	if len(args) > 5 && args[0] == "sh" && args[1] == "-c" {
		args = args[5:]
	}
	for _, arg := range args {
		if arg == "env" || arg == "/usr/bin/env" || strings.Contains(arg, "=") {
			continue
		}
		resolved, err := exec.LookPath(arg)
		if err != nil {
			return dirs
		}
		dirs = append(dirs, filepath.Dir(resolved))
		if real, err := filepath.EvalSymlinks(resolved); err == nil {
			dirs = append(dirs, installRoot(real))
		}
		return dirs
	}
	return dirs
}

// installRoot widens a versioned binary path to its package folder so the
// CLI can load files next to it (node_modules, versions/<v>/…).
func installRoot(binary string) string {
	dir := filepath.Dir(binary)
	for current := dir; current != "/" && current != "."; current = filepath.Dir(current) {
		base := filepath.Base(current)
		if base == "versions" || base == "node_modules" {
			return filepath.Dir(current)
		}
	}
	return dir
}

func existing(paths []string) []string {
	out := paths[:0]
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			out = append(out, path)
		}
	}
	return out
}

// ArgFilePaths returns the absolute paths of existing files named in argv,
// either as an argument or as the value of a --flag=value argument. A launch
// reads these (configs, prompts, helpers) and they may live outside the
// granted folders.
func ArgFilePaths(args []string) []string {
	var paths []string
	for _, arg := range args {
		if _, value, ok := strings.Cut(arg, "="); ok && strings.HasPrefix(arg, "-") {
			arg = value
		}
		arg = strings.Trim(arg, `'"`)
		if !filepath.IsAbs(arg) {
			continue
		}
		if info, err := os.Stat(arg); err == nil && info.Mode().IsRegular() {
			paths = append(paths, arg)
		}
	}
	return paths
}

// MCPCommandPaths returns the MCP server programs named in MCP config files
// (`{"mcpServers":{"x":{"command":"/abs/bin", "args":[…]}}}`), plus any
// absolute file arguments, so a confined CLI can still start its MCP
// servers — in production the AgentWorks bridge.
func MCPCommandPaths(configFiles []string) []string {
	var paths []string
	for _, file := range configFiles {
		data, err := os.ReadFile(file)
		if err != nil || len(data) > 1<<20 {
			continue
		}
		var config struct {
			MCPServers map[string]struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"mcpServers"`
		}
		if json.Unmarshal(data, &config) != nil {
			continue
		}
		for _, server := range config.MCPServers {
			if command := strings.TrimSpace(server.Command); command != "" {
				if !filepath.IsAbs(command) {
					if resolved, err := exec.LookPath(command); err == nil {
						command = resolved
					}
				}
				if filepath.IsAbs(command) {
					paths = append(paths, command)
					if real, err := filepath.EvalSymlinks(command); err == nil {
						paths = append(paths, real)
					}
				}
			}
			paths = append(paths, ArgFilePaths(server.Args)...)
		}
	}
	return paths
}

// JSONFilePaths returns absolute paths of existing files mentioned anywhere
// in JSON config files, including inside command strings such as
// `sh /tmp/helper.sh` or `python3 /tmp/hooks/x.py`.
func JSONFilePaths(configFiles []string) []string {
	var paths []string
	var walk func(any)
	walk = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			for _, item := range v {
				walk(item)
			}
		case []any:
			for _, item := range v {
				walk(item)
			}
		case string:
			paths = append(paths, ArgFilePaths(strings.Fields(v))...)
		}
	}
	for _, file := range configFiles {
		data, err := os.ReadFile(file)
		if err != nil || len(data) > 1<<20 {
			continue
		}
		var root any
		if json.Unmarshal(data, &root) == nil {
			walk(root)
		}
	}
	return paths
}

// LandlockCmd confines a structured-transport launch (exec.Cmd) the same way
// LandlockArgs confines a tmux launch. cmd.Env already carries the private
// home paths (MergeCodingAgentSecretEnvironment). It is a no-op when the
// policy does not confine the CLI on this host.
func LandlockCmd(policy *llmtypes.CLISecurityPolicy, cmd *exec.Cmd, workingDir string, runtimeReadPaths, runtimeWritePaths []string) (func(), error) {
	return LandlockCmdWithCredentials(policy, cmd, workingDir, runtimeReadPaths, runtimeWritePaths, nil)
}

// LandlockCmdWithCredentials is LandlockCmd with login files the adapter made
// for this launch (a key file): the CLI may read them, the shells it starts may
// not, exactly as for the account's own login files. Each must sit outside
// every folder the shells are granted.
func LandlockCmdWithCredentials(policy *llmtypes.CLISecurityPolicy, cmd *exec.Cmd, workingDir string, runtimeReadPaths, runtimeWritePaths, credentials []string) (func(), error) {
	if policy.SeatbeltEnforced() {
		return SeatbeltCmd(policy, cmd, workingDir, SeatbeltGrants{ReadPaths: runtimeReadPaths, WritePaths: runtimeWritePaths})
	}
	if !policy.LandlockEnforced() {
		return func() {}, nil
	}
	if cmd == nil || len(cmd.Args) == 0 {
		return func() {}, errors.New("coding CLI command is empty")
	}
	if workingDir == "" {
		workingDir = cmd.Dir
	}
	args := append([]string{cmd.Path}, cmd.Args[1:]...)
	wrapped, cleanup, err := landlockArgs(policy, args, workingDir, append(runtimeReadPaths, ArgFilePaths(args)...), runtimeWritePaths, cmd.Env, credentials)
	if err != nil {
		return func() {}, err
	}
	cmd.Path = wrapped[0]
	cmd.Args = wrapped
	if slotfs.IsSlotLaunch(policy.PrivateHome) {
		// The user's own Linux account runs the CLI; the request travels in a file so the CLI keeps
		// its standard input and output.
		unwrap, wrapErr := slotfs.WrapCmd(cmd, policy.PrivateHome, slotLaunchEnv(policy, cmd.Env))
		if wrapErr != nil {
			cleanup()
			return func() {}, wrapErr
		}
		inner := cleanup
		return func() { unwrap(); inner() }, nil
	}
	return cleanup, nil
}

// ChildLaunchEnv carries a ChildLaunch to the host process as JSON.
const ChildLaunchEnv = "AGENTWORKS_CLI_LAUNCH"

// ChildLaunch is how a trusted host process starts the confined CLI itself:
// Runner --config <file> -- EnvArgs... <cli> <args>, with Policy written to a
// fresh file in a folder only the host can write (never one a confined CLI is
// granted), which the launcher deletes when it reads it.
type ChildLaunch struct {
	Runner  string          `json:"runner"`
	EnvArgs []string        `json:"env_args"`
	Policy  json.RawMessage `json:"policy"`
}

// LandlockChild confines only the CLI that a trusted host process starts (the
// Claude Agent SDK bridge spawns Claude Code), not the host. The launcher's
// /proc/self belongs to the process it execs; a confined host cannot give a
// child its own /proc entry, and Claude (Bun) aborts at startup without it
// (PLAT-872-51). The launch goes to the host in ChildLaunchEnv; on a slot
// launch the host runs as the user's account and is given hostReads, and
// hostWrites for its private folder. It is
// a no-op when the policy does not enforce Landlock.
func LandlockChild(policy *llmtypes.CLISecurityPolicy, host *exec.Cmd, cliArgs []string, workingDir string, cliReads, cliWrites, hostReads, hostWrites []string) (func(), error) {
	if !policy.LandlockEnforced() {
		return func() {}, nil
	}
	wrapped, cleanup, err := LandlockArgs(policy, cliArgs, workingDir, append(cliReads, ArgFilePaths(cliArgs)...), cliWrites)
	if err != nil {
		return func() {}, err
	}
	handedOff := false
	defer func() {
		if !handedOff {
			cleanup()
		}
	}()
	if len(wrapped) < 5+len(cliArgs) || wrapped[1] != "--config" || wrapped[3] != "--" {
		return func() {}, errors.New("unexpected Landlock launch shape")
	}
	raw, err := os.ReadFile(wrapped[2])
	if err != nil {
		return func() {}, fmt.Errorf("read CLI Landlock policy: %w", err)
	}
	launch, err := json.Marshal(ChildLaunch{Runner: wrapped[0], EnvArgs: wrapped[4 : len(wrapped)-len(cliArgs)], Policy: raw})
	if err != nil {
		return func() {}, err
	}
	// The bridge receives the policy bytes, not this staging file. Keep the
	// refresh observer until the bridge exits, rather than closing it here.
	_ = os.Remove(wrapped[2])
	host.Env = append(host.Env, ChildLaunchEnv+"="+string(launch))
	if !slotfs.IsSlotLaunch(policy.PrivateHome) {
		handedOff = true
		return cleanup, nil
	}
	slot, _ := slotfs.SlotOf(canonical(policy.PrivateHome))
	for _, path := range hostReads {
		if err := slotfs.GrantRuntime(slot, path, false); err != nil {
			return func() {}, fmt.Errorf("give the user's account launch files: %w", err)
		}
	}
	for _, path := range hostWrites {
		if err := slotfs.GrantRuntime(slot, path, true); err != nil {
			return func() {}, fmt.Errorf("give the user's account launch files: %w", err)
		}
	}
	// The bridge runs unconfined as the slot: nothing from its environment may make node load other code
	// (PLAT-872-84: a slot-writable --require would run with the CLI's login).
	env := host.Env[:0:0]
	for _, entry := range host.Env {
		if !strings.HasPrefix(entry, "NODE_OPTIONS=") && !strings.HasPrefix(entry, "NODE_PATH=") {
			env = append(env, entry)
		}
	}
	host.Env = slotLaunchEnv(policy, env)
	unwrap, err := slotfs.WrapCmd(host, policy.PrivateHome, host.Env)
	if err != nil {
		return func() {}, err
	}
	handedOff = true
	return func() { unwrap(); cleanup() }, nil
}

// slotLaunchEnv is what a launch run as a person's slot starts with: no secret the server holds for itself
// (WORKSPACE_API_TOKEN, the bridge secret, the global secrets) and no login but this CLI's own. The launcher
// removed them before it started the CLI, but itself kept them, as the slot, for its whole run; a process of
// the slot's uid can read another's environment (PLAT-872-86, found on a live server).
func slotLaunchEnv(policy *llmtypes.CLISecurityPolicy, env []string) []string {
	own := map[string]bool{}
	names := policy.OwnLoginEnv
	if len(names) == 0 {
		names = ownLoginEnv[strings.TrimSpace(policy.Provider)]
	}
	for _, name := range names {
		own[name] = true
	}
	login := map[string]bool{}
	for _, name := range cliLoginEnv {
		login[name] = true
	}
	out := env[:0:0]
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if llmtypes.IsServerOwnedSecretKey(name) || llmtypes.IsPlatformSecretEnvName(name) || (!own[name] && (login[name] || isLoginEnvName(name))) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// isLoginEnvName matches the launcher's rule for a login-like variable (workspace security.isLoginEnvName).
func isLoginEnvName(name string) bool {
	if strings.HasPrefix(name, "SECRET_") {
		return false
	}
	for _, suffix := range []string{"_API_KEY", "_AUTH_TOKEN", "_OAUTH_TOKEN"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// ConfigDirFiles lists the JSON files directly inside dir (e.g. a working
// directory's .cursor/ or .claude/), for grants derived from project configs.
func ConfigDirFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	return files
}

// appendGitConfig returns the env assignments that add key/value pairs to git's environment configuration after the
// entries env already has: KEY_n/VALUE_n from the current GIT_CONFIG_COUNT, and the new count. Every layer appends;
// none replaces another's settings (the sandbox's hooks and fsmonitor protection, a connected GitHub account's
// credential helper).
func appendGitConfig(env []string, pairs ...string) []string {
	count := 0
	for _, kv := range env {
		if value, ok := strings.CutPrefix(kv, "GIT_CONFIG_COUNT="); ok {
			if n, err := strconv.Atoi(value); err == nil && n > 0 {
				count = n
			}
		}
	}
	out := make([]string, 0, len(pairs)+1)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, "GIT_CONFIG_KEY_"+strconv.Itoa(count)+"="+pairs[i], "GIT_CONFIG_VALUE_"+strconv.Itoa(count)+"="+pairs[i+1])
		count++
	}
	return append(out, "GIT_CONFIG_COUNT="+strconv.Itoa(count))
}

// canonicalThroughExisting resolves a path that may not exist yet through its
// nearest existing folder, so a link above it (/var on a Mac) is followed.
func canonicalThroughExisting(path string) string {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "" || clean == "." {
		return ""
	}
	rest := ""
	for dir := clean; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(dir); err == nil {
			if resolved, err := filepath.EvalSymlinks(dir); err == nil {
				return filepath.Join(resolved, rest)
			}
			return filepath.Join(dir, rest)
		}
		if parent := filepath.Dir(dir); parent == dir {
			return clean
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}
