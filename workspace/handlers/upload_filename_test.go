package handlers

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func TestUploadFilenameKeepsDotfiles(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  string
	}{
		{input: ".env", want: ".env"},
		{input: ".env.local", want: ".env.local"},
		{input: "report.txt", want: "report.txt"},
		{input: ".", want: "untitled"},
		{input: "..", want: "untitled"},
	} {
		if got := sanitizeFilename(tt.input); got != tt.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
	if !isAllowedFile(".env", "application/octet-stream") {
		t.Fatal(".env upload should be accepted")
	}
}

func TestUploadEmptyDotfileToCrew(t *testing.T) {
	docsDir := t.TempDir()
	previousDocsDir := viper.Get("docs-dir")
	viper.Set("docs-dir", docsDir)
	t.Cleanup(func() { viper.Set("docs-dir", previousDocsDir) })

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if _, err := writer.CreateFormFile("file", ".env"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("folder_path", "Chats/Work/projects/alpha"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-User-ID", "crew-user")
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = req
	UploadFile(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	path := filepath.Join(docsDir, "_users", "crew-user", "Chats", "Work", "projects", "alpha", ".env")
	var response struct {
		Data struct {
			AbsolutePath string `json:"absolute_path"`
			FilePath     string `json:"filepath"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.AbsolutePath != path || response.Data.FilePath != "Chats/Work/projects/alpha/.env" {
		t.Fatalf("upload paths = %#v, want absolute path %q and user-relative filepath", response.Data, path)
	}
	contents, err := os.ReadFile(path)
	if err != nil || len(contents) != 0 {
		t.Fatalf("empty .env upload missing or changed: size=%d, err=%v", len(contents), err)
	}
}
