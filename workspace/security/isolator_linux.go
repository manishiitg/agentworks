//go:build linux

package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/manishiitg/coding-agent-loop/workspace/gogconfig"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

var sandboxCapabilityOnce sync.Once
var sandboxCapability SandboxCapability

func (iso *Isolator) executeIsolatedLinuxPlatform(ctx context.Context, command string, args []string) (*exec.Cmd, func(), error) {
	if abi, err := landlockABI(); err == nil && abi >= 1 {
		policy, policyErr := iso.landlockPolicy()
		if policyErr == nil && (len(policy.ReadOnlyOverlays) > 0 || len(policy.HiddenPaths) > 0 || len(policy.PrivateRoots) > 0) && !landlockNamespacesAvailable() {
			policyErr = fmt.Errorf("blocked paths inside granted paths need the launcher's namespaces")
		}
		if policyErr != nil {
			// Never fall back to the mount-namespace backend here. It ignores the user's slot (the command ran as the
			// service account) and leaves the rest of the host as the service account sees it: an agent's shell with
			// a blocked db.sqlite in its project read the platform's .env and wrote the service account's home
			// (Excellence 2026-10-03). A policy Landlock cannot carry is refused.
			return nil, nil, fmt.Errorf("SANDBOX_UNAVAILABLE: this Folder Guard policy cannot be enforced: %w", policyErr)
		}
		return iso.landlockCommand(ctx, policy, command, args)
	}

	// The mount-namespace backend cannot run a command as the user's slot account: never use it for one.
	if iso.Slot != "" {
		return nil, nil, errors.New("SANDBOX_UNAVAILABLE: running as the user's account needs Landlock")
	}
	if mountNamespaceAvailable() {
		return iso.executeIsolatedMountNamespace(ctx, command, args)
	}
	return nil, nil, errors.New("SANDBOX_UNAVAILABLE: neither Landlock filesystem rules nor an unprivileged mount namespace are available")
}

