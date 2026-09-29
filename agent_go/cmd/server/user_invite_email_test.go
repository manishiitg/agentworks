package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Adding a person emails them a Supabase Auth invitation that lands on this
// deployment's sign-in page. Every outcome is reported, the service key never
// appears in a result, and an email problem never stops the person being added.
func TestInviteEmailThroughSupabase(t *testing.T) {
	var gotPath, gotQuery, gotKey, gotAuth, gotBody string
	status, response := http.StatusOK, `{"id":"abc"}`
	supabase := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		gotKey, gotAuth = r.Header.Get("apikey"), r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	defer supabase.Close()
	t.Setenv("SUPABASE_URL", supabase.URL)
	t.Setenv("SUPABASE_SERVICE_ROLE_KEY", "service-role-secret")
	ctx := context.Background()

	got := sendSupabaseInvite(ctx, "ana@gmail.com", "https://agents.example.com", "manish", "Excellence Technologies")
	if got.Status != inviteEmailSent || gotPath != "/auth/v1/invite" || !strings.Contains(gotQuery, "redirect_to=https%3A%2F%2Fagents.example.com") ||
		gotKey != "service-role-secret" || gotAuth != "Bearer service-role-secret" || !strings.Contains(gotBody, `"email":"ana@gmail.com"`) || !strings.Contains(gotBody, "Excellence Technologies") {
		t.Fatalf("sent = %+v, path %s?%s key %q body %s", got, gotPath, gotQuery, gotKey, gotBody)
	}
	for name, tc := range map[string]struct {
		status   int
		body     string
		want     string
		contains string
	}{
		"already registered": {422, `{"error_code":"email_exists","msg":"A user with this email address has already been registered"}`, inviteEmailExists, ""},
		"rate limit":         {429, `{"error_code":"over_email_send_rate_limit","msg":"email rate limit exceeded"}`, inviteEmailFailed, "rate limit"},
		"default smtp team":  {400, `{"msg":"Email address not authorized"}`, inviteEmailFailed, "custom SMTP"},
		"other error":        {500, `{"msg":"boom"}`, inviteEmailFailed, "HTTP 500"},
	} {
		status, response = tc.status, tc.body
		got := sendSupabaseInvite(ctx, "ana@gmail.com", "", "manish", "App")
		if got.Status != tc.want || !strings.Contains(got.Detail, tc.contains) || strings.Contains(got.Detail, "service-role-secret") {
			t.Fatalf("%s: %+v", name, got)
		}
	}

	// No key: nothing is sent and the admin is told to copy the invitation.
	t.Setenv("SUPABASE_SERVICE_ROLE_KEY", "")
	if got := sendSupabaseInvite(ctx, "ana@gmail.com", "", "manish", "App"); got.Status != inviteEmailNotConfigured {
		t.Fatalf("without a key = %+v", got)
	}
	// An unreachable auth service is a failure, not a panic, and says nothing secret.
	t.Setenv("SUPABASE_SERVICE_ROLE_KEY", "service-role-secret")
	t.Setenv("SUPABASE_URL", "http://127.0.0.1:1")
	if got := sendSupabaseInvite(ctx, "ana@gmail.com", "", "manish", "App"); got.Status != inviteEmailFailed || strings.Contains(got.Detail, "service-role-secret") {
		t.Fatalf("unreachable = %+v", got)
	}
}

func TestAddingAPersonInvitesThemButEmailNeverBlocksAdding(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[]}`)
	supabase := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer supabase.Close()
	t.Setenv("SUPABASE_URL", supabase.URL)
	t.Setenv("SUPABASE_SERVICE_ROLE_KEY", "k")
	t.Setenv("PUBLIC_URL", "https://agents.example.com")
	api := &StreamingAPI{}

	rec := httptest.NewRecorder()
	api.handleAdminCreateUser(rec, adminRequest(http.MethodPost, "/x", `{"username":"ana@gmail.com","email":"ana@gmail.com","products":["code"],"invite":true}`, &UserClaims{UserID: "adm"}, nil))
	var view userAdminView
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &view) != nil {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	if view.InviteEmail != inviteEmailFailed || view.SignInURL != "https://agents.example.com" || !view.Invited {
		t.Fatalf("view = %+v", view)
	}
	if dir, _ := readUserDirectoryFile(); dir.byEmail("ana@gmail.com") == nil {
		t.Fatal("the person was not added because the email failed")
	}

	// Without an invite request nothing is sent.
	plain := httptest.NewRecorder()
	api.handleAdminCreateUser(plain, adminRequest(http.MethodPost, "/x", `{"username":"bob@gmail.com","email":"bob@gmail.com"}`, &UserClaims{UserID: "adm"}, nil))
	if strings.Contains(plain.Body.String(), "invite_email") {
		t.Fatalf("an invitation was attempted without being asked for: %s", plain.Body.String())
	}

	// Resend works only for a person added by email who has not signed in.
	t.Setenv("SUPABASE_SERVICE_ROLE_KEY", "")
	resend := httptest.NewRecorder()
	api.handleAdminInviteUser(resend, adminRequest(http.MethodPost, "/x", "", &UserClaims{UserID: "adm"}, map[string]string{"id": view.ID}))
	if !strings.Contains(resend.Body.String(), inviteEmailNotConfigured) || !strings.Contains(resend.Body.String(), "https://agents.example.com") {
		t.Fatalf("resend = %d %s", resend.Code, resend.Body.String())
	}
	gone := httptest.NewRecorder()
	api.handleAdminInviteUser(gone, adminRequest(http.MethodPost, "/x", "", &UserClaims{UserID: "adm"}, map[string]string{"id": "nobody"}))
	if gone.Code != http.StatusNotFound {
		t.Fatalf("unknown id = %d", gone.Code)
	}
	if got := api.invitePerson(context.Background(), httptest.NewRequest(http.MethodPost, "/x", nil), UserRecord{Email: "z@x.com", PasswordHash: "hash"}, "adm"); got.Status != inviteEmailFailed {
		t.Fatalf("invited someone who already has a password: %+v", got)
	}
}
