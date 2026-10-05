//go:build linux

package security

import (
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const landlockBaseFSAccess = unix.LANDLOCK_ACCESS_FS_EXECUTE |
	unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
	unix.LANDLOCK_ACCESS_FS_READ_FILE |
	unix.LANDLOCK_ACCESS_FS_READ_DIR |
	unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
	unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
	unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
	unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
	unix.LANDLOCK_ACCESS_FS_MAKE_REG |
	unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
	unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
	unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
	unix.LANDLOCK_ACCESS_FS_MAKE_SYM

func landlockHandledFS(abi int) uint64 {
	rights := uint64(landlockBaseFSAccess)
	if abi >= 2 {
		rights |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		rights |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		rights |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return rights
}

func landlockReadFS(handled uint64) uint64 {
	return handled & (unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR)
}

// RunLandlockLauncher applies policy to the current process and replaces it
// with argv. It is called only by the dedicated launcher binary.
func RunLandlockLauncher(policy LandlockPolicy, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("missing command")
	}
	if policy.PrivatePTS && os.Getenv(privatePTSChildEnv) != "1" {
		return runPrivatePTSChild(policy, argv)
	}
	_ = os.Unsetenv(privatePTSChildEnv)
	abi, err := landlockABI()
	if err != nil || abi < 1 {
		return fmt.Errorf("SANDBOX_UNAVAILABLE: Landlock filesystem ABI unavailable: %w", err)
	}
	if policy.PrivateTmp {
		if err := enterPrivateTmp(policy); err != nil {
			return fmt.Errorf("SANDBOX_UNAVAILABLE: %w", err)
		}
	}
	if policy.PrivatePTS {
		if err := enterPrivatePTS(); err != nil {
			return fmt.Errorf("SANDBOX_UNAVAILABLE: %w", err)
		}
	}
	handled := landlockHandledFS(abi)
	attr := unix.LandlockRulesetAttr{Access_fs: handled}
	rulesetFD, _, errno := unix.Syscall6(
		unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&attr)),
		unsafe.Sizeof(attr),
		0,
		0,
		0,
		0,
	)
	if errno != 0 {
		return fmt.Errorf("SANDBOX_UNAVAILABLE: create Landlock ruleset: %w", errno)
	}
	defer unix.Close(int(rulesetFD))

	readAccess := landlockReadFS(handled)
	writeAccess := handled
	for _, path := range landlockSystemReadPaths() {
		if err := addLandlockPathRule(int(rulesetFD), path, readAccess); err != nil {
			return err
		}
	}
	for _, path := range landlockSystemWritePaths(policy.PrivateTmp, policy.BrowserScoped) {
		if err := addLandlockPathRule(int(rulesetFD), path, writeAccess); err != nil {
			return err
		}
	}
	if policy.PrivatePTS {
		for _, path := range []string{"/dev/pts", "/dev/ptmx"} {
			if err := addLandlockPathRule(int(rulesetFD), path, writeAccess); err != nil {
				return err
			}
		}
	}
	// The policy's own grants. A grant this account may not even stat (a slot asked for an app-owned 0700 tree,
	// PLAT-478) is left out with a warning instead of refusing the whole command: it could grant nothing the
	// account's permissions allow, so the command gets less, never more, and still runs fully confined.
	for _, path := range policy.ReadPaths {
		if err := addPolicyPathRule(int(rulesetFD), path, readAccess); err != nil {
			return err
		}
	}
	for _, path := range policy.WritePaths {
		if err := addPolicyPathRule(int(rulesetFD), path, writeAccess); err != nil {
			return err
		}
	}
	for _, path := range policy.ListPaths {
		if err := addPolicyPathRule(int(rulesetFD), path, handled&unix.LANDLOCK_ACCESS_FS_READ_DIR); err != nil {
			return err
		}
	}

	if err := os.Chdir(policy.WorkDir); err != nil {
		return fmt.Errorf("SANDBOX_UNAVAILABLE: enter working directory: %w", err)
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("SANDBOX_UNAVAILABLE: set no_new_privs: %w", err)
	}
	_, _, errno = unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, rulesetFD, 0, 0)
	if errno != 0 {
		return fmt.Errorf("SANDBOX_UNAVAILABLE: enforce Landlock ruleset: %w", errno)
	}
	if err := syscall.Exec(argv[0], argv, ScrubPlatformSecretEnv(os.Environ())); err != nil {
		return fmt.Errorf("execute sandboxed command: %w", err)
	}
	return nil
}

