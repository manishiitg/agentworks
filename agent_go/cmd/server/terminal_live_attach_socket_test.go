package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/liveattach"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
)

// widthTrackingResponder answers the seed chain like seedResponder, but its
// screen capture names the width of the most recent resize-window, so a test
// can tell which geometry a seed was captured at. beforeScreen (optional) runs
// right before the screen capture reply — the splice point.
func widthTrackingResponder(beforeScreen func(), extra func(cmd string) (liveattach.Reply, bool)) func(string) liveattach.Reply {
	var mu sync.Mutex
	width := 0
	return func(cmd string) liveattach.Reply {
		if extra != nil {
			if reply, ok := extra(cmd); ok {
				return reply
			}
		}
		switch {
		case strings.HasPrefix(cmd, "resize-window"):
			var cols, rows int
			var target string
			if _, err := fmt.Sscanf(cmd, "resize-window -t %s -x %d -y %d", &target, &cols, &rows); err == nil {
				mu.Lock()
				width = cols
				mu.Unlock()
			}
			return liveattach.Reply{}
		case strings.Contains(cmd, "#{history_size}"):
			return liveattach.Reply{Lines: []string{"0"}}
		case strings.Contains(cmd, "#{cursor_x}"):
			return liveattach.Reply{Lines: []string{"0,0"}}
		case strings.HasPrefix(cmd, "capture-pane"):
			if beforeScreen != nil {
				beforeScreen()
			}
			mu.Lock()
			w := width
			mu.Unlock()
			return liveattach.Reply{Lines: []string{fmt.Sprintf("screen@%d", w)}}
		}
		return liveattach.Reply{}
	}
}

func drainFrames(ch chan liveAttachFrame) []liveAttachFrame {
	var frames []liveAttachFrame
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				return frames
			}
			frames = append(frames, f)
		default:
			return frames
		}
	}
}

// TestLiveAttachReseedQueuesMarkerThenSeed pins the in-band reseed ordering: at
// the new capture's splice point the viewer gets the text marker and then the
// seed, back to back. Output produced before the capture precedes the marker
// (the client drops it); output after it follows the seed. Nothing at the old
// width can land after the marker.
func TestLiveAttachReseedQueuesMarkerThenSeed(t *testing.T) {
	var stRef atomic.Pointer[liveAttachStream]
	var reseeding atomic.Bool
	installFakeAttach(t, widthTrackingResponder(func() {
		if reseeding.Load() {
			stRef.Load().broadcast([]byte("pre-capture-output"))
		}
	}, nil))
	m := newLiveAttachManager()
	st, viewer, seed, err := m.addViewer(context.Background(), "reseedA", 120, 36)
	if err != nil {
		t.Fatalf("addViewer: %v", err)
	}
	defer func() {
		st.unsubscribe(viewer)
		waitStreamDone(t, st)
	}()
	stRef.Store(st)
	if !strings.Contains(string(seed), "screen@120") {
		t.Fatalf("initial seed = %q, want capture at 120 cols", seed)
	}

	st.broadcast([]byte("old-width-output"))
	reseeding.Store(true)
	if err := st.reseedViewer(context.Background(), viewer, 90, 30, 7); err != nil {
		t.Fatalf("reseedViewer: %v", err)
	}
	reseeding.Store(false)
	st.broadcast([]byte("post-seed-output"))

	frames := drainFrames(viewer.ch)
	markerAt := -1
	for i, f := range frames {
		if f.text {
			if markerAt >= 0 {
				t.Fatalf("more than one marker queued: %+v", frames)
			}
			markerAt = i
		}
	}
	if markerAt < 0 || markerAt+2 >= len(frames) {
		t.Fatalf("frames = %q, want ... marker, seed, live", frameStrings(frames))
	}
	var marker liveAttachReseedMarker
	if err := json.Unmarshal(frames[markerAt].data, &marker); err != nil {
		t.Fatalf("marker is not JSON: %q", frames[markerAt].data)
	}
	if marker != (liveAttachReseedMarker{Type: "reseed", Epoch: 7, Cols: 90, Rows: 30}) {
		t.Fatalf("marker = %+v, want reseed epoch 7 at 90x30", marker)
	}
	for _, f := range frames[:markerAt] {
		if strings.Contains(string(f.data), "screen@") {
			t.Fatalf("seed bytes queued before the marker: %q", frameStrings(frames))
		}
	}
	if got := string(frames[:markerAt][0].data); got != "old-width-output" {
		t.Fatalf("first pre-marker frame = %q, want old-width-output", got)
	}
	if !strings.Contains(string(frames[:markerAt][len(frames[:markerAt])-1].data), "pre-capture-output") {
		t.Fatalf("pre-capture output must precede the marker: %q", frameStrings(frames))
	}
	seedFrame := frames[markerAt+1]
	if seedFrame.text || !strings.HasPrefix(string(seedFrame.data), "\x1bc") || !strings.Contains(string(seedFrame.data), "screen@90") {
		t.Fatalf("frame after marker = %q, want the RIS seed captured at 90 cols", seedFrame.data)
	}
	after := frames[markerAt+2:]
	if len(after) != 1 || string(after[0].data) != "post-seed-output" {
		t.Fatalf("frames after seed = %q, want only post-seed-output", frameStrings(after))
	}

	// The viewer stayed subscribed (no eviction, no reconnect).
	if viewer.wasSuperseded() {
		t.Fatal("reseed marked its own viewer superseded")
	}
	st.mu.Lock()
	_, subscribed := st.subs[viewer]
	st.mu.Unlock()
	if !subscribed {
		t.Fatal("viewer was unsubscribed by its reseed")
	}
}

