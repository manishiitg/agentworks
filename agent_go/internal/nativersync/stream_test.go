package nativersync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// PLAT-856-442: a 300+ MB native journal must transfer as deltas on the
// second move, and return with identical bytes through real rsync processes.
func TestLargeJournalRoundtripUsesRsyncDeltas(t *testing.T) {
	if _, e := Binary(); e != nil {
		t.Skip(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	source, destination := t.TempDir(), t.TempDir()
	path := filepath.Join(source, "session.jsonl")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	block := bytes.Repeat([]byte("native-journal-fixture\n"), 50000)
	for n := 0; n < 310; n++ {
		if _, e = f.Write(block); e != nil {
			t.Fatal(e)
		}
	}
	f.Close()
	transfer := func(upload bool) (int64, int64) {
		t.Helper()
		started := time.Now()
		result := make(chan error, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if e != nil {
				result <- e
				return
			}
			defer conn.Close()
			result <- Server(ctx, destination, !upload, &Socket{Conn: conn})
		}))
		defer server.Close()
		conn, _, e := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
		if e != nil {
			t.Fatal(e)
		}
		defer conn.Close()
		stream := &Socket{Conn: conn}
		if e = Client(ctx, source, upload, stream); e != nil {
			conn.Close()
			t.Fatalf("client: %v; server: %v", e, <-result)
		}
		if e = <-result; e != nil {
			t.Fatal(e)
		}
		t.Logf("upload=%t sent=%d received=%d elapsed=%s", upload, stream.Sent, stream.Received, time.Since(started))
		return stream.Sent, stream.Received
	}
	firstSent, _ := transfer(true)
	completed := filepath.Join(t.TempDir(), "session.jsonl")
	if e := os.Link(filepath.Join(destination, "session.jsonl"), completed); e != nil {
		t.Fatal(e)
	}
	completedInfo, _ := os.Stat(completed)
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	f.Write([]byte("new-local-turn\n"))
	f.Close()
	sent, received := transfer(true)
	if info, e := os.Stat(completed); e != nil || info.Size() != completedInfo.Size() {
		t.Fatal("next transfer changed the completed mirror")
	}
	if firstSent < 300<<20 || sent+received > 1<<20 {
		t.Fatalf("not a delta: initial=%d update=%d+%d", firstSent, sent, received)
	}
	remote := filepath.Join(destination, "session.jsonl")
	f, _ = os.OpenFile(remote, os.O_APPEND|os.O_WRONLY, 0600)
	f.Write([]byte("new-server-turn\n"))
	f.Close()
	sent, received = transfer(false)
	if sent+received > 1<<20 {
		t.Fatalf("return copied the whole journal: %d+%d", sent, received)
	}
	hash := func(p string) []byte {
		f, e := os.Open(p)
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		h := sha256.New()
		if _, e = io.Copy(h, f); e != nil {
			t.Fatal(e)
		}
		return h.Sum(nil)
	}
	if !bytes.Equal(hash(path), hash(remote)) {
		t.Fatal("roundtrip changed journal bytes")
	}
	t.Logf("initial=%d bytes; return delta=%d bytes", firstSent, sent+received)
}

func TestMain(m *testing.M) {
	if RunPipe(os.Args[1:]) {
		return
	}
	os.Exit(m.Run())
}

// The peer controls rsync protocol bytes, so filesystem confinement must hold
// even if a future rsync parser accepts a path outside the private stage.
func TestRsyncCannotReadOrWriteOutsideStaging(t *testing.T) {
	if _, e := Binary(); e != nil {
		t.Skip(e)
	}
	stage, other := t.TempDir(), t.TempDir()
	own := filepath.Join(stage, "own.txt")
	foreign := filepath.Join(other, "private.txt")
	if e := os.WriteFile(own, []byte("own"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(foreign, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{foreign, filepath.Join(stage, "stolen.txt")}, {own, filepath.Join(other, "escaped.txt")}} {
		command, cleanup, e := Command(t.Context(), stage, args, nil)
		if e != nil {
			t.Fatal(e)
		}
		out, e := command.CombinedOutput()
		cleanup()
		if e == nil || (!bytes.Contains(out, []byte("Operation not permitted")) && !bytes.Contains(out, []byte("Permission denied"))) {
			t.Fatalf("not a confinement refusal: %v %s", e, out)
		}
	}
	if _, e := os.Stat(filepath.Join(stage, "stolen.txt")); !os.IsNotExist(e) {
		t.Fatal("read another private file")
	}
	if _, e := os.Stat(filepath.Join(other, "escaped.txt")); !os.IsNotExist(e) {
		t.Fatal("wrote outside staging")
	}
}
