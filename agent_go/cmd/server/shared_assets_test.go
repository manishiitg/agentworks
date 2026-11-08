package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
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

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

func sharedTestURL(p string) string {
	return "/api/public/file?" + url.Values{"path": {base64.StdEncoding.EncodeToString([]byte(p))}}.Encode()
}
func TestSharedAssetsWorkflowRolesAndPersonalIsolation(t *testing.T) {
	f := newExternalToolsFixture(t)
	for _, operation := range []string{"read", "list", "archive"} {
		for _, user := range []string{"owner", "reader", "outsider"} {
			p := "Workflow/invoices/docs"
			if operation == "read" {
				p += "/process.md"
			}
			r := adminRequest("GET", sharedTestURL(p)+"&uid=owner", "", &UserClaims{UserID: user, Username: user}, nil)
			w := httptest.NewRecorder()
			f.api.servePublicAsset(w, r, operation)
			want := 200
			if user == "outsider" {
				want = 403
			}
			if w.Code != want {
				t.Fatalf("%s %s: %d %s", user, operation, w.Code, w.Body)
			}
		}
	}
	for _, p := range []string{"../config/users.json", "Workflow/invoices/../secret/workflow.json", "Workflow/invoices/secrets/key", "_users/owner/Downloads/private.txt", "config/users.json"} {
		r := adminRequest("GET", sharedTestURL(p), "", &UserClaims{UserID: "reader", Username: "reader"}, nil)
		w := httptest.NewRecorder()
		f.api.handlePublicFile(w, r)
		if w.Code == 200 {
			t.Fatal("private path accepted", p)
		}
	}
	r := adminRequest("GET", sharedTestURL("Downloads/private.txt")+"&uid=owner", "", &UserClaims{UserID: "reader", Username: "reader"}, nil)
	w := httptest.NewRecorder()
	f.api.handlePublicFile(w, r)
	if w.Code != 403 {
		t.Fatal("uid authorized another user's file", w.Code)
	}
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","access":{"owners":["owner"],"readers":[]}}`)
	r = adminRequest("GET", sharedTestURL("Workflow/invoices/docs/process.md"), "", &UserClaims{UserID: "reader", Username: "reader"}, nil)
	w = httptest.NewRecorder()
	f.api.handlePublicFile(w, r)
	if w.Code != 403 {
		t.Fatal("removed reader retains share access")
	}
}
func TestSharedAssetLinksLargeDownloadsAndRevocation(t *testing.T) {
	tokenTestSetup(t)
	f := newExternalToolsFixture(t)
	filename := "docs/日本 report.bin"
	payload := strings.Repeat("asset-data", 350000)
	f.write(t, "Workflow/invoices/"+filename, payload)
	w := f.call(t, "owner", "get_file_link", map[string]any{"workflow_id": "invoices", "path": filename})
	result := externalTestBody(t, w, 200)
	preview, err := url.Parse(result["preview_url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(preview.Query().Get("path"))
	if err != nil || string(decoded) != "Workflow/invoices/"+filename {
		t.Fatal("bad link encoding", preview, err)
	}
	if result["size"].(float64) != float64(len(payload)) || strings.Contains(preview.RawQuery, "token") {
		t.Fatal("bad asset metadata")
	}
	folderResult := externalTestBody(t, f.call(t, "owner", "get_file_link", map[string]any{"workflow_id": "invoices", "path": "docs"}), 200)
	folderPreview, err := url.Parse(folderResult["preview_url"].(string))
	if err != nil || folderPreview.Path != "/folder" || folderResult["type"] != "folder" || folderResult["download_url"] != nil {
		t.Fatal("bad folder link metadata", folderResult, err)
	}
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	token, raw, err := store.Issue(context.Background(), accesstokens.Token{Name: "reader", UserID: "reader", Username: "reader", Scopes: []string{"files:read"}, WorkflowIDs: []string{"invoices"}, ExpiresAt: now.Add(time.Hour)}, now)
	if err != nil {
		t.Fatal(err)
	}
	handler := AuthMiddleware(http.HandlerFunc(f.api.handleExternalAssetContent))
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := agentworksclient.New(server.URL, raw)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "asset.bin")
	n, err := client.Download(context.Background(), "invoices", filename, destination)
	if err != nil || n != int64(len(payload)) {
		t.Fatal("download failed", n, err)
	}
	data, _ := os.ReadFile(destination)
	if string(data) != payload {
		t.Fatal("download corrupted")
	}
	request := httptest.NewRequest("GET", "/api/external/v1/files/content?"+url.Values{"workflow_id": {"invoices"}, "path": {filename}}.Encode(), nil)
	request.Header.Set("Authorization", "Bearer "+raw)
	request.Header.Set("Range", "bytes=0-15")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != 206 || w.Body.String() != payload[:16] {
		t.Fatal("range failed", w.Code, w.Body)
	}
	request.Method = "HEAD"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD failed", w.Code, w.Body)
	}
	// Personal MCP credentials follow the person's live role (PLAT-856,
	// owner 20 Oct), rather than the older saved scope/workflow selections.
	for _, restriction := range []accesstokens.Token{
		{Name: "no file scope", UserID: "reader", Username: "reader", Scopes: []string{"workflows:read"}, AllWorkflows: true, ExpiresAt: now.Add(time.Hour)},
		{Name: "other workflow", UserID: "reader", Username: "reader", Scopes: []string{"files:read"}, WorkflowIDs: []string{"secret"}, ExpiresAt: now.Add(time.Hour)},
	} {
		_, restricted, err := store.Issue(context.Background(), restriction, now)
		if err != nil {
			t.Fatal(err)
		}
		limited := request.Clone(request.Context())
		limited.Method = "GET"
		limited.Header.Set("Authorization", "Bearer "+restricted)
		denied := httptest.NewRecorder()
		handler.ServeHTTP(denied, limited)
		if denied.Code != http.StatusPartialContent || denied.Body.String() != payload[:16] {
			t.Fatal("personal role access changed", restriction.Name, denied.Code)
		}
	}
	// Project connections keep saved consent. Exercise the real grant-to-claims
	// mapping and asset handler, with the permitted case as its control.
	t.Setenv("PUBLIC_URL", "https://app.example.com")
	for _, allowed := range []bool{false, true} {
		scopes := []string{"workflows:read"}
		if allowed {
			scopes = append(scopes, "files:read")
		}
		connection := mcpOAuthTokenForGrant(mcpOAuthGrant{
			UserID: "reader", Username: "reader", Scopes: scopes,
			Resource: "https://app.example.com" + projectMCPPrefix + "workflow/invoices/view",
		})
		claims, err := accessTokenClaims(connection)
		if err != nil {
			t.Fatal(err)
		}
		if claims.AccessToken == nil || claims.AccessToken.ProjectKind != "workflow" {
			t.Fatal("project connection lost its binding")
		}
		scoped := adminRequest(http.MethodGet, request.URL.String(), "", claims, nil)
		scoped.Header.Set("Range", "bytes=0-15")
		result := httptest.NewRecorder()
		f.api.handleExternalAssetContent(result, scoped)
		if allowed {
			if result.Code != http.StatusPartialContent || result.Body.String() != payload[:16] {
				t.Fatalf("permitted project download: HTTP%d", result.Code)
			}
		} else if result.Code != http.StatusForbidden || !strings.Contains(result.Body.String(), "insufficient_scope") {
			t.Fatalf("project without file consent: HTTP%d %s", result.Code, result.Body.String())
		}
	}
	// One token per account: each Issue above revoked the reader's previous
	// token, so issue the full token again before checking removal —
	// otherwise the refusal below would come from the replaced token, not
	// from the lost workflow access this step is about.
	token, raw, err = store.Issue(context.Background(), accesstokens.Token{Name: "reader", UserID: "reader", Username: "reader", Scopes: []string{"files:read"}, WorkflowIDs: []string{"invoices"}, ExpiresAt: now.Add(time.Hour)}, now)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+raw)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != 200 {
		t.Fatal("the reader's reissued token was refused before removal", w.Code, w.Body.String())
	}
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","access":{"owners":["owner"],"readers":[]}}`)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != 403 && w.Code != 404 {
		t.Fatal("removed reader retains PAT download access", w.Code, w.Body.String())
	}
	if err = store.Revoke(context.Background(), token.ID, "reader", now); err != nil {
		t.Fatal(err)
	}
	request.Method = "GET"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != 401 {
		t.Fatal("revoked PAT download accepted")
	}
}
func TestSharedAssetArchiveOmitsPrivateAndSymlinks(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.write(t, "Workflow/invoices/secrets/private.txt", "secret")
	f.write(t, "Workflow/invoices/builder/conversation/private.json", "secret")
	if err := os.Symlink(filepath.Join(f.docs, "Workflow/secret"), filepath.Join(f.docs, "Workflow/invoices/escape")); err != nil {
		t.Fatal(err)
	}
	r := adminRequest("GET", sharedTestURL("Workflow/invoices"), "", &UserClaims{UserID: "reader", Username: "reader"}, nil)
	w := httptest.NewRecorder()
	f.api.handlePublicFolderDownload(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range archive.File {
		if strings.Contains(file.Name, "private") || strings.Contains(file.Name, "escape") {
			t.Fatal("private artifact archived", file.Name)
		}
		reader, _ := file.Open()
		_, _ = io.Copy(io.Discard, reader)
		reader.Close()
	}
	w = httptest.NewRecorder()
	f.api.handlePublicFolder(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var listing struct{ Data []map[string]any }
	json.Unmarshal(w.Body.Bytes(), &listing)
	found := false
	for _, item := range listing.Data {
		if item["filepath"] == "Workflow/invoices/docs" {
			found = true
			if len(item["children"].([]any)) != 1 {
				t.Fatal("folder hierarchy missing")
			}
		}
	}
	if !found {
		t.Fatal("docs folder missing")
	}
}
