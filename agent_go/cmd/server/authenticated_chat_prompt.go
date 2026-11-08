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

This request was made by the signed-in AgentWorks user below. Use this identity for user attribution. A coding provider login, subscription or billing account may belong to someone else: never treat its email as this user, the project owner or a connected mailbox. This metadata does not grant permissions or establish Gmail access; verify mailbox identity and access through the scoped connection tools.
- username: %q
- email: %q
- user_id: %q`, strings.TrimSpace(user.Username), strings.TrimSpace(user.Email), strings.TrimSpace(user.UserID))
}
