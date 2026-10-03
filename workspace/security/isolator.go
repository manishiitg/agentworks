package security

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
	"github.com/manishiitg/coding-agent-loop/workspace/gogconfig"
)

type Isolator struct {
	// Slot, when set (Linux only), runs the command as that slot account through sudo and
	// slotctl instead of as the service account (see the slots package).
	Slot string
	// ExtraEnv is the per-call environment the platform hands a command (session token, secrets, workflow
	// variables, step paths). For a slot it must be in the request that is written when the command is wrapped:
	// a command run as another account only sees what that request carries, so values appended to the command's
	// environment afterwards never reach it (Confida 2026-10-01: 401 from the tools gateway, no secrets, no
	// variables in a slotted workflow's shell). Callers pass only keys they have already filtered.
	ExtraEnv     map[string]string
	ReadPaths    []string
	WritePaths   []string
	BlockedPaths []string // Paths to explicitly deny READ AND WRITE (deny-list, takes precedence)
	// BlockedWritePaths denies writes only — reads still pass through. Used for paths
	// the agent should inspect but never modify (e.g. a workflow's planning/ folder).
	// macOS: emitted as `(deny file-write*)` scoped to these subpaths.
	// Linux: bind-mounted read-only over the writable subtree so writes return EROFS.
	BlockedWritePaths []string
	WorkDir           string
	BaseDir           string // Workspace base directory (default: /app/workspace-docs)
	// StrictAllowlist flips the sandbox profile from "allow everything except
	// one denied directory" (the default below — a reasonable model for
	// AgentWorks' own workflows, which trust the rest of the host since it's
	// the same developer's machine) to a genuine deny-by-default allow-list:
	// only ReadPaths/WritePaths and the minimal system paths needed to exec a
	// shell are visible. Use this for untrusted callers (e.g. a child's own
	// chat) where the real home directory, credentials, and everything else
	// on the machine must be invisible, not just the app's own project root.
	// Opt-in and additive: every existing caller that leaves this false gets
	// byte-for-byte the same profile as before.
	StrictAllowlist bool
	// AllowNetwork opts a StrictAllowlist caller into outbound network access.
	// macOS's sandbox-exec does NOT support scoping this to specific hosts —
	// (remote ip "some-host:443") is rejected outright ("host must be * or
	// localhost in network address"), confirmed by direct testing — so this
	// is all-or-nothing. It is a real, accepted tradeoff for whichever caller
	// sets it: every OTHER secret that caller's shell has access to via env
	// could in principle be sent to ANY host now, not just wherever the
	// caller intends. Leave false (default) for callers — like a child's own
	// shell — that should stay fully network-denied.
	AllowNetwork bool
	// BrowserSession is the managed browser this command's owner (workflow,
	// Crew, Code project) uses. When set, the command gets only that
	// browser's socket folder and profile (see scopeBrowser); when empty it
	// keeps the shared browser grants.
	BrowserSession string
	// AllowPTY gives the command its own terminal devices (a private /dev/pts, see LandlockPolicy.PrivatePTS), which
	// an interactive shell needs. Never set for ordinary commands.
	AllowPTY bool
	// Interactive keeps the caller's terminal as the command's standard input when it runs as a slot: the request
	// then travels in a file in the slot's run folder instead of on stdin (slots.WrapCommandFile).
	Interactive bool
}

const defaultBaseDir = "/app/workspace-docs"

//go:embed runtime/agentworks_browser.py
var browserRuntime []byte

// getBaseDir returns the configured base directory or the default
func (iso *Isolator) getBaseDir() string {
	if iso.BaseDir != "" {
		return iso.BaseDir
	}
	return defaultBaseDir
}

// canonicalPath returns the kernel-visible spelling of a path. macOS exposes
// /var and /tmp through symlinks to /private; sandbox-exec rules written with
// the unresolved spelling do not protect the resolved path.
func canonicalPath(path string) string {
	if path == "" {
		return ""
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		return resolved
	}

	// Write targets may not exist yet. Resolve the nearest existing ancestor,
	// then append the missing suffix without changing its meaning.
	ancestor := absPath
	var suffix []string
	for {
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return filepath.Clean(absPath)
		}
		suffix = append([]string{filepath.Base(ancestor)}, suffix...)
		ancestor = parent
		if resolved, err := filepath.EvalSymlinks(ancestor); err == nil {
			parts := append([]string{resolved}, suffix...)
			return filepath.Join(parts...)
		}
	}
}

