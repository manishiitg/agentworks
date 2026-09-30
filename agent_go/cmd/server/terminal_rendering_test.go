package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/liveattach"
)

func TestLiveAttachRenderingBoundsQueuedBytes(t *testing.T) {
	installFakeAttach(t, seedResponder(nil, []string{"screen"}, "0,0"))
	manager := newLiveAttachManager()
	stream, viewer, _, err := manager.addViewer(context.Background(), "render-byte-bound", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	// Fill with large frames while staying well below the frame-count bound.
	frame := make([]byte, 2*1024*1024)
	for count := 0; count < 10; count++ {
		stream.broadcast(frame)
	}
	bytes := 0
	for chunk := range viewer.ch {
		bytes += len(chunk.data)
	}
	if bytes != liveAttachSubMaxBytes {
		t.Fatalf("queued %d bytes, want a whole-stream drop at %d", bytes, liveAttachSubMaxBytes)
	}
	waitStreamDone(t, stream)
}

func TestLiveAttachRenderingANSIAssertionsHandlePrivateModes(t *testing.T) {
	if got := stripAnsiForTest("\x1b[1;30r\x1b[?6l\x1b[?7h\x1b[4l\x1b[10;1Htick-x-9"); got != "tick-x-9" {
		t.Fatalf("private-mode seed obscured the first live tick: %q", got)
	}
}

func TestLiveAttachRenderingRestoresScrollRegionAfterPainting(t *testing.T) {
	seed := string(buildLiveAttachSeed(liveattach.Reply{}, liveattach.Reply{Lines: []string{"header", "body", "footer"}},
		liveattach.Reply{Lines: []string{"4,5|1,1,1,0,0,0,0,0,0,2,9,1,0,1"}}))
	// Region starts at absolute row 2; an origin-relative CUP must restore
	// absolute cursor row 5 as relative row 3 (both become one-based on wire).
	wantTail := "header\r\nbody\r\nfooter\x1b[3;10r\x1b[?6h\x1b[?7l\x1b[4h\x1b[4;5H"
	if !strings.HasSuffix(seed, wantTail) {
		t.Fatalf("seed did not restore modes after painting, or cursor origin is wrong: %q", seed)
	}
	// Old cursor-only servers/tests still get a normal reset and absolute CUP.
	legacy := string(buildLiveAttachSeed(liveattach.Reply{}, liveattach.Reply{Lines: []string{"screen"}}, liveattach.Reply{Lines: []string{"4,5"}}))
	if !strings.HasSuffix(legacy, "screen\x1b[6;5H") {
		t.Fatalf("legacy seed changed: %q", legacy)
	}
}

func TestLiveAttachRenderingBlockedSocketWriteExpires(t *testing.T) {
	result := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			result <- err
			return
		}
		defer conn.Close()
		if tcp, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
			_ = tcp.SetWriteBuffer(1024)
		}
		// The peer deliberately never reads. A large frame must eventually fill
		// the network buffers and return a timeout rather than pinning a writer.
		result <- writeLiveAttachMessage(conn, websocket.BinaryMessage, make([]byte, 8*1024*1024), 50*time.Millisecond)
	}))
	defer server.Close()
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	select {
	case err := <-result:
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() {
			t.Fatalf("write error = %v, want a bounded network timeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked terminal writer did not expire")
	}
}