func frameStrings(frames []liveAttachFrame) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		prefix := "bin:"
		if f.text {
			prefix = "text:"
		}
		out = append(out, prefix+string(f.data))
	}
	return out
}

// TestLiveAttachReseedStaleGeometryQueuesNothing: an external resize during the
// reseed window makes the capture's width unknowable, so no marker/seed is
// queued and the client falls back to reconnecting.
func TestLiveAttachReseedStaleGeometryQueuesNothing(t *testing.T) {
	var stRef atomic.Pointer[liveAttachStream]
	var reseeding atomic.Bool
	installFakeAttach(t, widthTrackingResponder(func() {
		if reseeding.Load() {
			st := stRef.Load()
			st.mu.Lock()
			st.geometryEpoch++
			st.mu.Unlock()
		}
	}, nil))
	m := newLiveAttachManager()
	st, viewer, _, err := m.addViewer(context.Background(), "reseedStale", 120, 36)
	if err != nil {
		t.Fatalf("addViewer: %v", err)
	}
	defer func() {
		st.unsubscribe(viewer)
		waitStreamDone(t, st)
	}()
	stRef.Store(st)
	reseeding.Store(true)
	if err := st.reseedViewer(context.Background(), viewer, 90, 30, 1); err == nil {
		t.Fatal("reseedViewer succeeded across an external geometry change")
	}
	for _, f := range drainFrames(viewer.ch) {
		if f.text {
			t.Fatalf("stale reseed queued a marker: %q", f.data)
		}
	}
}

type liveAttachSocketHarness struct {
	api         *StreamingAPI
	fake        *fakeControlChannel
	wsBase      string
	tmuxSession string
}

func newLiveAttachSocketHarness(t *testing.T, name string, respond func(string) liveattach.Reply) *liveAttachSocketHarness {
	t.Helper()
	fake := installFakeAttach(t, respond)
	store := terminals.NewStore()
	sessionID := "session-" + name
	terminalID := sessionID + ":main:" + sessionID
	tmuxSession := "tmux-" + name
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, tmuxSession, "pane", 1))
	api := &StreamingAPI{terminalStore: store, liveAttach: newLiveAttachManager()}
	// Hijacked handlers outlive httptest's Close; wait for them so a lingering
	// handler never reads a tunable the next test is rewriting.
	var handlers sync.WaitGroup
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		r = mux.SetURLVars(r, map[string]string{"terminal_id": terminalID})
		api.handleTerminalStream(w, r)
	}))
	t.Cleanup(func() {
		server.Close()
		done := make(chan struct{})
		go func() {
			handlers.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("terminal stream handlers did not exit after their sockets closed")
		}
	})
	return &liveAttachSocketHarness{
		api:         api,
		fake:        fake,
		wsBase:      "ws" + strings.TrimPrefix(server.URL, "http") + "/stream",
		tmuxSession: tmuxSession,
	}
}