func sandboxQuoted(path string) string {
	path = strings.ReplaceAll(path, `\`, `\\`)
	return strings.ReplaceAll(path, `"`, `\"`)
}

func (iso *Isolator) sandboxPath(path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(iso.getBaseDir(), strings.TrimSuffix(path, "/"))
	}
	return canonicalPath(path)
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// sandboxAllowedPath resolves an allow-list entry without letting a path that
// was granted inside the workspace escape through a symlink. Absolute paths
// outside BaseDir remain valid because callers use them for explicit host
// grants such as Downloads access.
func (iso *Isolator) sandboxAllowedPath(path string) (string, bool) {
	baseDir := filepath.Clean(iso.getBaseDir())
	canonicalBaseDir := canonicalPath(baseDir)
	workspaceScoped := !filepath.IsAbs(path)

	resolvedPath := path
	if workspaceScoped {
		resolvedPath = filepath.Join(baseDir, strings.TrimSuffix(path, "/"))
	} else {
		resolvedPath = filepath.Clean(path)
		workspaceScoped = pathWithin(resolvedPath, baseDir) || pathWithin(resolvedPath, canonicalBaseDir)
	}

	canonicalAllowedPath := canonicalPath(resolvedPath)
	if workspaceScoped && !pathWithin(canonicalAllowedPath, canonicalBaseDir) {
		return "", false
	}
	return canonicalAllowedPath, true
}

// hostGogRestricted keeps the service account's Google CLI store out of user
// slots. Its private files cannot be opened after the identity switch, and the
// shared store/keyring must not cross that boundary even if permissions allow it.
// Local trusted shells retain their existing direct CLI access.
func (iso *Isolator) hostGogRestricted() bool {
	return iso.StrictAllowlist || iso.Slot != ""
}

// ExecuteIsolated runs a command with filesystem restrictions.
// Linux selects a verified kernel backend (Landlock first, then a proven mount
// namespace); macOS uses sandbox-exec.
// Returns the command, a cleanup function, and an error
func (iso *Isolator) ExecuteIsolated(ctx context.Context, command string, args []string) (*exec.Cmd, func(), error) {
	// Keep socket paths short even when the workflow working directory is deep.
	// Clone the policy: concurrent commands must never share or mutate grants.
	tmp, releaseScratch, err := allocateScratch()
	if err != nil {
		return nil, nil, fmt.Errorf("allocate sandbox scratch: %w", err)
	}
	local := *iso
	if err := os.WriteFile(filepath.Join(tmp, "agentworks_browser.py"), browserRuntime, 0600); err != nil {
		releaseScratch()
		return nil, nil, fmt.Errorf("prepare browser helper: %w", err)
	}
	local.WritePaths = append(append([]string{}, iso.WritePaths...), canonicalPath(tmp))
	browserSocket := local.scopeBrowser()
	if home := gogconfig.TerminalHome(iso.hostGogRestricted()); home != "" {
		if err := os.MkdirAll(home, 0700); err != nil {
			releaseScratch()
			return nil, nil, fmt.Errorf("prepare GOG_HOME: %w", err)
		}
		local.WritePaths = append(local.WritePaths, canonicalPath(home))
		local.ReadPaths = append(append([]string{}, iso.ReadPaths...), canonicalPath(home))
	}
	var cmd *exec.Cmd
	var backendCleanup func()
	if runtime.GOOS == "darwin" {
		cmd, backendCleanup, err = local.executeIsolatedMacOS(ctx, command, args)
	} else {
		cmd, backendCleanup, err = local.executeIsolatedLinux(ctx, command, args)
	}
	cleanup := func() {
		if backendCleanup != nil {
			backendCleanup()
		}
		releaseScratch()
	}
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	// Chrome outlives this CLI invocation. Its IPC files must survive the
	// per-command scratch cleanup, within this browser's private socket scope.
	runtimeTmp := tmp
	if browserSocket != "" && (command == "agent-browser" || strings.HasPrefix(strings.TrimSpace(command), "agent-browser ")) {
		runtimeTmp = filepath.Join(browserSocket, "tmp")
		if err := os.MkdirAll(runtimeTmp, 0700); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("prepare browser temporary directory: %w", err)
		}
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		filtered := cmd.Env[:0]
		for _, value := range cmd.Env {
			if !strings.HasPrefix(value, key+"=") {
				filtered = append(filtered, value)
			}
		}
		cmd.Env = append(filtered, key+"="+runtimeTmp)
	}
	// No workflow or Crew folder to keep a home in: this command's own
	// scratch, never the shared /tmp.
	cmd.Env = privateSandboxHome(cmd.Env, filepath.Join(runtimeTmp, "home"))
	if browserSocket != "" {
		cmd.Env = replaceEnv(cmd.Env, "AGENT_BROWSER_SOCKET_DIR", browserSocket)
	}
	pythonPath := tmp
	filtered := cmd.Env[:0]
	for _, value := range cmd.Env {
		if strings.HasPrefix(value, "PYTHONPATH=") {
			pythonPath += string(os.PathListSeparator) + strings.TrimPrefix(value, "PYTHONPATH=")
		} else {
			filtered = append(filtered, value)
		}
	}
	cmd.Env = append(filtered, "PYTHONPATH="+pythonPath)
	return cmd, cleanup, nil
}

// executeIsolatedLinux uses unshare for filesystem isolation on Linux
func (iso *Isolator) executeIsolatedLinux(ctx context.Context, command string, args []string) (*exec.Cmd, func(), error) {
	return iso.executeIsolatedLinuxPlatform(ctx, command, args)
}

