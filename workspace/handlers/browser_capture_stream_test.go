package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/spf13/viper"
)

func captureTestJPEG(blank bool, reverse bool) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 160, 90))
	for y := 0; y < 90; y++ {
		for x := 0; x < 160; x++ {
			value := uint8(255)
			if !blank && ((x < 80) != reverse) {
				value = 0
			}
			img.Set(x, y, color.RGBA{value, value, value, 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, nil)
	return b.Bytes()
}
func captureTestServer(t *testing.T, blank bool) (int, *atomic.Int32, func()) {
	t.Helper()
	var selected atomic.Int32
	stop := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		ticker := time.NewTicker(60 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if err := conn.WriteJSON(map[string]interface{}{"type": "frame", "data": base64.StdEncoding.EncodeToString(captureTestJPEG(blank, selected.Load() == 1))}); err != nil {
					return
				}
			}
		}
	}))
	parsed, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(parsed.Port())
	return port, &selected, func() { close(stop); server.Close() }
}

func TestCaptureStreamFollowsSelectedTabAndEncodesUsableVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required for recording integration")
	}
	port, selected, closeServer := captureTestServer(t, false)
	defer closeServer()
	path := filepath.Join(t.TempDir(), "video.webm")
	stream, err := startCaptureStream(port, path)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.finish()
	time.Sleep(350 * time.Millisecond)
	selected.Store(1) // Same stream, different selected tab; no new recording.
	time.Sleep(450 * time.Millisecond)
	stream.finish()
	if stream.err != nil || !stream.nonBlank || stream.frames < 5 {
		t.Fatalf("frames=%d nonblank=%v err=%v", stream.frames, stream.nonBlank, stream.err)
	}
	if err := validateCaptureVideo(path); err != nil {
		t.Fatal(err)
	}
	// Decode all frames and prove both tab appearances reached the actual file.
	output, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-vf", "scale=16:9", "-pix_fmt", "gray", "-f", "rawvideo", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	seenDark, seenLight := false, false
	for offset := 0; offset+144 <= len(output); offset += 144 {
		pixel := output[offset+4*16+2]
		seenDark = seenDark || pixel < 50
		seenLight = seenLight || pixel > 200
	}
	if !seenDark || !seenLight {
		t.Fatal("encoded video missed a selected-tab change")
	}
}

func TestCaptureStreamDetectsBlankFramesAndDisconnect(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	port, _, closeServer := captureTestServer(t, true)
	stream, err := startCaptureStream(port, filepath.Join(t.TempDir(), "blank.webm"))
	if err != nil {
		closeServer()
		t.Fatal(err)
	}
	closeServer()
	select {
	case <-stream.done:
	case <-time.After(15 * time.Second):
		stream.finish()
		t.Fatal("dead browser did not stop recording")
	}
	if stream.nonBlank || stream.err == nil {
		t.Fatalf("blank/dead stream accepted: %v %v", stream.nonBlank, stream.err)
	}
}

func TestUserCaptureStaleStateStartsFreshAndRejectsBlankEvidence(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix native IPC fixture")
	}
	root := t.TempDir()
	// macOS Unix socket names are limited to 104 bytes; use a short real socket.
	socket, err := os.MkdirTemp("/tmp", "capture-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socket)
	old := viper.GetString("docs-dir")
	viper.Set("docs-dir", root)
	defer viper.Set("docs-dir", old)
	t.Setenv("AGENT_BROWSER_SOCKET_DIR", socket)
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "")
	const session = "user-0123456789abcdef--browser"
	os.MkdirAll(filepath.Join(root, "Workflow", "one"), 0700)
	listener, err := net.Listen("unix", filepath.Join(socket, session+".sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	// Exercise the native daemon protocol actually used by runCaptureCommand,
	// together with the real websocket stream and ffmpeg encoder below.
	var ipcCalls atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer conn.Close()
				var request map[string]any
				if json.NewDecoder(conn).Decode(&request) != nil {
					return
				}
				ipcCalls.Add(1)
				if request["action"] == "har_stop" {
					if path, ok := request["path"].(string); ok && path != os.DevNull {
						_ = os.WriteFile(path, []byte(`{"log":{"version":"1.2","entries":[]}}`), 0600)
					}
				}
				_ = json.NewEncoder(conn).Encode(map[string]any{"id": request["id"], "success": true, "data": map[string]any{"messages": []any{}}})
			}()
		}
	}()
	port, _, closeServer := captureTestServer(t, true)
	defer closeServer()
	os.WriteFile(filepath.Join(socket, session+".stream"), []byte(strconv.Itoa(port)), 0600)
	marker := filepath.Join(socket, session+".capture.json")
	writeCaptureJSON(marker, browserCapture{Workspace: "Workflow/one", Directory: "Workflow/one/browser-recordings/old", Recording: true, VideoActive: true, Source: "live-stream"})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/:session", BrowserRecording)
	call := func(action string) browserCapture {
		req := httptest.NewRequest("POST", "/"+session, strings.NewReader(`{"action":"`+action+`","workspace_path":"Workflow/one","owner_session":"run-new"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: %s", action, rec.Body.String())
		}
		var result browserCapture
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	stale := call("status")
	if stale.Recording || stale.Validation != "failed" {
		t.Fatalf("stale state still active: %+v", stale)
	}
	started := call("start")
	defer func() {
		browserCaptureLock.Lock()
		if stream := captureStreams[session]; stream != nil {
			stream.finish()
			delete(captureStreams, session)
		}
		browserCaptureLock.Unlock()
	}()
	if !started.Recording || strings.HasSuffix(started.Directory, "/old") || started.OwnerSession != "run-new" {
		t.Fatalf("stale capture reused: %+v", started)
	}
	time.Sleep(250 * time.Millisecond)
	stopped := call("stop")
	if stopped.Recording || stopped.Validation != "failed" || len(stopped.Errors) == 0 {
		t.Fatalf("blank evidence accepted: %+v", stopped)
	}
	fresh := call("start")
	if fresh.Directory == started.Directory {
		t.Fatal("retry reused previous video")
	}
	os.Remove(filepath.Join(socket, session+".stream"))
	interrupted := call("status")
	if interrupted.Recording || interrupted.Validation != "failed" {
		t.Fatal("missing browser left capture stuck")
	}
	if ipcCalls.Load() < 8 {
		t.Fatalf("capture bypassed native IPC: %d calls", ipcCalls.Load())
	}
}
