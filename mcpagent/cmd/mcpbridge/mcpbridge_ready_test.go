package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestMakeReadyFileWriter pins the marker-writer contract: no-op on empty path,
// creates the file (with parent dirs) on first call, idempotent afterwards.
func TestMakeReadyFileWriter(t *testing.T) {
	// Empty path is a no-op and must not panic.
	makeReadyFileWriter("")()

	marker := filepath.Join(t.TempDir(), "nested", "ready.marker")
	write := makeReadyFileWriter(marker)

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker should not exist before the first tools/list; stat err=%v", err)
	}
	write()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker not created on first call: %v", err)
	}
	// Idempotent: a second tools/list must not error or rewrite-explode.
	write()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker missing after second call: %v", err)
	}
}

// TestMcpbridgeWritesReadyFileOnToolsList runs the REAL bridge binary, speaks the
// MCP stdio handshake to it (initialize → initialized → tools/list), and asserts
// it creates MCP_READY_FILE. This is the ground-truth proof that the adapter's
// readiness gate has something real to wait on: the marker appears exactly when
// the CLI would have finished discovering the tools.
func TestMcpbridgeWritesReadyFileOnToolsList(t *testing.T) {
	// Build the bridge from current source into a temp path (tests THIS code,
	// not a possibly-stale ~/go/bin binary).
	bin := filepath.Join(t.TempDir(), "mcpbridge")
	//nolint:gosec // G204: test-controlled temp output path, constant "go build" command.
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build mcpbridge: %v\n%s", err, out)
	}

	marker := filepath.Join(t.TempDir(), "ready.marker")
	//nolint:gosec // G204: bin is the test's own freshly built binary in a temp dir.
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"MCP_API_URL=http://127.0.0.1:1/unused",
		"MCP_API_TOKEN=test-token",
		"MCP_TOOLS_FILE="+writeBridgeToolsFileForTest(t, []byte(`[{"name":"echo","description":"echo tool","type":"custom"}]`)),
		"MCP_READY_FILE="+marker,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bridge: %v", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// Drain stdout so the server's responses never block on a full pipe.
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
		}
	}()

	// Newline-delimited JSON-RPC: initialize, initialized notification, tools/list.
	handshake := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"ready-test","version":"0"}}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	if _, err := stdin.Write([]byte(handshake)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	// The marker must appear shortly after tools/list is answered.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			return // success: bridge wrote the readiness marker on tools/list
		}
		if time.Now().After(deadline) {
			t.Fatalf("bridge did not create MCP_READY_FILE %q within 5s of tools/list", marker)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
