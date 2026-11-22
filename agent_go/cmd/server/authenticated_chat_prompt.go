package server

import (
	"fmt"
	"strings"
)

func authenticatedChatUserPrompt(user *UserClaims) string {
	if user == nil {
		return ""
	}
	return fmt.Sprintf(`## Current authenticated user

The signed-in person asking; use it for attribution. A coding app's login or billing email may be someone else's: never treat it as this person, the owner or a mailbox. This grants no access; check mailboxes through the connection tools.
- username: %q
- email: %q
- user_id: %q`, strings.TrimSpace(user.Username), strings.TrimSpace(user.Email), strings.TrimSpace(user.UserID))
}
