//go:build linux

package slots

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func within(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	return candidate == root || strings.HasPrefix(candidate, root+string(filepath.Separator))
}

// Validate checks a request against the allow-list and returns the resolved working folder.
func (cfg ExecConfig) Validate(req ExecRequest) (cwd string, err error) {
	if len(req.Argv) == 0 || !filepath.IsAbs(req.Argv[0]) || filepath.Clean(req.Argv[0]) != req.Argv[0] {
		return "", errors.New("the program must be an absolute, clean path")
	}
	if !cfg.AllowsProgram(req.Argv[0]) {
		return "", fmt.Errorf("%s is not an allowed program", req.Argv[0])
	}
	if req.Argv[0] == TmuxPath {
		// tmux for a slot: only that slot's own socket, so the request cannot reach another slot's server.
		me, uerr := user.Current()
		if uerr != nil || !ValidSlot(me.Username) || cfg.SlotRunRoot == "" {
			return "", errors.New("tmux is only run as a slot account")
		}
		if len(req.Argv) < 4 || req.Argv[1] != "-S" || req.Argv[2] != SlotSocket(cfg.SlotRunRoot, me.Username) {
			return "", errors.New("tmux must use this slot's own socket")
		}
	}
	if req.Argv[0] == ChmodPath {
		// Only "chmod 660 <this slot's own tmux socket>": tmux makes its socket owner-only, and the
		// platform (in the slot's group) needs group access to drive the terminal.
		me, uerr := user.Current()
		if uerr != nil || !ValidSlot(me.Username) || cfg.SlotRunRoot == "" {
			return "", errors.New("chmod is only run as a slot account")
		}
		if len(req.Argv) != 3 || req.Argv[1] != "660" || req.Argv[2] != SlotSocket(cfg.SlotRunRoot, me.Username) {
			return "", errors.New("chmod may only open this slot's own tmux socket to its group")
		}
	}
	for _, entry := range req.Env {
		if !strings.Contains(entry, "=") || strings.ContainsRune(entry, 0) {
			return "", errors.New("malformed environment entry")
		}
	}
	for _, arg := range req.Argv {
		if strings.ContainsRune(arg, 0) {
			return "", errors.New("malformed argument")
		}
	}
	if !filepath.IsAbs(req.Cwd) {
		return "", errors.New("the working folder must be an absolute path")
	}
	resolved, err := filepath.EvalSymlinks(req.Cwd)
	if err != nil {
		return "", fmt.Errorf("working folder: %w", err)
	}
	for _, root := range cfg.AllowedCwd {
		if rootResolved, rerr := filepath.EvalSymlinks(root); rerr == nil && within(rootResolved, resolved) {
			return resolved, nil
		}
	}
	return "", errors.New("the working folder is outside the allowed folders")
}

// namespaceAttr starts the program in its own user and mount namespaces with the slot's own
// identity mapped to itself: the same view the shell tool's launcher builds for a private /tmp.
func namespaceAttr() *syscall.SysProcAttr {
	uid, gid := os.Getuid(), os.Getgid()
	return &syscall.SysProcAttr{
		Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS,
		UidMappings: []syscall.SysProcIDMap{{ContainerID: uid, HostID: uid, Size: 1}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: gid, HostID: gid, Size: 1}},
		AmbientCaps: []uintptr{unix.CAP_SYS_ADMIN},
	}
}

