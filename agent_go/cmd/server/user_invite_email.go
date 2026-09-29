package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// Invitation emails (docs/design/user_accounts_and_workflow_sharing.md,
// "Invitation email"). Adding a person by email sends them a message through
// the same Supabase project that handles Google sign-in: Supabase Auth's admin
// invite. It needs SUPABASE_URL and SUPABASE_SERVICE_ROLE_KEY. The key is a
// full-admin secret for that Supabase project: keep it in the server's private
// environment file, never in code or in a reply. Without it nothing is sent
// and the admin copies the invitation instead. Sending never blocks adding
// the person.

const (
	inviteEmailSent          = "sent"
	inviteEmailNotConfigured = "not_configured"
	inviteEmailExists        = "exists"
	inviteEmailFailed        = "failed"
)

type inviteEmailResult struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// inviteHTTPClient is a var so tests can point it at a stand-in.
var inviteHTTPClient = &http.Client{Timeout: 15 * time.Second}

// publicBaseURL is the address people open this deployment at.
func publicBaseURL(r *http.Request) string {
	return strings.TrimSuffix(deriveOAuthRedirectURI(r), "/api/oauth/callback")
}

// sendSupabaseInvite asks Supabase Auth to email an invitation. The link in
// the email lands on redirectTo (this deployment's sign-in page), where the
// person continues with Google using the invited address. The result never
// includes the key or Supabase's raw response.
func sendSupabaseInvite(ctx context.Context, email, redirectTo, invitedBy, appName string) inviteEmailResult {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("SUPABASE_URL")), "/")
	key := strings.TrimSpace(os.Getenv("SUPABASE_SERVICE_ROLE_KEY"))
	if base == "" || key == "" {
		return inviteEmailResult{Status: inviteEmailNotConfigured}
	}
	endpoint := base + "/auth/v1/invite"
	if redirectTo != "" {
		endpoint += "?redirect_to=" + url.QueryEscape(redirectTo)
	}
	body, _ := json.Marshal(map[string]interface{}{"email": email, "data": map[string]string{"invited_by": invitedBy, "app": appName}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return inviteEmailResult{Status: inviteEmailFailed, Detail: "could not build the request"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", key)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := inviteHTTPClient.Do(req)
	if err != nil {
		return inviteEmailResult{Status: inviteEmailFailed, Detail: "the auth service could not be reached"}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode/100 == 2 {
		return inviteEmailResult{Status: inviteEmailSent}
	}
	var failure struct {
		ErrorCode string `json:"error_code"`
		Message   string `json:"msg"`
		Alt       string `json:"message"`
		Desc      string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &failure)
	message := strings.TrimSpace(firstNonEmptyTrimmed(failure.Message, failure.Alt, failure.Desc))
	switch {
	case failure.ErrorCode == "email_exists" || strings.Contains(strings.ToLower(message), "already been registered"):
		return inviteEmailResult{Status: inviteEmailExists}
	case resp.StatusCode == http.StatusTooManyRequests || failure.ErrorCode == "over_email_send_rate_limit":
		return inviteEmailResult{Status: inviteEmailFailed, Detail: "the email rate limit was reached; try again later"}
	case strings.Contains(strings.ToLower(message), "not authorized"):
		return inviteEmailResult{Status: inviteEmailFailed, Detail: "the auth service only emails its own team until custom SMTP is set up"}
	}
	detail := fmt.Sprintf("the auth service refused it (HTTP %d)", resp.StatusCode)
	if message != "" && len(message) <= 160 {
		detail += ": " + message
	}
	return inviteEmailResult{Status: inviteEmailFailed, Detail: detail}
}

// invitePerson sends the invitation for a directory record and reports what
// happened. Only an account that was added by email and has not signed in can
// be invited.
func (api *StreamingAPI) invitePerson(ctx context.Context, r *http.Request, rec UserRecord, invitedBy string) inviteEmailResult {
	if rec.Email == "" || rec.PasswordHash != "" || rec.SSO != nil {
		return inviteEmailResult{Status: inviteEmailFailed, Detail: "only a person added by email who has not signed in yet can be invited"}
	}
	appName := "AgentWorks"
	if name := strings.TrimSpace(os.Getenv("APP_NAME")); name != "" {
		appName = name
	}
	return sendSupabaseInvite(ctx, rec.Email, publicBaseURL(r), invitedBy, appName)
}

// POST /api/admin/users/{id}/invite: send the invitation again.
func (api *StreamingAPI) handleAdminInviteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	dir, err := readUserDirectoryFile()
	if err != nil {
		writeUsersError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rec := dir.byID(mux.Vars(r)["id"])
	if rec == nil {
		writeUsersError(w, http.StatusNotFound, "no such user")
		return
	}
	result := api.invitePerson(r.Context(), r, *rec, GetUserIDFromContext(r.Context()))
	writeUsersJSON(w, http.StatusOK, map[string]interface{}{"invite_email": result.Status, "invite_detail": result.Detail, "sign_in_url": publicBaseURL(r)})
}
