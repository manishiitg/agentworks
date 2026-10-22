package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/mark3labs/mcp-go/mcp"
)

// Real workspace reads, same caller permissions, standard MCP image blocks.
func TestDashboardPreviewMCPImagesAndPublicLinks(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("PUBLIC_URL", "https://app.example.com")
	t.Setenv("MCP_API_URL", "http://127.0.0.1:24000")
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	shots := []reportPreviewScreenshot{
		{Theme: "dark", Width: 1280, Path: "db/reports/preview/run/desktop-dark.png"},
		{Theme: "light", Width: 1280, Path: "db/reports/preview/run/desktop-light.png"},
		{Theme: "light", Width: 480, Path: "db/reports/preview/run/mobile-light.png"},
	}
	for _, shot := range shots {
		f.write(t, "Workflow/invoices/"+shot.Path, pngBytes.String())
	}
	body, _ := json.Marshal(map[string]any{"workspace": "Workflow/invoices", "screenshots": shots})
	rec := &externalMCPRecorder{status: 200, body: *bytes.NewBuffer(body)}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	call := func(claims *UserClaims) *mcp.CallToolResult {
		return f.api.externalMCPDashboardPreviewResult(adminRequest(http.MethodPost, externalMCPPath, "", claims, nil), rec)
	}
	result := call(owner)
	if len(result.Content) != 3 {
		t.Fatalf("expected JSON and two inline screenshots, got %d blocks: %v", len(result.Content), result)
	}
	for _, content := range result.Content[1:] {
		img, ok := mcp.AsImageContent(content)
		if !ok || img.MIMEType != "image/png" || img.Data != base64.StdEncoding.EncodeToString(pngBytes.Bytes()) {
			t.Fatal("screenshot did not arrive as the original PNG image block")
		}
	}
	if denied := call(&UserClaims{UserID: "outsider", Username: "outsider"}); len(denied.Content) != 1 {
		t.Fatal("inline images bypassed project access")
	}
	owner.AccessToken = &accesstokens.Token{Scopes: []string{"dashboards:read", "workflows:read", "runs:execute"}, WorkflowIDs: []string{"invoices"}, FileGuard: &wf.FolderGuard{ReadPaths: []string{shots[2].Path}}}
	if restricted := call(owner); len(restricted.Content) != 2 {
		t.Fatalf("inline images bypassed file grants or lost allowed image: %d blocks", len(restricted.Content))
	}
	base := f.api.reportPreviewResultBaseURL()
	page, err := parseDashboardURL(sharedAssetPublicURL(base, "report", "Workflow/invoices"), "db/reports/managed/example/revision/index.html")
	if err != nil {
		t.Fatal(err)
	}
	file := sharedAssetPublicURL(base, "file", "Workflow/invoices/"+shots[2].Path)
	for _, raw := range []string{page, file} {
		u, err := url.Parse(raw)
		if err != nil || u.Host != "app.example.com" || strings.Contains(raw, "token=") || strings.Contains(raw, "127.0.0.1") || strings.HasPrefix(u.Path, "/api/") {
			t.Fatalf("not an authenticated public app viewer link: %s", raw)
		}
	}
	u, _ := url.Parse(page)
	if u.Query().Get("document") != "db/reports/managed/example/revision/index.html" {
		t.Fatal("page link lost its exact dashboard document")
	}
}
