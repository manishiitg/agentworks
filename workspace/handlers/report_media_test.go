package handlers

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func TestReportMediaRawRangeAndSymlink(t *testing.T) {
	dir := t.TempDir()
	// macOS temp roots can themselves have a symlink prefix.
	dir, _ = filepath.EvalSymlinks(dir)
	old := viper.GetString("docs-dir")
	viper.Set("docs-dir", dir)
	defer viper.Set("docs-dir", old)
	assets := filepath.Join(dir, "Workflow/test/db/assets")
	if err := os.MkdirAll(assets, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "clip.webm"), []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outside.webm"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "outside.webm"), filepath.Join(assets, "escape.webm")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		file   string
		status int
	}{{"clip.webm", 206}, {"escape.webm", 403}} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "filepath", Value: "Workflow/test/db/assets/" + tc.file}}
		c.Request = httptest.NewRequest("GET", "/?report_media=true", nil)
		c.Request.Header.Set("Range", "bytes=2-5")
		GetRawDocument(c)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.file, w.Code, w.Body.String())
		}
		if tc.status == 206 && w.Body.String() != "2345" {
			t.Fatal("incorrect bytes")
		}
	}
}

// A large file opened at a line pages from that line, so the browser never reads everything before it.
func TestRawDocumentLineOffset(t *testing.T) {
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	old := viper.GetString("docs-dir")
	viper.Set("docs-dir", dir)
	defer viper.Set("docs-dir", old)
	if err := os.WriteFile(filepath.Join(dir, "big.log"), []byte("one\ntwo\nthree\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for line, want := range map[string]string{"1": `"offset":0`, "3": `"offset":8`, "9": `"found":false`} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "filepath", Value: "big.log"}}
		c.Request = httptest.NewRequest("GET", "/?line_offset="+line, nil)
		GetRawDocument(c)
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("line %s: %d %s", line, w.Code, w.Body.String())
		}
	}
}
