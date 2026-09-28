package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHostedAdminCookieIsSecureBehindTLSProxy(t *testing.T) {
	a := &Admin{HumanToken: "private-alpha-secret-with-32-characters", PublicURL: "https://caplayer.example.com"}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/admin/login", strings.NewReader("token=private-alpha-secret-with-32-characters"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.uiLogin(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want %d", w.Code, http.StatusSeeOther)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("hosted admin cookie must be Secure, HttpOnly, and SameSite Strict: %+v", cookies)
	}
}
