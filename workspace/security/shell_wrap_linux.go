//go:build linux

package security

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Shell wrapping (PLAT-872-84). A Crew's coding CLI runs as its owner's slot with the server's shared AI logins in
// its own Landlock layer; the shell commands it starts must not reach them. The CLIs ignore $SHELL and exec
// /bin/bash or /bin/sh themselves, so inside the command's private mount namespace the launcher binds the real
// shells to shellWrapDir and mounts itself over them. Started under a shell's name it is the wrapper: it removes
// the login variables, adds the Inner layer once per command tree and runs the real shell.

// shellWrapDir is inside the command's private /tmp, so only this command tree sees it.
const shellWrapDir = "/tmp/.agentworks-shells"

// shellLayerEnv marks a process tree that already runs under the Inner layer: a shell started from a wrapped shell
// only runs the real shell (Landlock allows at most 16 layers). The launcher clears it before the CLI starts, so
// the CLI itself never carries it.
const shellLayerEnv = "AGENTWORKS_SHELL_LAYER"

var defaultWrappedShells = []string{"/usr/bin/bash", "/usr/bin/sh", "/usr/bin/dash", "/bin/bash", "/bin/sh", "/bin/dash"}

type shellWrapConfig struct {
	Inner    LandlockPolicy    `json:"inner"`
	ScrubEnv []string          `json:"scrub_env,omitempty"`
	Real     map[string]string `json:"real"` // shell name as invoked (bash, sh, dash) -> the real shell's private path
}

// applyShellWrap runs in the launcher inside its namespaces, before Landlock, while it may still mount.
func applyShellWrap(wrap *ShellWrap) error {
	if wrap == nil {
		return nil
	}
	if err := os.Mkdir(shellWrapDir, 0o755); err != nil {
		return fmt.Errorf("wrap shells: %w", err)
	}
	shells := wrap.Shells
	if len(shells) == 0 {
		shells = defaultWrappedShells
	}
	config := shellWrapConfig{Inner: wrap.Inner, ScrubEnv: wrap.ScrubEnv, Real: map[string]string{}}
	realOf := map[string]string{} // resolved shell binary -> its private copy
	for _, shell := range shells {
		resolved, err := filepath.EvalSymlinks(shell)
		if err != nil {
			continue // not installed here
		}
		real, done := realOf[resolved]
		if !done {
			real = filepath.Join(shellWrapDir, fmt.Sprintf("real-%d-%s", len(realOf), filepath.Base(resolved)))
			if err := os.WriteFile(real, nil, 0o755); err != nil {
				return fmt.Errorf("wrap shells: %w", err)
			}
			if err := unix.Mount(resolved, real, "", unix.MS_BIND, ""); err != nil {
				return fmt.Errorf("wrap shells: keep %s: %w", resolved, err)
			}
			// The running launcher itself: always present, wherever its file lives (the private /tmp may hide it).
			if err := unix.Mount("/proc/self/exe", resolved, "", unix.MS_BIND, ""); err != nil {
				return fmt.Errorf("wrap shells: wrap %s: %w", resolved, err)
			}
			realOf[resolved] = real
		}
		config.Real[filepath.Base(shell)] = real
		config.Real[filepath.Base(resolved)] = real
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(shellWrapDir, "config.json"), raw, 0o444); err != nil {
		return fmt.Errorf("wrap shells: %w", err)
	}
	return nil
}

// WrappedShellInvocation reports whether this process is the launcher started under a shell's name inside a
// wrapped command tree.
func WrappedShellInvocation(argv0 string) bool {
	switch filepath.Base(argv0) {
	case "bash", "sh", "dash":
	default:
		return false
	}
	_, err := os.Stat(filepath.Join(shellWrapDir, "config.json"))
	return err == nil
}

// RunWrappedShell is the wrapper: it never returns on success (the real shell replaces it).
func RunWrappedShell(argv []string) error {
	raw, err := os.ReadFile(filepath.Join(shellWrapDir, "config.json"))
	if err != nil {
		return fmt.Errorf("SANDBOX_UNAVAILABLE: read shell wrap: %w", err)
	}
	var config shellWrapConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("SANDBOX_UNAVAILABLE: decode shell wrap: %w", err)
	}
	real := config.Real[filepath.Base(argv[0])]
	if real == "" {
		return fmt.Errorf("SANDBOX_UNAVAILABLE: no real shell for %s", filepath.Base(argv[0]))
	}
	if os.Getenv(shellLayerEnv) == "1" {
		return syscall.Exec(real, argv, os.Environ())
	}
	drop := map[string]bool{}
	for _, name := range config.ScrubEnv {
		drop[strings.TrimSpace(name)] = true
	}
	env := []string{shellLayerEnv + "=1"}
	for _, entry := range ScrubPlatformSecretEnv(os.Environ()) {
		name, _, _ := strings.Cut(entry, "=")
		if !drop[name] && name != shellLayerEnv {
			env = append(env, entry)
		}
	}
	abi, err := landlockABI()
	if err != nil || abi < 1 {
		return fmt.Errorf("SANDBOX_UNAVAILABLE: Landlock filesystem ABI unavailable: %w", err)
	}
	if err := restrictLandlock(config.Inner, abi); err != nil {
		return err
	}
	return syscall.Exec(real, argv, env)
}
