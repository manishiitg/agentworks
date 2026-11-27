package voicestt

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type fakeVoiceService struct {
	mu       sync.Mutex
	modelDir string
	ready    bool
	loading  bool
	active   int
	warmHits int
}

func (f *fakeVoiceService) Available() bool { return true }

func (f *fakeVoiceService) Status() Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Status{
		Available:     true,
		Installed:     ModelInstalled(f.modelDir),
		Loading:       f.loading,
		Ready:         f.ready,
		ActiveStreams: f.active,
		ModelDir:      f.modelDir,
		SizeMB:        ModelSizeMB,
	}
}

func (f *fakeVoiceService) Warm() {
	f.mu.Lock()
	f.warmHits++
	f.ready = true
	f.loading = false
	f.mu.Unlock()
}

func (f *fakeVoiceService) Unload() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active > 0 || f.loading {
		return false
	}
	f.ready = false
	return true
}

func (f *fakeVoiceService) Transcribe(_ context.Context, samples []float32) (string, error) {
	return fmt.Sprintf("samples=%d first=%.2f", len(samples), samples[0]), nil
}

func (f *fakeVoiceService) ServeStream(upgrader *websocket.Upgrader, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	f.mu.Lock()
	f.active++
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()

	var totalSamples int
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		switch msgType {
		case websocket.BinaryMessage:
			samples := DecodePCM16(data)
			totalSamples += len(samples)
			_ = conn.WriteJSON(StreamMessage{
				Type: "partial",
				Text: fmt.Sprintf("heard %d", totalSamples),
			})
		case websocket.TextMessage:
			var ctrl struct {
				Action string `json:"action"`
			}
			if json.Unmarshal(data, &ctrl) == nil && ctrl.Action == "finish" {
				_ = conn.WriteJSON(StreamMessage{
					Type:           "final",
					Text:           fmt.Sprintf("final %d", totalSamples),
					EndOfUtterance: true,
				})
			}
		}
	}
}

func (*fakeVoiceService) Close() error { return nil }

// TestVoiceHelperSubprocess acts as a fake voice-helper binary when invoked
// with GO_WANT_VOICE_HELPER_PROCESS=1 by the tests below.
func TestVoiceHelperSubprocess(t *testing.T) {
	if os.Getenv("GO_WANT_VOICE_HELPER_PROCESS") != "1" {
		return
	}

	var modelDir string
	var stdinAuth bool
	fs := flag.NewFlagSet("voice-helper", flag.ContinueOnError)
	fs.StringVar(&modelDir, "model-dir", "", "")
	fs.BoolVar(&stdinAuth, "stdin-auth", false, "")

	// Skip test flags before "--".
	var helperArgs []string
	for i, arg := range os.Args {
		if arg == "--" {
			helperArgs = os.Args[i+1:]
			break
		}
	}
	if err := fs.Parse(helperArgs); err != nil {
		os.Exit(2)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var token string
	if stdinAuth {
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			os.Exit(3)
		}
		token = strings.TrimSpace(line)
		go func() {
			_, _ = io.Copy(io.Discard, reader)
			cancel()
		}()
	}

	svc := &fakeVoiceService{modelDir: modelDir}
	if err := RunHelperServer(ctx, svc, token, os.Stdout); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

func newTestHelperClient(t *testing.T, modelDir string) *HelperClient {
	t.Helper()
	t.Setenv("GO_WANT_VOICE_HELPER_PROCESS", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	c := NewHelperClient(exe, modelDir, "-test.run=^TestVoiceHelperSubprocess$", "--")
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestHelperHandlerRejectsUnauthorizedRequests(t *testing.T) {
	svc := &fakeVoiceService{modelDir: t.TempDir()}
	srv := httptest.NewServer(NewHelperHandler(svc, "secret-token"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/status")
	if err != nil {
		t.Fatalf("GET /status: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/status", nil)
	req.Header.Set(helperTokenHeader, "secret-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("authorized GET /status: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorized status = %d, want 200", resp.StatusCode)
	}
}

func TestHelperClientLifecycleTranscribeAndStream(t *testing.T) {
	modelDir := t.TempDir()
	c := newTestHelperClient(t, modelDir)

	// Status before Warm must not spawn a helper subprocess.
	st := c.Status()
	if !st.Available || st.Ready {
		t.Fatalf("initial status = %+v, want available=true ready=false", st)
	}
	c.mu.Lock()
	if c.proc != nil {
		c.mu.Unlock()
		t.Fatal("Status() spawned helper eagerly before any voice action")
	}
	c.mu.Unlock()

	// Warm spawns the helper asynchronously and flips Ready once /warm completes.
	c.Warm()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.Status().Ready {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !c.Status().Ready {
		t.Fatalf("helper did not become ready after Warm(): %+v", c.Status())
	}

	// Batch Transcribe round-trips float32 PCM cleanly.
	text, err := c.Transcribe(context.Background(), []float32{0.5, -0.25, 0.125})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "samples=3 first=0.50" {
		t.Fatalf("Transcribe = %q, want %q", text, "samples=3 first=0.50")
	}

	// WebSocket /stream proxies binary PCM16 frames and {"action":"finish"} replies.
	upgrader := &websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.ServeStream(upgrader, w, r)
	}))
	defer proxySrv.Close()

	wsURL := "ws" + strings.TrimPrefix(proxySrv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy ws: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{0, 0, 1, 0, 2, 0, 3, 0}); err != nil {
		t.Fatalf("write binary pcm: %v", err)
	}
	var partial StreamMessage
	if err := conn.ReadJSON(&partial); err != nil {
		t.Fatalf("read partial: %v", err)
	}
	if partial.Type != "partial" || partial.Text != "heard 4" {
		t.Fatalf("partial = %+v, want partial 'heard 4'", partial)
	}
	if err := conn.WriteJSON(map[string]string{"action": "finish"}); err != nil {
		t.Fatalf("write finish: %v", err)
	}
	var final StreamMessage
	if err := conn.ReadJSON(&final); err != nil {
		t.Fatalf("read final: %v", err)
	}
	if final.Type != "final" || !final.EndOfUtterance || final.Text != "final 4" {
		t.Fatalf("final = %+v, want final 'final 4'", final)
	}
	_ = conn.Close()

	// Wait briefly for the server-side stream handler to release active count.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && c.active.Load() > 0 {
		time.Sleep(15 * time.Millisecond)
	}

	// Unload releases the model and stops the helper process to reclaim RSS.
	if !c.Unload() {
		t.Fatal("Unload() = false, want true")
	}
	c.mu.Lock()
	procAfterUnload := c.proc
	c.mu.Unlock()
	if procAfterUnload != nil {
		t.Fatal("Unload() left helper process running")
	}
	if st := c.Status(); !st.Available || st.Ready {
		t.Fatalf("status after unload = %+v, want available=true ready=false", st)
	}

	// Next Transcribe lazily respawns the helper cleanly.
	text, err = c.Transcribe(context.Background(), []float32{0.25})
	if err != nil {
		t.Fatalf("Transcribe after unload: %v", err)
	}
	if text != "samples=1 first=0.25" {
		t.Fatalf("Transcribe after unload = %q, want %q", text, "samples=1 first=0.25")
	}
}
