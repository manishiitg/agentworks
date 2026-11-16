package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	convertAppearanceWallpaper = func(ctx context.Context, source, output string, spec appearanceClipSpec) error {
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
	// #2771: keep the same downloaded aerial when the playback preset is too
	// large. Sparse outputs reproduce high-fps sizes without loading a codec.
	firstConvert := convertAppearanceWallpaper
	for _, fitsAt := range []int{2, 4, 0} {
		t.Run(fmt.Sprintf("oversize_fits_at_%d", fitsAt), func(t *testing.T) {
			attempts := 0
			var deadline time.Time
			convertAppearanceWallpaper = func(ctx context.Context, source, output string, spec appearanceClipSpec) error {
				attempts++
				if spec != appearanceClipAttempts[attempts-1] || source != filepath.Join(root, "aerial.mov") {
					t.Fatal("fallback changed its admitted source or skipped a bounded preset")
				}
				current, ok := ctx.Deadline()
				if !ok || (attempts > 1 && !current.Equal(deadline)) {
					t.Fatal("fallback extended the import deadline")
				}
				deadline = current
				if err := firstConvert(ctx, source, output, spec); err != nil {
					return err
				}
				if attempts != fitsAt {
					return os.Truncate(output, appearanceClipMaxBytes+1)
				}
				return nil
			}
			got := invoke(owner, `{"operation":"import_mac_wallpaper","wallpaper_id":"`+list.Wallpapers[0].ID+`"}`)
			if fitsAt == 0 {
				if got.Code != 422 || attempts != len(appearanceClipAttempts) {
					t.Fatalf("unbounded oversize retry: status=%d attempts=%d", got.Code, attempts)
				}
			} else {
				spec := appearanceClipAttempts[fitsAt-1]
				if got.Code != 200 || attempts != fitsAt || !strings.Contains(got.Body.String(), fmt.Sprintf("%d-second %dp", spec.seconds, spec.height)) {
					t.Fatalf("smaller copy not imported accurately: status=%d attempts=%d body=%s", got.Code, attempts, got.Body.String())
				}
			}
			dir, _ := appearanceMediaDir(owner.UserID)
			left, _ := filepath.Glob(filepath.Join(dir, "import-*"))
			if len(left) != 0 {
				t.Fatal("temporary oversize clips retained")
			}
		})
	}
	convertAppearanceWallpaper = firstConvert
	// Two real imports cannot launch simultaneous converters on the same Mac account.
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan *httptest.ResponseRecorder, 1)
	baseConvert := convertAppearanceWallpaper
	convertAppearanceWallpaper = func(ctx context.Context, source, output string, spec appearanceClipSpec) error {
		close(started)
		<-release
		return baseConvert(ctx, source, output, spec)
	}
	body := `{"operation":"import_mac_wallpaper","wallpaper_id":"` + list.Wallpapers[0].ID + `"}`
	go func() { done <- invoke(owner, body) }()
	<-started
	busy := invoke(owner, body)
	close(release)
	finished := <-done
	if busy.Code != 409 || finished.Code != 200 {
		t.Fatal("parallel imports were not serialized", busy.Code, finished.Code)
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