// executeIsolatedMountNamespace is the legacy Linux mount-namespace backend.
// Callers must prove that unshare is permitted for the service identity before
// selecting it; invoking it optimistically is PLAT-118's root cause.
func (iso *Isolator) executeIsolatedMountNamespace(ctx context.Context, command string, args []string) (*exec.Cmd, func(), error) {
	// Generate mount script for isolation
	mountScript := iso.generateMountScript(command, args)

	// Create unique temp script file (PID + timestamp to avoid collisions)
	scriptPath := filepath.Join("/tmp", fmt.Sprintf("exec-%d-%d.sh", os.Getpid(), time.Now().UnixNano()))

	// Write script with error handling
	if err := os.WriteFile(scriptPath, []byte(mountScript), 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to write mount script: %w", err)
	}

	// Cleanup function to remove script after execution
	cleanup := func() {
		os.Remove(scriptPath)
	}

	// Execute using unshare (creates new mount + user namespace).
	// --mount: mount namespace, containing the bind mounts/tmpfs the script sets up.
	// --user --map-root-user: enters a new user namespace mapped back to the
	//   caller's own UID -- the standard unprivileged-mount-namespace pattern
	//   (the same one rootless container runtimes use). Required: a plain
	//   "-m" needs CAP_SYS_ADMIN in the CURRENT user namespace, which an
	//   unprivileged service account never has, so mount/bind-mount inside
	//   the script (both requiring CAP_SYS_ADMIN) would fail outright without
	//   it -- this mirrors mountNamespaceAvailable()'s probe, which must test
	//   the exact same privilege shape this actually uses.
	// --propagation private: don't propagate mounts to parent namespace.
	cmd := exec.CommandContext(ctx, "unshare", "--mount", "--user", "--map-root-user", "--propagation", "private", "sh", scriptPath)

	// Set working directory for proper error messages
	cmd.Dir = iso.WorkDir

	// CRITICAL: Set safe environment (no secrets), with package-manager state
	// routed to the workflow's persistent sandbox folder (PLAT-284).
	cmd.Env = iso.toolEnv(gogconfig.Environment(BuildSafeEnvironment(), iso.hostGogRestricted()))

	return cmd, cleanup, nil
}

// executeIsolatedMacOS uses sandbox-exec for filesystem isolation on macOS
func (iso *Isolator) executeIsolatedMacOS(ctx context.Context, command string, args []string) (*exec.Cmd, func(), error) {
	// Generate sandbox profile
	profile := iso.generateSandboxProfile()

	// Create unique temp profile file
	profilePath := filepath.Join("/tmp", fmt.Sprintf("sandbox-%d-%d.sb", os.Getpid(), time.Now().UnixNano()))

	// Write profile with error handling
	if err := os.WriteFile(profilePath, []byte(profile), 0644); err != nil {
		return nil, nil, fmt.Errorf("failed to write sandbox profile: %w", err)
	}

	// Cleanup function to remove profile after execution
	cleanup := func() {
		os.Remove(profilePath)
	}

	// Build full command
	fullCmd := command
	if len(args) > 0 {
		fullCmd = fmt.Sprintf("%s %s", command, strings.Join(args, " "))
	}

	// Execute using sandbox-exec
	cmd := exec.CommandContext(ctx, "sandbox-exec", "-f", profilePath, "sh", "-c", fullCmd)

	// Set working directory
	cmd.Dir = iso.WorkDir

	// CRITICAL: Set safe environment (no secrets). The default macOS profile
	// leaves $HOME writable, so installs never failed here the way they did
	// under Landlock -- but route them identically so a workflow behaves the
	// same on a Mac as on a Linux deployment (PLAT-284).
	cmd.Env = iso.toolEnv(gogconfig.Environment(BuildSafeEnvironment(), iso.hostGogRestricted()))

	return cmd, cleanup, nil
}