// addPolicyPathRule is addLandlockPathRule for a grant from the policy: a path this account is not permitted to
// stat is skipped and reported on stderr (SANDBOX_GRANT_SKIPPED). Any other failure still refuses the command.
func addPolicyPathRule(rulesetFD int, path string, allowed uint64) error {
	if _, err := os.Stat(path); grantStatSkippable(err) {
		fmt.Fprintf(os.Stderr, "SANDBOX_GRANT_SKIPPED: %s is not accessible to this account; it is not granted\n", path)
		return nil
	}
	return addLandlockPathRule(rulesetFD, path, allowed)
}

func addLandlockPathRule(rulesetFD int, path string, allowed uint64) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("SANDBOX_UNAVAILABLE: inspect Landlock path: %w", err)
	}
	if !info.IsDir() {
		allowed &= ^uint64(unix.LANDLOCK_ACCESS_FS_READ_DIR |
			unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
			unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
			unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
			unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
			unix.LANDLOCK_ACCESS_FS_MAKE_REG |
			unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
			unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
			unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
			unix.LANDLOCK_ACCESS_FS_MAKE_SYM |
			unix.LANDLOCK_ACCESS_FS_REFER)
	}
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("SANDBOX_UNAVAILABLE: open Landlock path: %w", err)
	}
	defer unix.Close(fd)
	rule := unix.LandlockPathBeneathAttr{Allowed_access: allowed, Parent_fd: int32(fd)}
	_, _, errno := unix.Syscall6(
		unix.SYS_LANDLOCK_ADD_RULE,
		uintptr(rulesetFD),
		unix.LANDLOCK_RULE_PATH_BENEATH,
		uintptr(unsafe.Pointer(&rule)),
		0,
		0,
		0,
	)
	if errno != 0 {
		return fmt.Errorf("SANDBOX_UNAVAILABLE: add Landlock path rule: %w", errno)
	}
	return nil
}

