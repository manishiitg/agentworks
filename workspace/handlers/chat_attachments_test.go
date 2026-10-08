package handlers

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func TestChatAttachmentsCannotEscapeTheirUploadFolder(t *testing.T) {
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	docs := t.TempDir()
	old := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", old) })
	rel := "_users/owner/Chats/Code/projects/demo/uploads/chats/" + base64.RawURLEncoding.EncodeToString([]byte("chat-one"))
	root := filepath.Join(docs, filepath.FromSlash(rel))
	must(os.MkdirAll(root, 0700))
	must(os.WriteFile(filepath.Join(root, "notes.txt"), []byte(strings.Repeat("ü", 40000)), 0600))
	must(os.WriteFile(filepath.Join(root, "screen.png"), []byte("fixture"), 0600))
	outside := filepath.Join(docs, "outside.txt")
	must(os.WriteFile(outside, []byte("private fixture"), 0600))
	must(os.Symlink(outside, filepath.Join(root, "escape.txt")))
	r := gin.New()
	r.GET("/api/chat-attachments/*filepath", GetChatAttachment)
	get := func(p string) *httptest.ResponseRecorder {
		q := httptest.NewRequest(http.MethodGet, "/api/chat-attachments/"+p, nil)
		q.Header.Set("X-User-ID", "owner")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, q)
		return rec
	}
	good := get(rel + "/notes.txt")
	var text struct {
		Content   string
		Truncated bool
	}
	must(json.Unmarshal(good.Body.Bytes(), &text))
	if good.Code != 200 || !text.Truncated || len(text.Content) != 64<<10 {
		t.Fatalf("bounded text: %d %s", good.Code, good.Body.String())
	}
	image := get(rel + "/screen.png")
	if image.Code != 200 || !strings.Contains(image.Body.String(), `"data":"Zml4dHVyZQ=="`) {
		t.Fatalf("image: %d %s", image.Code, image.Body.String())
	}
	for _, p := range []string{rel + "/escape.txt", "_users/other/Chats/Code/projects/demo/uploads/chats/Y2hhdA/secret.txt", "outside.txt"} {
		if got := get(p); got.Code == 200 {
			t.Fatalf("escaped scope: %s", p)
		}
	}
	if mkfifo, err := exec.LookPath("mkfifo"); err == nil {
		must(exec.Command(mkfifo, filepath.Join(root, "pipe.txt")).Run())
		done := make(chan int, 1)
		go func() { done <- get(rel + "/pipe.txt").Code }()
		select {
		case code := <-done:
			if code != http.StatusForbidden {
				t.Fatalf("FIFO status %d", code)
			}
		case <-time.After(time.Second):
			t.Fatal("FIFO blocked attachment inspection")
		}
	}
	moved := filepath.Dir(root) + "/moved"
	must(os.Rename(root, moved))
	must(os.Symlink(moved, root))
	if got := get(rel + "/notes.txt"); got.Code == 200 {
		t.Fatal("followed replaced upload parent")
	}
}
