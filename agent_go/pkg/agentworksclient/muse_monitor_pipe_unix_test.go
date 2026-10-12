//go:build !windows

package agentworksclient

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A Monitor reader exits when its broker generation ends, even on macOS where a
// non-blocking FIFO never reports end-of-file after the writer closes. Without
// this the old command lingered and Muse listed it as a live Monitor.
func TestMuseMonitorReaderEndsWithItsBroker(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "muse-monitor")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, uuid.NewString()+".json")
	session := uuid.NewString()
	record := MuseMonitorSubscription{Source: "agentworks-muse-watch", SessionID: session, AttachmentID: "attach-1", HostKey: "host", DataHome: root}
	holder, err := holdMuseMonitorBroker(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = createMuseMonitorFiles(path, record); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	writer, writerErr := make(chan *os.File, 1), make(chan error, 1)
	go func() {
		w, e := openMuseMonitorWriter(ctx, path)
		writer <- w
		writerErr <- e
	}()
	got, finished := make(chan MuseWatchHint, 1), make(chan error, 1)
	go func() {
		finished <- ReadMuseMonitorPipe(ctx, path, session, "attach-1", func(h MuseWatchHint) error { got <- h; return nil })
	}()
	w := <-writer
	if e := <-writerErr; e != nil {
		t.Fatal(e)
	}
	line, _ := json.Marshal(MuseWatchHint{Kind: "offer", Source: record.Source, SessionID: session, AttachmentID: "attach-1"})
	if _, err = w.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the reader never delivered the hint")
	}
	// The broker generation ends: writer and lock go away, the reader must follow.
	w.Close()
	holder.Close()
	select {
	case e := <-finished:
		if e != nil {
			t.Fatalf("reader should end cleanly with its broker: %v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the reader of a finished broker kept running")
	}
}
