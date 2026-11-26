package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

var appearanceMediaImports sync.Map

type appearanceMediaResponse struct {
	http.ResponseWriter
	status     int
	bytes      int64
	mediaBytes int64
}

func (w *appearanceMediaResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *appearanceMediaResponse) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	return n, err
}
func appearanceMediaLog(w http.ResponseWriter, operation, id string) (*appearanceMediaResponse, func()) {
	started := time.Now()
	if !validAppearanceAssetID(id) {
		id = "invalid"
	}
	response := &appearanceMediaResponse{ResponseWriter: w, status: 200}
	return response, func() {
		outcome := "accepted"
		if response.status >= 400 {
			outcome = "refused"
		}
		log.Printf("[APPEARANCE_MEDIA] operation=%s id=%s bytes=%d media_bytes=%d outcome=%s status=%d ms=%d", operation, id, response.bytes, response.mediaBytes, outcome, response.status, time.Since(started).Milliseconds())
	}
}

const appearanceClipMaxBytes = 24 << 20

// Only downloaded Apple aerial directories, never caller-supplied native paths.
var appearanceWallpaperRoots = func() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, "Library/Application Support/com.apple.wallpaper/aerials/videos"), "/Library/Application Support/com.apple.idleassetsd/Customer/4KSDR240FPS"}
}

func appearanceMediaKey(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

type appearanceWallpaper struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	source string
}

func downloadedAppearanceWallpapers() []appearanceWallpaper {
	out := []appearanceWallpaper{}
	for _, dir := range appearanceWallpaperRoots() {
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil || resolved != filepath.Clean(dir) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || (ext != ".mov" && ext != ".mp4") {
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
				continue
			}
			file := filepath.Join(dir, entry.Name())
			out = append(out, appearanceWallpaper{ID: appearanceMediaKey(file), Name: fmt.Sprintf("Downloaded Mac aerial %d", len(out)+1), Bytes: info.Size(), source: file})
		}
	}
	return out
}
func appearanceMediaDir(user string) (string, error) {
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "appearance-media", appearanceMediaKey(user)), nil
}

// AVFoundation conversion is supplied by macOS. Never install a codec/runtime or
// send the original to a hosted service. Metadata filtering stays on by default.
// Generic Preset1280x720 preserves high-frame-rate aerials (about 240fps); the
// Apple playback preset bounds the frame rate/bitrate for a background clip.
type appearanceClipSpec struct {
	preset  string
	seconds int
	height  int
}

// High-frame-rate aerials can still exceed the cap with the playback preset.
// Retry only oversized valid conversions, within the import's single deadline.
var appearanceClipAttempts = [...]appearanceClipSpec{
	{"PresetAppleM4V720pHD", 30, 720},
	{"PresetAppleM4V720pHD", 15, 720},
	{"PresetAppleM4V480pSD", 15, 480},
	{"PresetAppleM4V480pSD", 8, 480},
}