// RunExec reads one request from stdin, validates it and runs the program, returning its exit
// code. Output goes straight to stdout and stderr. It is what `slotctl exec` runs, as the slot.
func RunExec(stdin io.Reader, stdout, stderr *os.File, cfg ExecConfig) int {
	decoder := json.NewDecoder(io.LimitReader(stdin, maxRequestBytes+1))
	decoder.DisallowUnknownFields()
	var request ExecRequest
	if err := decoder.Decode(&request); err != nil {
		fmt.Fprintln(stderr, "slotctl: could not read the request")
		return 125
	}
	if request.NativeSession != nil {
		me, err := user.Current()
		if err != nil || os.Getuid() == 0 || me.Uid != fmt.Sprint(os.Getuid()) || len(request.Argv) != 0 || request.Cwd != "" || len(request.Env) != 0 || request.FD3 != "" || request.Userns {
			fmt.Fprintln(stderr, "slotctl: native session requires its assigned OS account")
			return 125
		}
		cfg, err = LoadNativeExecConfig(ConfigBesideExecutable())
		if err != nil {
			fmt.Fprintln(stderr, "slotctl:", err)
			return 125
		}
		if _, err = request.NativeSession.NativeRuntime(cfg, me.Username); err != nil {
			fmt.Fprintln(stderr, "slotctl:", err)
			return 125
		}
		stream := bufio.NewReader(io.MultiReader(decoder.Buffered(), stdin))
		if c, e := stream.ReadByte(); e != nil || c != '\n' {
			fmt.Fprintln(stderr, "slotctl: invalid native header framing")
			return 125
		}
		exe, err := os.Executable()
		if err != nil {
			return 125
		}
		helper := filepath.Join(filepath.Dir(exe), "native-session")
		info, err := os.Lstat(helper)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
			fmt.Fprintln(stderr, "slotctl: native helper unavailable")
			return 125
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 {
			fmt.Fprintln(stderr, "slotctl: native helper must be root-owned")
			return 125
		}
		header, err := json.Marshal(request.NativeSession)
		if err != nil {
			return 125
		}
		cmd := exec.Command(helper)
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		cmd.Stdin = io.MultiReader(bytes.NewReader(append(header, '\n')), stream)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err = cmd.Run(); err != nil {
			fmt.Fprintln(stderr, "slotctl: native helper:", err)
			return 125
		}
		return 0
	}
	body, err := json.Marshal(request)
	if err != nil {
		return 125
	}
	return runBody(body, nil, stdout, stderr, cfg)
}

// RunExecFile is RunExec for a request left in a file, so the program keeps the caller's standard
// input (a coding CLI reads its prompt there). The file must be a regular file inside this slot's
// own run folder; it is removed once read.
func RunExecFile(path string, childStdin *os.File, stdout, stderr *os.File, cfg ExecConfig) int {
	me, err := user.Current()
	if err != nil || !ValidSlot(me.Username) || cfg.SlotRunRoot == "" {
		fmt.Fprintln(stderr, "slotctl: request files are only read as a slot account")
		return 126
	}
	dir := filepath.Join(cfg.SlotRunRoot, me.Username)
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || !within(dir, clean) {
		fmt.Fprintln(stderr, "slotctl: refused: the request file must be inside this slot's run folder")
		return 126
	}
	info, err := os.Lstat(clean)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxRequestBytes {
		fmt.Fprintln(stderr, "slotctl: refused: the request file is not a plain file of a sensible size")
		return 126
	}
	body, err := os.ReadFile(clean)
	_ = os.Remove(clean)
	if err != nil {
		fmt.Fprintln(stderr, "slotctl: could not read the request file")
		return 125
	}
	return runBody(body, childStdin, stdout, stderr, cfg)
}

