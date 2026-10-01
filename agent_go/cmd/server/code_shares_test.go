package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func putCodeShares(t *testing.T, api *StreamingAPI, caller, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := mux.SetURLVars(profileRouteRequest(http.MethodPut, "/api/agent-profiles/code/projects/c0de0001-0000/shares", []byte(body), caller), map[string]string{"project_id": "c0de0001-0000"})
	rec := httptest.NewRecorder()
	api.handlePutCodeShares(rec, req)
	return rec
}

func codeTurnAccess(api *StreamingAPI, userID string) (WorkflowAccessLevel, error) {
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: userID, Username: userID})
	return api.conversationTargetAccess(ctx, QueryRequest{AgentProfileID: "code", AgentProfileConversationKey: "c0de0001-0000", SelectedFolder: codePrivacyOwnerRoot})
}

func sharedCodeRows(t *testing.T, api *StreamingAPI, caller string) []sharedProjectSummary {
	t.Helper()
	req := mux.SetURLVars(profileRouteRequest(http.MethodGet, "/api/agent-profiles/code/shared-projects", nil, caller), map[string]string{"id": "code"})
	rec := httptest.NewRecorder()
	api.handleListSharedProjects(rec, req)
	var decoded struct {
		Projects []sharedProjectSummary `json:"projects"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &decoded) != nil {
		t.Fatalf("shared listing = %d %s", rec.Code, rec.Body.String())
	}
	return decoded.Projects
}

// Old persisted grants must never revive human access, including the most
// privileged co_owner grant. Exercise the real HTTP/file/conversation gates.
func TestCodeLegacySharesGrantNoAccess(t *testing.T) {
	api, profile := newCodePrivacyFixture(t)
	for _, role := range []string{"viewer", "editor", "co_owner"} {
		t.Run(role, func(t *testing.T) {
			legacy := `{"projects":{"owner/c0de0001-0000":{"owner_id":"owner","project_id":"c0de0001-0000","grants":{"other":"` + role + `"}}}}`
			if err := writeFileToWorkspace(context.Background(), codeSharesFilePath(), legacy); err != nil {
				t.Fatal(err)
			}
			if level, err := codeTurnAccess(api, "other"); err == nil || level != WorkflowAccessNone {
				t.Fatalf("legacy %s ran Code: %v %v", role, level, err)
			}
			if rows := sharedCodeRows(t, api, "other"); len(rows) != 0 {
				t.Fatalf("legacy grant listed Code: %+v", rows)
			}
			if codeLinkReadAllowed(context.Background(), &UserClaims{UserID: "other"}, "owner", codePrivacyOwnerRoot) {
				t.Fatal("legacy grant opened Code file links")
			}
			if _, _, err := resolveConversationBindingForUser(context.Background(), "other", profile, "c0de0001-0000"); err == nil {
				t.Fatal("legacy grant opened a Code conversation")
			}
			service := &ProductScheduleService{api: api, registry: api.agentProfiles}
			if service.crewProjectExists(context.Background(), "other", "code", "c0de0001-0000") {
				t.Fatal("legacy grant stamped outbound Code calls")
			}
			if level, err := codeTurnAccess(api, "owner"); err != nil || level != WorkflowAccessOwner {
				t.Fatalf("owner lost access: %v %v", level, err)
			}
			if !codeLinkReadAllowed(context.Background(), &UserClaims{UserID: "owner"}, "owner", codePrivacyOwnerRoot) {
				t.Fatal("owner lost file links")
			}
			after, present, err := readFileFromWorkspace(context.Background(), codeSharesFilePath())
			if err != nil || !present || after != legacy {
				t.Fatal("legacy data was rewritten or deleted")
			}
		})
	}
}

func TestCodeSharingEndpointsRetired(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	for _, caller := range []string{"owner", "other"} {
		if rec := putCodeShares(t, api, caller, `{"grants":[{"user":"other","role":"co_owner"}]}`); rec.Code != http.StatusGone {
			t.Fatalf("PUT shares = %d %s", rec.Code, rec.Body.String())
		}
		req := mux.SetURLVars(profileRouteRequest(http.MethodGet, "/x", nil, caller), map[string]string{"project_id": "guessed-private-id"})
		rec := httptest.NewRecorder()
		api.handleGetCodeShares(rec, req)
		if rec.Code != http.StatusGone || strings.Contains(rec.Body.String(), "owner_id") {
			t.Fatalf("GET shares = %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestCodeIsAValidOutboundCallerForOwnerOnly(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	service := &ProductScheduleService{api: api, registry: api.agentProfiles}
	if !service.crewProjectExists(context.Background(), "owner", "code", "c0de0001-0000") {
		t.Fatal("owner cannot call from Code")
	}
	if service.crewProjectExists(context.Background(), "other", "code", "c0de0001-0000") {
		t.Fatal("another person can call from Code")
	}
}

// A Code is never a folder reference/attachment or public call target,
// including for its owner. Private function resolution is tested separately.
func TestCodeExcludedFromPublicTargetsAndAttachments(t *testing.T) {
	newCodePrivacyFixture(t)
	_ = writeFileToWorkspace(context.Background(), codeSharesFilePath(), `{"projects":{"owner/c0de0001-0000":{"owner_id":"owner","project_id":"c0de0001-0000","grants":{"other":"co_owner"}}}}`)
	for _, user := range []string{"owner", "other"} {
		ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user, Username: user})
		for _, ref := range []string{"Chats/Code/projects/app-c0de0001", codePrivacyOwnerRoot} {
			if _, _, err := authorizeWorkflowContextPathsWithReadRoots(ctx, []string{ref}); err == nil {
				t.Fatalf("%s attached Code %s as a reference", user, ref)
			}
			if target, err := resolveTriggerTarget(ctx, &UserClaims{UserID: user, Username: user}, ref); err == nil {
				t.Fatalf("%s resolved Code %s as a call target: %+v", user, ref, target)
			}
		}
	}
}

// A Code answers only 1:1 Slack DMs and WhatsApp, each sender in their own
// chat, and only people with at least editor access.
func TestCodeBotTurnsAreDirectMessageOnly(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	ctx := context.Background()
	route := &services.ProfileRoute{ProfileID: "code", ConversationKey: "c0de0001-0000", WorkspaceUserID: "owner"}
	turn := func(sender string, msg services.BotIncomingMessage) error {
		msg.Text = "hi"
		msg.PresetProfile = route
		_, _, _, err := api.botProfileTurn(ctx, sender, msg, services.ThreadID{Platform: msg.Platform, ChannelID: "c", ThreadTS: "t"})
		return err
	}
	// Basic setup: without the bots feature a Code takes no chat-app
	// message at all, not even the owner's DM.
	if err := turn("owner", services.BotIncomingMessage{Platform: "slack", DirectMessage: true}); err == nil || !strings.Contains(err.Error(), "does not take chat-app messages yet") {
		t.Fatalf("a Code without bots took a DM: %v", err)
	}
	// With bots switched on (a later step), only 1:1 DMs are taken.
	withBots, err := api.agentProfiles.Resolve("code", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	withBots.Version = 2
	withBots.Features = []agentprofiles.FeatureBinding{{ID: "bots", Options: map[string]string{"channels": "slack,whatsapp", "dm_only": "true"}}}
	if err := agentprofiles.ResolveFeatures(&withBots); err != nil {
		t.Fatal(err)
	}
	if err := api.agentProfiles.RegisterProfile(withBots); err != nil {
		t.Fatal(err)
	}
	if err := turn("owner", services.BotIncomingMessage{Platform: "slack", ChannelID: "C123"}); err == nil || !strings.Contains(err.Error(), "only 1:1") {
		t.Fatalf("a Slack channel message reached the Code: %v", err)
	}
	if err := turn("owner", services.BotIncomingMessage{Platform: "gmail"}); err == nil {
		t.Fatal("a non-DM platform reached the Code")
	}
	// No access, then view-only: refused even in a DM.
	if err := turn("other", services.BotIncomingMessage{Platform: "slack", DirectMessage: true}); err == nil {
		t.Fatal("a DM from someone without access reached the Code")
	}
	putCodeShares(t, api, "owner", `{"grants":[{"user":"other","role":"viewer"}]}`)
	if err := turn("other", services.BotIncomingMessage{Platform: "whatsapp"}); err == nil {
		t.Fatal("a viewer's WhatsApp message ran the Code")
	}
	// WhatsApp is private per person: even an editor of a shared Code cannot
	// reach it there, only its owner.
	putCodeShares(t, api, "owner", `{"grants":[{"user":"other","role":"editor"}]}`)
	if err := turn("other", services.BotIncomingMessage{Platform: "whatsapp"}); err == nil || !strings.Contains(err.Error(), "owner only") {
		t.Fatalf("an editor reached the Code on WhatsApp: %v", err)
	}
	if err := turn("other", services.BotIncomingMessage{Platform: "slack", DirectMessage: true}); err == nil {
		t.Fatal("a former editor's Slack DM reached private Code")
	}

}

// The owner can follow up while their first turn is still active.
func TestOwnerCodeFollowUpBeforeHistoryIsSaved(t *testing.T) {
	api, profile := newCodePrivacyFixture(t)
	putCodeShares(t, api, "owner", `{"grants":[{"user":"owner","role":"editor"}]}`)
	req := profileRouteRequest(http.MethodPost, "/", nil, "owner")
	first, err := api.resolveAgentProfileConversation(req, profile, "c0de0001-0000")
	if err != nil {
		t.Fatal(err)
	}
	api.activeSessions = map[string]*ActiveSessionInfo{first.SessionID: {SessionID: first.SessionID, UserID: "owner"}}
	follow := profileRouteRequest(http.MethodPost, "/", nil, "owner")
	follow.Header.Set("X-Session-ID", first.SessionID)
	follow.Header.Set("X-Conversation-Continuation", "true")
	again, err := api.resolveAgentProfileConversation(follow, profile, "c0de0001-0000")
	if err != nil {
		t.Fatalf("follow-up while the first turn runs: %v", err)
	}
	if again.SessionID != first.SessionID {
		t.Fatalf("follow-up went to %q, want %q", again.SessionID, first.SessionID)
	}
}
