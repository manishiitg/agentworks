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
	upload := func(claims *UserClaims, data []byte, filename string, dashboard ...bool) *httptest.ResponseRecorder {
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
		if len(dashboard) > 0 && dashboard[0] {
			r.URL.RawQuery = "workspace=" + externalCommandCrewRoot
			f.api.handleDashboardUpload(w, r)
		} else {
			f.api.handleCrewChatUpload(w, r)
		}
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
	// Custom dashboards use the exact chat writer and caller-selected area.
	for _, actor := range []string{"owner", "other", "runner"} {
		claims := &UserClaims{UserID: actor, Username: actor}
		if actor == "runner" {
			claims.AccessToken = &accesstokens.Token{Scopes: []string{"crews:run"}, AllCrews: true}
		}
		for _, transport := range []string{"website", "mcp"} {
			var response *httptest.ResponseRecorder
			if transport == "website" {
				response = upload(claims, image, "reference.png", true)
			} else {
				response = httptest.NewRecorder()
				r := adminRequest(http.MethodPost, "/api/dashboards", "", claims, nil)
				f.api.dashboardUploadBase64(response, r, externalCommandCrewRoot, "reference.png", base64.StdEncoding.EncodeToString(image))
			}
			if response.Code != 200 {
				t.Fatalf("%s/%s: %d %s", actor, transport, response.Code, response.Body.String())
			}
			var saved map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
				t.Fatal(err)
			}
			prefix := "uploads/"
			if actor != "owner" {
				prefix = "shared/" + actor + "/"
			}
			if !strings.HasPrefix(saved["path"].(string), prefix) {
				t.Fatalf("%s/%s escaped attachment area: %q", actor, transport, saved["path"])
			}
		}
	}
	if w := upload(&UserClaims{UserID: "other"}, make([]byte, crewSharedFileMaxBytes+1), "big.png"); w.Code != 413 || !strings.Contains(w.Body.String(), "smaller than 5 MB") {
		t.Fatalf("over-limit = %d %s", w.Code, w.Body.String())
	}
	// A malformed form is a format failure, not a file-size failure.
	bad := adminRequest(http.MethodPost, "/api/agent-profiles/work/projects/team/uploads", `{}`, &UserClaims{UserID: "owner"}, map[string]string{"id": "work", "project_id": "team"})
	bad.Header.Set("Content-Type", "application/json")
	invalid := httptest.NewRecorder()
	f.api.handleCrewChatUpload(invalid, bad)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "upload was not understood") {
		t.Fatalf("malformed upload = %d %s", invalid.Code, invalid.Body.String())
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