func (h *liveAttachSocketHarness) dial(t *testing.T, cols, rows int) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("%s?cols=%d&rows=%d", h.wsBase, cols, rows), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read initial seed: %v", err)
	}
	return conn
}

func (h *liveAttachSocketHarness) stream(t *testing.T) *liveAttachStream {
	t.Helper()
	h.api.liveAttach.mu.Lock()
	defer h.api.liveAttach.mu.Unlock()
	st := h.api.liveAttach.sessions[h.tmuxSession]
	if st == nil {
		t.Fatal("live attach stream not registered")
	}
	return st
}

// waitCloseCode reads until the socket closes and returns the close code (or
// -1 for a non-close read error).
func waitCloseCode(t *testing.T, conn *websocket.Conn, within time.Duration) int {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(within))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			var ce *websocket.CloseError
			if errors.As(err, &ce) {
				return ce.Code
			}
			if strings.Contains(err.Error(), "i/o timeout") {
				t.Fatalf("socket did not close within %v", within)
			}
			return -1
		}
	}
}

// TestHandleTerminalStreamReseedsOnSameSocket drives the reseed end to end over
// a real WebSocket: the marker (echoing each request's epoch) arrives as a text
// frame immediately before the binary seed at the new width, and the socket
// stays open and live afterwards.
func TestHandleTerminalStreamReseedsOnSameSocket(t *testing.T) {
	h := newLiveAttachSocketHarness(t, "reseed-socket", widthTrackingResponder(nil, nil))
	conn := h.dial(t, 120, 36)

	for _, step := range []struct{ epoch, cols int }{{3, 90}, {4, 100}} {
		req, _ := json.Marshal(map[string]any{"type": "resize", "cols": step.cols, "rows": 30, "reseed": true, "epoch": step.epoch})
		if err := conn.WriteMessage(websocket.TextMessage, req); err != nil {
			t.Fatalf("send resize: %v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		mt, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read marker (epoch %d): %v", step.epoch, err)
		}
		var marker liveAttachReseedMarker
		if mt != websocket.TextMessage || json.Unmarshal(data, &marker) != nil {
			t.Fatalf("first frame after reseed request = type %d %q, want text marker", mt, data)
		}
		if marker.Type != "reseed" || marker.Epoch != step.epoch || marker.Cols != step.cols || marker.Rows != 30 {
			t.Fatalf("marker = %+v, want epoch %d at %dx30", marker, step.epoch, step.cols)
		}
		mt, data, err = conn.ReadMessage()
		if err != nil {
			t.Fatalf("read seed: %v", err)
		}
		if mt != websocket.BinaryMessage || !strings.Contains(string(data), fmt.Sprintf("screen@%d", step.cols)) {
			t.Fatalf("frame after marker = type %d %q, want binary seed at %d cols", mt, data, step.cols)
		}
	}

	h.stream(t).broadcast([]byte("live-after-reseed"))
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, data, err := conn.ReadMessage()
	if err != nil || mt != websocket.BinaryMessage || string(data) != "live-after-reseed" {
		t.Fatalf("live frame after reseed = type %d %q err %v", mt, data, err)
	}
}

