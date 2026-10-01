package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Replay belongs to the live panel, not the test runner's evidence directory.
// Capture is bounded and asynchronous; a slow disk/encoder cannot stall a test.
const playwrightReplayLimit = 32 << 20
const playwrightReplayTTL = time.Hour

// Encoding is CPU-bounded and each worker uses one ffmpeg thread. Two workers
// keep short test replays responsive when a suite finishes several cases at once.
var playwrightEncoders = make(chan struct{}, 2)
var playwrightReplaySweep sync.Once

func sweepAbandonedPlaywrightReplays() {
	entries, _ := os.ReadDir(os.TempDir())
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "agentworks-playwright-replay-") {
			continue
		}
		info, err := entry.Info()
		if err == nil && time.Since(info.ModTime()) > playwrightReplayTTL {
			_ = os.RemoveAll(filepath.Join(os.TempDir(), entry.Name()))
		}
	}
	time.AfterFunc(playwrightReplayTTL, sweepAbandonedPlaywrightReplays)
}

type replayFrame struct {
	jpeg []byte
	at   time.Time
}
type playwrightRecording struct {
	mu                                                    sync.Mutex
	id, owner, workspace, run, label, dir, state, problem string
	created                                               time.Time
	frames                                                chan replayFrame
	finished                                              chan struct{}
	expiry                                                *time.Timer
	finishOnce                                            sync.Once
	ctx                                                   context.Context
	cancel                                                context.CancelFunc
}

func (api *StreamingAPI) newPlaywrightRecording(s *playwrightLiveSession) *playwrightRecording {
	playwrightReplaySweep.Do(func() { go sweepAbandonedPlaywrightReplays() })
	ctx, cancel := context.WithCancel(context.Background())
	rec := &playwrightRecording{id: s.id, owner: s.owner, workspace: s.workspace, run: s.run, label: s.label, state: "recording", created: time.Now(), frames: make(chan replayFrame, 4), finished: make(chan struct{}), ctx: ctx, cancel: cancel}
	api.playwrightLive.Lock()
	if api.playwrightLive.recordings == nil {
		api.playwrightLive.recordings = map[string]*playwrightRecording{}
	}
	// A fixed ceiling also bounds recordings from clients which never open a panel.
	if len(api.playwrightLive.recordings) >= 16 {
		rec.state, rec.problem = "error", "Recording capacity reached. Close older replays to free space."
	} else {
		var err error
		rec.dir, err = os.MkdirTemp("", "agentworks-playwright-replay-")
		if err != nil {
			rec.state, rec.problem = "error", "Unable to create temporary recording."
		}
		api.playwrightLive.recordings[s.id] = rec
	}
	api.playwrightLive.Unlock()
	if rec.dir != "" {
		go rec.capture()
	}
	rec.mu.Lock()
	if rec.state != "deleted" {
		rec.expiry = time.AfterFunc(playwrightReplayTTL, func() { api.deletePlaywrightRecording(rec) })
	}
	rec.mu.Unlock()
	return rec
}

