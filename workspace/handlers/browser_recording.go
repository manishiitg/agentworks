package handlers

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

type browserCapture struct {
	StreamPort     int        `json:"stream_port,omitempty"`
	OwnerSession   string     `json:"owner_session,omitempty"`
	BrowserSession string     `json:"browser_session"`
	Source         string     `json:"source,omitempty"`
	Validation     string     `json:"validation,omitempty"`
	Frames         int        `json:"frames,omitempty"`
	Workspace      string     `json:"workspace"`
	Directory      string     `json:"directory"`
	StartedAt      time.Time  `json:"started_at"`
	Recording      bool       `json:"recording"`
	VideoActive    bool       `json:"video_active"`
	HARActive      bool       `json:"har_active"`
	StoppedAt      *time.Time `json:"stopped_at,omitempty"`
	Files          []string   `json:"files,omitempty"`
	Errors         []string   `json:"errors,omitempty"`
}

var browserCaptureLock sync.Mutex

// Internal route: the agent API verifies session ownership and workflow write access.
func BrowserRecording(c *gin.Context) {
	var req struct {
		OwnerSession     string                    `json:"owner_session"`
		Workspace        string                    `json:"workspace_path"`
		Action           string                    `json:"action"`
		WorkingDirectory string                    `json:"working_directory"`
		FolderGuard      *models.FolderGuardConfig `json:"folder_guard"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid recording request"})
		return
	}
	if req.Action != "start" && req.Action != "stop" && req.Action != "status" {
		c.JSON(400, gin.H{"error": "Invalid recording action"})
		return
	}
	session := c.Param("session")
	port, socketDir, liveErr := browserLiveEndpoint(session)
	if !browserLiveSessionName.MatchString(session) {
		c.JSON(400, gin.H{"error": "Invalid browser session"})
		return
	}
	if liveErr != nil {
		for _, candidate := range browserSocketDirs(session) {
			if _, err := os.Stat(filepath.Join(candidate, session+".capture.json")); err == nil {
				socketDir = candidate
				break
			}
		}
		if socketDir == "" {
			c.JSON(404, gin.H{"error": "Browser session is no longer running"})
			return
		}
	}
	root, err := filepath.EvalSymlinks(viper.GetString("docs-dir"))
	if err != nil {
		c.JSON(500, gin.H{"error": "Workspace unavailable"})
		return
	}
	workspace, err := filepath.EvalSymlinks(filepath.Join(root, req.Workspace))
	if err != nil || !capturePathWithin(root, workspace) || workspace == root {
		c.JSON(400, gin.H{"error": "Invalid workflow workspace"})
		return
	}
	browserCaptureLock.Lock()
	defer browserCaptureLock.Unlock()
	marker := filepath.Join(socketDir, session+".capture.json")
	capture := browserCapture{}
	if data, readErr := os.ReadFile(marker); readErr == nil {
		if json.Unmarshal(data, &capture) != nil {
			c.JSON(409, gin.H{"error": "Recording state is unreadable"})
			return
		}
		// Reconcile cached state with the actual encoder; never reuse stale captures.
		if capture.Recording && (liveErr != nil || (capture.Source == "live-stream" && capture.VideoActive && (capture.StreamPort != port || captureStreams[session] == nil || !captureStreams[session].active()))) {
			if stream := captureStreams[session]; stream != nil {
				stream.finish()
				delete(captureStreams, session)
			}
			capture.Recording, capture.VideoActive, capture.HARActive = false, false, false
			capture.Validation = "failed"
			capture.Errors = append(capture.Errors, "Recording interrupted: browser or recording service stopped. Start a new capture for this run.")
			stopped := time.Now().UTC()
			capture.StoppedAt = &stopped
			_ = writeCaptureJSON(marker, capture)
		}
		if capture.Workspace != req.Workspace && (req.Action == "start" || req.Action == "status") && !capture.Recording {
			capture = browserCapture{}
		} else if capture.Workspace != req.Workspace {
			c.JSON(409, gin.H{"error": "Recording belongs to a different workflow"})
			return
		}
	}
	if capture.Recording && req.Action != "status" && capture.OwnerSession != "" && req.OwnerSession != "" && req.OwnerSession != capture.OwnerSession {
		c.JSON(409, gin.H{"error": "Recording belongs to another run"})
		return
	}
	base := filepath.Join(workspace, "browser-recordings")
	// UI requests have already passed workflow authorization in the agent API.
	// Tool requests additionally carry the invoking session's current guard.
	if guard := req.FolderGuard; guard != nil {
		allowed := append(append([]string{}, guard.ReadPaths...), guard.WritePaths...)
		blocked := append([]string{}, guard.BlockedPaths...)
		if req.Action != "status" {
			allowed = guard.WritePaths
			blocked = append(blocked, guard.BlockedWritePaths...)
		}
		target := base
		if capture.Directory != "" && (req.Action != "start" || capture.Recording) {
			target = filepath.Join(root, capture.Directory)
		}
		err := security.AuthorizeBrowserCaptureDirectory(target, root, allowed, blocked)
		// Workflow steps may only write their run/step output directory. Keep
		// their evidence there while retaining workflow ownership of the capture.
		if err != nil && req.Action == "start" && !capture.Recording && req.WorkingDirectory != "" {
			candidate := filepath.Join(root, req.WorkingDirectory, "browser-recordings")
			if capturePathWithin(workspace, candidate) {
				err = security.AuthorizeBrowserCaptureDirectory(candidate, root, allowed, blocked)
				base = candidate
			}
		}
		if !guard.Enabled || err != nil {
			c.JSON(403, gin.H{"error": "Capture requires access to its workspace recording directory"})
			return
		}
	}
	if liveErr != nil && req.Action == "start" {
		c.JSON(404, gin.H{"error": "Open the workflow browser before starting a fresh capture"})
		return
	}
	if req.Action == "status" {
		c.JSON(200, capture)
		return
	}
	if req.Action == "start" && capture.Recording {
		c.JSON(200, capture)
		return
	}
	if req.Action == "stop" && !capture.Recording {
		c.JSON(200, capture)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	run := func(args ...string) ([]byte, error) { return runCaptureCommand(ctx, socketDir, session, args...) }
	if req.Action == "start" {
		if err := os.MkdirAll(base, 0700); err != nil {
			c.JSON(500, gin.H{"error": "Cannot create recording folder"})
			return
		}
		actual, err := filepath.EvalSymlinks(base)
		if err != nil || !capturePathWithin(workspace, actual) {
			c.JSON(400, gin.H{"error": "Invalid recording folder"})
			return
		}
		dir, err := os.MkdirTemp(actual, time.Now().UTC().Format("20060102T150405Z-"))
		if err != nil {
			c.JSON(500, gin.H{"error": "Cannot create recording folder"})
			return
		}
		rel, _ := filepath.Rel(root, dir)
		capture = browserCapture{OwnerSession: req.OwnerSession, BrowserSession: session, Workspace: req.Workspace, Directory: filepath.ToSlash(rel), StartedAt: time.Now().UTC()}
		// Clear an abandoned HAR after a recording-service restart. Discard old-run
		// traffic; it must not enter the new capture's evidence bundle.
		if browserconfig.IsUserSession(session) {
			_, _ = run("network", "har", "stop", os.DevNull)
		}
		// Exclude response bodies; HAR still records request timing, URLs and headers.
		if _, err = run("network", "har", "start", "--content", "none"); err == nil {
			_, err = run("console", "--clear")
		}
		if err == nil {
			_, err = run("errors", "--clear")
		}
		if err == nil {
			if browserconfig.IsUserSession(session) {
				capture.Source = "live-stream"
				capture.StreamPort = port
				capture.Validation = "pending"
				var stream *captureStream
				stream, err = startCaptureStream(port, filepath.Join(dir, "video.webm"))
				if err == nil {
					captureStreams[session] = stream
				}
			} else {
				_, err = run("record", "start", filepath.Join(dir, "video.webm"), "--fps", "10")
			}
		}
		if err != nil {
			_, _ = run("network", "har", "stop", filepath.Join(dir, "network.har"))
			capture.Errors = []string{err.Error()}
			writeCaptureJSON(filepath.Join(dir, "manifest.json"), capture)
			c.JSON(502, gin.H{"error": "Recording could not start: " + err.Error()})
			return
		}
		capture.Recording = true
		capture.VideoActive = true
		capture.HARActive = true
		if err := writeCaptureJSON(marker, capture); err != nil {
			if stream := captureStreams[session]; stream != nil {
				stream.finish()
				delete(captureStreams, session)
			} else {
				_, _ = run("record", "stop")
			}
			_, _ = run("network", "har", "stop", filepath.Join(dir, "network.har"))
			c.JSON(500, gin.H{"error": "Cannot persist recording status"})
			return
		}
		c.JSON(200, capture)
		return
	}
	dir, err := filepath.EvalSymlinks(filepath.Join(root, capture.Directory))
	if err != nil || !capturePathWithin(workspace, dir) {
		c.JSON(400, gin.H{"error": "Invalid recording destination"})
		return
	}
	capture.Errors = nil
	capture.Files = nil
	// Attempt every export even if one fails, so partial evidence remains useful.
	for _, item := range []struct {
		File string
		Args []string
	}{
		{"console.json", []string{"console"}}, {"errors.json", []string{"errors"}},
		{"record-stop.json", []string{"record", "stop"}}, {"har-stop.json", []string{"network", "har", "stop", filepath.Join(dir, "network.har")}},
	} {
		if item.File == "record-stop.json" && !capture.VideoActive {
			continue
		}
		if item.File == "har-stop.json" && !capture.HARActive {
			continue
		}
		var output []byte
		var runErr error
		if item.File == "record-stop.json" && capture.Source == "live-stream" {
			stream := captureStreams[session]
			if stream != nil {
				stream.finish()
				delete(captureStreams, session)
				capture.Frames = stream.frames
				runErr = stream.err
				if runErr == nil && (stream.frames < 2 || !stream.nonBlank) {
					runErr = fmt.Errorf("recording has no usable page footage (blank or too short)")
				}
				if runErr == nil {
					runErr = validateCaptureVideo(filepath.Join(dir, "video.webm"))
				}
			} else {
				runErr = fmt.Errorf("recording service no longer owns this capture")
			}
			capture.VideoActive = false
			capture.Validation = "passed"
			if runErr != nil {
				capture.Validation = "failed"
			}
			output, _ = json.Marshal(map[string]interface{}{"frames": capture.Frames, "validation": capture.Validation})
		} else {
			output, runErr = run(item.Args...)
		}
		if runErr != nil && (strings.Contains(strings.ToLower(runErr.Error()), "no recording in progress") || strings.Contains(strings.ToLower(runErr.Error()), "no frames captured")) {
			capture.VideoActive = false
			capture.Validation = "failed"
		}
		if runErr == nil && item.File == "record-stop.json" {
			capture.VideoActive = false
		}
		if runErr == nil && item.File == "har-stop.json" {
			capture.HARActive = false
		}
		if runErr != nil {
			capture.Errors = append(capture.Errors, item.File+": "+runErr.Error())
		}
		if len(output) > 0 {
			if err := os.WriteFile(filepath.Join(dir, item.File), output, 0600); err != nil {
				capture.Errors = append(capture.Errors, err.Error())
			}
		}
	}
	capture.Recording = capture.VideoActive || capture.HARActive
	if capture.Recording {
		_ = writeCaptureJSON(marker, capture)
		c.JSON(200, capture)
		return
	}
	stoppedAt := time.Now().UTC()
	capture.StoppedAt = &stoppedAt
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if !entry.IsDir() && entry.Name() != "capture.zip" && entry.Name() != "manifest.json" {
			capture.Files = append(capture.Files, entry.Name())
		}
	}
	capture.Files = append(capture.Files, "manifest.json")
	if err := writeCaptureJSON(filepath.Join(dir, "manifest.json"), capture); err != nil {
		capture.Errors = append(capture.Errors, err.Error())
	}
	if err := zipCapture(dir, capture.Files); err != nil {
		capture.Errors = append(capture.Errors, err.Error())
	} else {
		capture.Files = append(capture.Files, "capture.zip")
	}
	if err := writeCaptureJSON(marker, capture); err != nil {
		capture.Errors = append(capture.Errors, err.Error())
	}
	c.JSON(200, capture)
}

func capturePathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func runCaptureCommand(ctx context.Context, socketDir, session string, args ...string) ([]byte, error) {
	// Managed diagnostic recording must not upgrade the live daemon either.
	if browserconfig.IsUserSession(session) {
		request := map[string]any{"id": "capture-action"}
		if len(args) == 0 {
			return nil, fmt.Errorf("missing capture operation")
		}
		switch args[0] {
		case "console", "errors":
			request["action"] = args[0]
			request["clear"] = len(args) == 2 && args[1] == "--clear"
		case "network":
			if len(args) < 3 || args[1] != "har" {
				return nil, fmt.Errorf("unsupported capture operation")
			}
			switch args[2] {
			case "start":
				request["action"] = "har_start"
				request["content"] = "none"
			case "stop":
				request["action"] = "har_stop"
				if len(args) == 4 {
					request["path"] = args[3]
				}
			default:
				return nil, fmt.Errorf("unsupported capture operation")
			}
		case "record":
			if len(args) != 2 || args[1] != "stop" {
				return nil, fmt.Errorf("managed video uses the live stream")
			}
			request["action"] = "recording_stop"
		default:
			return nil, fmt.Errorf("unsupported capture operation")
		}
		return existingBrowserCommand(ctx, socketDir, session, request)
	}
	argv := append([]string{"--session", session}, browserconfig.HeadlessArgsForSession(session)...)
	argv = append(argv, args...)
	argv = append(argv, "--json")
	cmd := exec.CommandContext(ctx, "agent-browser", argv...)
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "AGENT_BROWSER_SOCKET_DIR=") && !(browserconfig.SharedEnabled() && strings.HasPrefix(env, "TZ=")) {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "AGENT_BROWSER_SOCKET_DIR="+socketDir)
	if browserconfig.SharedEnabled() {
		cmd.Env = append(cmd.Env, "TZ=UTC")
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s failed: %s", args[0], strings.TrimSpace(string(output)))
	}
	var result struct {
		Success *bool  `json:"success"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(output, &result) == nil && result.Success != nil && !*result.Success {
		return output, fmt.Errorf("%s", result.Error)
	}
	return output, nil
}
func writeCaptureJSON(path string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".capture-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err = temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func zipCapture(dir string, files []string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.OpenFile("capture.zip", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	for _, name := range files {
		input, err := root.Open(name)
		if err != nil {
			writer.Close()
			file.Close()
			return err
		}
		entry, err := writer.Create(name)
		if err == nil {
			_, err = io.Copy(entry, input)
		}
		input.Close()
		if err != nil {
			writer.Close()
			file.Close()
			return err
		}
	}
	if err := writer.Close(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
