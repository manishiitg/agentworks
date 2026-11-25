package server

import (
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// macSenderAccount is the server account a Mac's own person is signed in with. On a Mac every local agent belongs
// to the one local user, who has no name in the Mac's user list, so a message row read "Person". The name
// comes from the sign-in the sender's Crew is paired with, else the Mac's only sign-in. Display only.
func macSenderAccount(source agentMessageEndpoint) (UserInfo, string, bool) {
	if !IsLocalMode() || source.UserID == "" || source.UserID != GetDefaultUserID() {
		return UserInfo{}, "", false
	}
	project := source.ID
	if source.Hosted != nil && source.Hosted.Project != "" {
		project = source.Hosted.Project
	}
	if b, err := crewroot.ReadSync(project); err == nil && b != nil && b.ConnectionID != "" {
		if c, err := desktopConnectionFor(source.UserID, b.ConnectionID); err == nil && c.User.Username != "" {
			return c.User, c.ID, true
		}
	}
	if all, err := readDesktopConnections(source.UserID); err == nil && len(all) == 1 && all[0].User.Username != "" {
		return all[0].User, all[0].ID, true
	}
	return UserInfo{}, "", false
}
