package server

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// macSenderAccount is the server account a Mac's own person is signed in with. On a Mac every local agent belongs
// to the one local user, who has no name or photo in the Mac's user list, so a message row read "Person". The name
// and photo come from the sign-in the sender's Crew is paired with, else the Mac's only sign-in. Display only.
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

var macSenderPhotos = struct {
	sync.Mutex
	byConnection map[string]macSenderPhoto
}{byConnection: map[string]macSenderPhoto{}}

type macSenderPhoto struct {
	fields userAvatarFields
	at     time.Time
}

// macSenderAvatar is the signed-in person's current photo, asked from their server: the sign-in saved no photo,
// and a photo's address changes whenever they change it. Kept for a few minutes so a chat full of messages asks once.
func macSenderAvatar(ctx context.Context, owner, connectionID string) userAvatarFields {
	macSenderPhotos.Lock()
	cached, ok := macSenderPhotos.byConnection[connectionID]
	macSenderPhotos.Unlock()
	if ok && time.Since(cached.at) < 5*time.Minute {
		return cached.fields
	}
	c, err := desktopConnectionFor(owner, connectionID)
	if err != nil {
		return userAvatarFields{}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := crewTeamRemoteDo(ctx, owner, c, "GET", "/api/auth/me", "application/json", nil)
	if err != nil {
		return cached.fields
	}
	defer resp.Body.Close()
	var me userAvatarFields
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&me) != nil {
		return cached.fields
	}
	macSenderPhotos.Lock()
	macSenderPhotos.byConnection[connectionID] = macSenderPhoto{fields: me, at: time.Now()}
	macSenderPhotos.Unlock()
	return me
}