func (api *StreamingAPI) deletePlaywrightRecording(rec *playwrightRecording) {
	api.playwrightLive.Lock()
	delete(api.playwrightLive.recordings, rec.id)
	api.playwrightLive.Unlock()
	rec.mu.Lock()
	rec.state = "deleted"
	if rec.expiry != nil {
		rec.expiry.Stop()
	}
	rec.cancel()
	rec.mu.Unlock()
	if rec.dir != "" {
		_ = os.RemoveAll(rec.dir)
	}
	// Cancellation also stops an encoder that still holds an open file.
}
func (rec *playwrightRecording) add(data []byte) {
	if rec.ctx.Err() != nil {
		return
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width > 1280 || config.Height > 720 {
		return
	}
	select {
	case rec.frames <- replayFrame{jpeg: data, at: time.Now()}:
	default:
	}
}
func (rec *playwrightRecording) finish() { rec.finishOnce.Do(func() { close(rec.finished) }) }
func (rec *playwrightRecording) status() (string, string) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.state, rec.problem
}
func (rec *playwrightRecording) setState(state, problem string) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.state != "deleted" {
		rec.state, rec.problem = state, problem
	}
}
func (rec *playwrightRecording) capture() {
	defer func() {
		if rec.ctx.Err() != nil {
			_ = os.RemoveAll(rec.dir)
		}
	}()
	manifest, err := os.Create(filepath.Join(rec.dir, "frames.txt"))
	if err != nil {
		rec.setState("error", "Unable to record browser frames.")
		return
	}
	count, size := 0, 0
	var previous time.Time
	write := func(frame replayFrame) error {
		if size+len(frame.jpeg) > playwrightReplayLimit {
			return fmt.Errorf("limit")
		}
		if count > 0 {
			if _, err := fmt.Fprintf(manifest, "duration %.3f\n", frame.at.Sub(previous).Seconds()); err != nil {
				return err
			}
		}
		name := fmt.Sprintf("%06d.jpg", count)
		if err := os.WriteFile(filepath.Join(rec.dir, name), frame.jpeg, 0600); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(manifest, "file '%s'\n", name); err != nil {
			return err
		}
		count++
		size += len(frame.jpeg)
		previous = frame.at
		return nil
	}
	truncated := false
capture:
	for {
		select {
		case <-rec.ctx.Done():
			_ = manifest.Close()
			return
		case frame := <-rec.frames:
			if err = write(frame); err != nil {
				truncated = true
				break capture
			}
		case <-rec.finished:
			for {
				select {
				case frame := <-rec.frames:
					if err = write(frame); err != nil {
						truncated = true
						break capture
					}
				default:
					break capture
				}
			}
		}
	}
	if count == 0 {
		_ = manifest.Close()
		rec.setState("error", "No browser frames were recorded.")
		return
	}
	// Repeat the final image so concat honors its duration, including static pages.
	duration := time.Since(previous).Seconds()
	if duration < 0.25 {
		duration = 0.25
	}
	_, _ = fmt.Fprintf(manifest, "duration %.3f\nfile '%06d.jpg'\n", duration, count-1)
	_ = manifest.Close()
	rec.setState("queued", "")
	select {
	case playwrightEncoders <- struct{}{}:
	case <-rec.ctx.Done():
		return
	}
	defer func() { <-playwrightEncoders }()
	rec.setState("saving", "")
	encodeCtx, cancel := context.WithTimeout(rec.ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(encodeCtx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "concat", "-safe", "1", "-i", filepath.Join(rec.dir, "frames.txt"), "-vf", "scale=1280:720:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2", "-filter_threads", "1", "-r", "4", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "28", "-threads", "1", "-pix_fmt", "yuv420p", "-movflags", "+faststart", filepath.Join(rec.dir, "replay.mp4"))
	cmd.Env = minimalChildEnv()
	err = cmd.Run()
	for i := 0; i < count; i++ {
		_ = os.Remove(filepath.Join(rec.dir, fmt.Sprintf("%06d.jpg", i)))
	}
	_ = os.Remove(filepath.Join(rec.dir, "frames.txt"))
	if err != nil {
		rec.setState("error", "Replay could not be saved.")
		return
	}
	warning := ""
	if truncated {
		warning = "Recording reached its size limit; this replay is partial."
	}
	rec.setState("ready", warning)
	// Keep the worker alive to own deletion, including explicit panel closure.
	<-rec.ctx.Done()
}

func (api *StreamingAPI) handlePlaywrightRecording(w http.ResponseWriter, r *http.Request, id string) {
	api.playwrightLive.Lock()
	rec := api.playwrightLive.recordings[id]
	api.playwrightLive.Unlock()
	if rec == nil || !api.playwrightVisible(GetUserFromContext(r.Context()), r.URL.Query().Get("workspace_path"), rec.owner, rec.workspace) {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost {
		var body struct {
			Action string `json:"action"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body) != nil || body.Action != "delete" {
			http.Error(w, "Invalid replay action", 400)
			return
		}
		api.deletePlaywrightRecording(rec)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	state, _ := rec.status()
	if state != "ready" {
		http.Error(w, "Recording is not ready", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, no-store")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="playwright-replay.mp4"`)
	}
	http.ServeFile(w, r, filepath.Join(rec.dir, "replay.mp4"))
}
