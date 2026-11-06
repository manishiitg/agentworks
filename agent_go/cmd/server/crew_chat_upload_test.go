package server

import (
	"bytes"
	"crypto/rand"
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
	"github.com/manishiitg/coding-agent-loop/workspace/chunks"
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

// PLAT-856-509 (one sync process, 6 Nov): a chat attachment goes as chunks through the same checks and writer. The owner
// and a Run person upload past the old limits; a Run person cannot name a file outside their shared/<id>/ folder; the
// plan answers nobody without access; one person's chunks are not another's.
func TestCrewChunkedUploadUsesTheSameChecksAndWriter(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work"]},{"id":"other","role":"editor","products":["work"]}]}`)
	data := make([]byte, 12<<20) // over the 10 MiB form and 11 MiB content limits
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	batches := []*bytes.Buffer{{}} // at most chunks.BatchBytes a request, as every client sends
	file, err := chunks.Split(bytes.NewReader(data), int64(len(data)), false, func(id string, piece []byte) error {
		if batches[len(batches)-1].Len()+chunks.MaxBytes > chunks.BatchBytes {
			batches = append(batches, &bytes.Buffer{})
		}
		return chunks.WriteFrame(batches[len(batches)-1], id, piece)
	})
	if err != nil {
		t.Fatal(err)
	}
	call := func(claims *UserClaims, method, step string, body []byte, contentType string) *httptest.ResponseRecorder {
		t.Helper()
		r := adminRequest(method, "/api/agent-profiles/work/projects/team/uploads", "", claims, map[string]string{"id": "work", "project_id": "team", "step": step})
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		f.api.handleCrewChatUpload(w, r)
		return w
	}
	finish := func(claims *UserClaims, fields map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		fields["file"] = file
		body, _ := json.Marshal(fields)
		return call(claims, http.MethodPost, "", body, "application/json")
	}
	plan, _ := json.Marshal(map[string]any{"file": file})
	missing := func(claims *UserClaims) int {
		t.Helper()
		w := call(claims, http.MethodPost, "plan", plan, "application/json")
		var reply struct {
			Missing []string `json:"missing"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil {
			t.Fatalf("plan: %d %s", w.Code, w.Body.String())
		}
		return len(reply.Missing)
	}
	owner, runner := &UserClaims{UserID: "owner", Username: "owner"}, &UserClaims{UserID: "other", Username: "other"}
	if w := finish(owner, map[string]any{"name": "early.bin"}); w.Code != http.StatusConflict {
		t.Fatalf("finish before sending: %d %s", w.Code, w.Body.String())
	}
	for _, claims := range []*UserClaims{owner, runner} {
		if missing(claims) == 0 {
			t.Fatalf("%s: chunks of another person's upload were reported present", claims.UserID)
		}
		for _, batch := range batches {
			if w := call(claims, http.MethodPut, "chunks", batch.Bytes(), "application/octet-stream"); w.Code != 200 {
				t.Fatalf("%s chunks: %d %s", claims.UserID, w.Code, w.Body.String())
			}
		}
		if got := missing(claims); got != 0 {
			t.Fatalf("%s still missing %d", claims.UserID, got)
		}
		w := finish(claims, map[string]any{"name": "report final.bin"})
		if w.Code != 200 {
			t.Fatalf("%s finish: %d %s", claims.UserID, w.Code, w.Body.String())
		}
		var reply struct {
			Filepath string `json:"filepath"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		folder := "uploads/"
		if claims == runner {
			folder = "shared/other/"
		}
		if !strings.HasPrefix(reply.Filepath, externalCommandCrewRoot+"/"+folder) || !strings.HasSuffix(reply.Filepath, "-report-final.bin") {
			t.Fatalf("%s destination: %s", claims.UserID, reply.Filepath)
		}
		stored, err := os.ReadFile(filepath.Join(f.docs, reply.Filepath))
		if err != nil || !bytes.Equal(stored, data) {
			t.Fatalf("%s stored file differs: %v", claims.UserID, err)
		}
	}
	// A Run person's chunks cannot be written outside their own folder, nor anyone's onto a protected path.
	if w := finish(runner, map[string]any{"path": "notes/planted.bin"}); w.Code != http.StatusForbidden {
		t.Fatalf("run person outside shared: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(f.docs, externalCommandCrewRoot, "notes", "planted.bin")); err == nil {
		t.Fatal("a Run person's upload left a file outside their folder")
	}
	if w := finish(owner, map[string]any{"path": "builder/planted.bin"}); w.Code != http.StatusForbidden {
		t.Fatalf("protected path: %d %s", w.Code, w.Body.String())
	}
	view := &UserClaims{UserID: "other", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	if w := call(view, http.MethodPost, "plan", plan, "application/json"); w.Code != http.StatusForbidden {
		t.Fatalf("view token plan: %d %s", w.Code, w.Body.String())
	}
	if err := defaultProjectOwners().SetPrivate("work", "test-team", true); err != nil {
		t.Fatal(err)
	}
	if w := call(runner, http.MethodPost, "plan", plan, "application/json"); w.Code != http.StatusNotFound {
		t.Fatalf("revoked plan: %d %s", w.Code, w.Body.String())
	}
}
