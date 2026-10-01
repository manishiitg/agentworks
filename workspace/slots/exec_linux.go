//go:build linux

package slots

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"

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
	allowed := false
	for _, program := range cfg.AllowedExec {
		// A pattern may use * for one path segment (release folders change at every deploy).
		if program == req.Argv[0] {
			allowed = true
			break
		}
		if ok, _ := filepath.Match(program, req.Argv[0]); ok && strings.Contains(program, "*") {
			allowed = true
			break
		}
	}
	if !allowed {
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
	body, err := io.ReadAll(io.LimitReader(stdin, maxRequestBytes+1))
	if err != nil || len(body) > maxRequestBytes {
		fmt.Fprintln(stderr, "slotctl: could not read the request")
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
	if policyWriter != nil {
		go func() {
			_, _ = policyWriter.WriteString(req.FD3)
			_ = policyWriter.Close()
		}()
	}
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		for sig := range signals {
			_ = cmd.Process.Signal(sig)
		}
	}()
	waitErr := cmd.Wait()
	signal.Stop(signals)
	close(signals)
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
