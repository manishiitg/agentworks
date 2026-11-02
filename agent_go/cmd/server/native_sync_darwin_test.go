package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/nativersync"
)

// The backend test executable links the same audio dylibs as a go-run backend.
// Its native-sync-pipe entry must work before running ordinary test flags.
func TestMain(m *testing.M) {
	if nativersync.RunPipe(os.Args[1:]) {
		return
	}
	os.Exit(m.Run())
}

// PLAT-856-495: real rsync re-exec of the cgo backend failed before its pipe
// started because Seatbelt denied dylibs under Go's module cache. This uses
// the development layout (no bundled lib/) and the actual native pipe path.
func TestNativeRsyncMacDevHelperLoadsLinkedLibraries(t *testing.T) {
	if _, err := nativersync.Binary(); err != nil {
		t.Skip(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(executable), "lib")); !os.IsNotExist(err) {
		t.Skip("regression requires the development layout without bundled libraries")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	source, target := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "session.jsonl"), []byte("native session journal\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			result <- err
			return
		}
		defer conn.Close()
		result <- nativersync.Server(ctx, target, false, &nativersync.Socket{Conn: conn})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := nativersync.Client(ctx, source, true, &nativersync.Socket{Conn: conn}); err != nil {
		conn.Close()
		t.Fatalf("development native helper: %v; peer: %v", err, <-result)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(target, "session.jsonl")); err != nil || string(raw) != "native session journal\n" {
		t.Fatalf("journal transfer: %q %v", raw, err)
	}
}