// generateSandboxProfile creates a macOS sandbox profile for filesystem isolation
func (iso *Isolator) generateSandboxProfile() string {
	if iso.StrictAllowlist {
		return iso.generateStrictSandboxProfile()
	}

	var sb strings.Builder

	sb.WriteString("(version 1)\n")
	sb.WriteString("(allow default)\n\n")

	baseDir := canonicalPath(iso.getBaseDir())

	// Mode 1: BlockedPaths only (deny-list mode for chat)
	if len(iso.BlockedPaths) > 0 && len(iso.ReadPaths) == 0 && len(iso.WritePaths) == 0 {
		sb.WriteString("; Deny-list mode: block specific paths\n")
		sb.WriteString("(deny file-read* file-write*\n")
		for _, path := range iso.BlockedPaths {
			fullPath := iso.sandboxPath(path)
			sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", sandboxQuoted(fullPath)))
		}
		sb.WriteString(")\n\n")

		return sb.String()
	}

	// Mode 2: Allow-list mode (workflow mode with ReadPaths/WritePaths)
	//
	// Strategy: deny the project root directory (source code, server configs,
	// .env files) then re-allow only workspace-docs subpaths within it.
	// Everything else (home dir tool configs, system paths) stays accessible
	// so CLIs like aws, gcloud, kubectl, docker etc. work normally.
	projectRoot := canonicalPath(filepath.Dir(baseDir))
	sb.WriteString("; Deny project root (source code, server configs, .env)\n")
	sb.WriteString(fmt.Sprintf("(deny file-read* file-write* (subpath \"%s\"))\n\n", sandboxQuoted(projectRoot)))

	// Homebrew SQLite resolves an absolute database path by reading metadata for
	// every ancestor, including projectRoot. Without this literal metadata-only
	// grant, sqlite3_open() returns EPERM even when the database itself is inside
	// an allowed path. This does not permit listing or reading projectRoot files.
	sb.WriteString("; Allow project root metadata for canonical path resolution\n")
	sb.WriteString(fmt.Sprintf("(allow file-read-metadata (literal \"%s\"))\n\n", sandboxQuoted(projectRoot)))

	// getcwd() needs real read access (not just metadata/stat) on the working
	// directory and each ancestor up to baseDir — verified empirically: with a
	// stripped-down environment (no inherited $PWD, e.g. BuildSafeEnvironment),
	// the shell's own startup getcwd() call fails under metadata-only grants
	// with "cannot access parent directories: Operation not permitted", even
	// though the directory is otherwise fully accessible. (literal ...), not
	// (subpath ...), so this still only grants each named directory's own
	// listing/stat — never its subtree — and does not bypass the allow-list.
	workDir := canonicalPath(iso.WorkDir)
	if workDir != "" {
		sb.WriteString("; Allow working directory read access for getcwd\n")
		written := make(map[string]bool)
		writeLiteralRead := func(path string) {
			if path == "" || written[path] {
				return
			}
			written[path] = true
			sb.WriteString(fmt.Sprintf("(allow file-read* (literal \"%s\"))\n", sandboxQuoted(path)))
		}
		writeLiteralRead(workDir)
		for dir := filepath.Dir(workDir); strings.HasPrefix(dir, baseDir+string(filepath.Separator)); dir = filepath.Dir(dir) {
			writeLiteralRead(dir)
		}
		writeLiteralRead(baseDir)
		sb.WriteString("\n")
	}

	// Allow read-only access to configured read paths (within workspace-docs)
	if len(iso.ReadPaths) > 0 {
		sb.WriteString("; Allowed read paths\n")
		sb.WriteString("(allow file-read*\n")
		for _, path := range iso.ReadPaths {
			fullPath, ok := iso.sandboxAllowedPath(path)
			if !ok {
				continue
			}
			// A sandbox `subpath` rule covers descendants but not the directory
			// node itself. Grant the already-authorized root literally as well so
			// callers can list it and use it as the parent of an allowed file.
			sb.WriteString(fmt.Sprintf("  (literal \"%s\")\n", sandboxQuoted(fullPath)))
			sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", sandboxQuoted(fullPath)))
		}
		sb.WriteString(")\n\n")
	}

	// Allow read+write access to configured write paths
	if len(iso.WritePaths) > 0 {
		sb.WriteString("; Allowed write paths\n")
		sb.WriteString("(allow file-read* file-write*\n")
		for _, path := range iso.WritePaths {
			fullPath, ok := iso.sandboxAllowedPath(path)
			if !ok {
				continue
			}
			sb.WriteString(fmt.Sprintf("  (literal \"%s\")\n", sandboxQuoted(fullPath)))
			sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", sandboxQuoted(fullPath)))
		}
		sb.WriteString(")\n\n")
	}

	// Explicit deny for blocked paths (takes precedence, added last)
	if len(iso.BlockedPaths) > 0 {
		sb.WriteString("; Explicit deny for blocked paths (overrides allows)\n")
		sb.WriteString("(deny file-read* file-write*\n")
		for _, path := range iso.BlockedPaths {
			fullPath := iso.sandboxPath(path)
			sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", sandboxQuoted(fullPath)))
		}
		sb.WriteString(")\n")
	}

	// Explicit deny for write-only blocked paths — reads pass through, writes denied.
	// Used for paths agents must be able to inspect but not modify (planning/, etc.).
	if len(iso.BlockedWritePaths) > 0 {
		sb.WriteString("; Explicit deny for write-only blocked paths (reads allowed)\n")
		sb.WriteString("(deny file-write*\n")
		for _, path := range iso.BlockedWritePaths {
			fullPath := iso.sandboxPath(path)
			sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", sandboxQuoted(fullPath)))
		}
		sb.WriteString(")\n")
	}

	return sb.String()
}

// strictSystemReadPaths are read-only, non-personal system paths a shell needs
// to exec ordinary POSIX tools (the shell binary itself, dyld, ls/cat/mkdir,
// locale data). None of these hold user/family data — everything that does
// (the real home directory, credentials, browser profiles, and so on) is
// simply absent from this list, which is the point of deny-by-default: unlike
// generateSandboxProfile's default mode, there is no implicit "allow" for
// anything not named here.
var strictSystemReadPaths = []string{
	"/usr", "/bin", "/sbin", "/System", "/Library", "/opt",
	"/private/etc", "/private/var/db", "/private/var/select",
}