func landlockSystemReadPaths() []string {
	paths := []string{
		"/bin", "/sbin", "/usr", "/lib", "/lib64",
		// All of /etc, read-only. This used to be an enumerated dozen entries
		// (ssl, resolv.conf, passwd, ld.so.*, fonts, ...), so any tool that
		// read one more config file died with EACCES: pip needs
		// /etc/debian_version (distro id for its User-Agent) and then
		// /etc/mime.types, and each surfaced as its own live "installing
		// packages is impossible" failure on Dominion (PLAT-283). Read-only
		// /etc is what every ordinary program assumes. DAC still applies, so
		// this grants nothing the service user cannot already read outside
		// the sandbox -- verified on Dominion: no file under /etc is readable
		// by the service user without also being world-readable. Landlock
		// rules are additive, so a deployment cannot carve a secret back out
		// of this grant: keep service-readable secrets out of /etc (Dominion
		// keeps them in /srv/dominion/.env). /proc stays narrow, see below.
		"/etc",
		// The explicit entries below are NOT redundant with "/etc" above: a
		// path_beneath rule on /etc covers what lives under /etc, not what a
		// symlink there points at. On systemd-resolved hosts (Ubuntu, i.e.
		// Dominion) /etc/resolv.conf -> /run/systemd/resolve/stub-resolv.conf,
		// so with "/etc" alone every DNS lookup in the sandbox failed
		// ("Temporary failure in name resolution") -- found live while
		// verifying PLAT-283. existingCanonicalPaths resolves each of these
		// individually, which is what grants the target.
		"/etc/ssl", "/etc/ca-certificates", "/etc/resolv.conf", "/etc/hosts",
		"/etc/nsswitch.conf", "/etc/passwd", "/etc/group", "/etc/localtime",
		"/etc/ld.so.cache", "/etc/ld.so.conf", "/etc/ld.so.conf.d",
		// Headless Chromium needs font metadata and a small set of read-only
		// kernel/cpu facts during startup. Keep the /proc entries narrow:
		// granting all of /proc would let a guarded command inspect other
		// processes' environments, including service credentials.
		"/etc/fonts", "/usr/share/fonts",
		// /proc/self is bound to the already-sanitized launcher process. The
		// launcher execs Chromium in place, so this does not expose unrelated
		// service processes or their environments.
		"/proc/self", "/proc/thread-self",
		"/proc/cpuinfo", "/proc/meminfo", "/proc/stat",
		"/proc/sys/fs/inotify/max_user_watches",
		"/sys/devices", "/sys/bus/pci/devices",
		"/dev/null", "/dev/zero", "/dev/full", "/dev/random", "/dev/urandom", "/dev/tty",
	}
	// The system Chrome the browser launches by default (/usr/bin/google-chrome -> /etc/alternatives -> /opt/google/chrome/google-chrome).
	// Without it every browser start in the sandbox fails with "Failed to launch Chrome at /usr/bin/google-chrome: Permission denied"
	// (Excellence 2026-10-03, once the mount-namespace fallback that could reach /opt was removed). Dropped when it is not installed.
	paths = append(paths, "/opt/google/chrome")
	if browserPath := strings.TrimSpace(os.Getenv("AGENT_BROWSER_EXECUTABLE_PATH")); browserPath != "" {
		if resolved, err := filepath.EvalSymlinks(browserPath); err == nil {
			paths = append(paths, filepath.Dir(resolved))
		}
	}
	// A deployment can install its own CLI tools outside the standard system
	// dirs above (Dominion: /srv/dominion/tools/bin, on PATH for every
	// service). Without an explicit grant here, a landlocked step gets
	// "Permission denied" (rc=126) trying to exec them -- not a read/config
	// problem, an exec-rights one: PLACE_PAPER_TRADES on Dominion hit exactly
	// this trying to launch the alpaca CLI, every run since the tool was
	// first installed. Colon-separated, same convention as PATH.
	if extra := strings.TrimSpace(os.Getenv("SANDBOX_EXTRA_SYSTEM_PATHS")); extra != "" {
		for _, path := range strings.Split(extra, ":") {
			if path = strings.TrimSpace(path); path != "" {
				paths = append(paths, path)
			}
		}
	}
	return existingCanonicalPaths(paths)
}