var convertAppearanceWallpaper = func(ctx context.Context, source, output string, spec appearanceClipSpec) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/avconvert", "--source", source, "--output", output, "--preset", spec.preset, "--duration", strconv.Itoa(spec.seconds))
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func (api *StreamingAPI) appearanceMacWallpaper(w http.ResponseWriter, r *http.Request, request appearanceRequest) {
	if request.Operation == "import_mac_wallpaper" {
		response, done := appearanceMediaLog(w, "import", request.WallpaperID)
		defer done()
		w = response
	}
	claims := GetUserFromContext(r.Context())
	if !computerUseAllowed(claims) {
		http.Error(w, "Import a wallpaper from your own local Mac account.", 403)
		return
	}
	if request.Operation == "import_mac_wallpaper" {
		value, _ := appearanceMediaImports.LoadOrStore(claims.UserID, &sync.Mutex{})
		lock := value.(*sync.Mutex)
		if !lock.TryLock() {
			http.Error(w, "Another wallpaper is being prepared. Wait for it to finish, then try again.", 409)
			return
		}
		defer lock.Unlock()
	}
	if request.Operation == "import_mac_wallpaper" {
		if appearanceMediaFull(claims.UserID) {
			http.Error(w, appearanceMediaFullMessage, 400)
			return
		}
	}
	list := downloadedAppearanceWallpapers()
	if request.Operation == "mac_wallpapers" {
		_ = json.NewEncoder(w).Encode(map[string]any{"wallpapers": list, "instructions": "Choose an id to import a personal clip, up to 30 seconds at 720p. Large aerials automatically use a shorter or smaller copy; originals stay untouched. No automatic theme change."})
		return
	}
	var selected *appearanceWallpaper
	for i := range list {
		if list[i].ID == request.WallpaperID {
			selected = &list[i]
			break
		}
	}
	if selected == nil {
		http.Error(w, "Choose an id from your downloaded Mac wallpapers.", 400)
		return
	}
	dir, err := appearanceMediaDir(claims.UserID)
	if err != nil || os.MkdirAll(dir, 0700) != nil {
		http.Error(w, "Could not prepare your personal media.", 503)
		return
	}
	tmp, err := os.CreateTemp(dir, "import-*.m4v")
	if err != nil {
		http.Error(w, "Could not prepare your personal clip.", 503)
		return
	}
	name := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(name)
	defer os.Remove(name)
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	// Each attempt uses the same source admitted from the current opaque-ID list.
	var file *os.File
	var info os.FileInfo
	var chosen appearanceClipSpec
	for attempt, spec := range appearanceClipAttempts {
		if ctx.Err() != nil {
			http.Error(w, "This aerial took too long to prepare. Try again when your Mac is less busy.", 422)
			return
		}
		// avconvert requires a new destination, including after an oversized attempt.
		if err = os.Remove(name); err != nil && !os.IsNotExist(err) {
			http.Error(w, "Could not prepare your personal clip.", 503)
			return
		}
		started := time.Now()
		outcome := "failed"
		var size int64
		convertErr := convertAppearanceWallpaper(ctx, selected.source, name, spec)
		if convertErr == nil {
			file, err = os.Open(name)
			if err == nil {
				info, err = file.Stat()
				if err == nil {
					size = info.Size()
					header := make([]byte, 12)
					_, readErr := io.ReadFull(file, header)
					if !info.Mode().IsRegular() || readErr != nil || string(header[4:8]) != "ftyp" {
						outcome = "invalid"
					} else if size > appearanceClipMaxBytes {
						outcome = "oversize"
					} else {
						outcome = "ready"
					}
				}
			}
		}
		log.Printf("[APPEARANCE_MEDIA] operation=convert id=%s attempt=%d preset=%s seconds=%d bytes=%d outcome=%s ms=%d", selected.ID, attempt+1, spec.preset, spec.seconds, size, outcome, time.Since(started).Milliseconds())
		if outcome == "ready" {
			chosen = spec
			break
		}
		if file != nil {
			_ = file.Close()
			file = nil
		}
		if outcome != "oversize" {
			http.Error(w, "The Mac could not prepare this clip. Try again or choose a different downloaded aerial.", 422)
			return
		}
	}
	if file == nil {
		http.Error(w, "This aerial could not be made small enough, even as a short clip. Try another downloaded aerial.", 422)
		return
	}
	defer file.Close()
	_, _ = file.Seek(0, io.SeekStart)
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		http.Error(w, "The clip could not be saved.", 503)
		return
	}
	id := hex.EncodeToString(hash.Sum(nil))
	_ = file.Close()
	if err = os.Rename(name, filepath.Join(dir, id+".mp4")); err != nil {
		http.Error(w, "The clip could not be saved.", 503)
		return
	}
	_ = os.Chmod(filepath.Join(dir, id+".mp4"), 0600)
	if response, ok := w.(*appearanceMediaResponse); ok {
		response.mediaBytes = info.Size()
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"asset_id": id, "bytes": info.Size(), "summary": fmt.Sprintf("Imported a %d-second %dp personal copy. Original and current theme unchanged. Apply background_video with this asset_id, dim and playback_rate; verify the app visually.", chosen.seconds, chosen.height)})
}
func (api *StreamingAPI) handleAppearanceVideo(w http.ResponseWriter, r *http.Request) {
	response, done := appearanceMediaLog(w, "serve", mux.Vars(r)["id"])
	defer done()
	w = response
	claims := GetUserFromContext(r.Context())
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !computerUseAllowed(claims) {
		http.Error(w, "Personal video unavailable.", 403)
		return
	}
	id := mux.Vars(r)["id"]
	if !validAppearanceAssetID(id) {
		http.Error(w, "Personal video unavailable.", 404)
		return
	}
	dir, err := appearanceMediaDir(claims.UserID)
	if err != nil {
		http.Error(w, "Personal video unavailable.", 404)
		return
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		http.Error(w, "Personal video unavailable.", 404)
		return
	}
	defer root.Close()
	file, err := root.Open(id + ".mp4")
	if err != nil {
		http.Error(w, "Personal video unavailable.", 404)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > appearanceClipMaxBytes {
		http.Error(w, "Personal video unavailable.", 404)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	http.ServeContent(w, r, "theme.mp4", info.ModTime(), file)
}
func validAppearanceAssetID(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 32 && id == strings.ToLower(id)
}