// strictSystemDevices are device nodes ordinary shell commands rely on.
var strictSystemDevices = []string{
	"/dev/null", "/dev/zero", "/dev/urandom", "/dev/random", "/dev/tty",
}

// generateStrictSandboxProfile builds a genuine deny-by-default macOS sandbox
// profile (StrictAllowlist mode): only ReadPaths/WritePaths and the minimal
// system paths above are visible. Everything else on the machine — the real
// home directory, SSH/cloud credentials, browser data, anything outside the
// allow-list — is invisible, unlike generateSandboxProfile's default mode,
// which only denies the app's own project root and allows everything else.
func (iso *Isolator) generateStrictSandboxProfile() string {
	var sb strings.Builder
	sb.WriteString("(version 1)\n")
	sb.WriteString("(deny default)\n\n")

	sb.WriteString("; Minimal process/IPC permissions so the shell can run at all\n")
	sb.WriteString("(allow process-exec*)\n")
	sb.WriteString("(allow process-fork)\n")
	sb.WriteString("(allow signal (target self))\n")
	sb.WriteString("(allow sysctl-read)\n")
	sb.WriteString("(allow mach-lookup)\n")
	sb.WriteString("(allow iokit-open)\n\n")

	if iso.AllowNetwork {
		// Outbound network, opted into explicitly (see AllowNetwork's own doc
		// comment for the tradeoff this accepts). Getting actual hostname
		// resolution working here took real trial and error: (allow
		// network*) alone connects fine to a raw IP (confirmed via curl
		// --resolve) but getaddrinfo()-based resolution still silently failed
		// with NO logged sandbox denial — `log show --predicate 'eventMessage
		// contains "deny"'` showed nothing network-related at all. The actual
		// missing pieces, found by removing restrictions one at a time until
		// resolution worked: the mach-lookup for opendirectoryd (glibc-style
		// resolution goes through it) IS covered by the unscoped (allow
		// mach-lookup) above, but file-read-metadata on "/", "/var", "/etc"
		// plus broader /var subpath read access were still being silently
		// denied and were load-bearing for the resolver to actually get a
		// response back. Do not narrow this without re-testing a real `curl
		// https://<host>` end to end — a change that "looks sufficient" by
		// reading Apple's sandbox docs was insufficient twice before this.
		sb.WriteString("; Outbound network (opted in) + what hostname resolution actually needs — see AllowNetwork's doc comment\n")
		sb.WriteString("(allow network*)\n")
		sb.WriteString("(allow ipc-posix-shm-read-data)\n")
		sb.WriteString("(allow ipc-posix-shm)\n")
		sb.WriteString("(allow file-read-metadata (literal \"/\") (literal \"/var\") (literal \"/etc\"))\n")
		sb.WriteString("(allow file-read* (subpath \"/var\"))\n\n")
	}

	// The shell itself (and ordinary tools resolving relative/absolute paths)
	// needs to list "/"'s own top-level entries — standard macOS directory
	// names (Users, System, bin, etc.), not their contents. Without this,
	// sandbox-exec's kernel-level denial of "file-read-data /" aborts the
	// process outright rather than failing the specific command cleanly.
	sb.WriteString("; Root directory's own listing (top-level names only, not descendants)\n")
	sb.WriteString("(allow file-read-data (literal \"/\"))\n\n")

	sb.WriteString("; Read-only system paths needed for ordinary shell tools to run — no user data here\n")
	sb.WriteString("(allow file-read*\n")
	for _, p := range strictSystemReadPaths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", sandboxQuoted(canonicalPath(p))))
	}
	sb.WriteString(")\n\n")

	sb.WriteString("; Device nodes ordinary shell commands rely on\n")
	sb.WriteString("(allow file-read* file-write-data\n")
	for _, p := range strictSystemDevices {
		sb.WriteString(fmt.Sprintf("  (literal \"%s\")\n", sandboxQuoted(p)))
	}
	sb.WriteString(")\n\n")

	if iso.AllowPTY {
		// An interactive shell (Code's terminal) makes its own pseudo-terminals: tmux's server opens /dev/ptmx and a
		// /dev/ttysNNN, and exits at once without them ("error connecting to ... tmux.sock (Operation not permitted)").
		sb.WriteString("; Pseudo-terminals for an interactive shell (only when one is asked for)\n")
		sb.WriteString("(allow pseudo-tty)\n")
		sb.WriteString("(allow file-read* file-write* file-ioctl (literal \"/dev/ptmx\") (regex #\"^/dev/ttys[0-9]+$\"))\n\n")
	}

	sb.WriteString("; Scratch space for ordinary temp files (compiler/interpreter caches, etc.)\n")
	sb.WriteString("(allow file-read* file-write*\n")
	sb.WriteString("  (subpath \"/private/tmp\")\n")
	sb.WriteString("  (subpath \"/private/var/folders\")\n")
	sb.WriteString(")\n\n")

	workDir := canonicalPath(iso.WorkDir)
	if workDir != "" {
		// Deliberately still metadata-only here, unlike the default profile's
		// equivalent grant: (literal dir) + file-read* turns out to also permit
		// *listing* that directory's entries (verified empirically), and this
		// chain runs all the way to "/" — upgrading it would expose the real
		// filenames in the real home directory and above, which is exactly what
		// StrictAllowlist exists to prevent. So getcwd() stays cosmetically
		// broken here (a stat-only warning, not a functional block — real
		// commands against allowed paths still work) in exchange for genuinely
		// not leaking anything outside the allow-list.
		sb.WriteString("; Directory metadata along the path to WorkDir, needed for getcwd()\n")
		for dir := workDir; ; dir = filepath.Dir(dir) {
			sb.WriteString(fmt.Sprintf("(allow file-read-metadata (literal \"%s\"))\n", sandboxQuoted(dir)))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
			sb.WriteString(fmt.Sprintf("(allow file-read-metadata (literal \"%s\"))\n", sandboxQuoted(dir)))
			if dir == string(filepath.Separator) {
				break
			}
		}
		sb.WriteString("\n")
	}

	if len(iso.ReadPaths) > 0 {
		sb.WriteString("; Allowed read paths\n")
		sb.WriteString("(allow file-read*\n")
		for _, path := range iso.ReadPaths {
			fullPath, ok := iso.sandboxAllowedPath(path)
			if !ok {
				continue
			}
			sb.WriteString(fmt.Sprintf("  (literal \"%s\")\n", sandboxQuoted(fullPath)))
			sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", sandboxQuoted(fullPath)))
		}
		sb.WriteString(")\n\n")
	}

	if len(iso.WritePaths) > 0 {
		sb.WriteString("; Allowed write paths\n")
		sb.WriteString("(allow file-read* file-write*\n")
		for _, path := range iso.WritePaths {
			fullPath, ok := iso.sandboxAllowedPath(path)
			if !ok {
				continue
			}
			sb.WriteString(fmt.Sprintf("  (literal \"%s\")\n", sandboxQuoted(fullPath)))
			sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", sandboxQuoted(fullPath)))
		}
		sb.WriteString(")\n\n")
	}

	if len(iso.BlockedPaths) > 0 {
		sb.WriteString("; Explicit deny for blocked paths (overrides allows)\n")
		sb.WriteString("(deny file-read* file-write*\n")
		for _, path := range iso.BlockedPaths {
			fullPath := iso.sandboxPath(path)
			sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", sandboxQuoted(fullPath)))
		}
		sb.WriteString(")\n")
	}

	if len(iso.BlockedWritePaths) > 0 {
		sb.WriteString("; Explicit deny for write-only blocked paths (reads allowed)\n")
		sb.WriteString("(deny file-write*\n")
		for _, path := range iso.BlockedWritePaths {
			fullPath := iso.sandboxPath(path)
			sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", sandboxQuoted(fullPath)))
		}
		sb.WriteString(")\n")
	}

	return sb.String()
}

