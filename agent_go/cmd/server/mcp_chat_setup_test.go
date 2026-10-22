package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestMCPChatCallbackUsesOnlyTheAuthenticatedOwnersRequest(t *testing.T) {
	t.Setenv("PUBLIC_URL", "")
	api := &StreamingAPI{lastQueryRequests: map[string]QueryRequest{"chat": {userID: "owner", oauthRedirectURI: "http://localhost:51733/api/oauth/callback"}}}
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, "chat")
	if got := api.mcpToolOAuthRedirect(ctx, "owner"); got != "http://localhost:51733/api/oauth/callback" {
		t.Fatal("authenticated local callback not available to the Builder")
	}
	if got := api.mcpToolOAuthRedirect(ctx, "other"); got != "" {
		t.Fatal("borrowed another person's callback")
	}
	var request QueryRequest
	if err := json.Unmarshal([]byte(`{"oauthRedirectURI":"https://untrusted.example/callback","oauth_redirect_uri":"https://untrusted.example/callback"}`), &request); err != nil || request.oauthRedirectURI != "" {
		t.Fatal("client JSON controlled the callback address")
	}
	t.Setenv("PUBLIC_URL", "https://configured.example")
	if got := api.mcpToolOAuthRedirect(ctx, "owner"); got != "https://configured.example/api/oauth/callback" {
		t.Fatal("deployment callback did not take precedence")
	}
}