func landlockSystemWritePaths(privateTmp, browserScoped bool) []string {
	// Never the host /tmp: every sandboxed command runs as the same service
	// user, so a shared /tmp let one user's agent read what another's left
	// there -- on RTS a Crew's repository clones, and git credentials written
	// through HOME=/tmp (2026-09-28). With a private /tmp (its own tmpfs, see
	// private_tmp_ns_linux.go) the command may use /tmp freely. Without one,
	// only the browser folders stay writable: the daemons' socket folder, and
	// the managed Chrome wrapper's temp folder, where Chrome keeps its shared
	// memory (/dev/shm is not granted). Without that grant every sandboxed
	// browser launch failed ("Creating shared memory in /tmp/aw-browser-<uid>
	// failed: Permission denied", RTS 2026-09-28).
	// A command scoped to its own browser has that browser's socket folder
	// and profile in its policy; every other browser stays out of reach.
	var paths []string
	if !browserScoped {
		paths = append(paths, browserSocketDir)
	}
	if privateTmp {
		paths = append(paths, "/tmp")
	} else {
		paths = append(paths, browserTempDir())
	}
	paths = append(paths,
		"/dev/null", "/dev/zero", "/dev/full", "/dev/random", "/dev/urandom", "/dev/tty",
	)
	if profile := browserconfig.SharedProfile(); profile != "" && !browserScoped {
		// A user or workflow browser never actually writes to `profile` itself --
		// HeadlessArgsForSession launches Chrome against `<profile>-users/<id>`
		// or `<profile>-workflows/<id>` instead (see workspace/browserconfig/
		// launch.go). Landlock rules don't cover sibling paths that merely share
		// a string prefix, so granting only the bare `profile` path left every
		// shared-profile Chrome launch unable to create its ProcessSingleton
		// socket: "Chrome exited early ... Failed to create socket directory."
		// Confirmed live on SparkQuill the moment AGENT_BROWSER_SHARED_PROFILE
		// was first enabled.
		// Project browsers (one per Crew/product project) live under
		// `<profile>-projects/<id>`. Create the roots here, before the ruleset
		// is built: existingCanonicalPaths drops a missing directory, and a
		// missing root would leave the first browser of that kind unwritable.
		for _, root := range []string{profile + "-users", profile + "-workflows", profile + "-projects"} {
			_ = os.MkdirAll(root, 0o700)
		}
		paths = append(paths, profile, profile+"-users", profile+"-workflows", profile+"-projects")
	}
	return existingCanonicalPaths(paths)
}

func existingCanonicalPaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		resolved := path
		if canonical, err := filepath.EvalSymlinks(path); err == nil {
			resolved = canonical
		}
		if _, err := os.Stat(resolved); err != nil {
			continue
		}
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		result = append(result, resolved)
	}
	return result
}

// browserTempDir is the managed Chrome wrapper's temp folder
// (deploy/aws-ec2/server/chrome-headless-wrapper.sh).
func browserTempDir() string {
	return fmt.Sprintf("/tmp/aw-browser-%d", os.Getuid())
}

// platformSecretEnvNames and platformSecretEnvPrefixes are the platform's OWN secrets: what the app reads from its
// environment (sign-in, Supabase, the keyring password, the admin and allow lists, the service tokens). A confined
// coding CLI and everything it runs inherits the launcher's environment, so without this its built-in shell could
// print them (PLAT-491: SUPABASE_SERVICE_ROLE_KEY and GOG_KEYRING_PASSWORD were readable from a Crew's native shell).
// A CLI's OWN login (CLAUDE_CODE_OAUTH_TOKEN, CURSOR_API_KEY, provider keys) and the per-chat scope (SECRET_*, MCP_*)
// are deliberately not here: the adapters set those for the launch.
var platformSecretEnvNames = []string{
	"AUTH_SECRET", "ACCESS_PASSWORD", "GOG_KEYRING_PASSWORD", "ADMIN_USERS", "AUTH_ALLOWED_EMAILS",
	"CAPLAYER_SERVICE_TOKEN_FILE", "SSH_AUTH_SOCK",
}

var platformSecretEnvPrefixes = []string{"SUPABASE_", "GLOBAL_SECRET_", "VAULT_"}

// platformSecretEnvExtraEnv adds host-specific names (comma separated) to the list above.
const platformSecretEnvExtraEnv = "AGENTWORKS_CLI_ENV_DENY"

// ScrubPlatformSecretEnv returns env without the platform's own secrets (see platformSecretEnvNames). Order and every
// other entry are unchanged.
func ScrubPlatformSecretEnv(env []string) []string {
	extra := map[string]bool{}
	for _, name := range strings.Split(os.Getenv(platformSecretEnvExtraEnv), ",") {
		if name = strings.TrimSpace(name); name != "" {
			extra[name] = true
		}
	}
	denied := func(key string) bool {
		if extra[key] {
			return true
		}
		for _, name := range platformSecretEnvNames {
			if key == name {
				return true
			}
		}
		for _, prefix := range platformSecretEnvPrefixes {
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
		return false
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if denied(key) {
			continue
		}
		out = append(out, entry)
	}
	return out
}
