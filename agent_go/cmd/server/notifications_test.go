package server

import (
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