// TestHandleTerminalStreamRefusesInputFrames pins the display-only socket:
// binary input, `input`/`key` frames and unparsable or unknown JSON never reach
// the pane. Only `resize` is acted on.
func TestHandleTerminalStreamRefusesInputFrames(t *testing.T) {
	h := newLiveAttachSocketHarness(t, "refuse-input", widthTrackingResponder(nil, nil))
	var mu sync.Mutex
	var tmuxCalls []string
	record := func(args []string) {
		mu.Lock()
		tmuxCalls = append(tmuxCalls, strings.Join(args, " "))
		mu.Unlock()
	}
	origRun, origOutput := runTerminalTmuxCommand, runTerminalTmuxOutputCommand
	runTerminalTmuxCommand = func(_ context.Context, _ string, args ...string) error {
		record(args)
		return nil
	}
	runTerminalTmuxOutputCommand = func(_ context.Context, args ...string) (string, error) {
		record(args)
		return "", nil
	}
	t.Cleanup(func() { runTerminalTmuxCommand, runTerminalTmuxOutputCommand = origRun, origOutput })

	conn := h.dial(t, 120, 36)
	frames := []struct {
		mt   int
		data string
	}{
		{websocket.BinaryMessage, "rm -rf /\r"},
		{websocket.TextMessage, `{"type":"input","text":"typed","submit":true}`},
		{websocket.TextMessage, `{"type":"key","key":"Enter"}`},
		{websocket.TextMessage, `not json at all`},
		{websocket.TextMessage, `{"type":"bogus","text":"x"}`},
		{websocket.TextMessage, `{"type":"resize","cols":91,"rows":30}`},
	}
	for _, f := range frames {
		if err := conn.WriteMessage(f.mt, []byte(f.data)); err != nil {
			t.Fatalf("write frame: %v", err)
		}
	}

	// Frames are processed in order, so the resize-window for the last frame
	// proves every earlier one was already handled.
	var inBand []string
	deadline := time.After(3 * time.Second)
waitResize:
	for {
		select {
		case cmd := <-h.fake.commands:
			inBand = append(inBand, cmd)
			if strings.HasPrefix(cmd, "resize-window") && strings.Contains(cmd, "-x 91") {
				break waitResize
			}
		case <-deadline:
			t.Fatalf("resize frame never applied; in-band commands: %q", inBand)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, cmd := range append(append([]string(nil), inBand...), tmuxCalls...) {
		for _, forbidden := range []string{"send-keys", "paste-buffer", "load-buffer", "set-buffer"} {
			if strings.Contains(cmd, forbidden) {
				t.Fatalf("input frame reached the pane via %q", cmd)
			}
		}
	}
}

func TestHandleTerminalStreamReadLimitClosesOversizedFrame(t *testing.T) {
	h := newLiveAttachSocketHarness(t, "read-limit", widthTrackingResponder(nil, nil))
	conn := h.dial(t, 120, 36)
	huge := `{"type":"resize","pad":"` + strings.Repeat("a", liveAttachReadLimitBytes+1024) + `"}`
	_ = conn.WriteMessage(websocket.TextMessage, []byte(huge))
	if code := waitCloseCode(t, conn, 3*time.Second); code != websocket.CloseMessageTooBig && code != -1 {
		t.Fatalf("close code = %d, want %d (message too big)", code, websocket.CloseMessageTooBig)
	}
}

func setLiveAttachAccess(t *testing.T, allowed *atomic.Bool) {
	t.Helper()
	orig := liveAttachCanAccess
	liveAttachCanAccess = func(*StreamingAPI, *http.Request, string) bool { return allowed.Load() }
	t.Cleanup(func() { liveAttachCanAccess = orig })
}

func TestHandleTerminalStreamClosesWhenAccessRevoked(t *testing.T) {
	var allowed atomic.Bool
	allowed.Store(true)
	setLiveAttachAccess(t, &allowed)
	origInterval := liveAttachAccessRecheckInterval
	liveAttachAccessRecheckInterval = 50 * time.Millisecond
	t.Cleanup(func() { liveAttachAccessRecheckInterval = origInterval })

	h := newLiveAttachSocketHarness(t, "access-periodic", widthTrackingResponder(nil, nil))
	conn := h.dial(t, 120, 36)
	allowed.Store(false)
	if code := waitCloseCode(t, conn, 3*time.Second); code != liveAttachAccessRevokedCloseCode {
		t.Fatalf("close code = %d, want %d (access revoked)", code, liveAttachAccessRevokedCloseCode)
	}
}

func TestHandleTerminalStreamResizeRechecksAccess(t *testing.T) {
	var allowed atomic.Bool
	allowed.Store(true)
	setLiveAttachAccess(t, &allowed)
	origMin := liveAttachResizeAccessRecheckMin
	liveAttachResizeAccessRecheckMin = 0
	t.Cleanup(func() { liveAttachResizeAccessRecheckMin = origMin })

	h := newLiveAttachSocketHarness(t, "access-resize", widthTrackingResponder(nil, nil))
	conn := h.dial(t, 120, 36)
	allowed.Store(false)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":93,"rows":30,"reseed":true,"epoch":1}`)); err != nil {
		t.Fatalf("send resize: %v", err)
	}
	if code := waitCloseCode(t, conn, 3*time.Second); code != liveAttachAccessRevokedCloseCode {
		t.Fatalf("close code = %d, want %d (access revoked)", code, liveAttachAccessRevokedCloseCode)
	}
	for {
		select {
		case cmd := <-h.fake.commands:
			if strings.Contains(cmd, "-x 93") {
				t.Fatalf("resize acted on after access was revoked: %q", cmd)
			}
		default:
			return
		}
	}
}

// TestHandleTerminalStreamClosesWhenCLIExits: remain-on-exit keeps a dead pane
// (no %exit), so the socket must notice #{pane_dead} and close with the
// dedicated code instead of sitting "connected" on a frozen screen.
func TestHandleTerminalStreamClosesWhenCLIExits(t *testing.T) {
	origPoll := liveAttachPaneDeadPollInterval
	liveAttachPaneDeadPollInterval = 50 * time.Millisecond
	t.Cleanup(func() { liveAttachPaneDeadPollInterval = origPoll })

	var dead atomic.Bool
	h := newLiveAttachSocketHarness(t, "pane-dead", widthTrackingResponder(nil, func(cmd string) (liveattach.Reply, bool) {
		if strings.Contains(cmd, "#{pane_dead}") {
			if dead.Load() {
				return liveattach.Reply{Lines: []string{"1"}}, true
			}
			return liveattach.Reply{Lines: []string{"0"}}, true
		}
		return liveattach.Reply{}, false
	}))
	conn := h.dial(t, 120, 36)

	// Alive pane: the socket stays open across several polls.
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, _, err := conn.ReadMessage(); err == nil || !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("socket closed while the pane was alive: %v", err)
	}
	// A timed-out read poisons a gorilla conn; dial a fresh viewer for the
	// dead-pane half (it supersedes the first).
	conn = h.dial(t, 120, 36)
	dead.Store(true)
	if code := waitCloseCode(t, conn, 3*time.Second); code != liveAttachCLIExitedCloseCode {
		t.Fatalf("close code = %d, want %d (CLI exited)", code, liveAttachCLIExitedCloseCode)
	}
}

