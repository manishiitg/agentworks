package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Bundled user-browser capture consumes the same selected-tab stream as the
// viewer. Native `record start` pins a CDP target and misses subsequent tabs.
// Entries are owned by browserCaptureLock. A service restart deliberately makes
// a persisted recording inactive rather than pretending its encoder survived.
var captureStreams = map[string]*captureStream{}

type captureStream struct {
	stop          chan struct{}
	done          chan struct{}
	once          sync.Once
	cancel        context.CancelFunc
	frames        int
	dropped       int
	received      atomic.Uint64
	nonBlank      bool
	width, height int
	maxLag        time.Duration
	started       time.Time
	duration      time.Duration
	err           error
}

type captureFrame struct{ data []byte }

func startCaptureStream(port int, output string) (*captureStream, error) {
	return startCaptureEncoder(port, output, 10, true, "")
}

// Both viewer capture and extension record use this encoder. The receive side
// keeps only the latest image; missed clock ticks skip frames rather than
// aborting the take or building an unbounded catch-up queue (PLAT-691).
func startCaptureEncoder(port int, output string, fps int, fitViewer bool, session string) (*captureStream, error) {
	conn, _, err := (&websocket.Dialer{HandshakeTimeout: 5 * time.Second}).Dial(
		fmt.Sprintf("ws://127.0.0.1:%d/", port), http.Header{"Origin": []string{"http://localhost"}})
	if err != nil {
		return nil, fmt.Errorf("connect recording stream: %w", err)
	}
	conn.SetReadLimit(8 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	filter := "pad=ceil(iw/2)*2:ceil(ih/2)*2,setpts=PTS-STARTPTS"
	if fitViewer {
		filter = "scale=1280:720:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2,setpts=PTS-STARTPTS"
	}
	argv := []string{"-y", "-hide_banner", "-loglevel", "error", "-use_wallclock_as_timestamps", "1", "-f", "image2pipe", "-c:v", "mjpeg", "-framerate", strconv.Itoa(fps), "-i", "pipe:0", "-vf", filter, "-fps_mode", "vfr"}
	if strings.EqualFold(filepath.Ext(output), ".mp4") {
		// Fragmented MP4 remains readable if capture ends before a normal trailer.
		argv = append(argv, "-c:v", "libx264", "-preset", "ultrafast", "-movflags", "+frag_keyframe+empty_moov")
	} else {
		argv = append(argv, "-c:v", "libvpx", "-deadline", "realtime", "-cpu-used", "4", "-b:v", "1M")
	}
	argv = append(argv, "-pix_fmt", "yuv420p", "-threads", "1", output)
	cmd := exec.CommandContext(ctx, "ffmpeg", argv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		conn.Close()
		cancel()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		stdin.Close()
		conn.Close()
		cancel()
		return nil, err
	}
	stream := &captureStream{stop: make(chan struct{}), done: make(chan struct{}), cancel: cancel}
	incoming := make(chan captureFrame, 1)
	disconnected := make(chan error, 1)
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				disconnected <- err
				return
			}
			var message struct {
				Type string `json:"type"`
				Data string `json:"data"`
			}
			if json.Unmarshal(raw, &message) != nil || message.Type != "frame" {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(message.Data)
			if err != nil || len(data) == 0 {
				continue
			}
			stream.received.Add(1)
			select {
			case <-incoming:
			default:
			}
			select {
			case incoming <- captureFrame{data: data}:
			default:
			}
		}
	}()
	ready := make(chan struct{})
	go func() {
		defer close(stream.done)
		defer cancel()
		defer conn.Close()
		period := time.Second / time.Duration(fps)
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		var latest []byte
		lastFrame := -1
		lagReported := false
		running := true
		for running {
			select {
			case <-stream.stop:
				running = false
			case err := <-disconnected:
				stream.err = fmt.Errorf("browser stream disconnected: %w", err)
				running = false
			case frame := <-incoming:
				latest = frame.data
				if !stream.nonBlank {
					stream.nonBlank = captureFrameHasContent(latest)
				}
				if stream.width == 0 {
					if config, err := jpeg.DecodeConfig(bytes.NewReader(latest)); err == nil {
						stream.width, stream.height = config.Width, config.Height
					}
				}
			case tick := <-ticker.C:
				if len(latest) == 0 {
					continue
				}
				now := time.Now()
				if stream.started.IsZero() {
					stream.started = now
				}
				frame := int(now.Sub(stream.started) / period)
				if skipped := frame - lastFrame - 1; skipped > 0 {
					stream.dropped += skipped
				}
				lastFrame = frame
				lag := now.Sub(tick)
				if lag > stream.maxLag {
					stream.maxLag = lag
				}
				if lag > 500*time.Millisecond && !lagReported {
					lagReported = true
					log.Printf("[BROWSER_RECORDING] event=encoder_lag browser_session=%q fps=%d lag_ms=%d dropped_frames=%d action=skip_frames", session, fps, lag.Milliseconds(), stream.dropped)
				}
				writeStart := time.Now()
				if _, err := stdin.Write(latest); err != nil {
					stream.err = fmt.Errorf("video encoder input: %w", err)
					running = false
					continue
				}
				if lag := time.Since(writeStart); lag > stream.maxLag {
					stream.maxLag = lag
				}
				stream.frames++
				if stream.frames == 1 {
					close(ready)
				}
			}
		}
		if !stream.started.IsZero() {
			stream.duration = time.Since(stream.started)
			if skipped := int(stream.duration/period) + 1 - stream.frames; skipped > stream.dropped {
				stream.dropped = skipped
			}
		}
		// Closing the pipe lets FFmpeg finish whatever it encoded. A lost stream or
		// slow capture is reported separately from the readability of that file.
		stdin.Close()
		timer := time.AfterFunc(10*time.Second, cancel)
		if err := cmd.Wait(); err != nil && stream.err == nil {
			detail := strings.TrimSpace(stderr.String())
			if len(detail) > 2048 {
				detail = detail[len(detail)-2048:]
			}
			stream.err = fmt.Errorf("video encoder: %w: %s", err, detail)
		}
		timer.Stop()
	}()
	select {
	case <-ready:
		return stream, nil
	case <-stream.done:
		return nil, fmt.Errorf("recording ended before its first frame: %v", stream.err)
	case <-time.After(10 * time.Second):
		stream.finish()
		return nil, fmt.Errorf("recording stream produced no frames")
	}
}

