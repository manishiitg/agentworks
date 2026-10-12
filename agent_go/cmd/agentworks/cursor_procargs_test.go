package main

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func procArgs(argc int32, execPath string, padding int, args ...string) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, argc)
	b.WriteString(execPath)
	b.Write(make([]byte, 1+padding))
	for _, a := range args {
		b.WriteString(a)
		b.WriteByte(0)
	}
	return b.Bytes()
}

// The macOS launch record is read only up to the Node entry script; the prompt
// and the environment that follow are never returned or required to parse.
func TestCursorProcArgsPrefixReadsOnlyTheLaunchPrefix(t *testing.T) {
	good := procArgs(4, "/u/.local/share/cursor-agent/versions/v1/node", 5,
		"/u/.local/bin/cursor-agent", "--use-system-ca", "/u/.local/share/cursor-agent/versions/v1/index.js", "SECRET PROMPT TEXT", "API_KEY=do-not-read")
	exec, argv0, entry, err := cursorProcArgsPrefix(good)
	if err != nil || argv0 != "/u/.local/bin/cursor-agent" || entry != "/u/.local/share/cursor-agent/versions/v1/index.js" || !strings.HasSuffix(exec, "/node") {
		t.Fatalf("launch prefix: %q %q %q %v", exec, argv0, entry, err)
	}
	if strings.Contains(exec+argv0+entry, "SECRET") || strings.Contains(exec+argv0+entry, "API_KEY") {
		t.Fatal("the prompt or environment leaked into the parsed prefix")
	}
	for name, raw := range map[string][]byte{
		"not cursor":    procArgs(3, "/usr/bin/node", 0, "/usr/bin/node", "/tmp/evil.js", "x"),
		"relative":      procArgs(3, "/n/node", 0, "cursor-agent", "index.js", "x"),
		"no entry":      procArgs(1, "/n/node", 0, "/u/.local/bin/cursor-agent"),
		"truncated":     {1, 0},
		"wrong script":  procArgs(3, "/n/node", 0, "/u/.local/bin/cursor-agent", "/tmp/other.js", "x"),
		"entry too far": procArgs(5, "/n/node", 0, "/u/.local/bin/cursor-agent", "--use-system-ca", "--inspect", "/u/index.js", "x"),
	} {
		if _, _, _, e := cursorProcArgsPrefix(raw); e == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}