func runBody(body []byte, childStdin *os.File, stdout, stderr *os.File, cfg ExecConfig) int {
	var req ExecRequest
	if err := json.Unmarshal(body, &req); err != nil {
		fmt.Fprintln(stderr, "slotctl: malformed request")
		return 125
	}
	cwd, err := cfg.Validate(req)
	if err != nil {
		fmt.Fprintf(stderr, "slotctl: refused: %v\n", err)
		return 126
	}
	// A new file or folder is private to the slot and its group (the platform's way in).
	syscall.Umask(0o007)

	cmd := exec.Command(req.Argv[0], req.Argv[1:]...)
	cmd.Dir = cwd
	cmd.Env = req.Env
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.Stdin = childStdin
	if req.Userns {
		cmd.SysProcAttr = namespaceAttr()
	}
	// The program runs in its own process group so a stop reaches everything it started. The platform cannot
	// signal a slot's processes itself (they belong to another account), so this program, which runs as the slot,
	// is the only one that can: a stop signal goes to the whole group, and a command that does not leave within
	// the grace period is killed.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	var policyWriter *os.File
	if req.FD3 != "" {
		reader, writer, perr := os.Pipe()
		if perr != nil {
			fmt.Fprintln(stderr, "slotctl: could not prepare the sandbox policy")
			return 125
		}
		cmd.ExtraFiles = []*os.File{reader}
		policyWriter = writer
		defer reader.Close()
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "slotctl: could not start: %v\n", err)
		return 127
	}
	if req.Argv[0] != TmuxPath && req.Argv[0] != ChmodPath {
		moveToAppScope(cmd.Process.Pid)
	}
	if policyWriter != nil {
		go func() {
			_, _ = policyWriter.WriteString(req.FD3)
			_ = policyWriter.Close()
		}()
	}
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	var stopped atomic.Bool
	go func() {
		for sig := range signals {
			stopped.Store(true)
			pgid := cmd.Process.Pid
			if sysSig, ok := sig.(syscall.Signal); ok {
				_ = syscall.Kill(-pgid, sysSig)
			} else {
				_ = cmd.Process.Signal(sig)
			}
			time.AfterFunc(StopGrace, func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
		}
	}()
	waitErr := cmd.Wait()
	signal.Stop(signals)
	close(signals)
	// The SIGKILL timer above lives in this process, which exits as soon as the program does. After a stop, a child that
	// ignores SIGTERM (a script with a trap, and the Chrome it started) would then run on for hours: wait out the grace
	// period here and kill what is left of the group before returning (PLAT-805). A program that was not stopped may
	// leave background jobs behind on purpose, so a normal exit leaves the group alone.
	if stopped.Load() {
		endProcessGroup(cmd.Process.Pid)
	}
	if waitErr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
		return exitErr.ExitCode()
	}
	fmt.Fprintf(stderr, "slotctl: %v\n", waitErr)
	return 125
}

// endProcessGroup waits up to StopGrace for every member of the process group to leave, then kills the rest.
func endProcessGroup(pgid int) {
	gone := func() bool { return syscall.Kill(-pgid, 0) != nil } // ESRCH: no member left
	for deadline := time.Now().Add(StopGrace); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if gone() {
			return
		}
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	for i := 0; i < 40 && !gone(); i++ {
		time.Sleep(50 * time.Millisecond)
	}
}

// BusctlPath asks the slot's own user manager for a scope.
const BusctlPath = "/usr/bin/busctl"

// App scopes cap what one slot program and the apps it leaves running may use.
const (
	appScopeMemoryMax = 4 << 30
	appScopeTasksMax  = 512
)

// moveToAppScope puts a just-started slot program in its own scope in the slot's user manager
// (PLAT-856-506). The program keeps its process group, sandbox and stop handling; only its cgroup
// moves out of the platform service, so a program it detached (setsid, nohup, pm2, docker -d) keeps
// running when the platform restarts. The slot can list them with systemctl --user list-units 'aw-app-*'.
// Best effort and never a second start: on failure the program simply runs as before.
func moveToAppScope(pid int) {
	uid := strconv.Itoa(os.Getuid())
	runtimeDir := "/run/user/" + uid
	if _, err := os.Stat(filepath.Join(runtimeDir, "bus")); err != nil {
		return
	}
	unit := fmt.Sprintf("aw-app-%d.scope", pid)
	cmd := exec.Command(BusctlPath, "--user", "--quiet", "call", "org.freedesktop.systemd1", "/org/freedesktop/systemd1",
		"org.freedesktop.systemd1.Manager", "StartTransientUnit", "ssa(sv)a(sa(sv))", unit, "fail", "5",
		"PIDs", "au", "1", strconv.Itoa(pid),
		"Slice", "s", "aw-apps.slice",
		"MemoryMax", "t", strconv.Itoa(appScopeMemoryMax),
		"TasksMax", "t", strconv.Itoa(appScopeTasksMax),
		"CollectMode", "s", "inactive-or-failed",
		"0")
	cmd.Env = []string{"XDG_RUNTIME_DIR=" + runtimeDir, "DBUS_SESSION_BUS_ADDRESS=unix:path=" + filepath.Join(runtimeDir, "bus")}
	_ = cmd.Run()
}
