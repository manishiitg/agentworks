package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

type browserStreamTake struct {
	stream *captureStream
	path   string
	fps    int
}

var browserStreamTakes = map[string]browserStreamTake{}

// Called only after ExecuteShellCommand has checked its workspace and prepared
// the session's managed artifact staging. The normal FinalizeArtifact request
// still checks the destination's write grant before publishing any bytes.
func executeBrowserStreamRecording(c *gin.Context, req models.ExecuteShellRequest) {
	reply := func(data any, err error) {
		result := map[string]any{"success": err == nil, "data": data}
		code := 0
		if err != nil {
			result["error"] = err.Error()
			code = 1
		}
		raw, _ := json.Marshal(result)
		c.JSON(200, models.APIResponse[models.ExecuteShellResponse]{Success: true, Data: models.ExecuteShellResponse{Stdout: string(raw), ExitCode: code}})
	}
	if req.Command != "true" || req.FolderGuard == nil || !req.FolderGuard.Enabled || req.FolderGuard.BrowserTransport != "extension" || req.ArtifactTransfer == nil || req.ArtifactTransfer.Kind != "video" || req.ArtifactTransfer.Finalize {
		reply(nil, fmt.Errorf("Stream recording requires the browser tool's scoped video artifact"))
		return
	}
	session := req.FolderGuard.BrowserSession
	output := req.ArtifactTransfer.SourcePath
	browserCaptureLock.Lock()
	defer browserCaptureLock.Unlock()
	action := req.StreamRecording.Action
	switch action {
	case "start":
		fps := req.StreamRecording.FPS
		if fps < 1 || fps > 60 {
			reply(nil, fmt.Errorf("Recording fps must be between 1 and 60"))
			return
		}
		format := strings.ToLower(filepath.Ext(output))
		if format != ".webm" && format != ".mp4" {
			reply(nil, fmt.Errorf("Recording requires WebM or MP4 output"))
			return
		}
		if _, exists := browserStreamTakes[session]; exists {
			reply(nil, fmt.Errorf("Stop the previous recording before starting another"))
			return
		}
		port, err := browserLivePort(session)
		if err != nil {
			reply(nil, fmt.Errorf("Open this session's browser stream before recording"))
			return
		}
		stream, err := startCaptureEncoder(port, output, fps, false, session)
		if err != nil {
			reply(nil, err)
			return
		}
		browserStreamTakes[session] = browserStreamTake{stream: stream, path: output, fps: fps}
		log.Printf("[BROWSER_RECORDING] action=start browser_session=%q fps=%d format=%s width=%d height=%d source=live_stream", session, fps, format, stream.width, stream.height)
		reply(map[string]any{"recording": true, "path": output, "fps": fps, "width": stream.width, "height": stream.height}, nil)
	case "stop":
		take, exists := browserStreamTakes[session]
		if !exists || take.path != output {
			reply(nil, fmt.Errorf("No recording owned by this browser session"))
			return
		}
		take.stream.finish()
		delete(browserStreamTakes, session)
		stream := take.stream
		usable := stream.frames > 0
		var validationErr error
		if usable {
			validationErr = validateCaptureVideo(output)
			usable = validationErr == nil
		}
		log.Printf("[BROWSER_RECORDING] action=stop browser_session=%q fps=%d width=%d height=%d frames=%d received_frames=%d dropped_frames=%d max_encoder_lag_ms=%d partial=%t usable=%t", session, take.fps, stream.width, stream.height, stream.frames, stream.received.Load(), stream.dropped, stream.maxLag.Milliseconds(), stream.err != nil, usable)
		if !usable {
			if validationErr != nil {
				reply(nil, validationErr)
			} else if stream.err != nil {
				reply(nil, stream.err)
			} else {
				reply(nil, fmt.Errorf("No video frames were saved"))
			}
			return
		}
		data := map[string]any{"path": output, "frames": stream.frames, "fps": take.fps, "droppedFrames": stream.dropped, "maxEncoderLagMs": stream.maxLag.Milliseconds(), "partial": stream.err != nil, "validation": "passed"}
		if stream.dropped > 0 {
			data["warning"] = fmt.Sprintf("The encoder was busy and skipped %d frames. The saved video may look choppy.", stream.dropped)
		}
		if stream.err != nil {
			data["warning"] = "Recording ended early. The usable part was saved."
			if stream.dropped > 0 {
				data["warning"] = fmt.Sprintf("Recording ended early. The usable part was saved; %d frames were skipped, so it may look choppy.", stream.dropped)
			}
		}
		if !stream.started.IsZero() {
			data["durationMs"] = stream.duration.Milliseconds()
		}
		reply(data, nil)
	default:
		reply(nil, fmt.Errorf("Recording supports start or stop"))
	}
}