func (s *captureStream) active() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}
func (s *captureStream) finish() {
	s.once.Do(func() { close(s.stop) })
	select {
	case <-s.done:
	case <-time.After(12 * time.Second):
		s.cancel()
		<-s.done
	}
}

// A valid container/byte count does not establish that a page was recorded.
// Detect uniform blank frames; semantic correctness still needs visual review.
func captureFrameHasContent(frame []byte) bool {
	img, err := jpeg.Decode(bytes.NewReader(frame))
	if err != nil {
		return false
	}
	bounds := img.Bounds()
	if bounds.Dx() < 2 || bounds.Dy() < 2 {
		return false
	}
	var min, max uint32 = 65535, 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y += maxInt(1, bounds.Dy()/32) {
		for x := bounds.Min.X; x < bounds.Max.X; x += maxInt(1, bounds.Dx()/32) {
			r, g, b, _ := img.At(x, y).RGBA()
			value := (r + g + b) / 3
			if value < min {
				min = value
			}
			if value > max {
				max = value
			}
		}
	}
	return max-min > 4096
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Check the finalized file can actually be decoded, not only that it exists.
func validateCaptureVideo(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-xerror", "-i", path, "-frames:v", "1", "-f", "null", "-")
	cmd.Stdout = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("recorded video could not be decoded: %w", err)
	}
	return nil
}