// resolveTempAndAbsPath computes the two locations generateMountScript's
// per-path steps need: where a configured path lives inside the preserved
// original-workspace copy (tempPath), and where it must end up in the
// tmpfs-hidden workspace (absPath).
func resolveTempAndAbsPath(path, baseDir, tempDir string) (tempPath, absPath string) {
	relPath := strings.TrimPrefix(path, baseDir+"/")
	if relPath == path && !strings.HasPrefix(path, "/") {
		relPath = path
	}
	tempPath = filepath.Join(tempDir, relPath)
	absPath = path
	if !strings.HasPrefix(path, "/") {
		absPath = filepath.Join(baseDir, path)
	}
	return tempPath, absPath
}

// writeCreatePlaceholder emits the shell lines that create an empty node at
// absPath matching tempPath's type (directory or plain file), without
// mounting anything yet.
//
// Every ReadPaths/WritePaths/BlockedWritePaths entry was previously assumed
// to be a directory unconditionally, both in whether a placeholder was
// created at all in this shape and in when: the old code created each
// path's placeholder and bind-mounted it in the same per-path step,
// interleaved with every other path's bind-mount. `mkdir -p "$absPath"`
// always creates a directory node (absPath never pre-exists, since step 3
// already tmpfs-hid the whole baseDir), and bind-mounting a *file* source
// onto that freshly created *directory* target fails at the kernel level
// with exactly `mount(2) system call failed: Not a directory`. Combined
// with `set -e`, this aborted the whole script -- every execute_shell_command
// call failed unconditionally, regardless of the command actually
// requested, whenever any configured path in scope happened to be a plain
// file rather than a directory. Confirmed live on the Dominion deployment
// 2026-08-31: Workflow/tectonicusadaytrading/schedule-runs.json (a real
// file) was the first one hit, but the bug was general, not specific to
// that one file -- any file-shaped read/write/blocked-write path would
// have hit it. The primary Landlock backend (landlock_runner_linux.go)
// already stats each path and handles this correctly; only this
// mount-namespace fallback (used when Landlock itself is unavailable) had
// the gap.
//
// Fixing only the directory-vs-file branch surfaced a second, narrower
// ordering bug: if a file's own parent directory was ALSO independently
// listed (e.g. the workflow root in ReadPaths, with one of its files
// listed again more specifically) and got read-only bind-mounted first,
// creating the file's own placeholder afterward failed with "Read-only
// file system" -- the touch happened after its parent was already
// read-only. All placeholders for every ReadPaths/WritePaths/BlockedWritePaths
// entry are therefore created in one pass (this function, called for every
// path up front) strictly before any bind-mount runs, while the whole tree
// is still the fresh, fully-writable tmpfs from step 3.
func writeCreatePlaceholder(sb *strings.Builder, tempPath, absPath string) {
	sb.WriteString(fmt.Sprintf("if [ -d \"%s\" ]; then\n", tempPath))
	sb.WriteString(fmt.Sprintf("  mkdir -p \"%s\"\n", absPath))
	sb.WriteString(fmt.Sprintf("elif [ -e \"%s\" ]; then\n", tempPath))
	sb.WriteString(fmt.Sprintf("  mkdir -p \"$(dirname \"%s\")\"\n", absPath))
	sb.WriteString(fmt.Sprintf("  : > \"%s\"\n", absPath))
	sb.WriteString("fi\n")
}