func (iso *Isolator) landlockPolicy() (LandlockPolicy, error) {
	// A read-only folder that does not exist grants nothing and cannot be granted later (the rules are fixed when the
	// command starts), so it must not stop the whole sandbox: the platform lists optional folders such as a workflow's
	// learnings/_global, and a workflow that never created one could not run a single shell command (PLAT-514, RTS
	// 2026-10-05). Only "does not exist" is skipped; a folder that exists but cannot be read still fails closed.
	reads, err := iso.canonicalOptionalPolicyPaths(iso.ReadPaths)
	if err != nil {
		return LandlockPolicy{}, err
	}
	writes, err := iso.canonicalPolicyPaths(iso.WritePaths)
	if err != nil {
		return LandlockPolicy{}, err
	}
	if iso.Slot != "" && iso.UserHome != "" {
		writes = append(writes, canonicalPath(iso.UserHome))
	}
	// A slot command never receives an app-private path (browser profiles and sockets, app state): PLAT-478.
	reads = withoutAppPrivatePaths(iso.Slot, reads, iso.getBaseDir(), iso.UserHome)
	writes = withoutAppPrivatePaths(iso.Slot, writes, iso.getBaseDir(), iso.UserHome)
	// Blocked paths are deny rules. A SQLite WAL/SHM sidecar is intentionally
	// absent until SQLite first writes in WAL mode; a missing deny target cannot
	// grant access and must not prevent the entire sandbox from starting. We
	// still fail closed for every required allow path and for other stat errors.
	blocked, err := iso.canonicalOptionalPolicyPaths(iso.BlockedPaths)
	if err != nil {
		return LandlockPolicy{}, err
	}
	blockedWrites, err := iso.canonicalOptionalPolicyPaths(iso.BlockedWritePaths)
	if err != nil {
		return LandlockPolicy{}, err
	}

	// Landlock rules are additive. A narrower rule cannot revoke a write grant
	// inherited from a writable parent. Reject those policies instead of
	// silently weakening BlockedPaths/BlockedWritePaths precedence.
	// A blocked path inside a granted one is hidden by the launcher (HiddenPaths); one that contains or equals a
	// granted path cannot be expressed and is refused.
	var hidden []string
	for _, denied := range blocked {
		inside := false
		for _, allowed := range append(append([]string{}, reads...), writes...) {
			if !pathsOverlapByContainment(denied, allowed) {
				continue
			}
			if denied == allowed || !pathWithin(denied, allowed) {
				return LandlockPolicy{}, fmt.Errorf("blocked path overlaps allowed path")
			}
			inside = true
		}
		if inside {
			hidden = append(hidden, denied)
		}
	}
	// A blocked-write path inside a writable one cannot be a Landlock rule
	// (rules only add access); the launcher mounts it read-only instead, which
	// needs its namespaces (see landlockNamespacesAvailable). One that
	// contains a writable path cannot be expressed either way.
	var overlays []string
	for _, deniedWrite := range blockedWrites {
		for _, writable := range writes {
			if !pathsOverlapByContainment(deniedWrite, writable) {
				continue
			}
			if !pathWithin(deniedWrite, writable) || deniedWrite == writable {
				return LandlockPolicy{}, fmt.Errorf("blocked-write path overlaps writable path")
			}
			overlays = append(overlays, deniedWrite)
			break
		}
	}

	// A slot command must not reach its slot's tmux server (PLAT-480, F1): Landlock does not govern connect() on
	// pathname Unix sockets (no such right below ABI 9), and the socket belongs to the same account. The launcher
	// shows an empty slot run folder in the command's own mount namespace, except the folders the command was
	// granted inside it (its terminal's folder, the output helper). When the host cannot give
	// the command that namespace the command is refused (executeIsolatedLinuxPlatform), never run with the socket in view.
	var privateRoots []string
	if root := slotRunRootToHide(iso.Slot); root != "" {
		privateRoots = append(privateRoots, root)
	}

	// The launcher enters WorkDir before restricting itself. Landlock can then
	// keep the directory usable as cwd without granting reads to its children;
	// this matches the existing mount/sandbox-exec contract.
	return LandlockPolicy{ReadPaths: reads, WritePaths: writes, WorkDir: canonicalPath(iso.WorkDir), BrowserScoped: iso.BrowserSession != "" || iso.Slot != "", PrivatePTS: iso.AllowPTY, ReadOnlyOverlays: overlays, HiddenPaths: hidden, PrivateRoots: privateRoots}, nil
}

func (iso *Isolator) canonicalPolicyPaths(paths []string) ([]string, error) {
	return iso.canonicalPolicyPathsWithMissing(paths, false)
}

func (iso *Isolator) canonicalOptionalPolicyPaths(paths []string) ([]string, error) {
	return iso.canonicalPolicyPathsWithMissing(paths, true)
}

