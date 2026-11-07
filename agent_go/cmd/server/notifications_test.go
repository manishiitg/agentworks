package server

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// "Every agent I can see" notifies only people who can watch the chat now (reviewer, 5 Nov): a joined Team member, not
// an invited person or an outsider, and nobody for a tab set to Only me or once the Team is off.
func TestNotifyOtherPeopleOnlyWhenTheyCanWatchTheChat(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","products":["work"]},{"id":"member","username":"member","products":["work"]},{"id":"invitee","username":"invitee","products":["work"]},{"id":"outsider","username":"outsider","products":["work"]}]}`)
	team := crewTeam{ProjectID: "crew-1", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member"}, {UserID: "invitee", Invited: true}}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	chat := notifyChat{owner: "owner", project: "crew-1", found: true}
	for user, want := range map[string]bool{"owner": true, "member": true, "invitee": false, "outsider": false} {
		if got := api.notifyCanSee(t.Context(), user, chat, "session-1"); got != want {
			t.Errorf("%s can see: %t, want %t", user, got, want)
		}
	}
	onlyMe := chat
	onlyMe.tab.Record.ConversationKey = "crew-1:chat:private"
	team.AgentChoices = map[string]map[string]bool{"owner": {"crew-1:chat:private": false}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	if api.notifyCanSee(t.Context(), "member", onlyMe, "session-1") {
		t.Error("a member was notified about a tab set to Only me")
	}
	team.On = false
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	if api.notifyCanSee(t.Context(), "member", chat, "session-1") {
		t.Error("a member was notified after the Team was turned off")
	}
}

// The server posts Web Push only to the browsers' own push services, never to an address a caller chose.
func TestNotifyWebPushOnlyToBrowserPushServices(t *testing.T) {
	for endpoint, want := range map[string]bool{
		"https://fcm.googleapis.com/fcm/send/abc":            true,
		"https://updates.push.services.mozilla.com/wpush/v2": true,
		"https://web.push.apple.com/QGx":                     true,
		"https://wns2-par02p.notify.windows.com/w/?token=1":  true,
		"http://fcm.googleapis.com/fcm/send/abc":             false,
		"https://fcm.googleapis.com.example.com/x":           false,
		"https://169.254.169.254/latest":                     false,
		"https://fcm.googleapis.com:8443/x":                  false,
	} {
		if got := notifyPushHost(endpoint); got != want {
			t.Errorf("%s: %t, want %t", endpoint, got, want)
		}
	}
}

// PLAT-856-561: the split app needs its existing private proof plus its local
// person's session. A browser's session/Origin or a different caller is refused.
func TestDesktopNotifyStreamRequiresThisMacAppAndPerson(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("DEFAULT_USER_ID", "owner")
	oldStartup, oldHost, oldKey := desktopStartup, desktopBootstrapHost, desktopSocketKey
	t.Cleanup(func() { desktopStartup, desktopBootstrapHost, desktopSocketKey = oldStartup, oldHost, oldKey })
	desktopBootstrapHost = "127.0.0.1:18743"
	desktopStartup.NotifySecret = "app-notify-proof"
	api := &StreamingAPI{}
	for _, mode := range []string{"packaged", "split"} {
		kinds := []string{"allowed", "missing-proof", "forged-proof", "wrong-person", "access-token", "wrong-host", "missing-session"}
		if mode == "split" {
			kinds = append(kinds, "wrong-origin")
		}
		for _, kind := range kinds {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				desktopSocketKey = nil
				if mode == "split" {
					desktopSocketKey = []byte("private-development-socket-key")
				}
				claims := &UserClaims{UserID: "owner"}
				if kind == "wrong-person" {
					claims.UserID = "other"
				}
				if kind == "access-token" {
					claims.AccessToken = &accesstokens.Token{}
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if kind != "missing-session" {
					ctx = context.WithValue(ctx, UserContextKey, claims)
				}
				request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18743/api/desktop/notify-stream", nil).WithContext(ctx)
				request.Header.Set("Authorization", "Bearer local-session")
				request.Header.Set("X-AgentWorks-Notify", desktopStartup.NotifySecret)
				if mode == "split" {
					request.Header.Set("Origin", "http://127.0.0.1:51733")
					request.Header.Set("X-AgentWorks-Desktop", desktopSocketProof(request.Header.Get("Origin")))
				}
				if kind == "missing-proof" {
					request.Header.Del("X-AgentWorks-Desktop")
					request.Header.Del("X-AgentWorks-Notify")
				}
				if kind == "forged-proof" {
					request.Header.Set("X-AgentWorks-Desktop", "forged")
					request.Header.Set("X-AgentWorks-Notify", "forged")
				}
				if kind == "wrong-origin" {
					request.Header.Set("Origin", "http://127.0.0.1:51734")
				}
				if kind == "wrong-host" {
					request.Host = "127.0.0.1:18744"
				}
				output := &notifyCancelWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
				api.handleDesktopNotifyStream(output, request)
				if kind == "allowed" {
					if output.Code != http.StatusOK || !strings.Contains(output.Body.String(), "event: ready") {
						t.Fatalf("app stream refused: %d", output.Code)
					}
				} else if output.Code != http.StatusForbidden {
					t.Fatalf("invalid app admitted: %d", output.Code)
				}
			})
		}
	}
}

type notifyCancelWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *notifyCancelWriter) Flush() { w.ResponseRecorder.Flush(); w.cancel() }
