package main

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// cursorProcArgsPrefix reads the launch prefix of a macOS KERN_PROCARGS2 buffer:
// the executable path and argv[0] up to the Node entry script. Layout: argc
// (int32), the executable path, NUL padding, then argv and the environment. Only
// the prefix is parsed; later arguments (prompt, API key) and the environment are
// never read.
func cursorProcArgsPrefix(raw []byte) (execPath, argv0, entry string, err error) {
	if len(raw) < 8 {
		return "", "", "", errors.New("short process argument record")
	}
	if argc := int32(binary.LittleEndian.Uint32(raw[:4])); argc < 2 {
		return "", "", "", errors.New("process has no Node entry argument")
	}
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end <= 0 {
		return "", "", "", errors.New("missing executable path")
	}
	execPath = string(rest[:end])
	rest = bytes.TrimLeft(rest[end:], "\x00")
	argv0, entry, err = cursorLeadingEntry(bytes.NewReader(rest))
	if err != nil {
		return "", "", "", err
	}
	return execPath, argv0, entry, nil
}
