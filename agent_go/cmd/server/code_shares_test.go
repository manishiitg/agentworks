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

func TestCodeSharingRoles(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)

	// Not shared: nothing for the other user.
	if level, err := codeTurnAccess(api, "other"); err == nil || level != WorkflowAccessNone {
		t.Fatalf("unshared Code reached: %v %v", level, err)
	}

	// Viewer: listed and can read, but cannot run the agent.
	if rec := putCodeShares(t, api, "owner", `{"grants":[{"user":"other","role":"viewer"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("share as viewer = %d %s", rec.Code, rec.Body.String())
	}
	rows := sharedCodeRows(t, api, "other")
	if len(rows) != 1 || rows[0].ID != "c0de0001-0000" || rows[0].Role != "viewer" || rows[0].OwnerID != "owner" {
		t.Fatalf("viewer listing = %+v", rows)
	}
	if level, err := codeTurnAccess(api, "other"); err == nil || level != WorkflowAccessNone {
		t.Fatalf("a viewer ran the agent: %v %v", level, err)
	}
	if !codeLinkReadAllowed(context.Background(), &UserClaims{UserID: "other"}, "owner", codePrivacyOwnerRoot) {
		t.Fatal("a viewer cannot open the Code's links")
	}
	// A viewer cannot change sharing.
	if rec := putCodeShares(t, api, "other", `{"grants":[{"user":"other","role":"co_owner"}]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer promoted itself: %d %s", rec.Code, rec.Body.String())
	}

	// Editor: write access in their own chat.
	if rec := putCodeShares(t, api, "owner", `{"grants":[{"user":"other","role":"editor"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("share as editor = %d", rec.Code)
	}
	if level, err := codeTurnAccess(api, "other"); err != nil || level != WorkflowAccessWrite {
		t.Fatalf("editor access = %v %v", level, err)
	}
	if rec := putCodeShares(t, api, "other", `{"grants":[]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("an editor changed sharing: %d", rec.Code)
	}

	// Co-owner: owner access and may manage sharing.
	if rec := putCodeShares(t, api, "owner", `{"grants":[{"user":"other","role":"co_owner"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("share as co-owner = %d", rec.Code)
	}
	if level, err := codeTurnAccess(api, "other"); err != nil || level != WorkflowAccessOwner {
		t.Fatalf("co-owner access = %v %v", level, err)
	}
	if rec := putCodeShares(t, api, "other", `{"grants":[{"user":"other","role":"co_owner"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("a co-owner could not manage sharing: %d", rec.Code)
	}

	// Unsharing removes access at once.
	if rec := putCodeShares(t, api, "owner", `{"grants":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("unshare = %d", rec.Code)
	}
	if level, err := codeTurnAccess(api, "other"); err == nil || level != WorkflowAccessNone {
		t.Fatalf("unshared user kept access: %v %v", level, err)
	}
	if rows := sharedCodeRows(t, api, "other"); len(rows) != 0 {
		t.Fatalf("unshared Code still listed: %+v", rows)
	}
	if codeLinkReadAllowed(context.Background(), &UserClaims{UserID: "other"}, "owner", codePrivacyOwnerRoot) {
		t.Fatal("unshared user can still open links")
	}
}

func TestCodeSharingRejectsBadInput(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	for _, body := range []string{
		`{"grants":[{"user":"nobody","role":"viewer"}]}`,
		`{"grants":[{"user":"other","role":"admin"}]}`,
		`not json`,
	} {
		if rec := putCodeShares(t, api, "owner", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("PUT %s = %d", body, rec.Code)
		}
	}
	// Someone with no access cannot even see the share list.
	req := mux.SetURLVars(profileRouteRequest(http.MethodGet, "/x", nil, "other"), map[string]string{"project_id": "c0de0001-0000"})
	rec := httptest.NewRecorder()
	api.handleGetCodeShares(rec, req)
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "owner") {
		t.Fatalf("stranger read the share list: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCodeChatBindingFollowsRole(t *testing.T) {
	api, profile := newCodePrivacyFixture(t)
	ctx := context.Background()
	putCodeShares(t, api, "owner", `{"grants":[{"user":"other","role":"viewer"}]}`)
	if _, _, err := resolveConversationBindingForUser(ctx, "other", profile, "c0de0001-0000"); err == nil {
		t.Fatal("a viewer got a chat of the shared Code")
	}
	putCodeShares(t, api, "owner", `{"grants":[{"user":"other","role":"editor"}]}`)
	binding, owned, err := resolveConversationBindingForUser(ctx, "other", profile, "c0de0001-0000")
	if err != nil || owned {
		t.Fatalf("editor binding owned=%v err=%v", owned, err)
	}
	// The editor's chat is their own: no coupling to the owner's manifest.
	if binding.ManifestPath != "" || binding.AuthoritativeSessionID != "" || binding.WorkspacePath != codePrivacyOwnerRoot {
		t.Fatalf("editor binding = %+v", binding)
	}
}

// A Code calling a Crew or workflow is a valid caller for its owner and for
// people it is shared with; nobody else can stamp calls as that Code.
func TestCodeIsAValidOutboundCaller(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	service := &ProductScheduleService{api: api, registry: api.agentProfiles}
	ctx := context.Background()
	if !service.crewProjectExists(ctx, "owner", "code", "c0de0001-0000") {
		t.Fatal("the owner's Code is not a valid caller")
	}
	if service.crewProjectExists(ctx, "other", "code", "c0de0001-0000") {
		t.Fatal("an unshared user stamped calls as the owner's Code")
	}
	putCodeShares(t, api, "owner", `{"grants":[{"user":"other","role":"editor"}]}`)
	if !service.crewProjectExists(ctx, "other", "code", "c0de0001-0000") {
		t.Fatal("an editor's calls from the shared Code were refused")
	}
}

// A Code is never a reference, attachment or call target, not even for its
// owner and not when shared.
func TestCodeIsNeverAReferenceOrTarget(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	putCodeShares(t, api, "owner", `{"grants":[{"user":"other","role":"co_owner"}]}`)
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
}
