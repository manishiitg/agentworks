package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// PLAT-746-1040: shared/project credentials cannot import/read the local OS
// owner's aerials, and an opaque source never becomes an arbitrary host path.
func TestAppearanceWallpaperImportStaysWithLocalOwner(t *testing.T) {
	f := newMCPLaptopFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("DEFAULT_USER_ID", "owner")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	root := t.TempDir()
	original := []byte("original Apple aerial stays untouched")
	if err := os.WriteFile(filepath.Join(root, "aerial.mov"), original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "aerial.mov"), filepath.Join(root, "alias.mov")); err != nil {
		t.Fatal(err)
	}
	roots, convert := appearanceWallpaperRoots, convertAppearanceWallpaper
	t.Cleanup(func() { appearanceWallpaperRoots = roots; convertAppearanceWallpaper = convert })
	appearanceWallpaperRoots = func() []string { return []string{root} }
	convertAppearanceWallpaper = func(ctx context.Context, source, output string) error {
		if source != filepath.Join(root, "aerial.mov") {
			t.Fatal("unexpected native source")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("conversion has no deadline")
		}
		return os.WriteFile(output, []byte{0, 0, 0, 12, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, 0600)
	}
	invoke := func(claims *UserClaims, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/appearance", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		f.api.handleAppearance(w, r)
		return w
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	for _, claims := range []*UserClaims{{UserID: "other"}, {UserID: "owner", AccessToken: &accesstokens.Token{ProjectKind: "work", ProjectIdentity: "project"}}, {UserID: "owner", Scope: "report-preview"}} {
		if got := invoke(claims, `{"operation":"mac_wallpapers"}`); got.Code != 403 {
			t.Fatalf("foreign/scoped list = %d", got.Code)
		}
	}
	listed := invoke(owner, `{"operation":"mac_wallpapers"}`)
	if listed.Code != 200 || strings.Contains(listed.Body.String(), root) {
		t.Fatal("inventory refused or leaked host path", listed.Code)
	}
	var list struct {
		Wallpapers []appearanceWallpaper `json:"wallpapers"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &list); err != nil || len(list.Wallpapers) != 1 {
		t.Fatal("expected real aerial only, not symlink", err)
	}
	if got := invoke(owner, `{"operation":"import_mac_wallpaper","wallpaper_id":"../../private"}`); got.Code != 400 {
		t.Fatal("native path accepted")
	}
	imported := invoke(owner, `{"operation":"import_mac_wallpaper","wallpaper_id":"`+list.Wallpapers[0].ID+`"}`)
	var result struct {
		AssetID string `json:"asset_id"`
	}
	if imported.Code != 200 || json.Unmarshal(imported.Body.Bytes(), &result) != nil || !validAppearanceAssetID(result.AssetID) {
		t.Fatal("import failed", imported.Code, imported.Body.String())
	}
	read := func(claims *UserClaims, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/appearance/videos/"+id, nil)
		r = mux.SetURLVars(r, map[string]string{"id": id})
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		f.api.handleAppearanceVideo(w, r)
		return w
	}
	if got := read(owner, result.AssetID); got.Code != 200 || got.Header().Get("Content-Type") != "video/mp4" {
		t.Fatal("own media refused", got.Code)
	}
	if got := read(&UserClaims{UserID: "other"}, result.AssetID); got.Code != 403 {
		t.Fatal("foreign clip read allowed")
	}
	if got := read(owner, "../outside"); got.Code != 404 {
		t.Fatal("media traversal accepted")
	}
	after, _ := os.ReadFile(filepath.Join(root, "aerial.mov"))
	if !bytes.Equal(after, original) {
		t.Fatal("original modified")
	}
	current := invoke(owner, `{"operation":"read"}`)
	if strings.Contains(current.Body.String(), `"asset_id"`) {
		t.Fatal("import silently applied theme")
	}
	if _, err := patchAppearanceTheme(defaultAppearanceTheme(), map[string]any{"background_video": map[string]any{"asset_id": result.AssetID, "dim": .7, "playback_rate": .5}}); err != nil {
		t.Fatal("imported own asset shape refused", err)
	}
}
