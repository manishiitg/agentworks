package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const appearanceImageMaxBytes = 5 << 20

type appearanceImageReadKey struct{}
type appearanceImage struct {
	AssetID    string  `json:"asset_id"`
	Fit        string  `json:"fit"`
	PositionX  int     `json:"position_x"`
	PositionY  int     `json:"position_y"`
	Brightness float64 `json:"brightness"`
}

func validAppearanceImage(t appearanceTheme) error {
	if b := t.BackgroundImage; b != nil {
		if t.BackgroundVideo != nil || t.BackgroundShader != nil {
			return errors.New("Choose one background; clear background_video/background_shader with null before selecting an image.")
		}
		if !validAppearanceAssetID(b.AssetID) || (b.Fit != "cover" && b.Fit != "contain" && b.Fit != "tile") || b.PositionX < 0 || b.PositionX > 100 || b.PositionY < 0 || b.PositionY > 100 || b.Brightness < .25 || b.Brightness > 1.5 {
			return errors.New("Use an own image asset_id, cover/contain/tile, position_x/y 0–100 and brightness 0.25–1.5.")
		}
	}
	return nil
}
func appearanceImageDir(user string) (string, error) {
	dir, err := appearanceMediaDir(user)
	return filepath.Join(dir, "images"), err
}

// Decode once under a per-person lock, bound dimensions before allocation, then
// re-encode pixels only: original filename/metadata/scripts never become assets.
func storeAppearanceImage(user string, data []byte, blur int) (string, int, error) {
	if len(data) > appearanceImageMaxBytes || blur < 0 || blur > 20 {
		return "", 0, errors.New("Choose a still image up to 5 MB and blur 0–20 px.")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || !slices.Contains([]string{"png", "jpeg", "webp"}, format) || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 16000 || cfg.Height > 16000 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		return "", 0, errors.New("Choose a PNG, JPEG or WebP image up to 16 million pixels.")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", 0, errors.New("This image could not be opened.")
	}
	w, h := cfg.Width, cfg.Height
	if w > 3840 {
		h = h * 3840 / w
		w = 3840
	}
	if h > 2160 {
		w = w * 2160 / h
		h = 2160
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	pixels := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(pixels, pixels.Bounds(), decoded, decoded.Bounds(), draw.Src, nil)
	if blur > 0 {
		pixels = blurAppearanceImage(pixels, blur)
	}
	var encoded bytes.Buffer
	if pixels.Opaque() {
		// Preserve 4K detail without inflating a normal JPEG photograph into a huge PNG.
		for _, quality := range []int{90, 80, 70} {
			encoded.Reset()
			if err := jpeg.Encode(&encoded, pixels, &jpeg.Options{Quality: quality}); err != nil {
				return "", 0, errors.New("Could not prepare this wallpaper.")
			}
			if encoded.Len() <= appearanceImageMaxBytes {
				break
			}
		}
	} else if err := png.Encode(&encoded, pixels); err != nil {
		return "", 0, errors.New("Could not prepare this wallpaper.")
	}
	if encoded.Len() > appearanceImageMaxBytes {
		return "", 0, errors.New("This picture is too large after preparation. Try a smaller wallpaper.")
	}
	dir, err := appearanceImageDir(user)
	if err != nil {
		return "", 0, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", 0, err
	}
	id := appearanceMediaKey(encoded.String())
	tmp, err := os.CreateTemp(dir, "import-*")
	if err != nil {
		return "", 0, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(encoded.Bytes())
	}
	closeErr := tmp.Close()
	if err != nil {
		return "", 0, err
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	if err = os.Rename(name, filepath.Join(dir, id+".image")); err != nil {
		return "", 0, err
	}
	return id, encoded.Len(), nil
}

// Two linear sliding-window passes bake static blur into pixels, not a live filter.
func blurAppearanceImage(src *image.NRGBA, radius int) *image.NRGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	tmp := image.NewNRGBA(src.Bounds())
	out := image.NewNRGBA(src.Bounds())
	pass := func(in, to *image.NRGBA, horizontal bool) {
		major, minor := h, w
		if !horizontal {
			major, minor = w, h
		}
		for a := 0; a < major; a++ {
			var sums [4]int
			count := 0
			at := func(b int) int {
				if horizontal {
					return in.PixOffset(b, a)
				}
				return in.PixOffset(a, b)
			}
			for b := 0; b < minor; b++ {
				add := b + radius
				if b == 0 {
					for x := 0; x <= radius && x < minor; x++ {
						i := at(x)
						for c := 0; c < 4; c++ {
							sums[c] += int(in.Pix[i+c])
						}
						count++
					}
				} else {
					if add < minor {
						i := at(add)
						for c := 0; c < 4; c++ {
							sums[c] += int(in.Pix[i+c])
						}
						count++
					}
					remove := b - radius - 1
					if remove >= 0 {
						i := at(remove)
						for c := 0; c < 4; c++ {
							sums[c] -= int(in.Pix[i+c])
						}
						count--
					}
				}
				i := at(b)
				for c := 0; c < 4; c++ {
					to.Pix[i+c] = uint8(sums[c] / count)
				}
			}
		}
	}
	pass(src, tmp, true)
	pass(tmp, out, false)
	return out
}
func readAppearanceImage(user, id string) ([]byte, error) {
	if !validAppearanceAssetID(id) {
		return nil, os.ErrNotExist
	}
	dir, err := appearanceImageDir(user)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(id + ".image")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > appearanceImageMaxBytes {
		return nil, os.ErrNotExist
	}
	return io.ReadAll(io.LimitReader(file, appearanceImageMaxBytes+1))
}
func (api *StreamingAPI) handleAppearanceImage(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		http.Error(w, "Sign in to use your wallpaper images.", 401)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, appearanceImageMaxBytes+(64<<10))
		if r.ParseMultipartForm(64<<10) != nil {
			http.Error(w, "Choose an image up to 5 MB.", 400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, _, err := r.FormFile("image")
		if err != nil {
			http.Error(w, "Choose an image file.", 400)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, appearanceImageMaxBytes+1))
		if err != nil {
			http.Error(w, "Could not read the image.", 400)
			return
		}
		api.importAppearanceImage(w, r, data, 0)
		return
	}
	id := mux.Vars(r)["id"]
	response, done := appearanceMediaLog(w, "image_serve", id)
	defer done()
	data, err := readAppearanceImage(claims.UserID, id)
	if err != nil {
		http.Error(response, "Personal image unavailable.", 404)
		return
	}
	response.Header().Set("Content-Type", http.DetectContentType(data))
	http.ServeContent(response, r, "wallpaper.image", time.Time{}, bytes.NewReader(data))
}
func (api *StreamingAPI) importAppearanceImage(w http.ResponseWriter, r *http.Request, data []byte, blur int) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		http.Error(w, "Sign in to import a wallpaper.", 401)
		return
	}
	response, done := appearanceMediaLog(w, "image_import", "")
	defer done()
	gate, _ := appearanceMediaImports.LoadOrStore(claims.UserID, &sync.Mutex{})
	lock := gate.(*sync.Mutex)
	if !lock.TryLock() {
		http.Error(response, "An image or clip is already being prepared. Try again in a moment.", 409)
		return
	}
	defer lock.Unlock()
	id, size, err := storeAppearanceImage(claims.UserID, data, blur)
	if err != nil {
		http.Error(response, err.Error(), 400)
		return
	}
	response.mediaBytes = int64(size)
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(map[string]any{"asset_id": id, "bytes": size, "blur_px": blur, "summary": "Personal wallpaper imported; original and current look unchanged. Apply background_image with this asset_id, fit, position_x/y and brightness."})
}

