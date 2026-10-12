package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type cursorProcess struct {
	PID, Parent int
	Host, Kind  string
}

// argv0 alone is not sufficient: the real kernel image and script must be the
// Node/index.js next to the resolved installed launcher. Same-user runtime
// tampering remains outside this routing check; it grants no account authority.
func cursorNodeEntry(argv0, image, entry string) error {
	name := filepath.Base(argv0)
	if name != "cursor-agent" && name != "agent" && name != "cursor" || !filepath.IsAbs(argv0) {
		return errors.New("not an installed Cursor launcher")
	}
	launcher, err := filepath.EvalSymlinks(argv0)
	if err != nil {
		return err
	}
	dir := filepath.Dir(launcher)
	if filepath.Base(launcher) != "cursor-agent" || filepath.Base(filepath.Dir(dir)) != "versions" ||
		filepath.Base(filepath.Dir(filepath.Dir(dir))) != "cursor-agent" {
		return errors.New("not a Cursor version installation")
	}
	for path, expected := range map[string]string{image: filepath.Join(dir, "node"), entry: filepath.Join(dir, "index.js")} {
		resolved, e := filepath.EvalSymlinks(path)
		want, w := filepath.EvalSymlinks(expected)
		if e != nil || w != nil || resolved != want {
			return errors.New("Cursor kernel image or entry does not match its launcher")
		}
	}
	for _, path := range []string{launcher, filepath.Join(dir, "node"), filepath.Join(dir, "index.js")} {
		info, e := os.Stat(path)
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
			return errors.New("unsafe Cursor installation file")
		}
	}
	return nil
}

func cursorLeadingEntry(reader io.Reader) (string, string, error) {
	r := io.LimitReader(reader, 16<<10)
	readArg := func() (string, error) {
		var arg strings.Builder
		var b [1]byte
		for arg.Len() < 4096 {
			if _, err := io.ReadFull(r, b[:]); err != nil {
				return "", err
			}
			if b[0] == 0 {
				return arg.String(), nil
			}
			arg.WriteByte(b[0])
		}
		return "", errors.New("oversized Cursor launch prefix")
	}
	argv0, err := readArg()
	if err != nil {
		return "", "", err
	}
	switch filepath.Base(argv0) {
	case "cursor-agent", "agent", "cursor":
	default:
		return "", "", errors.New("not a Cursor launcher")
	}
	// Read only the launch prefix, stopping at index.js. Never consume model
	// prompt arguments, API-key arguments or the process environment.
	for i := 0; i < 3; i++ {
		arg, e := readArg()
		if e != nil {
			return "", "", e
		}
		if arg == "--use-system-ca" {
			continue
		}
		if filepath.Base(arg) == "index.js" && filepath.IsAbs(arg) {
			return argv0, arg, nil
		}
		return "", "", errors.New("unverified Cursor Node entry")
	}
	return "", "", errors.New("missing Cursor Node entry")
}

func cursorAncestor() (cursorProcess, error) {
	pid := os.Getppid()
	for i := 0; i < 16 && pid >= 1; i++ {
		process, err := cursorProcessAt(pid)
		if err == nil && process.Host != "" {
			return process, nil
		}
		if process.Parent <= 1 || process.Parent == pid {
			break
		}
		pid = process.Parent
	}
	return cursorProcess{}, errors.New("cannot verify this exact Cursor process")
}

func cursorProcessForHost(host string) bool {
	// A host key includes its PID for a bounded exact recheck, never a scan or
	// selection of the newest process. The digest includes kernel birth data.
	parts := strings.Split(host, ":")
	if len(parts) != 3 || parts[0] != "cursor" || len(parts[2]) != 64 {
		return false
	}
	pid, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	process, err := cursorProcessAt(pid)
	return err == nil && process.Host == host
}

func cursorHostKey(pid int, digest string) string {
	return fmt.Sprintf("cursor:%d:%s", pid, digest)
}

func cursorAwareHostAlive(provider, host string) bool {
	if provider == "cursor" {
		return cursorProcessForHost(host)
	}
	return providerHostAlive(provider, host)
}
