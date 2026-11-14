package server

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/liveattach"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
)

func TestResolveLiveAttachRestartRecoveryRequiresMatchingTmuxOwner(t *testing.T) {
	oldRun := runTerminalTmuxCommand
	oldOutput := runTerminalTmuxOutputCommand
	defer func() {
		runTerminalTmuxCommand = oldRun
		runTerminalTmuxOutputCommand = oldOutput
	}()
	runTerminalTmuxCommand = func(context.Context, string, ...string) error { return nil }
	runTerminalTmuxOutputCommand = func(_ context.Context, args ...string) (string, error) {
		if len(args) == 5 && args[0] == "show-options" && args[4] == tmuxinput.OwnerSessionOption {
			return "owner-session\n", nil
		}
		return "", nil
	}

	api := &StreamingAPI{}
	req := httptest.NewRequest(http.MethodGet, "/api/terminals/main/stream?session_id=owner-session&tmux_session=mlp-test", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": "main"})
	rec := httptest.NewRecorder()
	snapshot, ok := api.resolveLiveAttachTerminal(rec, req)
	if !ok {
		t.Fatalf("restart recovery rejected matching owner: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if snapshot.SessionID != "owner-session" || snapshot.TmuxSession != "mlp-test" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestResolveLiveAttachRestartRecoveryRejectsDifferentTmuxOwner(t *testing.T) {
	oldRun := runTerminalTmuxCommand
	oldOutput := runTerminalTmuxOutputCommand
	defer func() {
		runTerminalTmuxCommand = oldRun
		runTerminalTmuxOutputCommand = oldOutput
	}()
	runTerminalTmuxCommand = func(context.Context, string, ...string) error { return nil }
	runTerminalTmuxOutputCommand = func(context.Context, ...string) (string, error) {
		return "different-session\n", nil
	}

	api := &StreamingAPI{}
	req := httptest.NewRequest(http.MethodGet, "/api/terminals/main/stream?session_id=owner-session&tmux_session=mlp-other", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": "main"})
	rec := httptest.NewRecorder()
	if _, ok := api.resolveLiveAttachTerminal(rec, req); ok {
		t.Fatal("restart recovery accepted a tmux session owned by another chat")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestLiveAttachInitialSizeRejectsTinyGeometry(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/stream?cols=20&rows=8", nil)
	cols, rows := liveAttachInitialSize(req)
	if cols != liveAttachDefaultCols || rows != liveAttachDefaultRows {
		t.Fatalf("initial size = %dx%d, want defaults %dx%d", cols, rows, liveAttachDefaultCols, liveAttachDefaultRows)
	}
}

// fakeControlChannel emulates the tmux control-mode command loop: it reads
// command lines the stream writes to its control stdin and answers each one
// FIFO via deliverReply, using the provided responder. It is the test stand-in
// for the in-band %begin/%end protocol.
type fakeControlChannel struct {
	commands chan string
}

// installFakeAttach swaps the real control-mode loop for a fake that services
// in-band commands with respond() and blocks until the stream stops. It
// restores the original on cleanup and returns the channel of observed
// command lines.
func installFakeAttach(t *testing.T, respond func(cmd string) liveattach.Reply) *fakeControlChannel {
	t.Helper()
	fake := &fakeControlChannel{commands: make(chan string, 64)}
	orig := liveAttachAttachFn
	liveAttachAttachFn = func(st *liveAttachStream, ctx context.Context) {
		pr, pw := io.Pipe()
		st.setControlWriter(pw)
		go func() {
			<-ctx.Done()
			_ = pr.Close()
			_ = pw.Close()
		}()
		// Reader and responder are separate goroutines, mirroring the real
		// transport: the pipe is always being read (a PTY's kernel buffer
		// never blocks the writer on the scanner), while this goroutine plays
		// the scanner and answers commands FIFO.
		lines := make(chan string, 256)
		go func() {
			defer close(lines)
			sc := bufio.NewScanner(pr)
			for sc.Scan() {
				lines <- sc.Text()
			}
		}()
		for line := range lines {
			select {
			case fake.commands <- line:
			default:
			}
			var reply liveattach.Reply
			if respond != nil {
				reply = respond(line)
			}
			st.deliverReply(reply)
		}
	}
	origRun := runTerminalTmuxCommand
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		return nil
	}
	t.Cleanup(func() {
		liveAttachAttachFn = orig
		runTerminalTmuxCommand = origRun
	})
	return fake
}

// seedResponder answers the seed-chain commands with a canned seed.
func seedResponder(history, screen []string, cursor string) func(string) liveattach.Reply {
	return func(cmd string) liveattach.Reply {
		switch {
		case strings.Contains(cmd, "#{history_size}"):
			return liveattach.Reply{Lines: []string{strconv.Itoa(len(history))}}
		case strings.HasPrefix(cmd, "capture-pane") && strings.Contains(cmd, " -S "):
			return liveattach.Reply{Lines: history}
		case strings.HasPrefix(cmd, "capture-pane"):
			return liveattach.Reply{Lines: screen}
		case strings.Contains(cmd, "#{cursor_x}"):
			return liveattach.Reply{Lines: []string{cursor}}
		}
		return liveattach.Reply{}
	}
}

func waitStreamDone(t *testing.T, st *liveAttachStream) {
	t.Helper()
	select {
	case <-st.done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not stop")
	}
}

func TestLiveAttachAddViewerSeedsAndStreams(t *testing.T) {
	installFakeAttach(t, seedResponder(
		[]string{"old-one", "old-two", ""},
		[]string{"cur-one", "cur-two"},
		"4,2",
	))
	m := newLiveAttachManager()

	st, viewer, seed, err := m.addViewer(context.Background(), "sessA", 100, 40)
	if err != nil {
		t.Fatalf("addViewer: %v", err)
	}
	got := string(seed)
	if !strings.HasPrefix(got, "\x1bc") {
		t.Fatalf("seed prefix = %q, want RIS reset", got[:min(len(got), 8)])
	}
	historyAt := strings.Index(got, "old-one\r\nold-two\r\n")
	visibleAt := strings.Index(got, "cur-one\r\ncur-two")
	cursorAt := strings.LastIndex(got, "\x1b[3;5H")
	if historyAt < 0 || visibleAt < 0 || historyAt >= visibleAt {
		t.Fatalf("seed should be history then current screen: %q", got)
	}
	// Regression guard: no viewport clear between history and screen. xterm.js
	// ED(2) erases in place (no scrollback push), so a 2J here destroys the
	// history tail that is still inside the viewport right after the RIS —
	// the screen paint must scroll history into scrollback naturally instead.
	if strings.Contains(got, "\x1b[2J") {
		t.Fatalf("seed must not clear the viewport (erases in-viewport history): %q", got)
	}
	if cursorAt < 0 || cursorAt < visibleAt {
		t.Fatalf("seed should restore tmux cursor after visible screen: %q", got)
	}

	// Live bytes broadcast after the splice reach the viewer.
	st.broadcast([]byte("live"))
	select {
	case b := <-viewer.ch:
		if string(b.data) != "live" {
			t.Fatalf("got %q, want live", string(b.data))
		}
	case <-time.After(time.Second):
		t.Fatal("viewer did not receive live bytes")
	}

	st.unsubscribe(viewer)
	waitStreamDone(t, st)
}

func TestLiveAttachManagerRejectsEmptySession(t *testing.T) {
	installFakeAttach(t, nil)
	m := newLiveAttachManager()
	if _, _, _, err := m.addViewer(context.Background(), "   ", 80, 24); err == nil {
		t.Fatal("expected error for empty session name")
	}
}

func TestLiveAttachManagerRejectsUnsafeSession(t *testing.T) {
	installFakeAttach(t, nil)
	m := newLiveAttachManager()
	_, _, _, err := m.addViewer(context.Background(), "bad name; kill-server", 80, 24)
	if err == nil {
		t.Fatal("expected error for session name with unsafe characters")
	}
	// The failed seed must not leak an idle attach.
	m.mu.Lock()
	st := m.sessions["bad name; kill-server"]
	m.mu.Unlock()
	if st != nil {
		waitStreamDone(t, st)
	}
}

func TestHandleTerminalStreamRejectsCrossSiteOrigin(t *testing.T) {
	store := terminals.NewStore()
	sessionID := "session-live-attach-origin-reject"
	terminalID := sessionID + ":main:" + sessionID
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, "tmux-origin-reject", "pane", 1))
	api := &StreamingAPI{
		config:        ServerConfig{CORSOrigins: []string{"loopback"}},
		terminalStore: store,
		liveAttach:    newLiveAttachManager(),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = mux.SetURLVars(r, map[string]string{"terminal_id": terminalID})
		api.handleTerminalStream(w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/stream?cols=80&rows=24"
	header := http.Header{"Origin": []string{"https://evil.example"}}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		conn.Close()
		t.Fatal("dial stream succeeded from disallowed origin")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("status = %d, want %d", status, http.StatusForbidden)
	}
}

func TestHandleTerminalStreamAllowsLoopbackOrigin(t *testing.T) {
	installFakeAttach(t, seedResponder(nil, []string{"loopback seed"}, "0,0"))

	store := terminals.NewStore()
	sessionID := "session-live-attach-origin-allow"
	terminalID := sessionID + ":main:" + sessionID
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, "tmux-origin-allow", "pane", 1))
	api := &StreamingAPI{
		config:        ServerConfig{CORSOrigins: []string{"loopback"}},
		terminalStore: store,
		liveAttach:    newLiveAttachManager(),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = mux.SetURLVars(r, map[string]string{"terminal_id": terminalID})
		api.handleTerminalStream(w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/stream?cols=80&rows=24"
	header := http.Header{"Origin": []string{"http://127.0.0.1:51734"}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial stream from loopback origin: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, seed, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	if !strings.Contains(string(seed), "loopback seed") {
		t.Fatalf("seed = %q, want loopback seed", string(seed))
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b.`)

func stripAnsiForTest(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func TestLiveAttachInitialSizeClampsOversizedGeometry(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/stream?cols=5000&rows=2000", nil)
	cols, rows := liveAttachInitialSize(req)
	if cols != liveAttachMaxCols || rows != liveAttachMaxRows {
		t.Fatalf("initial size = %dx%d, want clamp to %dx%d", cols, rows, liveAttachMaxCols, liveAttachMaxRows)
	}
}
