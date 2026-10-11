package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Real MCP discovery/call, disk-backed workspace stat/read/list/archive, and
// authenticated asset handlers must all preserve the same Crew/file bounds.
func TestExternalCrewLinksOverMCPAndAssetAccess(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("PUBLIC_URL", "https://app.example.com")
	f.write(t, externalCommandCrewRoot+"/outputs/report.txt", "Report contents")
	f.write(t, externalCommandCrewRoot+"/outputs/blocked.txt", "Private contents")
	if err := os.Symlink(filepath.Join(f.docs, externalCommandCrewRoot, "outputs", "blocked.txt"), filepath.Join(f.docs, externalCommandCrewRoot, "outputs", "alias.txt")); err != nil {
		t.Fatal(err)
	}
	reader := crewReader("other")
	reader.AccessToken.AllCrews = false
	reader.AccessToken.CrewIDs = []string{"team"}
	reader.AccessToken.FileGuard = &wf.FolderGuard{ReadPaths: []string{"outputs"}, BlockedPaths: []string{"outputs/blocked.txt"}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv := serveExternalMCP(t, f.api, reader)
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	link := func(p string) map[string]any {
		t.Helper()
		result := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "get_file_link", "arguments": map[string]any{"crew_id": "team", "path": p}})
		requireRemoteSuccess(t, result, "get_file_link")
		var value map[string]any
		if err := json.Unmarshal([]byte(marshalStructured(t, result)), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	file, folder := link("outputs/report.txt"), link("outputs")
	if file["crew_id"] != "team" || file["size"] != float64(len("Report contents")) || folder["type"] != "folder" || folder["download_url"] != nil {
		t.Fatalf("links = %v / %v", file, folder)
	}
	serve := func(claims *UserClaims, raw, operation, method, byteRange string) *httptest.ResponseRecorder {
		t.Helper()
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		req := adminRequest(method, parsed.RequestURI(), "", claims, nil)
		if byteRange != "" {
			req.Header.Set("Range", byteRange)
		}
		w := httptest.NewRecorder()
		if parsed.Path == "/api/external/v1/files/content" {
			f.api.handleExternalAssetContent(w, req)
		} else {
			f.api.servePublicAsset(w, req, operation)
		}
		return w
	}
	preview, download := file["preview_url"].(string), file["download_url"].(string)
	if strings.Contains(preview, "token") || strings.Contains(download, "token") {
		t.Fatal("credentials in links")
	}
	for _, raw := range []string{preview, download} {
		if w := serve(reader, raw, "read", "GET", "bytes=0-5"); w.Code != 206 || w.Body.String() != "Report" {
			t.Fatalf("range read = %d %s", w.Code, w.Body.String())
		}
		if w := serve(reader, raw, "read", "HEAD", ""); w.Code != 200 || w.Body.Len() != 0 {
			t.Fatalf("HEAD = %d %s", w.Code, w.Body.String())
		}
		if w := serve(nil, raw, "read", "GET", ""); w.Code != 401 {
			t.Fatalf("anonymous read = %d", w.Code)
		}
	}
	folderURL := folder["preview_url"].(string)
	if w := serve(reader, folderURL, "list", "GET", ""); w.Code != 200 || strings.Contains(w.Body.String(), "blocked.txt") || strings.Contains(w.Body.String(), "alias.txt") || !strings.Contains(w.Body.String(), "report.txt") {
		t.Fatalf("guarded listing = %d %s", w.Code, w.Body.String())
	}
	w := serve(reader, folderURL, "archive", "GET", "")
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if w.Code != 200 || err != nil || len(archive.File) != 1 || archive.File[0].Name != "report.txt" {
		t.Fatalf("guarded archive = %d (%v)", w.Code, err)
	}
	archived, err := archive.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(archived)
	archived.Close()
	if err != nil || string(data) != "Report contents" {
		t.Fatalf("archive contents = %q (%v)", data, err)
	}
	call := func(claims *UserClaims, args map[string]any, status int) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"name": "get_file_link", "arguments": args})
		w := httptest.NewRecorder()
		f.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/v1/call", string(body), claims, nil))
		externalTestBody(t, w, status)
	}
	call(reader, map[string]any{"crew_id": "team", "workflow_id": "invoices", "path": "outputs/report.txt"}, 400)
	call(reader, map[string]any{"path": "outputs/report.txt"}, 400)
	call(reader, map[string]any{"workflow_id": "invoices", "path": "outputs/report.txt"}, 403)
	for _, p := range []string{"product.json", "builder/chat.json", "outputs/blocked.txt", ".env"} {
		call(reader, map[string]any{"crew_id": "team", "path": p}, 403)
	}
	call(reader, map[string]any{"crew_id": "team", "path": "../outputs/report.txt"}, 400)
	call(reader, map[string]any{"crew_id": "team", "path": "outputs/alias.txt"}, 403)
	call(reader, map[string]any{"crew_id": "team", "path": "outputs/missing.txt"}, 404)
	// Changing consent/bounds after creating a URL immediately refuses both
	// the link call and its browser/download routes, including the owner.
	for _, user := range []string{"owner", "other"} {
		denied := crewReader(user)
		denied.AccessToken.AllCrews = false
		denied.AccessToken.CrewIDs = []string{"another"}
		call(denied, map[string]any{"crew_id": "team", "path": "outputs/report.txt"}, 403)
		for _, raw := range []string{preview, download, folderURL} {
			if w := serve(denied, raw, "read", "GET", ""); w.Code != 403 {
				t.Fatalf("out-of-bound %s = %d", user, w.Code)
			}
		}
		denied.AccessToken.AllCrews = true
		denied.AccessToken.Scopes = []string{"files:read"}
		call(denied, map[string]any{"crew_id": "team", "path": "outputs/report.txt"}, 403)
		for _, raw := range []string{preview, download} {
			if w := serve(denied, raw, "read", "GET", ""); w.Code != 403 {
				t.Fatalf("missing Crew scope %s = %d", user, w.Code)
			}
		}
	}
	reader.AccessToken.FileGuard.BlockedPaths = append(reader.AccessToken.FileGuard.BlockedPaths, "outputs/report.txt")
	for _, raw := range []string{preview, download} {
		if w := serve(reader, raw, "read", "GET", ""); w.Code != 403 {
			t.Fatalf("new file restriction = %d", w.Code)
		}
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work"]},{"id":"other","role":"editor","products":["code"]}]}`)
	for _, raw := range []string{preview, download} {
		if w := serve(reader, raw, "read", "GET", ""); w.Code != 403 && w.Code != 404 {
			t.Fatalf("removed Crew product = %d", w.Code)
		}
	}
}