func (iso *Isolator) canonicalPolicyPathsWithMissing(paths []string, allowMissing bool) ([]string, error) {
	result := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		resolved, ok := iso.sandboxAllowedPath(path)
		if !ok {
			return nil, fmt.Errorf("policy path escapes the workspace boundary")
		}
		path = resolved
		if _, err := os.Stat(path); err != nil {
			if allowMissing && os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("policy path is unavailable: %w", err)
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	return result, nil
}

func pathsOverlapByContainment(left, right string) bool {
	return pathWithin(left, right) || pathWithin(right, left)
}

func (iso *Isolator) landlockCommand(ctx context.Context, policy LandlockPolicy, command string, args []string) (*exec.Cmd, func(), error) {
	runner, err := landlockRunnerPath()
	if err != nil {
		return nil, nil, fmt.Errorf("SANDBOX_UNAVAILABLE: %w", err)
	}
	// The browser daemons' socket folder must exist on the host before the
	// launcher binds it into the command's private /tmp.
	_ = os.MkdirAll(browserSocketDir, 0o700)
	_ = os.MkdirAll(browserTempDir(), 0o700)
	privateTmp := privateTmpAvailable(runner)
	if len(policy.ReadOnlyOverlays) > 0 && !privateTmp {
		return nil, nil, fmt.Errorf("SANDBOX_UNAVAILABLE: read-only overlays need the launcher's namespaces")
	}
	policy.PrivateTmp = privateTmp
	config, err := os.CreateTemp("", "agentworks-landlock-*.json")
	if err != nil {
		return nil, nil, fmt.Errorf("SANDBOX_UNAVAILABLE: create Landlock policy: %w", err)
	}
	configPath := config.Name()
	cleanup := func() { _ = os.Remove(configPath) }
	if err := config.Chmod(0600); err != nil {
		_ = config.Close()
		cleanup()
		return nil, nil, fmt.Errorf("SANDBOX_UNAVAILABLE: protect Landlock policy: %w", err)
	}
	if err := json.NewEncoder(config).Encode(policy); err != nil {
		_ = config.Close()
		cleanup()
		return nil, nil, fmt.Errorf("SANDBOX_UNAVAILABLE: encode Landlock policy: %w", err)
	}
	if err := config.Close(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("SANDBOX_UNAVAILABLE: close Landlock policy: %w", err)
	}

	fullCommand := command
	if len(args) > 0 {
		fullCommand += " " + strings.Join(args, " ")
	}
	cmd := exec.CommandContext(ctx, runner, "--config", configPath, "--", "/bin/sh", "-c", fullCommand)
	cmd.Dir = policy.WorkDir
	if privateTmp {
		cmd.SysProcAttr = privateTmpSysProcAttr()
	}
	// Package-manager state routed to disk Landlock lets the step write:
	// the workflow's persistent .sandbox-cache when granted (PLAT-284),
	// else the run folder (PLAT-283). Without this, pip/npm/venv default to
	// $HOME, which lies outside every step's grant, and every install died
	// with a bare permission error -- see sandbox_tool_env.go.
	cmd.Env = sandboxToolEnv(gogconfig.Environment(BuildSafeEnvironment(), iso.hostGogRestricted()), policy.WorkDir, policy.WritePaths)
	if iso.Slot != "" {
		// As the user's own account: always the project's private home, the same as their Code terminal (see SlotHomeEnv).
		cmd.Env = SlotHomeEnv(cmd.Env, policy.WorkDir, policy.WritePaths, iso.UserHome)
		// The request written by WrapCommand carries the environment as it is now: add the per-call values first.
		cmd.Env = MergeExtraEnv(cmd.Env, iso.ExtraEnv)
		// Run as the user's slot account: the namespaces and the policy are created after the switch.
		var wrapped *exec.Cmd
		var wrapErr error
		if iso.Interactive {
			var removeRequest func()
			wrapped, removeRequest, wrapErr = slots.WrapCommandFile(ctx, cmd, iso.Slot)
			if wrapErr == nil {
				inner := cleanup
				cleanup = func() { removeRequest(); inner() }
			}
		} else {
			wrapped, wrapErr = slots.WrapCommand(ctx, cmd, iso.Slot)
		}
		if wrapErr != nil {
			cleanup()
			return nil, nil, fmt.Errorf("SANDBOX_UNAVAILABLE: run as the user's slot: %w", wrapErr)
		}
		wrapped.Dir = "/"
		cmd = wrapped
	}
	return cmd, cleanup, nil
}

// landlockNamespacesAvailable reports whether the launcher can start in its
// own user and mount namespaces here (the private /tmp probe).
func landlockNamespacesAvailable() bool {
	runner, err := landlockRunnerPath()
	return err == nil && privateTmpAvailable(runner)
}

func landlockRunnerPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("AGENTWORKS_LANDLOCK_RUNNER")); override != "" {
		if info, err := os.Stat(override); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return override, nil
		}
		return "", fmt.Errorf("configured Landlock launcher is not executable")
	}
	if executable, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(executable), landlockRunnerName)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return candidate, nil
		}
	}
	if candidate, err := exec.LookPath(landlockRunnerName); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("Landlock launcher %q was not found", landlockRunnerName)
}

func landlockABI() (int, error) {
	version, _, errno := unix.Syscall6(
		unix.SYS_LANDLOCK_CREATE_RULESET,
		0,
		0,
		unix.LANDLOCK_CREATE_RULESET_VERSION,
		0,
		0,
		0,
	)
	if errno != 0 {
		return 0, errno
	}
	return int(version), nil
}

