package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func TestDocumentCreateOnlyEmptyFile(t *testing.T) {
	docs := t.TempDir()
	previous := viper.GetString("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", previous) })
	t.Setenv("WORKSPACE_FILE_STATE_DIR", filepath.Join(t.TempDir(), "locks"))
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Any("/api/documents/*filepath", HandleDocumentRequest)
	path := "/api/documents/Chats/Code/projects/project/functions/new.ts"
	request := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-ID", "owner")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	created := request(`{"content":"","create_only":true}`)
	if created.Code != http.StatusOK {
		t.Fatalf("create empty file: %d %s", created.Code, created.Body.String())
	}
	file := filepath.Join(docs, "_users", "owner", "Chats", "Code", "projects", "project", "functions", "new.ts")
	if data, err := os.ReadFile(file); err != nil || len(data) != 0 {
		t.Fatalf("empty nested file: %q %v", data, err)
	}
	read := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-User-ID", "owner")
	router.ServeHTTP(read, req)
	var document struct {
		Data struct {
			FilePath string `json:"filepath"`
			Encoding string `json:"encoding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &document); err != nil || document.Data.FilePath == "" || document.Data.Encoding != "utf-8" {
		t.Fatalf("read new empty file: %s %v", read.Body.String(), err)
	}
	// Refuse an empty existing file too, then preserve an intervening agent edit.
	for _, content := range []string{"", "agent wrote this"} {
		if err := os.WriteFile(file, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		conflict := request(`{"content":"","create_only":true}`)
		if conflict.Code != http.StatusConflict {
			t.Fatalf("existing file: %d %s", conflict.Code, conflict.Body.String())
		}
		if data, err := os.ReadFile(file); err != nil || string(data) != content {
			t.Fatalf("existing content changed: %q %v", data, err)
		}
	}
	// An occupied symlink must never be replaced or truncate its target.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(file), "target.ts")
	if err := os.WriteFile(target, []byte("keep target"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, file); err != nil {
		t.Fatal(err)
	}
	if response := request(`{"content":"","create_only":true}`); response.Code != http.StatusConflict {
		t.Fatalf("symlink: %d %s", response.Code, response.Body.String())
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "keep target" {
		t.Fatalf("symlink target changed: %q %v", data, err)
	}
	for _, body := range []string{`{"create_only":true}`, `{"content":null,"create_only":true}`} {
		if response := request(body); response.Code != http.StatusBadRequest {
			t.Fatalf("missing content accepted: %d %s", response.Code, response.Body.String())
		}
	}
}
