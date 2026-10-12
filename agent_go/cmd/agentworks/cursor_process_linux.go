//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func cursorProcessAt(pid int) (cursorProcess, error) {
	process := cursorProcess{PID: pid}
	root := filepath.Join("/proc", strconv.Itoa(pid))
	raw, err := os.ReadFile(filepath.Join(root, "stat"))
	if err != nil {
		return process, err
	}
	end := strings.LastIndex(string(raw), ") ")
	if end < 0 {
		return process, errors.New("invalid kernel process record")
	}
	fields := strings.Fields(string(raw)[end+2:])
	if len(fields) < 20 {
		return process, errors.New("incomplete kernel process record")
	}
	process.Parent, err = strconv.Atoi(fields[1])
	if err != nil {
		return process, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return process, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return process, errors.New("Cursor process belongs to another OS user")
	}
	args, err := os.Open(filepath.Join(root, "cmdline"))
	if err != nil {
		return process, err
	}
	defer args.Close()
	argv0, entry, err := cursorLeadingEntry(args)
	if err != nil {
		return process, err
	}
	image, err := os.Readlink(filepath.Join(root, "exe"))
	if err != nil || cursorNodeEntry(argv0, image, entry) != nil {
		return process, errors.New("Cursor executable/entry could not be verified")
	}
	after, e := os.ReadFile(filepath.Join(root, "stat"))
	if e != nil || string(after) == "" {
		return process, errors.New("Cursor process disappeared during verification")
	}
	afterEnd := strings.LastIndex(string(after), ") ")
	if afterEnd < 0 {
		return process, errors.New("invalid kernel process record")
	}
	afterFields := strings.Fields(string(after)[afterEnd+2:])
	if len(afterFields) < 20 || afterFields[19] != fields[19] {
		return process, errors.New("Cursor process changed during verification")
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return process, err
	}
	h := sha256.Sum256([]byte(strconv.Itoa(pid) + ":" + fields[19] + ":" + strings.TrimSpace(string(boot)) + ":" + image))
	process.Host, process.Kind = cursorHostKey(pid, hex.EncodeToString(h[:])), "cli"
	return process, nil
}

func cursorPIDDefinitelyGone(pid int) bool {
	_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	return errors.Is(err, os.ErrNotExist)
}
