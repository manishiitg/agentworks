package server

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGmailInboundDeploymentConfiguration(t *testing.T) {
	t.Setenv("GMAIL_INBOUND_TOPICS", `{"rts-client":"projects/rts-test/topics/agentworks-gmail","another-client":"projects/another-project/topics/agentworks-gmail"}`)
	t.Setenv("GMAIL_INBOUND_AUDIENCE", "https://video.realtrainingsys.com/api/hooks/gmail/events")
	t.Setenv("GMAIL_INBOUND_PUSH_EMAIL", "gmail-rts@rts-test.iam.gserviceaccount.com")
	c, e := readGmailInboundConfig()
	if e != nil || len(c.Topics) != 2 {
		t.Fatalf("config %+v %v", c, e)
	}
	for _, bad := range []string{"http://localhost/api/hooks/gmail/events", "https://example.com/api/hooks/gmail/events/extra", "https://example.com/api/hooks/gmail/events?token=secret"} {
		t.Setenv("GMAIL_INBOUND_AUDIENCE", bad)
		if _, e = readGmailInboundConfig(); e == nil {
			t.Fatalf("unsafe audience accepted: %s", bad)
		}
	}
	if !shouldSkipAuth(gmailInboundEventPath) || shouldSkipAuth(gmailInboundEventPath+"/extra") || shouldSkipAuth("/api/gmail-inbound/route") {
		t.Fatal("management endpoint exposed by authentication bypass")
	}
}
func TestGmailInboundAddressAndConversationIdentity(t *testing.T) {
	address, e := inboundAddress("Manish+existing@RTS.com", "1234-5678")
	if e != nil || address != "manish+agent-12345678@rts.com" {
		t.Fatalf("address %q %v", address, e)
	}
	if _, e = inboundAddress(strings.Repeat("x", 64)+"@example.com", "1234"); e == nil {
		t.Fatal("oversized receiving address accepted")
	}
	r := gmailinbound.Route{ID: "route1"}
	m := gmailinbound.Message{ID: "message1", From: "owner@example.com", ThreadID: "email-thread"}
	first := emailConversationID(r, m)
	m.ID = "reply"
	if emailConversationID(r, m) != first {
		t.Fatal("reply started a new chat")
	}
	m.ThreadID = "new-thread"
	if emailConversationID(r, m) == first {
		t.Fatal("new email conversation reused existing chat")
	}
	r.ID = "route2"
	m.ThreadID = "email-thread"
	if emailConversationID(r, m) == first {
		t.Fatal("two targets shared a chat")
	}
}
func TestGmailInboundTargetPreservesProjectAndWorkflowOwnership(t *testing.T) {
	fx := newCrewRunModeFixture(t)
	fx.api.productSchedules = NewProductScheduleService(fx.api, fx.api.agentProfiles)
	ctx := internalBotRequestContext(context.Background(), "owner")
	target, e := fx.api.inboundTarget(ctx, "owner", crewRunModeOwnerRoot)
	if e != nil || target.ProfileID != "work" || target.ProjectID != "crew-aaa" {
		t.Fatalf("owned crew %+v %v", target, e)
	}
	logical, e := fx.api.inboundTarget(ctx, "owner", "Chats/Work/projects/alpha")
	if e != nil || logical.WorkspacePath != target.WorkspacePath {
		t.Fatalf("logical path did not bind owner's physical workspace: %+v %v", logical, e)
	}
	if _, e = fx.api.inboundTarget(ctx, "reader", crewRunModeOwnerRoot); e == nil {
		t.Fatal("reader configured owner's mailbox route")
	}
	code := fx.profile
	code.ID = "code"
	code.Product = "code"
	code.Runtime.Workspace.ProjectsRoot = "Chats/Code/projects"
	if e = fx.api.agentProfiles.RegisterProfile(code); e != nil {
		t.Fatal(e)
	}
	codeRoot := "_users/owner/Chats/Code/projects/private"
	fx.files[codeRoot+"/product.json"] = `{"schema_version":1,"product":"code","id":"code-aaa","title":"Private","session_id":"code-session"}`
	fx.files[codeRoot+"/workflow.json"] = `{"schema_version":1,"product":"code","id":"code-aaa","title":"Private"}`
	codeTarget, e := fx.api.inboundTarget(ctx, "owner", codeRoot)
	if e != nil || codeTarget.ProfileID != "code" || codeTarget.ProjectID != "code-aaa" {
		t.Fatalf("owned Code %+v %v", codeTarget, e)
	}
	if _, e = fx.api.inboundTarget(ctx, "reader", codeRoot); e == nil {
		t.Fatal("reader configured a private Code")
	}
	target, e = fx.api.inboundTarget(ctx, "owner", "Workflow/shared")
	if e != nil || target.ProfileID != "" {
		t.Fatalf("owned workflow %+v %v", target, e)
	}
	if _, e = fx.api.inboundTarget(ctx, "reader", "Workflow/shared"); e == nil {
		t.Fatal("workflow reader configured incoming email")
	}
	request := profileRouteRequest("GET", "/api/gmail-inbound/route?workspace_path="+crewRunModeOwnerRoot, nil, "reader")
	rec := httptest.NewRecorder()
	fx.api.gmailInboundRoute(gmailInboundConfig{})(rec, request)
	if rec.Code != 403 {
		t.Fatalf("management ownership status %d", rec.Code)
	}
}