// writeBindMountOnly emits the shell line that bind-mounts tempPath onto
// absPath. Assumes writeCreatePlaceholder has already been run for this
// path (and every other configured path) earlier in the script, so no
// mkdir/touch happens here -- see writeCreatePlaceholder's comment for why
// placeholder creation and mounting must not be interleaved per-path.
func writeBindMountOnly(sb *strings.Builder, tempPath, absPath string, readOnly bool) {
	roFlag := ""
	if readOnly {
		roFlag = "-o ro "
	}
	sb.WriteString(fmt.Sprintf("if [ -e \"%s\" ]; then\n", tempPath))
	sb.WriteString(fmt.Sprintf("  mount --bind %s\"%s\" \"%s\"\n", roFlag, tempPath, absPath))
	sb.WriteString("fi\n")
}

// generateMountScript creates a shell script for filesystem isolation
// Strategy: Bind workspace to temp, hide with tmpfs, then selectively expose allowed paths
func (iso *Isolator) generateMountScript(command string, args []string) string {
	var sb strings.Builder

	sb.WriteString("#!/bin/sh\n")
	sb.WriteString("set -e\n\n")

	baseDir := iso.getBaseDir()

	// Mode 1: BlockedPaths only (deny-list mode for chat)
	// Hide blocked paths with tmpfs.
	if len(iso.BlockedPaths) > 0 && len(iso.ReadPaths) == 0 && len(iso.WritePaths) == 0 {
		sb.WriteString("# Deny-list mode: hide specific blocked paths\n")

		// Hide blocked paths with tmpfs
		for _, path := range iso.BlockedPaths {
			fullPath := path
			if !strings.HasPrefix(path, "/") {
				fullPath = filepath.Join(baseDir, strings.TrimSuffix(path, "/"))
			}
			sb.WriteString(fmt.Sprintf("mount -t tmpfs tmpfs \"%s\" 2>/dev/null || true\n", fullPath))
		}
		sb.WriteString("\n")

		// Change to working directory
		sb.WriteString(fmt.Sprintf("cd \"%s\"\n", iso.WorkDir))

		// Build and execute command
		fullCmd := command
		if len(args) > 0 {
			fullCmd = fmt.Sprintf("%s %s", command, strings.Join(args, " "))
		}

		// Escape single quotes in command for shell execution
		escapedCmd := strings.ReplaceAll(fullCmd, "'", "'\\''")
		sb.WriteString(fmt.Sprintf("exec sh -c '%s'\n", escapedCmd))

		return sb.String()
	}

	// Mode 2: Allow-list mode (workflow mode with ReadPaths/WritePaths)
	tempDir := "/tmp/workspace-original-$$"

	// Step 1: Create temp directory and bind mount workspace there (preserve original)
	sb.WriteString("# Preserve original workspace in temp location\n")
	sb.WriteString(fmt.Sprintf("mkdir -p \"%s\"\n", tempDir))
	sb.WriteString(fmt.Sprintf("mount --bind \"%s\" \"%s\"\n\n", baseDir, tempDir))

	// Step 2: Create write path directories in temp (= real filesystem via bind mount)
	// These must exist before tmpfs hides them.
	if len(iso.WritePaths) > 0 {
		sb.WriteString("# Create write path dirs in original workspace (via temp bind mount)\n")
		for _, path := range iso.WritePaths {
			relPath := strings.TrimPrefix(path, baseDir+"/")
			if relPath == path && !strings.HasPrefix(path, "/") {
				relPath = path
			}
			logicalTempPath := filepath.Join(tempDir, relPath)
			sb.WriteString(fmt.Sprintf("mkdir -p \"%s\"\n", logicalTempPath))
		}
		sb.WriteString("\n")
	}

	// Step 3: Hide workspace with tmpfs overlay
	sb.WriteString("# Hide workspace with tmpfs overlay\n")
	sb.WriteString(fmt.Sprintf("mount -t tmpfs tmpfs \"%s\"\n\n", baseDir))

	// Step 3.5: Create every configured path's placeholder node (directory
	// or empty file, matching what's actually at that path in tempDir)
	// before any bind-mount runs. Must happen in one pass across ALL of
	// ReadPaths/WritePaths/BlockedWritePaths, strictly before step 4 starts
	// -- see writeCreatePlaceholder's comment for why interleaving
	// placeholder creation with bind-mounting (the previous structure)
	// caused two different real failures.
	allConfiguredPaths := make([]string, 0, len(iso.ReadPaths)+len(iso.WritePaths)+len(iso.BlockedWritePaths))
	allConfiguredPaths = append(allConfiguredPaths, iso.ReadPaths...)
	allConfiguredPaths = append(allConfiguredPaths, iso.WritePaths...)
	allConfiguredPaths = append(allConfiguredPaths, iso.BlockedWritePaths...)
	if len(allConfiguredPaths) > 0 {
		sb.WriteString("# Create placeholder nodes for every configured path before mounting any of them\n")
		for _, path := range allConfiguredPaths {
			tempPath, absPath := resolveTempAndAbsPath(path, baseDir, tempDir)
			writeCreatePlaceholder(&sb, tempPath, absPath)
		}
		sb.WriteString("\n")
	}

	// Step 4: Bind-mount read paths from temp (read-only)
	// Includes write path dirs created in step 2 (they exist in the original filesystem)
	if len(iso.ReadPaths) > 0 {
		sb.WriteString("# Mount read-only paths from original workspace\n")
		for _, path := range iso.ReadPaths {
			tempPath, absPath := resolveTempAndAbsPath(path, baseDir, tempDir)
			writeBindMountOnly(&sb, tempPath, absPath, true)
		}
		sb.WriteString("\n")
	}

	// Step 5: Bind-mount write paths from temp (read-write, overrides read-only)
	if len(iso.WritePaths) > 0 {
		sb.WriteString("# Mount write paths read-write (overrides read-only for these subtrees)\n")
		for _, path := range iso.WritePaths {
			tempPath, absPath := resolveTempAndAbsPath(path, baseDir, tempDir)
			writeBindMountOnly(&sb, tempPath, absPath, false)
		}
		sb.WriteString("\n")
	}

	// Step 6: Overlay write-blocked subpaths as read-only. This runs AFTER step 5 so
	// the read-only bind-mount wins for these subtrees even when their parent is
	// bound read-write. Writes to these paths return EROFS; reads pass through
	// unchanged so the agent can still cat/jq planning files.
	if len(iso.BlockedWritePaths) > 0 {
		sb.WriteString("# Re-mount write-blocked paths as read-only (reads allowed, writes denied)\n")
		for _, path := range iso.BlockedWritePaths {
			tempPath, absPath := resolveTempAndAbsPath(path, baseDir, tempDir)
			writeBindMountOnly(&sb, tempPath, absPath, true)
		}
		sb.WriteString("\n")
	}

	// Change to working directory
	sb.WriteString(fmt.Sprintf("cd \"%s\"\n", iso.WorkDir))

	// Build and execute command
	fullCmd := command
	if len(args) > 0 {
		fullCmd = fmt.Sprintf("%s %s", command, strings.Join(args, " "))
	}

	// Escape single quotes in command for shell execution
	escapedCmd := strings.ReplaceAll(fullCmd, "'", "'\\''")
	sb.WriteString(fmt.Sprintf("exec sh -c '%s'\n", escapedCmd))

	return sb.String()
}