// mountNamespaceAvailable must probe the exact privilege shape
// executeIsolatedMountNamespace actually uses. A plain "unshare -m" (mount
// namespace only) needs CAP_SYS_ADMIN in the CURRENT user namespace, which an
// unprivileged service account never has -- it fails with EPERM regardless of
// any Landlock/AppArmor state, making this probe (and the fallback it gates)
// permanently unusable for every rootless deployment. Pairing it with --user
// --map-root-user first enters a new user namespace mapped back to the
// caller's own UID (the standard unprivileged-mount-namespace pattern, the
// same one rootless container runtimes use), which genuinely grants
// CAP_SYS_ADMIN inside that namespace. Confirmed live on the Dominion
// Hetzner deployment: identical "-m" alone failed with "Operation not
// permitted" as the unprivileged service user, while this form succeeded.
func mountNamespaceAvailable() bool {
	path, err := exec.LookPath("unshare")
	if err != nil {
		return false
	}
	cmd := exec.Command(path, "--mount", "--user", "--map-root-user", "--propagation", "private", "true")
	cmd.Env = BuildSafeEnvironment()
	return cmd.Run() == nil
}

func probeSandboxCapability() SandboxCapability {
	if abi, err := landlockABI(); err == nil && abi >= 1 {
		if runner, runnerErr := landlockRunnerPath(); runnerErr == nil {
			if preflightErr := landlockLauncherPreflight(runner); preflightErr == nil {
				detail := fmt.Sprintf("filesystem ABI %d; launcher preflight passed", abi)
				if privateTmpAvailable(runner) {
					detail += "; private /tmp"
				} else if privateTmpProbe.detail != "" {
					detail += "; " + privateTmpProbe.detail
				}
				return SandboxCapability{Available: true, Backend: "landlock", Detail: detail}
			}
		}
	}
	if mountNamespaceAvailable() {
		return SandboxCapability{Available: true, Backend: "mount_namespace"}
	}
	return SandboxCapability{Available: false, Detail: "SANDBOX_UNAVAILABLE"}
}

func CurrentSandboxCapability() SandboxCapability {
	sandboxCapabilityOnce.Do(func() {
		sandboxCapability = probeSandboxCapability()
	})
	return sandboxCapability
}

func landlockLauncherPreflight(runner string) error {
	config, err := os.CreateTemp("", "agentworks-landlock-preflight-*.json")
	if err != nil {
		return err
	}
	configPath := config.Name()
	defer os.Remove(configPath)
	if err := config.Chmod(0600); err != nil {
		_ = config.Close()
		return err
	}
	if err := json.NewEncoder(config).Encode(LandlockPolicy{WorkDir: os.TempDir()}); err != nil {
		_ = config.Close()
		return err
	}
	if err := config.Close(); err != nil {
		return err
	}
	cmd := exec.Command(runner, "--config", configPath, "--", "/bin/true")
	cmd.Env = BuildSafeEnvironment()
	return cmd.Run()
}

// CLILandlockRunner returns the Landlock launcher when this host can confine
// a coding CLI with it (Landlock ABI present and the launcher preflight
// passes), for the agent server to start CLIs under the same launcher.
func CLILandlockRunner() (string, bool) {
	if capability := CurrentSandboxCapability(); !capability.Available || capability.Backend != "landlock" {
		return "", false
	}
	runner, err := landlockRunnerPath()
	return runner, err == nil
}

// slotRunRootToHide is the folder holding every slot's tmux socket (slotctl's slot_run_root) when it exists on this
// host and the command runs as a slot; "" otherwise (not a slot command, no slots configured, nothing to hide).
func slotRunRootToHide(slot string) string {
	if slot == "" {
		return ""
	}
	cfg, err := slots.LoadExecConfig(slots.ConfigPath())
	if err != nil || strings.TrimSpace(cfg.SlotRunRoot) == "" {
		return ""
	}
	root := canonicalPath(cfg.SlotRunRoot)
	if info, err := os.Lstat(root); err != nil || !info.IsDir() {
		return ""
	}
	return root
}
