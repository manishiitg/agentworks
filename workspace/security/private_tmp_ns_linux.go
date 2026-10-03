//go:build linux

package security

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// A sandboxed command gets its own /tmp: an empty tmpfs in a private mount
// namespace, with only the folders its policy grants under /tmp (its scratch,
// the browser socket folder) bound back in. Removing /tmp from the Landlock
// grant stopped file access, but Landlock does not govern connect() on
// pathname Unix sockets (no such right on kernel 7.0 / ABI 8), so the shared
// tmux server socket /tmp/tmux-<uid>/default stayed reachable and a sandboxed
// shell could read or type into other users' CLIs (PLAT-364, verified on RTS
// 2026-09-28). In the command's own /tmp that socket does not exist.
// /proc/<pid>/root is no way around it: Landlock already denies a sandboxed
// process access to processes outside its domain.
//
// The workspace service creates the user and mount namespaces when it starts
// the launcher (Go cannot unshare a user namespace from a running,
// multi-threaded process), and only its own binary is allowed to by the
// host's AppArmor userns exception. The launcher receives CAP_SYS_ADMIN in
// that namespace only, uses it to mount, and clears it before exec.

// privateTmpDisabledEnv turns the private /tmp off (the command then sees the
// host /tmp, as before). For emergencies only.
const privateTmpDisabledEnv = "AGENTWORKS_SANDBOX_PRIVATE_TMP_DISABLED"

var privateTmpProbe struct {
	once   sync.Once
	ok     bool
	detail string
}

func privateTmpSysProcAttr() *syscall.SysProcAttr {
	uid, gid := os.Getuid(), os.Getgid()
	return &syscall.SysProcAttr{
		Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS,
		UidMappings: []syscall.SysProcIDMap{{ContainerID: uid, HostID: uid, Size: 1}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: gid, HostID: gid, Size: 1}},
		AmbientCaps: []uintptr{unix.CAP_SYS_ADMIN},
	}
}

// privateTmpAvailable reports, once per process, whether the launcher can
// start in its own namespaces here: it runs the real launcher with a private
// /tmp and /bin/true. Hosts that refuse (no userns permission) keep today's
// behaviour, and the sandbox health detail says so.
func privateTmpAvailable(runner string) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv(privateTmpDisabledEnv)), "true") {
		return false
	}
	privateTmpProbe.once.Do(func() {
		err := runPrivateTmpProbe(runner)
		privateTmpProbe.ok = err == nil
		if err != nil {
			// Health detail names capabilities only, never paths.
			privateTmpProbe.detail = "private /tmp unavailable"
			log.Printf("[SANDBOX] private /tmp unavailable, commands see the host /tmp: %v", err)
		}
	})
	return privateTmpProbe.ok
}

func runPrivateTmpProbe(runner string) error {
	config, err := os.CreateTemp("", "agentworks-landlock-probe-*.json")
	if err != nil {
		return err
	}
	configPath := config.Name()
	defer os.Remove(configPath)
	if err := config.Chmod(0o600); err != nil {
		_ = config.Close()
		return err
	}
	if err := json.NewEncoder(config).Encode(LandlockPolicy{WorkDir: "/", PrivateTmp: true}); err != nil {
		_ = config.Close()
		return err
	}
	if err := config.Close(); err != nil {
		return err
	}
	cmd := exec.Command(runner, "--config", configPath, "--", "/bin/sh", "-c", "test ! -e /tmp/"+filepath.Base(configPath))
	cmd.Env = BuildSafeEnvironment()
	cmd.SysProcAttr = privateTmpSysProcAttr()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// enterPrivateTmp runs in the launcher, inside the namespaces the service
// created, before Landlock is applied.
func enterPrivateTmp(policy LandlockPolicy) error {
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make mounts private: %w", err)
	}
	// Hold every granted path under /tmp before the tmpfs hides it.
	type held struct {
		path string
		fd   int
		dir  bool
	}
	var keep []held
	for _, path := range privateTmpKeepPaths(policy) {
		fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			continue // a missing grant stays missing, as without the tmpfs
		}
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			_ = unix.Close(fd)
			continue
		}
		keep = append(keep, held{path: path, fd: fd, dir: st.Mode&unix.S_IFMT == unix.S_IFDIR})
	}
	if err := unix.Mount("tmpfs", "/tmp", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=1777"); err != nil {
		return fmt.Errorf("mount private /tmp: %w", err)
	}
	for _, h := range keep {
		if h.dir {
			if err := os.MkdirAll(h.path, 0o700); err != nil {
				return fmt.Errorf("recreate %s: %w", h.path, err)
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
				return fmt.Errorf("recreate %s: %w", filepath.Dir(h.path), err)
			}
			f, err := os.OpenFile(h.path, os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				return fmt.Errorf("recreate %s: %w", h.path, err)
			}
			_ = f.Close()
		}
		if err := unix.Mount(fmt.Sprintf("/proc/self/fd/%d", h.fd), h.path, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
			return fmt.Errorf("bind %s: %w", h.path, err)
		}
		_ = unix.Close(h.fd)
	}
	if err := applyReadOnlyOverlays(policy.ReadOnlyOverlays); err != nil {
		return err
	}
	if err := applyHiddenPaths(policy.HiddenPaths); err != nil {
		return err
	}
	// The mount capability is for the steps above only.
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		return fmt.Errorf("clear ambient capabilities: %w", err)
	}
	return nil
}