// scopeBrowser grants the command only its own managed browser: the socket
// folder its daemon listens in and its Chrome profile. It returns the socket
// folder, or "" when the command has no managed browser (then the shared
// browser grants apply, as before).
func (iso *Isolator) scopeBrowser() string {
	session := strings.TrimSpace(iso.BrowserSession)
	if !browserconfig.IsUserSession(session) {
		iso.BrowserSession = ""
		return ""
	}
	socketDir := browserconfig.SocketDirForSession(session)
	if err := os.MkdirAll(socketDir, 0o700); err != nil {
		iso.BrowserSession = ""
		return ""
	}
	iso.WritePaths = append(iso.WritePaths, socketDir)
	if profile := browserconfig.ProfilePathForSession(session); profile != "" {
		if err := os.MkdirAll(profile, 0o700); err == nil {
			iso.WritePaths = append(iso.WritePaths, profile)
		}
	}
	return socketDir
}

func replaceEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return append(out, key+"="+value)
}

// MergeExtraEnv appends extra to env. PYTHONPATH keeps the platform helper importable beside the command's own
// temp folder, so a TMPDIR already in env is put in front of it.
func MergeExtraEnv(env []string, extra map[string]string) []string {
	for k, v := range extra {
		if k == "PYTHONPATH" {
			for _, entry := range env {
				if strings.HasPrefix(entry, "TMPDIR=") {
					v = strings.TrimPrefix(entry, "TMPDIR=") + string(os.PathListSeparator) + v
				}
			}
		}
		env = append(env, k+"="+v)
	}
	return env
}
