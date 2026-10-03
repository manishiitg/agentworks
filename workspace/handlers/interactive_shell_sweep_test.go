package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSweepInteractiveShellsStopsOnlyUntrackedShellsOfThePrefix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	config := filepath.Join(root, "slotctl.json")
	if err := os.WriteFile(config, []byte(`{"slot_run_root":"`+root+`/run"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTWORKS_SLOTCTL_CONFIG", config)
	mk := func(slot, id string) string {
		dir := filepath.Join(root, "run", slot, "shells", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	kept, stale, otherPrefix, otherSlot := mk("cf01", "code-keep"), mk("cf01", "code-stale"), mk("cf01", "slot-e2e-x"), mk("cf02", "code-old")
	router := gin.New()
	router.POST("/sweep", SweepInteractiveShells)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/sweep", bytes.NewReader([]byte(`{"prefix":"code-","keep":["code-keep"]}`))))
	if w.Code != http.StatusOK {
		t.Fatalf("sweep = %d %s", w.Code, w.Body.String())
	}
	for dir, wantGone := range map[string]bool{kept: false, stale: true, otherPrefix: false, otherSlot: true} {
		_, err := os.Stat(dir)
		if gone := os.IsNotExist(err); gone != wantGone {
			t.Errorf("%s: gone=%v, want %v", filepath.Base(dir), gone, wantGone)
		}
	}
	bad := httptest.NewRecorder()
	router.ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/sweep", bytes.NewReader([]byte(`{"prefix":"","keep":[]}`))))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("an empty prefix must be refused (it would stop every shell), got %d", bad.Code)
	}
}
