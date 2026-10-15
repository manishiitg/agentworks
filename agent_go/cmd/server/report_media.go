package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const reportMediaScope = "report-media"
const reportMediaStreamPath = "/api/workflow/report-media"
const reportMediaTTL = 30 * time.Minute

func observeDashboardMedia(w http.ResponseWriter, r *http.Request, stage string, workspace, file *string) (http.ResponseWriter, func()) {
	recorded := &statusCapturingResponseWriter{ResponseWriter: w}
	return recorded, func() {
		status := recorded.status
		if status == 0 {
			status = http.StatusOK
		}
		log.Printf("[DASHBOARD_MEDIA] stage=%s workspace=%q path=%q method=%s status=%d bytes=%d", stage, *workspace, *file, r.Method, status, recorded.bytes)
	}
}

// Only durable media can be embedded. Reject ambiguous paths rather than
// cleaning traversal into a different authorized resource.
func validReportMediaPath(workspace, file string) bool {
	for _, value := range []string{workspace, file} {
		if value == "" || path.IsAbs(value) || path.Clean(value) != value || strings.ContainsAny(value, "\\\x00%") {
			return false
		}
		for _, part := range strings.Split(value, "/") {
			if part == ".." || part == "." {
				return false
			}
		}
	}
	return strings.HasPrefix(file, "db/assets/") && reportMediaType(file) != ""
}

func reportMediaType(file string) string {
	switch strings.ToLower(path.Ext(file)) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	case ".wav":
		return "audio/wav"
	case ".ogg", ".oga":
		return "audio/ogg"
	case ".ogv":
		return "video/ogg"
	}
	return ""
}

// The normal bearer-authenticated API mints an expiring file-only credential.
// HTML media elements cannot set Authorization headers; never expose the user's
// general session token in their URLs.
func (api *StreamingAPI) handleReportMediaURL(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Workspace string `json:"workspace"`
		Path      string `json:"path"`
	}
	var finish func()
	w, finish = observeDashboardMedia(w, r, "url", &body.Workspace, &body.Path)
	defer finish()
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil || !validReportMediaPath(body.Workspace, body.Path) {
		http.Error(w, "This video needs a saved project asset. Use the path returned by Export, or ask the Builder to save the video under db/assets/.", http.StatusBadRequest)
		return
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if _, err := reportPreviewWorkspace(r, claims, body.Workspace); err != nil {
		http.Error(w, "workflow mismatch", http.StatusForbidden)
		return
	}
	if !requireWorkflowVisible(w, r, body.Workspace) {
		return
	}
	if ValidateConfiguredAuthSecret() != nil {
		http.Error(w, "media signing unavailable", 503)
		return
	}
	expires := time.Now().Add(reportMediaTTL)
	if claims.ExpiresAt != nil && claims.ExpiresAt.Time.Before(expires) {
		expires = claims.ExpiresAt.Time
	}
	media := &UserClaims{UserID: claims.UserID, Username: claims.Username, Email: claims.Email,
		Scope: reportMediaScope, ScopeWorkspace: body.Workspace, ScopeFile: body.Path,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(expires), IssuedAt: jwt.NewNumericDate(time.Now()), Issuer: "mcp-agent-builder", Subject: claims.UserID}}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, media).SignedString(GetAuthSecret())
	if err != nil {
		http.Error(w, "media signing failed", 500)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"url": reportMediaStreamPath + "?token=" + url.QueryEscape(token), "expires_at": expires.UTC()})
}

func (api *StreamingAPI) handleReportMediaStream(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	workspace, file := "", ""
	if claims != nil {
		workspace, file = claims.ScopeWorkspace, claims.ScopeFile
	}
	var finish func()
	w, finish = observeDashboardMedia(w, r, "stream", &workspace, &file)
	defer finish()
	if claims == nil || claims.Scope != reportMediaScope || !validReportMediaPath(claims.ScopeWorkspace, claims.ScopeFile) {
		http.Error(w, "file-scoped media token required", http.StatusForbidden)
		return
	}
	// Recheck current permissions on each range request, including revocations.
	if !requireWorkflowVisible(w, r, claims.ScopeWorkspace) {
		return
	}
	segments := strings.Split(claims.ScopeWorkspace+"/"+claims.ScopeFile, "/")
	for i := range segments {
		segments[i] = url.PathEscape(segments[i])
	}
	upstream := getWorkspaceAPIURL() + "/api/documents/" + strings.Join(segments, "/") + "/raw?report_media=true"
	// Workspace raw serving accepts GET and implements ranges via ServeContent.
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream, nil)
	if err != nil {
		http.Error(w, "invalid media request", 500)
		return
	}
	req.Header.Set("X-User-ID", claims.UserID)
	req.Header.Set("X-Workspace-Token", os.Getenv("WORKSPACE_API_TOKEN"))
	for _, header := range []string{"Range", "If-Range", "If-Modified-Since", "If-None-Match"} {
		if value := r.Header.Get(header); value != "" {
			req.Header.Set(header, value)
		}
	}
	resp, err := workspaceHTTPClient.Do(req)
	if err != nil {
		http.Error(w, "media service unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	for _, header := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
		if value := resp.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	if resp.StatusCode == 200 || resp.StatusCode == 206 {
		// ServeContent uses multipart/byteranges when a media engine asks for
		// more than one range. Preserve its boundary-bearing Content-Type: the
		// multipart body is not itself a video payload. Single-range and full
		// responses still get the trusted MIME inferred from the scoped path.
		upstreamType := resp.Header.Get("Content-Type")
		if strings.HasPrefix(strings.ToLower(upstreamType), "multipart/byteranges;") {
			w.Header().Set("Content-Type", upstreamType)
		} else {
			w.Header().Set("Content-Type", reportMediaType(claims.ScopeFile))
		}
		w.Header().Set("Content-Disposition", "inline")
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, resp.Body)
	}
}