func realTmuxSessionForTest(t *testing.T, prefix, script string, remainOnExit bool) string {
	t.Helper()
	if testing.Short() {
		t.Skip("short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), terminalTmuxActionTimeout)
	ok, _ := liveAttachTmuxSupported(ctx)
	cancel()
	if !ok {
		t.Skip("tmux unavailable or too old")
	}
	session := prefix + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := exec.Command("tmux", "new-session", "-d", "-s", session, "-x", "100", "-y", "30", "sh", "-c", script).Run(); err != nil {
		t.Skipf("cannot create tmux session: %v", err)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })
	if remainOnExit {
		if out, err := exec.Command("tmux", "set-option", "-w", "-t", session, "remain-on-exit", "on").CombinedOutput(); err != nil {
			t.Fatalf("set remain-on-exit: %v: %s", err, out)
		}
	}
	return session
}

func realTmuxSocketForTest(t *testing.T, tmuxSession string) *websocket.Conn {
	t.Helper()
	store := terminals.NewStore()
	sessionID := "session-" + tmuxSession
	terminalID := sessionID + ":main:" + sessionID
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, tmuxSession, "pane", 1))
	api := &StreamingAPI{terminalStore: store, liveAttach: newLiveAttachManager()}
	var handlers sync.WaitGroup
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		r = mux.SetURLVars(r, map[string]string{"terminal_id": terminalID})
		api.handleTerminalStream(w, r)
	}))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/stream?cols=100&rows=30", nil)
	if err != nil {
		server.Close()
		t.Fatalf("dial stream: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		server.Close()
		handlers.Wait()
	})
	return conn
}

