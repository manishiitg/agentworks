package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

// #2813: actual uploaded/file-backed images become only the caller's personal
// pixels. File import uses the real Crew admission and cannot choose host paths.
func TestAppearanceImagesUseOwnAssetsAndAdmittedFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	src := image.NewNRGBA(image.Rect(0, 0, 1100, 800))
	_, _ = rand.New(rand.NewSource(1)).Read(src.Pix)
	var original bytes.Buffer
	if err := png.Encode(&original, src); err != nil {
		t.Fatal(err)
	}
	if original.Len() < 2<<20 || original.Len() > appearanceImageMaxBytes {
		t.Fatal("fixture must exceed ordinary inline read threshold")
	}
	f.write(t, externalCommandCrewRoot+"/wallpaper.png", original.String())
	fileBefore := f.call(t, "owner", "read_crew_file", map[string]any{"crew_id": "team", "path": "wallpaper.png"})
	if fileBefore.Code != 200 || bytes.Contains(fileBefore.Body.Bytes(), []byte("content_base64")) {
		t.Fatal("ordinary file read unexpectedly includes large binary")
	}
	imported := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "import_image", "image_file": map[string]any{"crew_id": "team", "path": "wallpaper.png"}, "blur_px": 2})
	var result struct {
		AssetID string `json:"asset_id"`
	}
	if imported.Code != 200 || json.Unmarshal(imported.Body.Bytes(), &result) != nil || !validAppearanceAssetID(result.AssetID) {
		t.Fatalf("admitted file import: %d %s", imported.Code, imported.Body.String())
	}
	fileAfter := f.call(t, "owner", "read_crew_file", map[string]any{"crew_id": "team", "path": "wallpaper.png"})
	if !bytes.Equal(fileBefore.Body.Bytes(), fileAfter.Body.Bytes()) {
		t.Fatal("original or ordinary reader changed")
	}
	for _, args := range []map[string]any{
		{"crew_id": "team", "path": "../../outside.png"},
		{"crew_id": "foreign", "path": "wallpaper.png"},
	} {
		got := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "import_image", "image_file": args})
		if got.Code < 400 {
			t.Fatal("foreign/traversal import allowed")
		}
	}
	// Same existing Crew made private: a real foreign caller must be refused,
	// not merely an invented/missing Crew ID.
	if err := defaultProjectOwners().SetPrivate("work", "test-team", true); err != nil {
		t.Fatal(err)
	}
	denied := f.call(t, "other", "settings", map[string]any{"action": "appearance", "operation": "import_image", "image_file": map[string]any{"crew_id": "team", "path": "wallpaper.png"}})
	if denied.Code < 400 {
		t.Fatal("foreign private Crew image imported")
	}
	serve := func(user, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/appearance/images/"+id, nil)
		r = mux.SetURLVars(r, map[string]string{"id": id})
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user}))
		w := httptest.NewRecorder()
		f.api.handleAppearanceImage(w, r)
		return w
	}
	if own := serve("owner", result.AssetID); own.Code != 200 || own.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("own image unavailable: %d", own.Code)
	}
	if serve("other", result.AssetID).Code != 404 || serve("owner", "../private").Code != 404 {
		t.Fatal("image read crossed identity/path")
	}
	// The browser route uses selected bytes, not a path in Downloads or a filename.
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("image", "wallpaper.png")
	_, _ = part.Write(original.Bytes())
	_ = form.Close()
	r := httptest.NewRequest(http.MethodPost, "/api/appearance/images", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "owner"}))
	w := httptest.NewRecorder()
	f.api.handleAppearanceImage(w, r)
	if w.Code != 200 {
		t.Fatalf("picker upload: %d %s", w.Code, w.Body.String())
	}
	// A normal opaque 4K JPEG must not inflate into an over-limit PNG or be
	// silently downscaled just to fit storage. Preserve its native detail.
	photo := image.NewNRGBA(image.Rect(0, 0, 3840, 2160))
	_, _ = rand.New(rand.NewSource(2)).Read(photo.Pix)
	for i := 3; i < len(photo.Pix); i += 4 {
		photo.Pix[i] = 255
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, photo, &jpeg.Options{Quality: 65}); err != nil || jpg.Len() > appearanceImageMaxBytes {
		t.Fatal("4K JPEG fixture invalid")
	}
	photoID, _, err := storeAppearanceImage("owner", jpg.Bytes(), 0)
	if err != nil {
		t.Fatalf("4K photo refused: %v", err)
	}
	photoBytes, err := readAppearanceImage("owner", photoID)
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(photoBytes))
	if err != nil || format != "jpeg" || cfg.Width != 3840 || cfg.Height != 2160 {
		t.Fatal("photo lost native 4K detail")
	}
	deleted := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "delete_image", "image_id": photoID})
	if deleted.Code != 200 || serve("owner", photoID).Code != 404 {
		t.Fatal("own image delete failed")
	}
	if _, _, err := storeAppearanceImage("owner", []byte("<svg onload='code'>"), 0); err == nil {
		t.Fatal("markup accepted as pixels")
	}
	if _, _, err := storeAppearanceImage("owner", original.Bytes(), 21); err == nil {
		t.Fatal("unbounded blur")
	}
}
