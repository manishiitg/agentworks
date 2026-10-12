//go:build darwin

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// A Cursor CLI conversation runs as the installed Node binary executing the
// launcher's index.js. The kernel image, the leading arguments and the launcher
// must all agree; the process name alone proves nothing. Identity includes the
// kernel start time and this boot, so a reused PID is a different identity.
func cursorCLIProcessAt(pid int, info *unix.KinfoProc) (cursorProcess, error) {
	process := cursorProcess{PID: pid, Parent: int(info.Eproc.Ppid)}
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return process, errors.New("cannot read the Cursor launch arguments")
	}
	_, argv0, entry, err := cursorProcArgsPrefix(raw)
	if err != nil {
		return process, err
	}
	ctxImage, cancelImage := context.WithTimeout(context.Background(), time.Second)
	defer cancelImage()
	images, err := exec.CommandContext(ctxImage, "/usr/sbin/lsof", "-a", "-p", strconv.Itoa(pid), "-d", "txt", "-Fn").Output()
	if err != nil {
		return process, errors.New("cannot verify the Cursor executable image")
	}
	image := ""
	for _, line := range strings.Split(string(images), "\n") {
		if strings.HasPrefix(line, "n") {
			image = strings.TrimPrefix(line, "n")
			break // the first text mapping is the executable itself
		}
	}
	if image == "" || cursorNodeEntry(argv0, image, entry) != nil {
		return process, errors.New("Cursor executable/entry could not be verified")
	}
	after, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || after.Proc.P_starttime != info.Proc.P_starttime {
		return process, errors.New("Cursor process changed during verification")
	}
	boot, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return process, err
	}
	birth := strconv.FormatInt(info.Proc.P_starttime.Sec, 10) + "." + strconv.FormatInt(int64(info.Proc.P_starttime.Usec), 10)
	h := sha256.Sum256([]byte(strconv.Itoa(pid) + ":" + birth + ":" + strconv.FormatInt(boot.Sec, 10) + ":" + image))
	process.Host, process.Kind = cursorHostKey(pid, hex.EncodeToString(h[:])), "cli"
	return process, nil
}

// Desktop hooks and shell tools share the verified app ancestor, but every
// request still needs its own native UUID. No shared MCP child is enabled.
func cursorProcessAt(pid int) (cursorProcess, error) {
	process := cursorProcess{PID: pid}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || info.Proc.P_pid != int32(pid) {
		return process, errors.New("unavailable Cursor process metadata")
	}
	process.Parent = int(info.Eproc.Ppid)
	if info.Eproc.Ucred.Uid != uint32(os.Geteuid()) {
		return process, errors.New("Cursor process belongs to another OS user")
	}
	command := strings.TrimRight(string(info.Proc.P_comm[:]), "\x00")
	if command == "node" {
		return cursorCLIProcessAt(pid, info)
	}
	if !strings.Contains(command, "Cursor") {
		return process, errors.New("not a Cursor app process")
	}
	ctxImage, cancelImage := context.WithTimeout(context.Background(), time.Second)
	defer cancelImage()
	// Kernel text-image records avoid reading prompt arguments or environment.
	images, err := exec.CommandContext(ctxImage, "/usr/sbin/lsof", "-a", "-p", strconv.Itoa(pid), "-d", "txt", "-Fn").Output()
	if err != nil {
		return process, errors.New("cannot verify Cursor app image")
	}
	for _, line := range strings.Split(string(images), "\n") {
		if !strings.HasPrefix(line, "n") {
			continue
		}
		image, e := filepath.EvalSymlinks(strings.TrimPrefix(line, "n"))
		if e != nil || !strings.HasSuffix(image, "/Cursor.app/Contents/MacOS/Cursor") {
			continue
		}
		file, e := os.Stat(image)
		if e != nil || !file.Mode().IsRegular() || file.Mode().Perm()&0022 != 0 {
			continue
		}
		after, e := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if e != nil || after.Proc.P_starttime != info.Proc.P_starttime {
			return process, errors.New("Cursor process changed during verification")
		}
		birth := strconv.FormatInt(info.Proc.P_starttime.Sec, 10) + "." + strconv.FormatInt(int64(info.Proc.P_starttime.Usec), 10)
		h := sha256.Sum256([]byte(strconv.Itoa(pid) + ":" + birth + ":" + image))
		process.Host, process.Kind = cursorHostKey(pid, hex.EncodeToString(h[:])), "desktop"
		return process, nil
	}
	return process, errors.New("not a verified Cursor app image")
}

func cursorPIDDefinitelyGone(pid int) bool {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	return errors.Is(err, unix.ESRCH) || err == nil && info.Proc.P_pid != int32(pid)
}