// The same read_crew_file admission resolves the source. A private context flag
// permits its existing <=11MB reader to return only this internal image payload.
func (api *StreamingAPI) appearanceImageFile(w http.ResponseWriter, r *http.Request, request appearanceRequest) {
	ref := request.ImageFile
	if ref == nil || ref.CrewID == "" || ref.Path == "" {
		http.Error(w, "Choose an image_file in a Crew you can access.", 400)
		return
	}
	p, err := wf.CleanRelative(ref.Path)
	if err != nil || p == "." {
		http.Error(w, "Use a Crew-relative image path.", 400)
		return
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "Sign in to import an image.", 401)
		return
	}
	binding, _, _, ok := api.externalCrewResolve(r.Context(), claims, ref.CrewID)
	if !ok {
		http.Error(w, "Choose a file in a Crew you can access.", 403)
		return
	}
	roots, bound, err := localCrewRoots(binding.Binding.WorkspacePath)
	if err != nil {
		http.Error(w, "Crew folder unavailable.", 400)
		return
	}
	if bound && roots.SeparateFiles() {
		p = crewroot.FilesSegment + "/" + p
	}
	reply := httptest.NewRecorder()
	api.externalCrewCall(reply, r.WithContext(context.WithValue(r.Context(), appearanceImageReadKey{}, true)), "read_crew_file", map[string]any{"crew_id": ref.CrewID, "path": p, "agent_id": ref.AgentID})
	var file struct {
		ContentBase64 string `json:"content_base64"`
		Size          int64  `json:"size"`
	}
	if reply.Code >= 400 || json.Unmarshal(reply.Body.Bytes(), &file) != nil || file.Size > appearanceImageMaxBytes || len(file.ContentBase64) > base64.StdEncoding.EncodedLen(appearanceImageMaxBytes) {
		http.Error(w, "Read an image up to 5 MB from a Crew you can access.", 400)
		return
	}
	data, err := base64.StdEncoding.DecodeString(file.ContentBase64)
	if err != nil || int64(len(data)) != file.Size {
		http.Error(w, "Choose a complete PNG, JPEG or WebP file.", 400)
		return
	}
	api.importAppearanceImage(w, r, data, request.BlurPX)
}

func (api *StreamingAPI) appearanceImageLibrary(w http.ResponseWriter, r *http.Request, request appearanceRequest) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		http.Error(w, "Sign in to manage your images.", 401)
		return
	}
	dir, err := appearanceImageDir(claims.UserID)
	if err != nil {
		http.Error(w, "Images unavailable.", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if request.Operation == "delete_image" {
		if !validAppearanceAssetID(request.ImageID) {
			http.Error(w, "Choose an image_id from list_images.", 400)
			return
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			http.Error(w, "Image unavailable.", 404)
			return
		}
		defer root.Close()
		if err := root.Remove(request.ImageID + ".image"); err != nil {
			http.Error(w, "Image unavailable.", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"summary": "Personal image deleted. Themes referencing it now use their normal background."})
		return
	}
	images := []map[string]any{}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, "Images unavailable.", 503)
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".image")
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".image") && validAppearanceAssetID(id) {
			info, e := entry.Info()
			if e == nil {
				images = append(images, map[string]any{"asset_id": id, "bytes": info.Size()})
			}
			if len(images) >= 100 {
				break
			}
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"images": images, "limit": 100})
}