// TestLiveAttachRealTmuxReseedSameSocket drives a width change through real
// tmux control mode on one socket: the marker precedes a seed captured at the
// new width, and seed + following live stream carry every ticker line exactly
// once (no duplication, no gap at the reseed splice).
func TestLiveAttachRealTmuxReseedSameSocket(t *testing.T) {
	session := realTmuxSessionForTest(t, "live-attach-reseed-",
		`i=0; while true; do i=$((i+1)); echo tick-"x"-$i; sleep 0.05; done`, false)
	time.Sleep(300 * time.Millisecond)
	conn := realTmuxSocketForTest(t, session)

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read initial seed: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":80,"rows":30,"reseed":true,"epoch":5}`)); err != nil {
		t.Fatalf("send reseed: %v", err)
	}
	var marker liveAttachReseedMarker
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for reseed marker: %v", err)
		}
		if mt == websocket.TextMessage {
			if err := json.Unmarshal(data, &marker); err != nil {
				t.Fatalf("text frame is not a marker: %q", data)
			}
			break
		}
	}
	if marker.Type != "reseed" || marker.Epoch != 5 || marker.Cols != 80 {
		t.Fatalf("marker = %+v, want reseed epoch 5 at 80 cols", marker)
	}
	mt, seed, err := conn.ReadMessage()
	if err != nil || mt != websocket.BinaryMessage || !strings.HasPrefix(string(seed), "\x1bc") {
		t.Fatalf("frame after marker = type %d %q err %v, want RIS seed", mt, tail(string(seed), 80), err)
	}
	if out, err := exec.Command("tmux", "display-message", "-p", "-t", session, "#{window_width}").Output(); err != nil || strings.TrimSpace(string(out)) != "80" {
		t.Fatalf("tmux window width = %q (err %v), want 80", out, err)
	}

	var transcript strings.Builder
	transcript.Write(seed)
	deadline := time.Now().Add(time.Second)
	_ = conn.SetReadDeadline(deadline)
	for {
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			if strings.Contains(err.Error(), "i/o timeout") {
				break
			}
			t.Fatalf("read stream: %v", err)
		}
		if mt == websocket.TextMessage {
			t.Fatalf("unexpected text frame after reseed: %q", msg)
		}
		transcript.Write(msg)
	}
	got := transcript.String()
	ticks := map[int]int{}
	for _, line := range strings.Split(got, "\n") {
		line = strings.TrimSpace(stripAnsiForTest(line))
		if n, err := strconv.Atoi(strings.TrimPrefix(line, "tick-x-")); err == nil && strings.HasPrefix(line, "tick-x-") {
			ticks[n]++
		}
	}
	if len(ticks) < 10 {
		t.Fatalf("too few ticks after reseed (%d)\ntail: %q", len(ticks), tail(got, 600))
	}
	lo, hi := 1<<30, 0
	for n, c := range ticks {
		if c > 1 {
			t.Errorf("tick %d appeared %d times across the reseed splice", n, c)
		}
		lo, hi = min(lo, n), max(hi, n)
	}
	for n := lo; n <= hi; n++ {
		if ticks[n] == 0 {
			t.Errorf("tick %d missing across the reseed splice (range [%d,%d])", n, lo, hi)
		}
	}
}

// TestLiveAttachRealTmuxDeadPaneCloses: with remain-on-exit a finished CLI
// leaves a dead pane and no %exit; the socket must close with the CLI-exited
// code rather than sit "connected" on a frozen screen.
func TestLiveAttachRealTmuxDeadPaneCloses(t *testing.T) {
	origPoll := liveAttachPaneDeadPollInterval
	liveAttachPaneDeadPollInterval = 100 * time.Millisecond
	t.Cleanup(func() { liveAttachPaneDeadPollInterval = origPoll })

	session := realTmuxSessionForTest(t, "live-attach-dead-", `echo about-to-exit; sleep 1`, true)
	conn := realTmuxSocketForTest(t, session)
	if code := waitCloseCode(t, conn, 6*time.Second); code != liveAttachCLIExitedCloseCode {
		t.Fatalf("close code = %d, want %d (CLI exited)", code, liveAttachCLIExitedCloseCode)
	}
}
