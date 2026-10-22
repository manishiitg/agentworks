package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// Attach generated screenshots inside the existing authorized MCP call. Public
// browser links cannot carry the caller's browser sign-in into an AI client.
func (api *StreamingAPI) externalMCPDashboardPreviewResult(r *http.Request, rec *externalMCPRecorder) *mcp.CallToolResult {
	var preview struct {
		Workspace   string                    `json:"workspace"`
		Screenshots []reportPreviewScreenshot `json:"screenshots"`
	}
	var value map[string]any
	if json.Unmarshal(rec.body.Bytes(), &preview) != nil || json.Unmarshal(rec.body.Bytes(), &value) != nil || preview.Workspace == "" {
		return externalMCPDispatchResult(rec)
	}
	selected := []reportPreviewScreenshot{}
	// Prefer one desktop and one mobile image, using light when it was rendered.
	for _, width := range []int{reportPreviewDesktopWidth, reportPreviewMobileWidth, reportPreviewTabletWidth} {
		var choice *reportPreviewScreenshot
		for i := range preview.Screenshots {
			shot := &preview.Screenshots[i]
			if shot.Width == width && shot.Error == "" && (choice == nil || shot.Theme == "light") {
				choice = shot
			}
		}
		if choice != nil {
			selected = append(selected, *choice)
		}
		if len(selected) == 2 {
			break
		}
	}
	images := []mcp.Content{}
	attached := []reportPreviewScreenshot{}
	for _, shot := range selected {
		if path.Clean(shot.Path) != shot.Path || !strings.HasPrefix(shot.Path, reportPreviewScreenshotDir+"/") || !strings.HasSuffix(shot.Path, ".png") {
			continue
		}
		query := url.Values{"workspace": {preview.Workspace}, "path": {shot.Path}}
		request := httptest.NewRequest(http.MethodGet, reportPreviewAPIPrefix+"file?"+query.Encode(), nil).WithContext(r.Context())
		image := httptest.NewRecorder()
		// Reuse current product access and the caller's file grants.
		api.handleReportPreviewFile(image, request)
		data := image.Body.Bytes()
		if image.Code != http.StatusOK || len(data) > 2<<20 || !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
			continue
		}
		images = append(images, mcp.NewImageContent(base64.StdEncoding.EncodeToString(data), "image/png"))
		attached = append(attached, shot)
	}
	value["attached_screenshots"] = attached
	if len(images) > 0 {
		value["next_step"] = "Inspect the attached screenshot images, in attached_screenshots order, following the designer's preferences. Fix script/fetch errors and stale Loading placeholders. More screenshots are available through the browser links. If your client does not display images, say visual inspection is still needed."
	} else {
		value["next_step"] = "No screenshot image could be attached within the two-image, 2 MiB per-image limit and current file access. Open the screenshot links in a signed-in browser; visual inspection is still needed."
	}
	result, err := mcp.NewToolResultJSON(value)
	if err != nil {
		return externalMCPDispatchResult(rec)
	}
	result.Content = append(result.Content, images...)
	logfWithContext(api.httpRequestLogContext(r), "[REPORT_PREVIEW_MCP] workspace=%q attached_images=%d selected_images=%d", preview.Workspace, len(images), len(selected))
	return result
}
