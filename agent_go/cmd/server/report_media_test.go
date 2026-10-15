package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestReportMediaPathsAndScope(t *testing.T) {
	for _, p := range []string{"../db/assets/a.mp4", "db/assets/../a.mp4", "db/assets/a.html", "db/assets/%2e%2e/a.mp4", "/db/assets/a.mp4", "db/assets/a\\b.mp4"} {
		if validReportMediaPath("Workflow/test", p) {
			t.Fatalf("accepted %q", p)
		}
	}
	if !validReportMediaPath("Workflow/test", "db/assets/run/test.webm") {
		t.Fatal("valid path rejected")
	}
	if !scopeAllowsPath(reportMediaScope, reportMediaStreamPath) || scopeAllowsPath(reportMediaScope, reportPreviewAPIPrefix+"query") {
		t.Fatal("media scope escaped")
	}
}

func TestReportMediaRangeProxy(t *testing.T) {
	var diagnostics bytes.Buffer
	previousLog := log.Writer()
	log.SetOutput(&diagnostics)
	t.Cleanup(func() { log.SetOutput(previousLog) })
	withMemoryUserDirectory(t, `{"users":[{"id":"media-reader","username":"reader","admin":true,"can_create":true,"products":[]}]}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/raw") {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-User-ID") != "media-reader" || r.URL.Query().Get("report_media") != "true" {
			t.Error("missing isolation")
		}
		http.ServeContent(w, r, "test.webm", time.Time{}, strings.NewReader("0123456789"))
	}))
	defer upstream.Close()
	t.Setenv("WORKSPACE_API_URL", upstream.URL)
	api := &StreamingAPI{}
	t.Setenv("AUTH_SECRET", "report-media-test-secret-not-for-production")
	parentExpiry := time.Now().Add(time.Minute).Truncate(time.Second)
	mint := httptest.NewRequest("POST", reportPreviewAPIPrefix+"media-url", strings.NewReader(`{"workspace":"Workflow/test","path":"db/assets/test.webm"}`))
	mint = mint.WithContext(context.WithValue(mint.Context(), UserContextKey, &UserClaims{UserID: "media-reader", RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(parentExpiry)}}))
	minted := httptest.NewRecorder()
	api.handleReportMediaURL(minted, mint)
	if minted.Code != 200 {
		t.Fatalf("mint: %d %s", minted.Code, minted.Body.String())
	}
	var link struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(minted.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link.URL)
	if err != nil {
		t.Fatal(err)
	}
	claims := &UserClaims{}
	if _, err := jwt.ParseWithClaims(u.Query().Get("token"), claims, func(*jwt.Token) (any, error) { return GetAuthSecret(), nil }); err != nil {
		t.Fatal(err)
	}
	if claims.ScopeFile != "db/assets/test.webm" || claims.ScopeWorkspace != "Workflow/test" || !claims.ExpiresAt.Time.Equal(parentExpiry) {
		t.Fatalf("wrong capability: %+v", claims)
	}
	for _, target := range []struct {
		path   string
		status int
	}{{link.URL, 200}, {"/api/workflow/report-preview/query?token=" + u.Query().Get("token"), 403}} {
		w := httptest.NewRecorder()
		AuthMiddleware(http.HandlerFunc(api.handleReportMediaStream)).ServeHTTP(w, httptest.NewRequest("GET", target.path, nil))
		if w.Code != target.status {
			t.Fatalf("capability status: %d %s", w.Code, w.Body.String())
		}
	}
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
	expired, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(GetAuthSecret())
	wExpired := httptest.NewRecorder()
	AuthMiddleware(http.HandlerFunc(api.handleReportMediaStream)).ServeHTTP(wExpired, httptest.NewRequest("GET", reportMediaStreamPath+"?token="+expired, nil))
	if wExpired.Code != 401 {
		t.Fatal("expired credential accepted")
	}
	for _, tc := range []struct {
		method, rangeValue, body string
		status                   int
	}{
		{"GET", "bytes=2-5", "2345", 206}, {"GET", "bytes=90-", "invalid range: failed to overlap\n", 416}, {"HEAD", "bytes=2-5", "", 206},
	} {
		r := httptest.NewRequest(tc.method, reportMediaStreamPath, nil)
		r.Header.Set("Range", tc.rangeValue)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "media-reader", Scope: reportMediaScope, ScopeWorkspace: "Workflow/test", ScopeFile: "db/assets/test.webm"}))
		w := httptest.NewRecorder()
		api.handleReportMediaStream(w, r)
		if w.Code != tc.status || w.Body.String() != tc.body {
			t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
		}
		if tc.status == 206 && w.Header().Get("Content-Range") != "bytes 2-5/10" {
			t.Fatal("lost content range")
		}
	}

	// Chromium commonly uses one range, while other embedded browser engines
	// may combine multiple ranges. The proxy must preserve ServeContent's
	// boundary-bearing MIME or the browser interprets the multipart body as raw
	// WebM bytes and reports that the media is corrupt.
	multi := httptest.NewRequest("GET", reportMediaStreamPath, nil)
	multi.Header.Set("Range", "bytes=0-1,8-9")
	multi = multi.WithContext(context.WithValue(multi.Context(), UserContextKey, &UserClaims{UserID: "media-reader", Scope: reportMediaScope, ScopeWorkspace: "Workflow/test", ScopeFile: "db/assets/test.webm"}))
	multiResponse := httptest.NewRecorder()
	api.handleReportMediaStream(multiResponse, multi)
	if multiResponse.Code != http.StatusPartialContent {
		t.Fatalf("multi-range status=%d body=%q", multiResponse.Code, multiResponse.Body.String())
	}
	if contentType := multiResponse.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "multipart/byteranges;") {
		t.Fatalf("multi-range content type=%q", contentType)
	}
	if !strings.HasPrefix(multiResponse.Body.String(), "--") {
		t.Fatalf("multi-range body did not contain a MIME boundary: %q", multiResponse.Body.String())
	}
	if !strings.Contains(diagnostics.String(), `[DASHBOARD_MEDIA] stage=stream workspace="Workflow/test" path="db/assets/test.webm" method=GET status=206`) || strings.Contains(diagnostics.String(), u.Query().Get("token")) {
		t.Fatal("media delivery diagnostics are missing or contain a credential")
	}
}
