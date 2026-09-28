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
	if cookies[0].Value == a.HumanToken || len(cookies[0].Value) < 32 {
		t.Fatal("admin cookie exposed the master token or lacked entropy")
	}
	valid := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/admin/", nil)
	valid.AddCookie(cookies[0])
	if !a.authed(valid) {
		t.Fatal("issued admin session was not accepted")
	}
	invalid := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/admin/", nil)
	invalid.AddCookie(&http.Cookie{Name: "gw_admin", Value: a.HumanToken})
	if a.authed(invalid) {
		t.Fatal("master token was accepted as a cookie")
	}
}

func TestAdminLogoutRevokesCookieSession(t *testing.T) {
	a := &Admin{HumanToken: "local-test-secret", PublicURL: "http://127.0.0.1:18746"}
	login := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader("token=local-test-secret"))
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResponse := httptest.NewRecorder()
	a.uiLogin(loginResponse, login)
	cookies := loginResponse.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies: %+v", cookies)
	}
	logout := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	logout.AddCookie(cookies[0])
	logoutResponse := httptest.NewRecorder()
	a.requireUI(a.uiLogout)(logoutResponse, logout)
	if logoutResponse.Code != http.StatusSeeOther {
		t.Fatalf("logout status = %d", logoutResponse.Code)
	}
	if a.authed(logout) {
		t.Fatal("logged-out cookie was still accepted")
	}
	cleared := logoutResponse.Result().Cookies()
	if len(cleared) != 1 || cleared[0].MaxAge >= 0 {
		t.Fatalf("logout did not expire browser cookie: %+v", cleared)
	}
}
