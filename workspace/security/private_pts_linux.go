//go:build linux

package security

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

const privatePTSChildEnv = "AGENTWORKS_PRIVATE_PTS_CHILD"

// Agy opens /dev/ptmx and a new /dev/pts/<n>. Granting the host's /dev/pts
// would grant other chats' terminals (the CLIs share a service uid). Give
// this launch its own devpts instead. Re-exec with user/mount namespaces:
// Go cannot unshare a user namespace in its multi-threaded process.
func runPrivatePTSChild(policy LandlockPolicy, argv []string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	config, err := os.CreateTemp("", "agentworks-private-pts-*.json")
	if err != nil {
		return err
	}
	defer config.Close()
	defer os.Remove(config.Name())
	if err := config.Chmod(0600); err != nil {
		return err
	}
	if err := json.NewEncoder(config).Encode(policy); err != nil {
		return err
	}
	if _, err := config.Seek(0, 0); err != nil {
		return err
	}
	// Another command cannot replace the unlinked policy passed by fd.
	if err := os.Remove(config.Name()); err != nil {
		return err
	}
	args := append([]string{"--config", "/proc/self/fd/3", "--"}, argv...)
	child := exec.CommandContext(context.Background(), exe, args...)
	child.ExtraFiles = []*os.File{config}
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	child.Env = append(os.Environ(), privatePTSChildEnv+"=1")
	child.SysProcAttr = privateTmpSysProcAttr()
	child.SysProcAttr.Pdeathsig = syscall.SIGKILL
	if err := child.Run(); err != nil {
		return fmt.Errorf("private PTY launcher: %w", err)
	}
	return nil
}

func enterPrivatePTS() error {
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make PTY mounts private: %w", err)
	}
	if err := unix.Mount("devpts", "/dev/pts", "devpts", unix.MS_NOSUID|unix.MS_NOEXEC, "newinstance,ptmxmode=0666,mode=0600"); err != nil {
		return fmt.Errorf("mount private terminals: %w", err)
	}
	// A host with a separate ptmx device needs the private multiplexer bound
	// over it; Ubuntu's /dev/ptmx symlink already points into this mount.
	info, err := os.Lstat("/dev/ptmx")
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		if err := unix.Mount("/dev/pts/ptmx", "/dev/ptmx", "", unix.MS_BIND, ""); err != nil {
			return fmt.Errorf("bind private PTY multiplexer: %w", err)
		}
	}
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		return fmt.Errorf("clear PTY mount capabilities: %w", err)
	}
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	data := [2]unix.CapUserData{}
	if err := unix.Capset(&header, &data[0]); err != nil {
		return fmt.Errorf("drop PTY namespace capabilities: %w", err)
	}
	return nil
}
