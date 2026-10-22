package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// Exercise the chat route and actual guarded workspace writer, with different
// owner/caller identities. A client-supplied folder must never choose the target.
func TestCrewChatUploadUsesCallerFolder(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work"]},{"id":"other","role":"editor","products":["work"]},{"id":"runner","role":"viewer","products":["work"]}]}`)
	image, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aOS8AAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	upload := func(claims *UserClaims, data []byte, filename string) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		part, err := form.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(data)
		_ = form.WriteField("folder_path", "_users/owner/Chats/private")
		_ = form.Close()
		r := adminRequest(http.MethodPost, "/api/agent-profiles/work/projects/team/uploads", "", claims, map[string]string{"id": "work", "project_id": "team"})
		r.Body = io.NopCloser(&body)
		r.Header.Set("Content-Type", form.FormDataContentType())
		r.Header.Set("X-Request-ID", "crew-upload-proof")
		w := httptest.NewRecorder()
		f.api.handleCrewChatUpload(w, r)
		return w
	}
	for _, actor := range []string{"owner", "other", "runner"} {
		claims := &UserClaims{UserID: actor, Username: actor}
		if actor == "runner" {
			claims.AccessToken = &accesstokens.Token{Scopes: []string{"crews:run"}, AllCrews: true}
		}
		w := upload(claims, image, "image.png")
		if w.Code != http.StatusOK {
			t.Fatalf("%s upload = %d %s", actor, w.Code, w.Body.String())
		}
		var response struct {
			Filepath string `json:"filepath"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		folder := "uploads/"
		if actor != "owner" {
			folder = "shared/" + actor + "/"
		}
		if !strings.HasPrefix(response.Filepath, externalCommandCrewRoot+"/"+folder) || !strings.HasSuffix(response.Filepath, "-image.png") {
			t.Fatalf("%s destination = %s", actor, response.Filepath)
		}
		stored, err := os.ReadFile(filepath.Join(f.docs, response.Filepath))
		if err != nil || !bytes.Equal(stored, image) {
			t.Fatalf("stored image differs: %v", err)
		}
	}
	if w := upload(&UserClaims{UserID: "other"}, make([]byte, crewSharedFileMaxBytes+1), "big.png"); w.Code != 413 || !strings.Contains(w.Body.String(), "smaller than 5 MB") {
		t.Fatalf("over-limit = %d %s", w.Code, w.Body.String())
	}
	view := &UserClaims{UserID: "other", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	if w := upload(view, image, "view.png"); w.Code != 403 {
		t.Fatalf("view token upload = %d %s", w.Code, w.Body.String())
	}
	// A planted shared directory cannot redirect an attachment into private files.
	shared := filepath.Join(f.docs, externalCommandCrewRoot, "shared", "other")
	if err := os.Rename(shared, shared+"-saved"); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, shared); err != nil {
		t.Fatal(err)
	}
	if w := upload(&UserClaims{UserID: "other"}, image, "escape.png"); w.Code == 200 {
		t.Fatal("upload followed a symlink out of the Crew")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("upload changed outside files: %v", err)
	}
	if err := defaultProjectOwners().SetPrivate("work", "test-team", true); err != nil {
		t.Fatal(err)
	}
	if w := upload(&UserClaims{UserID: "other"}, image, "revoked.png"); w.Code != 404 {
		t.Fatalf("revoked sharing upload = %d %s", w.Code, w.Body.String())
	}
}