// privateTmpKeepPaths lists the policy paths under /tmp, outermost only.
func privateTmpKeepPaths(policy LandlockPolicy) []string {
	candidates := append(append(append([]string{}, policy.ReadPaths...), policy.WritePaths...), policy.WorkDir)
	if !policy.BrowserScoped {
		candidates = append(candidates, browserSocketDir)
	}
	var under []string
	for _, path := range candidates {
		clean := filepath.Clean(path)
		if clean != "/tmp" && strings.HasPrefix(clean, "/tmp/") {
			under = append(under, clean)
		}
	}
	sort.Slice(under, func(i, j int) bool { return len(under[i]) < len(under[j]) })
	var out []string
	for _, path := range under {
		covered := false
		for _, kept := range out {
			if path == kept || strings.HasPrefix(path, kept+"/") {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, path)
		}
	}
	return out
}

// applyReadOnlyOverlays bind-mounts each blocked-write path onto itself
// read-only, so writes, deletes and renames there fail even though Landlock
// grants write on the folder around it. The command cannot undo the mounts:
// it runs without CAP_SYS_ADMIN, and mounts made in a user namespace are
// locked to it. A path that vanished since the policy was built is skipped,
// as the mount-namespace backend does.
func applyReadOnlyOverlays(paths []string) error {
	for _, path := range paths {
		if err := unix.Mount(path, path, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
			if errors.Is(err, unix.ENOENT) {
				continue
			}
			return fmt.Errorf("protect %s: %w", path, err)
		}
		// A remount must keep the flags the kernel locked on the source
		// mount (nosuid, nodev, ...), or it is refused in a user namespace.
		var st unix.Statfs_t
		if err := unix.Statfs(path, &st); err != nil {
			return fmt.Errorf("protect %s: %w", path, err)
		}
		keep := uintptr(st.Flags) & (unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC | unix.MS_NOATIME | unix.MS_NODIRATIME | unix.MS_RELATIME)
		if err := unix.Mount("", path, "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY|keep, ""); err != nil {
			return fmt.Errorf("protect %s read-only: %w", path, err)
		}
	}
	return nil
}

// applyHiddenPaths mounts an empty placeholder (mode 000, read-only) over each blocked path, so the command can neither
// read nor change the real file or folder although Landlock grants the folder around it. The placeholders are made in
// the private /tmp and unlinked once mounted, so nothing the command can reach refers to them. A path that vanished
// since the policy was built is skipped, like a read-only overlay.
func applyHiddenPaths(paths []string) error {
	for i, path := range paths {
		var st unix.Stat_t
		if err := unix.Lstat(path, &st); err != nil {
			if errors.Is(err, unix.ENOENT) {
				continue
			}
			return fmt.Errorf("hide %s: %w", path, err)
		}
		source := filepath.Join("/tmp", fmt.Sprintf(".agentworks-hidden-%d", i))
		if st.Mode&unix.S_IFMT == unix.S_IFDIR {
			if err := os.Mkdir(source, 0o000); err != nil {
				return fmt.Errorf("hide %s: %w", path, err)
			}
		} else {
			f, err := os.OpenFile(source, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o000)
			if err != nil {
				return fmt.Errorf("hide %s: %w", path, err)
			}
			_ = f.Close()
		}
		if err := unix.Mount(source, path, "", unix.MS_BIND, ""); err != nil {
			return fmt.Errorf("hide %s: %w", path, err)
		}
		var fs unix.Statfs_t
		if err := unix.Statfs(path, &fs); err != nil {
			return fmt.Errorf("hide %s: %w", path, err)
		}
		keep := uintptr(fs.Flags) & (unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC | unix.MS_NOATIME | unix.MS_NODIRATIME | unix.MS_RELATIME)
		if err := unix.Mount("", path, "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY|keep, ""); err != nil {
			return fmt.Errorf("hide %s read-only: %w", path, err)
		}
		if st.Mode&unix.S_IFMT == unix.S_IFDIR {
			_ = os.Remove(source)
		} else {
			_ = unix.Unlink(source)
		}
	}
	return nil
}
